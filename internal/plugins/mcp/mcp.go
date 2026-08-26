// Package mcp provides an MCP (Model Context Protocol) server plugin that
// exposes system management tools to AI clients over Streamable HTTP and SSE.
package mcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
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
	sseHandler http.Handler
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

	// SSE transport for clients that do not support streamable HTTP (e.g.
	// Home Assistant). Exposed at /sse and restricted to the local network.
	p.sseHandler = mcp.NewSSEHandler(func(_ *http.Request) *mcp.Server {
		return p.server
	}, nil)

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin initialized (endpoints: /mcp, /sse)", p.Name())
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
// All three methods are registered so the MCP streamable HTTP handler can
// answer them per spec. In stateless mode the SDK handler accepts only POST
// and responds with 405 Method Not Allowed to GET and DELETE, which also
// prevents the SPA fallback from serving the web UI at /mcp.
func (p *MCPServerPlugin) Routes() []plugins.Route {
	mcpHandler := http.HandlerFunc(p.handleMCP)
	sseHandler := http.HandlerFunc(p.handleSSE)

	return []plugins.Route{
		{Method: "GET", Path: "/mcp", Handler: mcpHandler},
		{Method: "POST", Path: "/mcp", Handler: mcpHandler},
		{Method: "DELETE", Path: "/mcp", Handler: mcpHandler},

		// SSE transport: a hanging GET opens the event stream and POSTs to
		// /sse?sessionid=... carry client messages. LAN-only, no token auth.
		{Method: "GET", Path: "/sse", Handler: sseHandler},
		{Method: "POST", Path: "/sse", Handler: sseHandler},
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

// handleSSE serves the SSE transport endpoint. Unlike /mcp it has no
// token/bearer auth (Home Assistant cannot send one), so access is limited to
// the local network only. Bare POSTs (no sessionid) are served by the
// stateless streamable-HTTP handler, so clients can use either transport on
// the same /sse URL.
func (p *MCPServerPlugin) handleSSE(w http.ResponseWriter, r *http.Request) {
	if p.sseHandler == nil {
		http.Error(w, "Plugin not initialized", http.StatusServiceUnavailable)
		return
	}

	if !isLANRequest(r) {
		http.Error(w, "Forbidden: /sse is only reachable from the local network", http.StatusForbidden)
		return
	}

	// Streamable-HTTP clients send a bare POST (no sessionid). Serve them with
	// the stateless streamable handler directly, so clients such as Home
	// Assistant do not need the (fragile) 405 -> SSE fallback. SSE clients
	// GET the stream and POST messages to /sse?sessionid=..., which is handled
	// by the SSE handler below.
	if r.Method == http.MethodPost && r.URL.Query().Get("sessionid") == "" {
		p.mcpHandler.ServeHTTP(w, r)
		return
	}

	p.sseHandler.ServeHTTP(w, r)
}

// isLANRequest reports whether the request originates from a loopback or
// private (RFC 1918 / ULA) address. It inspects the direct peer address
// rather than proxy headers, so it cannot be spoofed by an external client.
func isLANRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
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
