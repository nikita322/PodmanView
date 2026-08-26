// Package mcp provides an MCP (Model Context Protocol) server plugin that
// exposes system management tools to AI clients over Streamable HTTP.
package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"podmanview/internal/auth"
	"podmanview/internal/events"
	"podmanview/internal/plugins"
)

//go:embed index.html
var htmlContent []byte

// MCPServerPlugin exposes an MCP server over HTTP.
type MCPServerPlugin struct {
	*plugins.BasePlugin

	server     *mcp.Server
	mcpHandler http.Handler
	jwtManager *auth.JWTManager
	authMw     *auth.Middleware
}

// New creates a new MCP server plugin instance.
func New() *MCPServerPlugin {
	p := &MCPServerPlugin{
		BasePlugin: plugins.NewBasePlugin(
			"mcp",
			"MCP server for AI agents: system, systemd, network, packages, Podman and command tools",
			"1.0.0",
			htmlContent,
		),
		server: mcp.NewServer(&mcp.Implementation{
			Name:    "podmanview-mcp",
			Version: "1.0.0",
		}, nil),
	}

	p.registerTools()

	return p
}

// Init initializes the plugin.
func (p *MCPServerPlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)

	if deps.Config != nil {
		p.jwtManager = auth.NewJWTManager(deps.Config.JWTSecret(), deps.Config.JWTExpiration())
		p.authMw = auth.NewMiddleware(p.jwtManager)
	}

	// Stateless mode: each request is served in a temporary session, so no
	// Mcp-Session-Id header or initialize/initialized handshake is required.
	p.mcpHandler = mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
		return p.server
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin initialized (endpoint: /mcp)", p.Name())
	}
	return nil
}

// Start starts the plugin.
func (p *MCPServerPlugin) Start(ctx context.Context) error {
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin started", p.Name())
	}
	return nil
}

// Stop stops the plugin.
func (p *MCPServerPlugin) Stop(ctx context.Context) error {
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin stopped", p.Name())
	}
	return nil
}

// Routes returns the MCP endpoint routes.
// In stateless mode only POST is supported: GET and DELETE (SSE stream and
// session termination) are not applicable.
func (p *MCPServerPlugin) Routes() []plugins.Route {
	handler := http.HandlerFunc(p.handleMCP)

	return []plugins.Route{
		{Method: "POST", Path: "/mcp", Handler: handler},
	}
}

// IsEnabled checks whether the plugin is enabled in storage.
func (p *MCPServerPlugin) IsEnabled() bool {
	if p.Deps() == nil || p.Deps().Storage == nil {
		return false
	}
	enabled, err := p.Deps().Storage.IsPluginEnabled(p.Name())
	if err != nil {
		return false
	}
	return enabled
}

// handleMCP serves the MCP endpoint. It is admin-only unless authentication is
// disabled. The plugin-enabled gate is applied by the router before this
// handler runs.
func (p *MCPServerPlugin) handleMCP(w http.ResponseWriter, r *http.Request) {
	deps := p.Deps()
	if deps == nil || deps.Config == nil {
		http.Error(w, "Plugin not initialized", http.StatusServiceUnavailable)
		return
	}

	if p.mcpHandler == nil {
		http.Error(w, "Plugin not initialized", http.StatusServiceUnavailable)
		return
	}

	// In no-auth mode the whole server is unprotected, so serve directly.
	if deps.Config.NoAuth() {
		p.mcpHandler.ServeHTTP(w, r)
		return
	}

	if p.authMw == nil {
		http.Error(w, "Authentication not initialized", http.StatusServiceUnavailable)
		return
	}

	p.authMw.RequireAdminOrToken(deps.Config.APIToken(), p.mcpHandler).ServeHTTP(w, r)
}

// logEvent records an audit event for a mutating MCP operation.
func (p *MCPServerPlugin) logEvent(ctx context.Context, eventType events.EventType, success bool, details string) {
	deps := p.Deps()
	if deps == nil || deps.EventStore == nil {
		return
	}

	username := "mcp"
	if user := auth.GetUserFromContext(ctx); user != nil {
		username = user.Username
	}

	if len(details) > 500 {
		details = details[:500] + "..."
	}

	deps.EventStore.Add(eventType, username, "", success, details)
}

// textResult marshals a value as an indented JSON text result.
func textResult(v interface{}) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return textResultError(fmt.Sprintf("failed to encode result: %v", err))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}
}

// textResultError returns a text result describing an error that occurred
// while producing a result (not a tool execution error).
func textResultError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// newTool creates a tool definition.
func newTool(name, description string) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: description}
}

// newReadOnlyTool creates a tool definition marked as read-only.
func newReadOnlyTool(name, description string) *mcp.Tool {
	t := newTool(name, description)
	t.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	return t
}
