package temperature

import (
	"encoding/json"
	"net/http"
	"time"

	"podmanview/internal/plugins"
)

// PluginSettings represents plugin configuration
type PluginSettings struct {
	UpdateInterval int `json:"updateInterval"` // Update interval in seconds
}

// handleGetTemperatures returns current temperature data as JSON.
// Endpoint: GET /api/plugins/temperature/data
// The route requires a valid session or PODMANVIEW_API_TOKEN.
func (p *TemperaturePlugin) handleGetTemperatures(w http.ResponseWriter, r *http.Request) {
	data := p.GetTemperatureData()
	plugins.WriteJSON(w, http.StatusOK, data)
}

// handleGetSettings returns current plugin settings
func (p *TemperaturePlugin) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	interval := int(p.updatePeriod.Seconds())
	p.mu.RUnlock()

	settings := PluginSettings{
		UpdateInterval: interval,
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

	// Update in-memory interval
	p.mu.Lock()
	p.updatePeriod = time.Duration(settings.UpdateInterval) * time.Second
	p.mu.Unlock()

	// Save update interval to storage
	if deps != nil && deps.Storage != nil {
		if err := deps.Storage.SetInt(p.Name(), "updateInterval", settings.UpdateInterval); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to save update interval to storage: %v", p.Name(), err)
			}
		}
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
		p.Logger().Printf("[%s] Settings updated: interval=%ds", p.Name(), settings.UpdateInterval)
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Settings updated successfully"})
}
