package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"podmanview/internal/plugins"
	"podmanview/internal/storage"
)

// PluginHandler handles plugin-related API requests
type PluginHandler struct {
	server *Server
}

// NewPluginHandler creates a new plugin handler
func NewPluginHandler(server *Server) *PluginHandler {
	return &PluginHandler{
		server: server,
	}
}

// List returns the list of all plugins
func (h *PluginHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.server.plugins == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	pluginsList := make([]map[string]interface{}, 0, len(h.server.plugins))

	for _, plugin := range h.server.plugins {
		pluginInfo := map[string]interface{}{
			"name":        plugin.Name(),
			"description": plugin.Description(),
			"version":     plugin.Version(),
			"enabled":     plugin.IsEnabled(),
			"ready":       true,
		}

		// Report missing host prerequisites so the UI can explain why a plugin
		// cannot be enabled instead of letting the toggle fail silently.
		if h.server.pluginRegistry != nil {
			if err := h.server.pluginRegistry.CanEnable(plugin.Name()); err != nil {
				var notReady *plugins.PluginNotReadyError
				if errors.As(err, &notReady) {
					pluginInfo["ready"] = false
					pluginInfo["not_ready_reason"] = notReady.Reason
				}
			}
		}

		pluginsList = append(pluginsList, pluginInfo)
	}

	writeJSON(w, http.StatusOK, pluginsList)
}

// Get returns information about a specific plugin
func (h *PluginHandler) Get(w http.ResponseWriter, r *http.Request) {
	pluginName := chi.URLParam(r, "name")

	if h.server.plugins == nil {
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	for _, plugin := range h.server.plugins {
		if plugin.Name() == pluginName {
			routes := plugin.Routes()
			routesCount := 0
			if routes != nil {
				routesCount = len(routes)
			}

			pluginInfo := map[string]interface{}{
				"name":         plugin.Name(),
				"description":  plugin.Description(),
				"version":      plugin.Version(),
				"enabled":      plugin.IsEnabled(),
				"routes_count": routesCount,
			}

			writeJSON(w, http.StatusOK, pluginInfo)
			return
		}
	}

	http.Error(w, "Plugin not found", http.StatusNotFound)
}

// GetHTML returns the HTML interface for a specific plugin
func (h *PluginHandler) GetHTML(w http.ResponseWriter, r *http.Request) {
	pluginName := chi.URLParam(r, "name")

	if h.server.plugins == nil {
		http.Error(w, "Plugin not found", http.StatusNotFound)
		return
	}

	for _, plugin := range h.server.plugins {
		if plugin.Name() == pluginName {
			html, err := plugin.GetHTML()
			if err != nil {
				http.Error(w, "Failed to get plugin HTML: "+err.Error(), http.StatusInternalServerError)
				return
			}

			if html == "" {
				http.Error(w, "Plugin has no HTML interface", http.StatusNotFound)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(html))
			return
		}
	}

	http.Error(w, "Plugin not found", http.StatusNotFound)
}

// Toggle enables or disables a plugin
func (h *PluginHandler) Toggle(w http.ResponseWriter, r *http.Request) {
	pluginName := chi.URLParam(r, "name")

	// Parse request body
	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Check if storage is available
	if h.server.storage == nil {
		http.Error(w, "Storage not available", http.StatusInternalServerError)
		return
	}

	// Verify host prerequisites before persisting anything: a plugin that cannot
	// run must not be stored as enabled (the browser plugin needs Chromium).
	if req.Enabled && h.server.pluginRegistry != nil {
		if err := h.server.pluginRegistry.CanEnable(pluginName); err != nil {
			var notReady *plugins.PluginNotReadyError
			if errors.As(err, &notReady) {
				writeJSON(w, http.StatusConflict, map[string]interface{}{
					"error":   notReady.Reason,
					"code":    "plugin_not_ready",
					"plugin":  pluginName,
					"enabled": false,
				})
				return
			}
			http.Error(w, "Failed to check plugin prerequisites: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Get existing config or create new one
	pluginConfig, err := h.server.storage.GetPluginConfig(pluginName)
	wasEnabled := false
	if err == storage.ErrPluginNotFound {
		pluginConfig = &storage.PluginConfig{
			Enabled: req.Enabled,
			Name:    pluginName,
		}
	} else if err != nil {
		http.Error(w, "Failed to get plugin config: "+err.Error(), http.StatusInternalServerError)
		return
	} else {
		wasEnabled = pluginConfig.Enabled
		pluginConfig.Enabled = req.Enabled
	}

	// Save to storage
	if err := h.server.storage.SetPluginConfig(pluginName, pluginConfig); err != nil {
		http.Error(w, "Failed to save plugin config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Try to dynamically enable/disable the plugin
	restartRequired := false
	if h.server.pluginRegistry != nil {
		ctx := r.Context()
		var err error
		if req.Enabled {
			err = h.server.pluginRegistry.EnablePlugin(ctx, pluginName)
		} else {
			err = h.server.pluginRegistry.DisablePlugin(ctx, pluginName)
		}
		if err != nil {
			// Roll back the stored state, otherwise the plugin would look enabled
			// after a failed start until the next successful toggle.
			pluginConfig.Enabled = wasEnabled
			if rollbackErr := h.server.storage.SetPluginConfig(pluginName, pluginConfig); rollbackErr != nil {
				if h.server.logger != nil {
					h.server.logger.Errorf("Failed to roll back plugin %s config: %v", pluginName, rollbackErr)
				}
			}

			var notReady *plugins.PluginNotReadyError
			if errors.As(err, &notReady) {
				writeJSON(w, http.StatusConflict, map[string]interface{}{
					"error":   notReady.Reason,
					"code":    "plugin_not_ready",
					"plugin":  pluginName,
					"enabled": false,
				})
				return
			}
			http.Error(w, "Failed to toggle plugin: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		restartRequired = true
	}

	response := map[string]interface{}{
		"success":          true,
		"plugin":           pluginName,
		"enabled":          req.Enabled,
		"restart_required": restartRequired,
	}

	writeJSON(w, http.StatusOK, response)
}
