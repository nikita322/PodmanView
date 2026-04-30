package temperature

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"podmanview/internal/mqtt"
	"podmanview/internal/plugins"
)

// PluginSettings represents plugin configuration
type PluginSettings struct {
	UpdateInterval int         `json:"updateInterval"` // Update interval in seconds
	MQTTEnabled    bool        `json:"mqttEnabled"`    // MQTT publishing enabled
	MQTT           mqtt.Config `json:"mqtt"`           // MQTT configuration
}

// MQTTStatus represents MQTT status
type MQTTStatus struct {
	Enabled     bool   `json:"enabled"`     // MQTT publishing enabled
	Connected   bool   `json:"connected"`   // MQTT client connected
	Configured  bool   `json:"configured"`  // MQTT broker configured
	BrokerURL   string `json:"brokerUrl"`   // MQTT broker URL (for display)
	TopicPrefix string `json:"topicPrefix"` // MQTT topic prefix
}

// MQTTToggleRequest represents request to toggle MQTT
type MQTTToggleRequest struct {
	Enabled bool `json:"enabled"` // Enable or disable MQTT
}

// handleGetTemperatures returns current temperature data
func (p *TemperaturePlugin) handleGetTemperatures(w http.ResponseWriter, r *http.Request) {
	data := p.GetTemperatureData()
	plugins.WriteJSON(w, http.StatusOK, data)
}

// handleGetSettings returns current plugin settings
func (p *TemperaturePlugin) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	interval := int(p.updatePeriod.Seconds())
	mqttEnabled := p.mqttEnabled
	mqttSettings := p.mqttSettings
	p.mu.RUnlock()

	settings := PluginSettings{
		UpdateInterval: interval,
		MQTTEnabled:    mqttEnabled,
		MQTT:           mqttSettings,
	}

	plugins.WriteJSON(w, http.StatusOK, settings)
}

// handleUpdateSettings updates plugin settings
func (p *TemperaturePlugin) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings PluginSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	// Validate interval (5-60 seconds)
	if settings.UpdateInterval < 5 || settings.UpdateInterval > 60 {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Update interval must be between 5 and 60 seconds"})
		return
	}

	deps := p.Deps()

	// Validate MQTT settings if enabled
	if settings.MQTTEnabled && settings.MQTT.Broker == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "MQTT broker is required when MQTT is enabled"})
		return
	}

	// Update in-memory interval
	p.mu.Lock()
	p.updatePeriod = time.Duration(settings.UpdateInterval) * time.Second
	mqttWasEnabled := p.mqttEnabled
	p.mqttEnabled = settings.MQTTEnabled
	p.mqttSettings = settings.MQTT
	if p.mqttSettings.Prefix == "" {
		p.mqttSettings.Prefix = "podmanview"
	}
	p.mu.Unlock()

	// Save update interval to storage
	if deps != nil && deps.Storage != nil {
		if err := deps.Storage.SetInt(p.Name(), "updateInterval", settings.UpdateInterval); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to save update interval to storage: %v", p.Name(), err)
			}
		}

		// Save MQTT enabled state
		if err := deps.Storage.SetBool(p.Name(), "mqttEnabled", settings.MQTTEnabled); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to save MQTT enabled state: %v", p.Name(), err)
			}
		}

		// Save MQTT settings
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
		// MQTT enabled: create client and connect
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
			p.mqttClient.Publish("sensor/temperature/availability", []byte("online"))
		}
	} else if mqttWasEnabled {
		// MQTT was enabled but now disabled: disconnect
		p.disconnectMQTT()
	}

	// Restart background task with new interval
	if err := p.RestartBackgroundTasks(); err != nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Failed to restart background tasks: %v", p.Name(), err)
		}
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to restart background tasks"})
		return
	}

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Settings updated: interval=%ds, mqttEnabled=%v", p.Name(), settings.UpdateInterval, settings.MQTTEnabled)
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Settings updated successfully"})
}

// handleGetMQTTStatus returns MQTT connection status
func (p *TemperaturePlugin) handleGetMQTTStatus(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	enabled := p.mqttEnabled
	settings := p.mqttSettings
	client := p.mqttClient
	p.mu.RUnlock()

	status := MQTTStatus{
		Enabled:     enabled,
		Connected:   client != nil && client.IsConnected(),
		Configured:  settings.Broker != "",
		BrokerURL:   settings.Broker,
		TopicPrefix: settings.Prefix,
	}

	plugins.WriteJSON(w, http.StatusOK, status)
}

// handleToggleMQTT enables or disables MQTT publishing
func (p *TemperaturePlugin) handleToggleMQTT(w http.ResponseWriter, r *http.Request) {
	var req MQTTToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	p.mu.RLock()
	settings := p.mqttSettings
	p.mu.RUnlock()

	// Check if MQTT is configured
	if settings.Broker == "" {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "MQTT is not configured. Please set MQTT broker in settings"})
		return
	}

	// Update MQTT enabled state
	p.mu.Lock()
	p.mqttEnabled = req.Enabled
	p.mu.Unlock()

	// Save to storage
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

	// Connect or disconnect based on the enabled state
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
			p.mqttClient.Publish("sensor/temperature/availability", []byte("online"))
		}

		if p.Logger() != nil {
			p.Logger().Printf("[%s] MQTT publishing enabled", p.Name())
		}
	} else {
		p.disconnectMQTT()

		if p.Logger() != nil {
			p.Logger().Printf("[%s] MQTT publishing disabled", p.Name())
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
