// Package demo provides a simple demonstration plugin
package demo

import (
	"context"
	"net/http"
	"sync"
	"time"

	"podmanview/internal/plugins"
)

// DemoPlugin is a simple demonstration plugin
type DemoPlugin struct {
	*plugins.BasePlugin
	mu        sync.Mutex
	startTime time.Time
	counter   int
}

// New creates a new DemoPlugin instance
func New() *DemoPlugin {
	return &DemoPlugin{
		BasePlugin: plugins.NewBasePlugin(
			"demo",
			"Simple demonstration plugin",
			"1.0.0",
		),
	}
}

// Init initializes the plugin
func (p *DemoPlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)

	p.LogInfo("Initializing demo plugin")

	// Read settings from configuration (if any)
	message := p.GetPluginSettingOrDefault("MESSAGE", "Hello from Demo Plugin!")
	p.LogInfo("Config message: %s", message)

	return nil
}

// Start starts the plugin
func (p *DemoPlugin) Start(ctx context.Context) error {
	p.startTime = time.Now()
	p.LogInfo("Starting demo plugin at %s", p.startTime)

	// Add event
	p.AddEvent("started", "Demo plugin started successfully")

	return nil
}

// Stop stops the plugin
func (p *DemoPlugin) Stop(ctx context.Context) error {
	p.LogInfo("Stopping demo plugin")
	p.AddEvent("stopped", "Demo plugin stopped")
	return nil
}

// Routes returns the plugin's HTTP routes
func (p *DemoPlugin) Routes() []plugins.Route {
	return []plugins.Route{
		{
			Method:      "GET",
			Path:        "/api/plugins/demo/info",
			Handler:     p.handleInfo,
			RequireAuth: true,
		},
		{
			Method:      "GET",
			Path:        "/api/plugins/demo/ping",
			Handler:     p.handlePing,
			RequireAuth: true,
		},
		{
			Method:      "POST",
			Path:        "/api/plugins/demo/counter",
			Handler:     p.handleCounter,
			RequireAuth: true,
		},
	}
}

// IsEnabled checks if the plugin is enabled
func (p *DemoPlugin) IsEnabled() bool {
	if p.Deps() == nil || p.Deps().Config == nil {
		return false
	}

	enabled := p.Deps().Config.EnabledPlugins()
	for _, name := range enabled {
		if name == p.Name() {
			return true
		}
	}

	return false
}

// HTTP Handlers

func (p *DemoPlugin) handleInfo(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(p.startTime)

	p.mu.Lock()
	currentCounter := p.counter
	p.mu.Unlock()

	// Get non-sensitive settings only (MESSAGE is safe to expose)
	settings := p.Deps().Config.PluginSettings(p.Name())
	safeSettings := make(map[string]string)
	// Only expose whitelisted settings to prevent information disclosure
	if msg, ok := settings["MESSAGE"]; ok {
		safeSettings["MESSAGE"] = msg
	}

	info := map[string]interface{}{
		"name":        p.Name(),
		"version":     p.Version(),
		"description": p.Description(),
		"start_time":  p.startTime.Format(time.RFC3339),
		"uptime":      uptime.String(),
		"counter":     currentCounter,
		"settings":    safeSettings,
	}

	plugins.WriteJSON(w, http.StatusOK, info)
}

func (p *DemoPlugin) handlePing(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().Unix(),
		"message":   "pong",
	}

	plugins.WriteJSON(w, http.StatusOK, response)
}

func (p *DemoPlugin) handleCounter(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.counter++
	newCounter := p.counter
	p.mu.Unlock()

	p.AddEvent("counter_incremented", "Counter incremented")
	p.LogInfo("Counter incremented to %d", newCounter)

	response := map[string]interface{}{
		"counter": newCounter,
	}

	plugins.WriteJSON(w, http.StatusOK, response)
}
