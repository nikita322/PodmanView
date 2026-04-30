package reactor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"podmanview/internal/mqtt"
	"podmanview/internal/plugins"
)

// PluginSettings represents reactor plugin configuration
type PluginSettings struct {
	MQTTEnabled bool        `json:"mqttEnabled"`
	MQTT        mqtt.Config `json:"mqtt"`
}

// MQTTToggleRequest represents request to toggle MQTT
type MQTTToggleRequest struct {
	Enabled bool `json:"enabled"`
}

// handleGetBlocks returns all reaction blocks
func (p *Plugin) handleGetBlocks(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	blocks := make([]ReactionBlock, len(p.blocks))
	copy(blocks, p.blocks)
	p.mu.RUnlock()

	plugins.WriteJSON(w, http.StatusOK, blocks)
}

// handleCreateBlock creates a new reaction block
func (p *Plugin) handleCreateBlock(w http.ResponseWriter, r *http.Request) {
	var block ReactionBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if err := validateBlock(block); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	block.ID = generateID()

	p.mu.Lock()
	p.blocks = append(p.blocks, block)
	p.mu.Unlock()

	if err := p.saveBlocks(); err != nil {
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save blocks"})
		return
	}

	p.invalidateCaches()
	p.resubscribe()

	plugins.WriteJSON(w, http.StatusCreated, block)
}

// handleUpdateBlock updates an existing reaction block
func (p *Plugin) handleUpdateBlock(w http.ResponseWriter, r *http.Request) {
	var block ReactionBlock
	if err := json.NewDecoder(r.Body).Decode(&block); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if block.ID == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Block ID is required"})
		return
	}

	if err := validateBlock(block); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	p.mu.Lock()
	found := false
	for i, b := range p.blocks {
		if b.ID == block.ID {
			p.blocks[i] = block
			found = true
			break
		}
	}
	p.mu.Unlock()

	if !found {
		plugins.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Block not found"})
		return
	}

	if err := p.saveBlocks(); err != nil {
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save blocks"})
		return
	}

	p.invalidateCaches()
	p.resubscribe()

	plugins.WriteJSON(w, http.StatusOK, block)
}

// handleDeleteBlock deletes a reaction block
func (p *Plugin) handleDeleteBlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Block ID is required"})
		return
	}

	p.mu.Lock()
	found := false
	for i, b := range p.blocks {
		if b.ID == req.ID {
			p.blocks = append(p.blocks[:i], p.blocks[i+1:]...)
			found = true
			break
		}
	}
	p.mu.Unlock()

	if !found {
		plugins.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Block not found"})
		return
	}

	if err := p.saveBlocks(); err != nil {
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save blocks"})
		return
	}

	p.invalidateCaches()
	p.resubscribe()

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Block deleted"})
}

// handleToggleBlock enables/disables a reaction block
func (p *Plugin) handleToggleBlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Block ID is required"})
		return
	}

	p.mu.Lock()
	found := false
	for i, b := range p.blocks {
		if b.ID == req.ID {
			p.blocks[i].Enabled = req.Enabled
			found = true
			break
		}
	}
	p.mu.Unlock()

	if !found {
		plugins.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Block not found"})
		return
	}

	if err := p.saveBlocks(); err != nil {
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save blocks"})
		return
	}

	p.resubscribe()

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Block toggled"})
}

// handleTestBlock manually triggers an action pipeline for testing
func (p *Plugin) handleTestBlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Block ID is required"})
		return
	}

	p.mu.RLock()
	var block *ReactionBlock
	for _, b := range p.blocks {
		if b.ID == req.ID {
			blockCopy := b
			block = &blockCopy
			break
		}
	}
	p.mu.RUnlock()

	if block == nil {
		plugins.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Block not found"})
		return
	}

	testPayload := []byte(`{"test": true}`)
	go p.executePipeline(*block, "test/manual", testPayload)

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Test triggered"})
}

// handleGetLogs returns execution logs
func (p *Plugin) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	logs := make([]ExecutionLog, len(p.logs))
	copy(logs, p.logs)
	p.mu.RUnlock()

	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}

	plugins.WriteJSON(w, http.StatusOK, logs)
}

// handleGetStatus returns plugin status
func (p *Plugin) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	client := p.mqttClient
	settings := p.mqttSettings
	enabledCount := 0
	for _, b := range p.blocks {
		if b.Enabled {
			enabledCount++
		}
	}
	status := map[string]interface{}{
		"mqttConfigured": settings.Broker != "",
		"mqttConnected":  client != nil && client.IsConnected(),
		"subscribed":     p.subscribed,
		"blockCount":     len(p.blocks),
		"enabledCount":   enabledCount,
	}
	p.mu.RUnlock()

	plugins.WriteJSON(w, http.StatusOK, status)
}

// handleGetSettings returns current plugin settings
func (p *Plugin) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	mqttEnabled := p.mqttEnabled
	settings := p.mqttSettings
	p.mu.RUnlock()

	plugins.WriteJSON(w, http.StatusOK, PluginSettings{
		MQTTEnabled: mqttEnabled,
		MQTT:        settings,
	})
}

// handleUpdateSettings updates plugin settings
func (p *Plugin) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings PluginSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	// Validate MQTT settings if enabled
	if settings.MQTTEnabled && settings.MQTT.Broker == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "MQTT broker is required when MQTT is enabled"})
		return
	}

	deps := p.Deps()

	p.mu.Lock()
	mqttWasEnabled := p.mqttEnabled
	p.mqttEnabled = settings.MQTTEnabled
	p.mqttSettings = settings.MQTT
	if p.mqttSettings.Prefix == "" {
		p.mqttSettings.Prefix = "podmanview"
	}
	p.mu.Unlock()

	// Save to storage
	if deps != nil && deps.Storage != nil {
		if err := deps.Storage.SetBool(p.Name(), "mqttEnabled", settings.MQTTEnabled); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to save MQTT enabled state: %v", p.Name(), err)
			}
		}
		if err := deps.Storage.SetJSON(p.Name(), "mqttSettings", p.mqttSettings); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to save MQTT settings: %v", p.Name(), err)
			}
			plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save MQTT settings"})
			return
		}
	}

	// Handle MQTT connection state changes
	if settings.MQTTEnabled {
		if err := p.createMQTTClient(); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to create MQTT client: %v", p.Name(), err)
			}
			plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to create MQTT client: " + err.Error()})
			return
		}
		if p.mqttClient != nil {
			if err := p.mqttClient.Connect(); err != nil {
				if p.Logger() != nil {
					p.Logger().Printf("[%s] Failed to connect to MQTT broker: %v", p.Name(), err)
				}
				plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to connect to MQTT broker: " + err.Error()})
				return
			}
		}
	} else if mqttWasEnabled {
		p.disconnectMQTT()
	}

	// Re-subscribe if needed
	p.resubscribe()

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Settings updated: mqttEnabled=%v", p.Name(), settings.MQTTEnabled)
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Settings updated successfully"})
}

// handleToggleMQTT enables or disables MQTT
func (p *Plugin) handleToggleMQTT(w http.ResponseWriter, r *http.Request) {
	var req MQTTToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	p.mu.RLock()
	settings := p.mqttSettings
	p.mu.RUnlock()

	if settings.Broker == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "MQTT is not configured. Please set MQTT broker in settings"})
		return
	}

	p.mu.Lock()
	p.mqttEnabled = req.Enabled
	p.mu.Unlock()

	deps := p.Deps()
	if deps != nil && deps.Storage != nil {
		if err := deps.Storage.SetBool(p.Name(), "mqttEnabled", req.Enabled); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to save MQTT enabled state: %v", p.Name(), err)
			}
			plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save settings"})
			return
		}
	}

	if req.Enabled {
		if err := p.createMQTTClient(); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to create MQTT client: %v", p.Name(), err)
			}
			plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create MQTT client"})
			return
		}
		if p.mqttClient != nil {
			if err := p.mqttClient.Connect(); err != nil {
				if p.Logger() != nil {
					p.Logger().Printf("[%s] Failed to connect to MQTT broker: %v", p.Name(), err)
				}
				plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to connect to MQTT broker"})
				return
			}
		}
		p.resubscribe()
		if p.Logger() != nil {
			p.Logger().Printf("[%s] MQTT enabled", p.Name())
		}
	} else {
		p.unsubscribeAll()
		p.disconnectMQTT()
		if p.Logger() != nil {
			p.Logger().Printf("[%s] MQTT disabled", p.Name())
		}
	}

	status := "enabled"
	if !req.Enabled {
		status = "disabled"
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "MQTT " + status + " successfully",
		"enabled": strconv.FormatBool(req.Enabled),
	})
}

// validateBlock performs validation on a reaction block
func validateBlock(block ReactionBlock) error {
	if block.Name == "" {
		return fmt.Errorf("block name is required")
	}
	if len(block.Trigger.Conditions) == 0 {
		return fmt.Errorf("at least one trigger condition is required")
	}
	for _, cond := range block.Trigger.Conditions {
		if cond.TopicPattern == "" {
			return fmt.Errorf("topic pattern is required for all conditions")
		}
		if _, err := regexp.Compile(cond.TopicPattern); err != nil {
			return fmt.Errorf("invalid topic regex '%s': %v", cond.TopicPattern, err)
		}
		if cond.PayloadRegex != "" {
			if _, err := regexp.Compile(cond.PayloadRegex); err != nil {
				return fmt.Errorf("invalid payload regex '%s': %v", cond.PayloadRegex, err)
			}
		}
		if cond.Mode != "" && cond.Mode != TriggerAlways && cond.Mode != TriggerOnChange {
			return fmt.Errorf("invalid trigger mode: %s", cond.Mode)
		}
	}
	if block.Trigger.Operator != OperatorAND && block.Trigger.Operator != OperatorOR {
		return fmt.Errorf("operator must be AND or OR")
	}
	if len(block.Actions) == 0 {
		return fmt.Errorf("at least one action is required")
	}
	for i, action := range block.Actions {
		if err := validateAction(i, action); err != nil {
			return err
		}
	}
	return nil
}

// validateAction validates a single action in the pipeline
func validateAction(index int, action Action) error {
	prefix := fmt.Sprintf("action[%d]", index)

	switch action.Type {
	case ActionHTTP:
		if action.HTTP == nil {
			return fmt.Errorf("%s: HTTP config is required", prefix)
		}
		if action.HTTP.URL == "" {
			return fmt.Errorf("%s: URL is required", prefix)
		}
		switch action.HTTP.Method {
		case MethodGET, MethodPOST, MethodPUT, MethodDELETE, MethodPATCH:
		default:
			return fmt.Errorf("%s: unsupported HTTP method: %s", prefix, action.HTTP.Method)
		}
	case ActionMQTT:
		if action.MQTT == nil {
			return fmt.Errorf("%s: MQTT config is required", prefix)
		}
		if action.MQTT.Topic == "" {
			return fmt.Errorf("%s: MQTT topic is required", prefix)
		}
		if action.MQTT.QoS > 2 {
			return fmt.Errorf("%s: MQTT QoS must be 0, 1, or 2", prefix)
		}
	case ActionDelay:
		if action.Delay == nil {
			return fmt.Errorf("%s: delay config is required", prefix)
		}
		if action.Delay.Seconds <= 0 {
			return fmt.Errorf("%s: delay must be > 0 seconds", prefix)
		}
		if action.Delay.Seconds > 3600 {
			return fmt.Errorf("%s: delay must be <= 3600 seconds", prefix)
		}
	default:
		return fmt.Errorf("%s: unknown action type: %s", prefix, action.Type)
	}

	return nil
}
