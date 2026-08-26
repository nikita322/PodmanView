package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type logsJournalInput struct {
	Unit  string `json:"unit,omitempty" jsonschema:"Optional systemd unit filter, e.g. sshd.service"`
	Lines int    `json:"lines,omitempty" jsonschema:"Number of log lines to return (default: 100, max: 1000)"`
}

func (p *MCPServerPlugin) registerLogsTool() {
	mcp.AddTool(p.server, newReadOnlyTool("logs.journalctl",
		"Read systemd journal logs (optionally filtered by unit)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in logsJournalInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolLogsJournal(ctx, in)
			return res, nil, err
		})
}

func (p *MCPServerPlugin) toolLogsJournal(ctx context.Context, in logsJournalInput) (*mcp.CallToolResult, error) {
	lines := in.Lines
	if lines == 0 {
		lines = 100
	}
	if lines < 1 {
		lines = 1
	}
	if lines > 1000 {
		lines = 1000
	}

	args := []string{
		"--no-pager",
		"-n", fmt.Sprintf("%d", lines),
	}

	if in.Unit != "" {
		args = append(args, "-u", in.Unit)
	}

	res, err := runWithTimeout(ctx, 30*time.Second, "", "journalctl", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to read journal: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("journalctl exited with code %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	return textResult(map[string]interface{}{
		"unit":  in.Unit,
		"lines": lines,
		"logs":  strings.TrimSpace(res.Stdout),
	}), nil
}
