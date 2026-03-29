// Package mqtt2http provides a plugin that listens to MQTT topics and triggers HTTP requests
package mqtt2http

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

	"podmanview/internal/plugins"
)

//go:embed index.html
var htmlContent []byte

const (
	pluginName    = "mqtt2http"
	storageKey    = "blocks"
	maxLogEntries = 100
)

// condStateKey uniquely identifies a condition's match state per topic
type condStateKey struct {
	blockID   string
	condIndex int
	topic     string
}

// Plugin listens to MQTT topics and triggers HTTP requests
type Plugin struct {
	*plugins.BasePlugin
	mu             sync.RWMutex
	blocks         []HookBlock
	logs           []ExecutionLog
	httpClient     *http.Client
	compiledRegex  map[string]*regexp.Regexp // cache: pattern -> compiled regex
	prevMatchState map[condStateKey]bool     // previous payload match state for edge triggers
	subscribed     bool                      // whether we have an active MQTT subscription
	backgroundCtx  context.Context
	backgroundStop context.CancelFunc
}

// New creates a new mqtt2http plugin instance
func New() *Plugin {
	return &Plugin{
		BasePlugin: plugins.NewBasePlugin(
			pluginName,
			"MQTT to HTTP trigger",
			"1.0.0",
			htmlContent,
		),
		blocks:         []HookBlock{},
		logs:           make([]ExecutionLog, 0, maxLogEntries),
		compiledRegex:  make(map[string]*regexp.Regexp),
		prevMatchState: make(map[condStateKey]bool),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Init initializes the plugin
func (p *Plugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)
	p.loadBlocks()

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
		{Method: "GET", Path: "/api/plugins/mqtt2http/blocks", Handler: p.handleGetBlocks, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/mqtt2http/blocks", Handler: p.handleCreateBlock, RequireAuth: true},
		{Method: "PUT", Path: "/api/plugins/mqtt2http/blocks", Handler: p.handleUpdateBlock, RequireAuth: true},
		{Method: "DELETE", Path: "/api/plugins/mqtt2http/blocks", Handler: p.handleDeleteBlock, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/mqtt2http/blocks/toggle", Handler: p.handleToggleBlock, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/mqtt2http/blocks/test", Handler: p.handleTestBlock, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/mqtt2http/logs", Handler: p.handleGetLogs, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/mqtt2http/status", Handler: p.handleGetStatus, RequireAuth: true},
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
	p.mu.RUnlock()

	if !hasEnabled {
		return nil
	}

	deps := p.Deps()
	if deps == nil || deps.MQTTClient == nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] MQTT client not configured, skipping subscription", p.Name())
		}
		return nil
	}

	if !deps.MQTTClient.IsConnected() {
		if err := deps.MQTTClient.Connect(); err != nil {
			return fmt.Errorf("failed to connect MQTT: %w", err)
		}
	}

	// Subscribe to all topics via wildcard "#"
	// We filter by regex on our side
	if err := deps.MQTTClient.SubscribeRaw("#", 1, p.onMessage); err != nil {
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

	deps := p.Deps()
	if deps != nil && deps.MQTTClient != nil && deps.MQTTClient.IsConnected() {
		deps.MQTTClient.Unsubscribe("#")
	}
}

// onMessage handles incoming MQTT messages
func (p *Plugin) onMessage(topic string, payload []byte) {
	p.mu.RLock()
	blocks := make([]HookBlock, len(p.blocks))
	copy(blocks, p.blocks)
	p.mu.RUnlock()

	for _, block := range blocks {
		if !block.Enabled {
			continue
		}
		if p.shouldTrigger(block, topic, payload) {
			switch block.ActionType {
			case ActionMQTT:
				go p.executeMQTTAction(block, topic, payload)
			default:
				go p.executeAction(block, topic, payload)
			}
		}
	}
}

// shouldTrigger checks if a block's conditions are met
func (p *Plugin) shouldTrigger(block HookBlock, topic string, payload []byte) bool {
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

	// AND: all matched; OR: none matched
	return block.Trigger.Operator == OperatorAND
}

// evalCondition evaluates a condition with edge trigger support.
// For mode "on_change": returns true only on transition from non-match to match.
// For mode "always" (default): returns true on every match.
func (p *Plugin) evalCondition(blockID string, condIndex int, cond TriggerCondition, topic string, payload []byte) bool {
	// First check if topic matches
	topicRe := p.getRegex(cond.TopicPattern)
	if topicRe == nil || !topicRe.MatchString(topic) {
		return false
	}

	// Check payload match (true if no payload regex specified)
	payloadMatches := true
	if cond.PayloadRegex != "" {
		payloadRe := p.getRegex(cond.PayloadRegex)
		if payloadRe == nil || !payloadRe.MatchString(string(payload)) {
			payloadMatches = false
		}
	}

	// For "always" mode (or empty/default), just return the match result
	if cond.Mode == "" || cond.Mode == TriggerAlways {
		return payloadMatches
	}

	// Edge trigger ("on_change"): fire only on transition false → true
	key := condStateKey{blockID: blockID, condIndex: condIndex, topic: topic}

	p.mu.Lock()
	prev := p.prevMatchState[key]
	p.prevMatchState[key] = payloadMatches
	p.mu.Unlock()

	// Trigger only when state transitions from false to true
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

// renderTemplate replaces template variables in a string with actual values.
// Supported variables:
//   - {{topic}}       — full MQTT topic
//   - {{topic_name}}  — last segment of topic (after last /)
//   - {{payload}}     — entire MQTT payload as string
//   - {{payload.X}}   — value of field X from JSON payload (top-level only)
//   - {{timestamp}}   — Unix timestamp in milliseconds
//   - {{timestamp_s}} — Unix timestamp in seconds
func renderTemplate(tmpl string, topic string, payload []byte) string {
	// Parse payload JSON once (best-effort, may be non-JSON)
	var payloadMap map[string]json.RawMessage
	json.Unmarshal(payload, &payloadMap)

	// Extract topic name (last segment)
	topicName := topic
	if idx := strings.LastIndex(topic, "/"); idx >= 0 && idx < len(topic)-1 {
		topicName = topic[idx+1:]
	}

	now := time.Now()

	return templateVarRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		// Strip {{ and }}
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
			// Check for payload.field
			if strings.HasPrefix(varName, "payload.") {
				field := varName[8:] // after "payload."
				if payloadMap != nil {
					if raw, ok := payloadMap[field]; ok {
						// Unquote strings, return raw for numbers/bools
						var s string
						if json.Unmarshal(raw, &s) == nil {
							return s
						}
						return string(raw)
					}
				}
			}
			return match // leave unknown variables as-is
		}
	})
}

// executeAction performs the HTTP request for a triggered block
func (p *Plugin) executeAction(block HookBlock, triggerTopic string, mqttPayload []byte) {
	start := time.Now()
	logEntry := ExecutionLog{
		BlockID:      block.ID,
		BlockName:    block.Name,
		Timestamp:    start,
		TriggerTopic: triggerTopic,
	}

	timeout := time.Duration(block.Action.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// Build request body with template rendering
	var bodyReader io.Reader
	contentType := ""

	switch block.Action.BodyType {
	case BodyForward:
		bodyReader = bytes.NewReader(mqttPayload)
		contentType = "application/json"
	case BodyJSON:
		bodyReader = strings.NewReader(renderTemplate(block.Action.Body, triggerTopic, mqttPayload))
		contentType = "application/json"
	case BodyForm:
		bodyReader = strings.NewReader(renderTemplate(block.Action.Body, triggerTopic, mqttPayload))
		contentType = "application/x-www-form-urlencoded"
	case BodyRaw:
		bodyReader = strings.NewReader(renderTemplate(block.Action.Body, triggerTopic, mqttPayload))
		contentType = "text/plain"
	case BodyNone:
		bodyReader = nil
	}

	// Render template variables in URL too
	reqURL := renderTemplate(block.Action.URL, triggerTopic, mqttPayload)
	if _, err := url.ParseRequestURI(reqURL); err != nil {
		logEntry.Error = fmt.Sprintf("invalid URL: %v", err)
		logEntry.DurationMs = time.Since(start).Milliseconds()
		p.addLog(logEntry)
		return
	}

	req, err := http.NewRequest(string(block.Action.Method), reqURL, bodyReader)
	if err != nil {
		logEntry.Error = fmt.Sprintf("failed to create request: %v", err)
		logEntry.DurationMs = time.Since(start).Milliseconds()
		p.addLog(logEntry)
		return
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range block.Action.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	logEntry.DurationMs = time.Since(start).Milliseconds()

	if err != nil {
		logEntry.Error = err.Error()
		p.addLog(logEntry)
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Block '%s' HTTP error: %v", p.Name(), block.Name, err)
		}
		return
	}
	defer resp.Body.Close()

	logEntry.StatusCode = resp.StatusCode
	p.addLog(logEntry)

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Block '%s' triggered by '%s' -> %s %s = %d (%dms)",
			p.Name(), block.Name, triggerTopic,
			block.Action.Method, block.Action.URL, resp.StatusCode, logEntry.DurationMs)
	}
}

// executeMQTTAction publishes an MQTT message for a triggered block
func (p *Plugin) executeMQTTAction(block HookBlock, triggerTopic string, mqttPayload []byte) {
	start := time.Now()
	logEntry := ExecutionLog{
		BlockID:      block.ID,
		BlockName:    block.Name,
		Timestamp:    start,
		TriggerTopic: triggerTopic,
	}

	deps := p.Deps()
	if deps == nil || deps.MQTTClient == nil {
		logEntry.Error = "MQTT client not available"
		logEntry.DurationMs = time.Since(start).Milliseconds()
		p.addLog(logEntry)
		return
	}

	if !deps.MQTTClient.IsConnected() {
		logEntry.Error = "MQTT client not connected"
		logEntry.DurationMs = time.Since(start).Milliseconds()
		p.addLog(logEntry)
		return
	}

	targetTopic := renderTemplate(block.MQTTAction.Topic, triggerTopic, mqttPayload)
	payload := renderTemplate(block.MQTTAction.Payload, triggerTopic, mqttPayload)

	var err error
	if block.MQTTAction.QoS > 0 || block.MQTTAction.Retain {
		err = deps.MQTTClient.PublishRaw(targetTopic, []byte(payload), block.MQTTAction.Retain)
	} else {
		err = deps.MQTTClient.PublishRaw(targetTopic, []byte(payload), false)
	}

	logEntry.DurationMs = time.Since(start).Milliseconds()

	if err != nil {
		logEntry.Error = err.Error()
		p.addLog(logEntry)
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Block '%s' MQTT publish error: %v", p.Name(), block.Name, err)
		}
		return
	}

	logEntry.StatusCode = 200 // virtual "OK" status for MQTT publish
	p.addLog(logEntry)

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Block '%s' triggered by '%s' -> MQTT publish to '%s' (%dms)",
			p.Name(), block.Name, triggerTopic, targetTopic, logEntry.DurationMs)
	}

	// Auto-off: send stop payload after delay
	if block.MQTTAction.AutoOffDelay > 0 && block.MQTTAction.AutoOffPayload != "" {
		go func() {
			time.Sleep(time.Duration(block.MQTTAction.AutoOffDelay) * time.Second)

			offPayload := renderTemplate(block.MQTTAction.AutoOffPayload, triggerTopic, mqttPayload)
			offErr := deps.MQTTClient.PublishRaw(targetTopic, []byte(offPayload), block.MQTTAction.Retain)

			offLog := ExecutionLog{
				BlockID:      block.ID,
				BlockName:    block.Name + " [auto-off]",
				Timestamp:    time.Now(),
				TriggerTopic: triggerTopic,
				StatusCode:   200,
			}
			if offErr != nil {
				offLog.Error = offErr.Error()
				offLog.StatusCode = 0
			}
			p.addLog(offLog)

			if p.Logger() != nil {
				p.Logger().Printf("[%s] Block '%s' auto-off sent to '%s'", p.Name(), block.Name, targetTopic)
			}
		}()
	}
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

// loadBlocks loads hook blocks from storage
func (p *Plugin) loadBlocks() {
	deps := p.Deps()
	if deps == nil || deps.Storage == nil {
		return
	}

	var blocks []HookBlock
	err := deps.Storage.GetJSON(p.Name(), storageKey, &blocks)
	if err != nil {
		return
	}

	p.mu.Lock()
	p.blocks = blocks
	p.mu.Unlock()
}

// saveBlocks persists hook blocks to storage
func (p *Plugin) saveBlocks() error {
	deps := p.Deps()
	if deps == nil || deps.Storage == nil {
		return fmt.Errorf("storage not available")
	}

	p.mu.RLock()
	blocks := make([]HookBlock, len(p.blocks))
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

// generateID creates a new unique block ID using timestamp + random
func generateID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())[4:12]
}
