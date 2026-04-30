// Package reactor provides a plugin that listens to MQTT topics and executes action pipelines
package reactor

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"podmanview/internal/mqtt"
	"podmanview/internal/plugins"
)

//go:embed index.html
var htmlContent []byte

const (
	pluginName    = "reactor"
	storageKey    = "blocks"
	maxLogEntries = 100
)

// condStateKey uniquely identifies a condition's match state per topic
type condStateKey struct {
	blockID   string
	condIndex int
	topic     string
}

// Plugin listens to MQTT topics and executes action pipelines
type Plugin struct {
	*plugins.BasePlugin
	mu             sync.RWMutex
	blocks         []ReactionBlock
	logs           []ExecutionLog
	compiledRegex  map[string]*regexp.Regexp
	prevMatchState map[condStateKey]bool
	subscribed     bool
	backgroundCtx  context.Context
	backgroundStop context.CancelFunc
	mqttEnabled    bool
	mqttSettings   mqtt.Config
	mqttClient     *mqtt.Client
}

// New creates a new reactor plugin instance
func New() *Plugin {
	return &Plugin{
		BasePlugin: plugins.NewBasePlugin(
			pluginName,
			"MQTT Reactor",
			"2.0.0",
			htmlContent,
		),
		blocks:         []ReactionBlock{},
		logs:           make([]ExecutionLog, 0, maxLogEntries),
		compiledRegex:  make(map[string]*regexp.Regexp),
		prevMatchState: make(map[condStateKey]bool),
		mqttSettings: mqtt.Config{
			Prefix: "podmanview",
		},
	}
}

// Init initializes the plugin
func (p *Plugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)
	p.loadBlocks()
	p.loadSettings()

	// Initialize MQTT client if enabled and configured
	if p.mqttEnabled && p.mqttSettings.Broker != "" {
		if err := p.createMQTTClient(); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to create MQTT client: %v", p.Name(), err)
			}
		} else if err := p.mqttClient.Connect(); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to connect to MQTT: %v", p.Name(), err)
			}
		}
	}

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin initialized with %d blocks", p.Name(), len(p.blocks))
	}
	return nil
}

// Start starts the plugin
func (p *Plugin) Start(ctx context.Context) error {
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin started", p.Name())
	}
	return nil
}

// Stop stops the plugin
func (p *Plugin) Stop(ctx context.Context) error {
	p.mu.Lock()
	if p.backgroundStop != nil {
		p.backgroundStop()
		p.backgroundStop = nil
	}
	p.mu.Unlock()

	p.unsubscribeAll()
	p.disconnectMQTT()

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin stopped", p.Name())
	}
	return nil
}

// IsEnabled checks if the plugin is enabled
func (p *Plugin) IsEnabled() bool {
	if p.Deps() == nil || p.Deps().Storage == nil {
		return false
	}
	enabled, err := p.Deps().Storage.IsPluginEnabled(p.Name())
	if err != nil {
		return false
	}
	return enabled
}

// Routes returns HTTP routes for the plugin API
func (p *Plugin) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "GET", Path: "/api/plugins/reactor/blocks", Handler: p.handleGetBlocks, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/reactor/blocks", Handler: p.handleCreateBlock, RequireAuth: true},
		{Method: "PUT", Path: "/api/plugins/reactor/blocks", Handler: p.handleUpdateBlock, RequireAuth: true},
		{Method: "DELETE", Path: "/api/plugins/reactor/blocks", Handler: p.handleDeleteBlock, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/reactor/blocks/toggle", Handler: p.handleToggleBlock, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/reactor/blocks/test", Handler: p.handleTestBlock, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/reactor/logs", Handler: p.handleGetLogs, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/reactor/status", Handler: p.handleGetStatus, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/reactor/settings", Handler: p.handleGetSettings, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/reactor/settings", Handler: p.handleUpdateSettings, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/reactor/mqtt", Handler: p.handleToggleMQTT, RequireAuth: true},
	}
}

// StartBackgroundTasks subscribes to MQTT and starts listening
func (p *Plugin) StartBackgroundTasks(ctx context.Context) error {
	p.mu.Lock()
	p.backgroundCtx, p.backgroundStop = context.WithCancel(ctx)
	p.mu.Unlock()

	return p.resubscribe()
}

// resubscribe unsubscribes from everything and subscribes to "#" if there are enabled blocks
func (p *Plugin) resubscribe() error {
	p.unsubscribeAll()

	p.mu.RLock()
	hasEnabled := false
	for _, b := range p.blocks {
		if b.Enabled {
			hasEnabled = true
			break
		}
	}
	client := p.mqttClient
	p.mu.RUnlock()

	if !hasEnabled {
		return nil
	}

	if client == nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] MQTT client not configured, skipping subscription", p.Name())
		}
		return nil
	}

	if !client.IsConnected() {
		if err := client.Connect(); err != nil {
			return fmt.Errorf("failed to connect MQTT: %w", err)
		}
	}

	if err := client.SubscribeRaw("#", 1, p.onMessage); err != nil {
		return fmt.Errorf("failed to subscribe to #: %w", err)
	}

	p.mu.Lock()
	p.subscribed = true
	p.mu.Unlock()

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Subscribed to MQTT wildcard '#'", p.Name())
	}

	return nil
}

// unsubscribeAll removes the wildcard subscription
func (p *Plugin) unsubscribeAll() {
	p.mu.Lock()
	wasSubscribed := p.subscribed
	p.subscribed = false
	p.mu.Unlock()

	if !wasSubscribed {
		return
	}

	p.mu.RLock()
	client := p.mqttClient
	p.mu.RUnlock()

	if client != nil && client.IsConnected() {
		client.Unsubscribe("#")
	}
}

// onMessage handles incoming MQTT messages
func (p *Plugin) onMessage(topic string, payload []byte) {
	p.mu.RLock()
	blocks := make([]ReactionBlock, len(p.blocks))
	copy(blocks, p.blocks)
	p.mu.RUnlock()

	for _, block := range blocks {
		if !block.Enabled {
			continue
		}
		if p.shouldTrigger(block, topic, payload) {
			go p.executePipeline(block, topic, payload)
		}
	}
}

// shouldTrigger checks if a block's conditions are met
func (p *Plugin) shouldTrigger(block ReactionBlock, topic string, payload []byte) bool {
	if len(block.Trigger.Conditions) == 0 {
		return false
	}

	for i, cond := range block.Trigger.Conditions {
		matched := p.evalCondition(block.ID, i, cond, topic, payload)

		if block.Trigger.Operator == OperatorOR && matched {
			return true
		}
		if block.Trigger.Operator == OperatorAND && !matched {
			return false
		}
	}

	return block.Trigger.Operator == OperatorAND
}

// evalCondition evaluates a condition with edge trigger support
func (p *Plugin) evalCondition(blockID string, condIndex int, cond TriggerCondition, topic string, payload []byte) bool {
	topicRe := p.getRegex(cond.TopicPattern)
	if topicRe == nil || !topicRe.MatchString(topic) {
		return false
	}

	payloadMatches := true
	if cond.PayloadRegex != "" {
		payloadRe := p.getRegex(cond.PayloadRegex)
		if payloadRe == nil || !payloadRe.MatchString(string(payload)) {
			payloadMatches = false
		}
	}

	if cond.Mode == "" || cond.Mode == TriggerAlways {
		return payloadMatches
	}

	key := condStateKey{blockID: blockID, condIndex: condIndex, topic: topic}

	p.mu.Lock()
	prev := p.prevMatchState[key]
	p.prevMatchState[key] = payloadMatches
	p.mu.Unlock()

	return payloadMatches && !prev
}

// getRegex returns compiled regex from cache or compiles it
func (p *Plugin) getRegex(pattern string) *regexp.Regexp {
	p.mu.RLock()
	re, ok := p.compiledRegex[pattern]
	p.mu.RUnlock()

	if ok {
		return re
	}

	compiled, err := regexp.Compile(pattern)
	if err != nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Invalid regex pattern '%s': %v", p.Name(), pattern, err)
		}
		return nil
	}

	p.mu.Lock()
	p.compiledRegex[pattern] = compiled
	p.mu.Unlock()

	return compiled
}

// templateVarRe matches template variables like {{topic}}, {{payload.field}}, {{timestamp}}
var templateVarRe = regexp.MustCompile(`\{\{(\w+(?:\.\w+)*)\}\}`)

// renderTemplate replaces template variables in a string with actual values
func renderTemplate(tmpl string, topic string, payload []byte) string {
	var payloadMap map[string]json.RawMessage
	json.Unmarshal(payload, &payloadMap)

	topicName := topic
	if idx := strings.LastIndex(topic, "/"); idx >= 0 && idx < len(topic)-1 {
		topicName = topic[idx+1:]
	}

	now := time.Now()

	return templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		varName := match[2 : len(match)-2]

		switch varName {
		case "topic":
			return topic
		case "topic_name":
			return topicName
		case "payload":
			return string(payload)
		case "timestamp":
			return strconv.FormatInt(now.UnixMilli(), 10)
		case "timestamp_s":
			return strconv.FormatInt(now.Unix(), 10)
		default:
			if strings.HasPrefix(varName, "payload.") {
				field := varName[8:]
				if payloadMap != nil {
					if raw, ok := payloadMap[field]; ok {
						var s string
						if json.Unmarshal(raw, &s) == nil {
							return s
						}
						return string(raw)
					}
				}
			}
			return match
		}
	})
}

// executePipeline runs the action pipeline sequentially
func (p *Plugin) executePipeline(block ReactionBlock, triggerTopic string, mqttPayload []byte) {
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Block '%s' triggered by '%s', executing %d actions",
			p.Name(), block.Name, triggerTopic, len(block.Actions))
	}

	for i, action := range block.Actions {
		var err error
		var statusCode int
		start := time.Now()

		switch action.Type {
		case ActionHTTP:
			statusCode, err = p.executeHTTP(action.HTTP, triggerTopic, mqttPayload)
		case ActionMQTT:
			err = p.executeMQTT(action.MQTT, triggerTopic, mqttPayload)
			if err == nil {
				statusCode = 200
			}
		case ActionDelay:
			p.executeDelay(action.Delay)
			statusCode = 200
		default:
			err = fmt.Errorf("unknown action type: %s", action.Type)
		}

		logEntry := ExecutionLog{
			BlockID:      block.ID,
			BlockName:    block.Name,
			ActionIndex:  i,
			ActionType:   string(action.Type),
			Timestamp:    start,
			TriggerTopic: triggerTopic,
			StatusCode:   statusCode,
			DurationMs:   time.Since(start).Milliseconds(),
		}

		if err != nil {
			logEntry.Error = err.Error()
			p.addLog(logEntry)
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Block '%s' action[%d] (%s) failed: %v",
					p.Name(), block.Name, i, action.Type, err)
			}
			return // stop pipeline on error
		}

		p.addLog(logEntry)
	}
}

// executeHTTP performs an HTTP request
func (p *Plugin) executeHTTP(cfg *HTTPActionConfig, triggerTopic string, mqttPayload []byte) (int, error) {
	if cfg == nil {
		return 0, fmt.Errorf("HTTP action config is nil")
	}

	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	var bodyReader io.Reader
	contentType := ""

	switch cfg.BodyType {
	case BodyForward:
		bodyReader = bytes.NewReader(mqttPayload)
		contentType = "application/json"
	case BodyJSON:
		bodyReader = strings.NewReader(renderTemplate(cfg.Body, triggerTopic, mqttPayload))
		contentType = "application/json"
	case BodyForm:
		bodyReader = strings.NewReader(renderTemplate(cfg.Body, triggerTopic, mqttPayload))
		contentType = "application/x-www-form-urlencoded"
	case BodyRaw:
		bodyReader = strings.NewReader(renderTemplate(cfg.Body, triggerTopic, mqttPayload))
		contentType = "text/plain"
	case BodyNone:
		bodyReader = nil
	}

	reqURL := renderTemplate(cfg.URL, triggerTopic, mqttPayload)
	if _, err := url.ParseRequestURI(reqURL); err != nil {
		return 0, fmt.Errorf("invalid URL: %w", err)
	}

	req, err := http.NewRequest(string(cfg.Method), reqURL, bodyReader)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}

// executeMQTT publishes an MQTT message
func (p *Plugin) executeMQTT(cfg *MQTTActionConfig, triggerTopic string, mqttPayload []byte) error {
	if cfg == nil {
		return fmt.Errorf("MQTT action config is nil")
	}

	p.mu.RLock()
	client := p.mqttClient
	p.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("MQTT client not available")
	}

	if !client.IsConnected() {
		return fmt.Errorf("MQTT client not connected")
	}

	targetTopic := renderTemplate(cfg.Topic, triggerTopic, mqttPayload)
	payload := renderTemplate(cfg.Payload, triggerTopic, mqttPayload)

	return client.PublishRaw(targetTopic, []byte(payload), cfg.Retain)
}

// executeDelay pauses execution for the specified duration
func (p *Plugin) executeDelay(cfg *DelayActionConfig) {
	if cfg == nil || cfg.Seconds <= 0 {
		return
	}
	time.Sleep(time.Duration(cfg.Seconds) * time.Second)
}

// addLog appends a log entry, maintaining max size
func (p *Plugin) addLog(entry ExecutionLog) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logs = append(p.logs, entry)
	if len(p.logs) > maxLogEntries {
		p.logs = p.logs[len(p.logs)-maxLogEntries:]
	}
}

// loadBlocks loads blocks from storage
func (p *Plugin) loadBlocks() {
	deps := p.Deps()
	if deps == nil || deps.Storage == nil {
		return
	}

	var blocks []ReactionBlock
	err := deps.Storage.GetJSON(p.Name(), storageKey, &blocks)
	if err != nil {
		return
	}

	p.mu.Lock()
	p.blocks = blocks
	p.mu.Unlock()
}

// saveBlocks persists blocks to storage
func (p *Plugin) saveBlocks() error {
	deps := p.Deps()
	if deps == nil || deps.Storage == nil {
		return fmt.Errorf("storage not available")
	}

	p.mu.RLock()
	blocks := make([]ReactionBlock, len(p.blocks))
	copy(blocks, p.blocks)
	p.mu.RUnlock()

	return deps.Storage.SetJSON(p.Name(), storageKey, blocks)
}

// invalidateCaches clears compiled regex cache and edge trigger state
func (p *Plugin) invalidateCaches() {
	p.mu.Lock()
	p.compiledRegex = make(map[string]*regexp.Regexp)
	p.prevMatchState = make(map[condStateKey]bool)
	p.mu.Unlock()
}

// generateID creates a new unique block ID
func generateID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())[4:12]
}

// loadSettings loads plugin settings from storage
func (p *Plugin) loadSettings() {
	deps := p.Deps()
	if deps == nil || deps.Storage == nil {
		return
	}

	// Load MQTT enabled state
	mqttEnabled, err := deps.Storage.GetBool(p.Name(), "mqttEnabled")
	if err == nil {
		p.mu.Lock()
		p.mqttEnabled = mqttEnabled
		p.mu.Unlock()
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Loaded MQTT enabled state: %v", p.Name(), mqttEnabled)
		}
	} else {
		deps.Storage.SetBool(p.Name(), "mqttEnabled", false)
	}

	// Load MQTT settings
	var settings mqtt.Config
	if err := deps.Storage.GetJSON(p.Name(), "mqttSettings", &settings); err == nil {
		p.mu.Lock()
		p.mqttSettings = settings
		if p.mqttSettings.Prefix == "" {
			p.mqttSettings.Prefix = "podmanview"
		}
		p.mu.Unlock()
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Loaded MQTT settings: broker=%s", p.Name(), settings.Broker)
		}
	} else {
		deps.Storage.SetJSON(p.Name(), "mqttSettings", p.mqttSettings)
	}
}

// createMQTTClient creates a new MQTT client based on current settings
func (p *Plugin) createMQTTClient() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Disconnect existing client if any
	if p.mqttClient != nil {
		p.mqttClient.Disconnect()
		p.mqttClient = nil
	}

	if p.mqttSettings.Broker == "" {
		return nil
	}

	client, err := mqtt.New(p.mqttSettings, p.Logger())
	if err != nil {
		return err
	}

	p.mqttClient = client
	return nil
}

// disconnectMQTT disconnects the MQTT client
func (p *Plugin) disconnectMQTT() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mqttClient != nil && p.mqttClient.IsConnected() {
		p.mqttClient.Disconnect()
	}
	p.mqttClient = nil
}
