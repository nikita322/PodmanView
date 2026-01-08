package led

import (
	"encoding/json"
	"net/http"

	"podmanview/internal/plugins"
)

// StatusResponse represents the response for status endpoint
type StatusResponse struct {
	State    *LEDState `json:"state"`
	Settings *Settings `json:"settings"`
}

// ToggleRequest represents the request to toggle LEDs
type ToggleRequest struct {
	Enable bool `json:"enable"`
}

// handleGetStatus returns the current LED status
func (p *LEDPlugin) handleGetStatus(w http.ResponseWriter, r *http.Request) {
	// Update state before returning
	p.updateState()

	state := p.GetState()
	settings := p.GetSettings()

	response := StatusResponse{
		State:    state,
		Settings: settings,
	}

	plugins.WriteJSON(w, http.StatusOK, response)
}

// handleToggleLEDs toggles all LEDs on or off
func (p *LEDPlugin) handleToggleLEDs(w http.ResponseWriter, r *http.Request) {
	var req ToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	// Check if LEDs are available
	state := p.GetState()
	if state.TotalLEDs == 0 {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "No LEDs available. This plugin requires a Linux system with accessible LEDs in /sys/class/leds",
		})
		return
	}

	if err := p.ToggleLEDs(req.Enable); err != nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Failed to toggle LEDs: %v", p.Name(), err)
		}
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to toggle LEDs: " + err.Error()})
		return
	}

	status := "enabled"
	if !req.Enable {
		status = "disabled"
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{
		"status":  "LEDs " + status + " successfully",
		"enabled": func() string {
			if req.Enable {
				return "true"
			}
			return "false"
		}(),
	})
}

// handleGetSettings returns current plugin settings
func (p *LEDPlugin) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings := p.GetSettings()
	plugins.WriteJSON(w, http.StatusOK, settings)
}

// handleUpdateSettings updates plugin settings
func (p *LEDPlugin) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if err := p.UpdateSettings(&settings); err != nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Failed to update settings: %v", p.Name(), err)
		}
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to update settings"})
		return
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Settings updated successfully"})
}
