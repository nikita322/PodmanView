package mcp

import (
	"context"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"podmanview/internal/events"
)

type powerRebootInput struct{}

type powerShutdownInput struct{}

func (p *MCPServerPlugin) registerPowerTools() {
	mcp.AddTool(p.server, newTool("power.reboot",
		"Reboot the host (admin only)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in powerRebootInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPowerAction(ctx, "reboot", events.EventSystemReboot)
			return res, nil, err
		})

	mcp.AddTool(p.server, newTool("power.shutdown",
		"Shut down the host (admin only)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in powerShutdownInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPowerAction(ctx, "poweroff", events.EventSystemShutdown)
			return res, nil, err
		})
}

func (p *MCPServerPlugin) toolPowerAction(ctx context.Context, action string, eventType events.EventType) (*mcp.CallToolResult, error) {
	p.logEvent(ctx, eventType, true, action)

	// Respond first, then run the action in the background so the response is
	// delivered before the host goes down.
	go func() {
		exec.Command("systemctl", action).Run()
	}()

	return textResult(map[string]interface{}{
		"status": action + " initiated",
	}), nil
}
