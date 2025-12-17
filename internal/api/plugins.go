package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
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

	plugins := make([]map[string]interface{}, 0, len(h.server.plugins))

	for _, plugin := range h.server.plugins {
		pluginInfo := map[string]interface{}{
			"name":        plugin.Name(),
			"description": plugin.Description(),
			"version":     plugin.Version(),
			"enabled":     plugin.IsEnabled(),
		}
		plugins = append(plugins, pluginInfo)
	}

	writeJSON(w, http.StatusOK, plugins)
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
