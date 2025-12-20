// Package demo provides a simple demonstration plugin
package demo

import (
	"context"
	"net/http"
	"path/filepath"
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
	// Get the path to the HTML file relative to this plugin's directory
	htmlPath := filepath.Join("internal", "plugins", "demo", "index.html")

	return &DemoPlugin{
		BasePlugin: plugins.NewBasePlugin(
			"demo",
			"Simple demonstration plugin",
			"1.0.0",
			htmlPath,
		),
	}
}

// Init initializes the plugin
func (p *DemoPlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)

	// Load counter from storage
	if deps.Storage != nil {
		counter, err := deps.Storage.GetInt(p.Name(), "counter")
		if err == nil {
			p.mu.Lock()
			p.counter = counter
			p.mu.Unlock()
			deps.Logger.Printf("[%s] Loaded counter from storage: %d", p.Name(), counter)
		}
	}

	return nil
}

// Start starts the plugin
func (p *DemoPlugin) Start(ctx context.Context) error {
	p.startTime = time.Now()
	return nil
}

// Stop stops the plugin
func (p *DemoPlugin) Stop(ctx context.Context) error {
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
	if p.Deps() == nil || p.Deps().Storage == nil {
		return false
	}
	enabled, err := p.Deps().Storage.IsPluginEnabled(p.Name())
	if err != nil {
		return false
	}
	return enabled
}

// HTTP Handlers

func (p *DemoPlugin) handleInfo(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	currentCounter := p.counter
	p.mu.Unlock()

	info := map[string]interface{}{
		"name":        p.Name(),
		"version":     p.Version(),
		"description": p.Description(),
		"start_time":  p.startTime.Format(time.RFC3339),
		"uptime":      time.Since(p.startTime).String(),
		"counter":     currentCounter,
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

	// Save counter to storage
	if p.Deps().Storage != nil {
		if err := p.Deps().Storage.SetInt(p.Name(), "counter", newCounter); err != nil {
			p.LogError("Failed to save counter to storage: %v", err)
		}
	}

	response := map[string]interface{}{
		"counter": newCounter,
	}

	plugins.WriteJSON(w, http.StatusOK, response)
}
