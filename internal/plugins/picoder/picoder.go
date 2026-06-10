// Package picoder provides Pi coding agent integration plugin for PodmanView
package picoder

import (
	"context"
	_ "embed"

	"podmanview/internal/plugins"
)

//go:embed index.html
var htmlContent []byte

// PicoderPlugin integrates pi coding agent into PodmanView
type PicoderPlugin struct {
	*plugins.BasePlugin
	sessions *SessionManager
}

// New creates a new PicoderPlugin instance
func New() *PicoderPlugin {
	return &PicoderPlugin{
		BasePlugin: plugins.NewBasePlugin(
			"picoder",
			"Pi Coding Agent — AI-powered coding sessions inside containers",
			"1.0.0",
			htmlContent,
		),
		sessions: NewSessionManager(),
	}
}

// Init initializes the plugin
func (p *PicoderPlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)
	p.sessions.SetLogger(deps.Logger)
	p.sessions.SetStorage(deps.Storage)
	p.sessions.SetPodmanClient(deps.PodmanClient)

	if err := p.sessions.LoadAll(); err != nil {
		p.Logger().Printf("[%s] Warning: failed to load sessions: %v", p.Name(), err)
	}

	p.Logger().Printf("[%s] Plugin initialized, %d sessions loaded", p.Name(), p.sessions.Count())
	return nil
}

// Start starts the plugin
func (p *PicoderPlugin) Start(ctx context.Context) error {
	p.Logger().Printf("[%s] Plugin started", p.Name())
	return nil
}

// Stop stops the plugin and kills all pi processes
func (p *PicoderPlugin) Stop(ctx context.Context) error {
	p.Logger().Printf("[%s] Stopping all sessions...", p.Name())
	if err := p.sessions.StopAll(); err != nil {
		p.Logger().Printf("[%s] Error stopping sessions: %v", p.Name(), err)
	}
	p.Logger().Printf("[%s] Plugin stopped", p.Name())
	return nil
}

// Routes returns the plugin's HTTP routes
func (p *PicoderPlugin) Routes() []plugins.Route {
	h := NewHandlers(p.sessions, p.Logger())
	return []plugins.Route{
		// Containers & sessions info
		{Method: "GET", Path: "/api/plugins/picoder/containers", Handler: h.ListContainers, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/picoder/containers/{id}/sessions", Handler: h.ContainerSessions, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/picoder/sessions", Handler: h.ListSessions, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/picoder/sessions", Handler: h.CreateSession, RequireAuth: true},
		{Method: "DELETE", Path: "/api/plugins/picoder/sessions/{id}", Handler: h.DeleteSession, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/picoder/sessions/{id}/compact", Handler: h.CompactSession, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/picoder/sessions/{id}/export", Handler: h.ExportSession, RequireAuth: true},
		// WebSocket for live interaction
		{Method: "GET", Path: "/api/plugins/picoder/sessions/{id}/ws", Handler: h.WSProxy, RequireAuth: true},
	}
}

// IsEnabled checks if the plugin is enabled
func (p *PicoderPlugin) IsEnabled() bool {
	if p.Deps() == nil || p.Deps().Storage == nil {
		return false
	}
	enabled, err := p.Deps().Storage.IsPluginEnabled(p.Name())
	if err != nil {
		return false
	}
	return enabled
}
