package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"podmanview/internal/events"
)

type podmanListInput struct {
	IncludeStats bool `json:"include_stats,omitempty" jsonschema:"Include live CPU/memory/network stats for running containers"`
}

type podmanManageInput struct {
	ID     string `json:"id" jsonschema:"Container ID or name"`
	Action string `json:"action" jsonschema:"Action: start, stop, restart or remove"`
}

type podmanLogsInput struct {
	ID   string `json:"id" jsonschema:"Container ID or name"`
	Tail int    `json:"tail,omitempty" jsonschema:"Number of lines to return (default: 100, max: 2000)"`
}

func (p *MCPServerPlugin) registerPodmanTools() {
	mcp.AddTool(p.server, newReadOnlyTool("podman.list",
		"List Podman containers and images (optionally with live container stats)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in podmanListInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPodmanList(ctx, in.IncludeStats)
			return res, nil, err
		})

	mcp.AddTool(p.server, newTool("podman.manage",
		"Manage a Podman container: start, stop, restart or remove"),
		func(ctx context.Context, req *mcp.CallToolRequest, in podmanManageInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPodmanManage(ctx, in)
			return res, nil, err
		})

	mcp.AddTool(p.server, newReadOnlyTool("podman.logs",
		"Get logs from a Podman container"),
		func(ctx context.Context, req *mcp.CallToolRequest, in podmanLogsInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPodmanLogs(ctx, in)
			return res, nil, err
		})
}

func (p *MCPServerPlugin) toolPodmanList(ctx context.Context, includeStats bool) (*mcp.CallToolResult, error) {
	deps := p.Deps()
	if deps == nil || deps.PodmanClient == nil {
		return nil, fmt.Errorf("podman client is not available")
	}

	containers, err := deps.PodmanClient.ListContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	images, err := deps.PodmanClient.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	result := map[string]interface{}{
		"containers": containers,
		"images":     images,
	}

	if includeStats {
		if stats, err := deps.PodmanClient.GetContainersStats(ctx); err == nil {
			result["stats"] = stats
		} else {
			result["statsError"] = err.Error()
		}
	}

	return textResult(result), nil
}

func (p *MCPServerPlugin) toolPodmanManage(ctx context.Context, in podmanManageInput) (*mcp.CallToolResult, error) {
	if in.ID == "" {
		return nil, fmt.Errorf("container id must not be empty")
	}

	deps := p.Deps()
	if deps == nil || deps.PodmanClient == nil {
		return nil, fmt.Errorf("podman client is not available")
	}

	var err error
	var eventType events.EventType

	switch in.Action {
	case "start":
		err = deps.PodmanClient.StartContainer(ctx, in.ID)
		eventType = events.EventContainerStart
	case "stop":
		err = deps.PodmanClient.StopContainer(ctx, in.ID)
		eventType = events.EventContainerStop
	case "restart":
		err = deps.PodmanClient.RestartContainer(ctx, in.ID)
		eventType = events.EventContainerRestart
	case "remove":
		err = deps.PodmanClient.RemoveContainer(ctx, in.ID, true)
		eventType = events.EventContainerRemove
	default:
		return nil, fmt.Errorf("action must be one of: start, stop, restart, remove")
	}

	p.logEvent(ctx, eventType, err == nil, fmt.Sprintf("container=%s action=%s", in.ID, in.Action))
	if err != nil {
		return nil, fmt.Errorf("failed to %s container %s: %w", in.Action, in.ID, err)
	}

	return textResult(map[string]interface{}{
		"status": "ok",
		"id":     in.ID,
		"action": in.Action,
	}), nil
}

func (p *MCPServerPlugin) toolPodmanLogs(ctx context.Context, in podmanLogsInput) (*mcp.CallToolResult, error) {
	if in.ID == "" {
		return nil, fmt.Errorf("container id must not be empty")
	}

	tail := in.Tail
	if tail == 0 {
		tail = 100
	}
	if tail < 1 {
		tail = 1
	}
	if tail > 2000 {
		tail = 2000
	}

	deps := p.Deps()
	if deps == nil || deps.PodmanClient == nil {
		return nil, fmt.Errorf("podman client is not available")
	}

	logs, err := deps.PodmanClient.GetContainerLogs(ctx, in.ID, tail)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs for %s: %w", in.ID, err)
	}

	return textResult(map[string]interface{}{
		"id":   in.ID,
		"tail": tail,
		"logs": logs,
	}), nil
}
