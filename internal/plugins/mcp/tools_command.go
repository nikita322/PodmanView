package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"podmanview/internal/events"
)

type commandRunInput struct {
	Command string `json:"command" jsonschema:"Shell command to execute"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"Timeout in seconds (default: 30, max: 300)"`
	WorkDir string `json:"work_dir,omitempty" jsonschema:"Working directory for the command (default: /)"`
}

// execResult is the structured result of running an external command.
type execResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// runExec runs an external command and captures stdout/stderr separately.
// A non-zero exit code is not treated as an error; the caller inspects
// execResult.ExitCode. An error is returned only if the command could not be
// started at all.
func runExec(ctx context.Context, dir, name string, args ...string) (execResult, error) {
	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	res := execResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, err
	}

	return res, nil
}

// runWithTimeout runs a command with a bounded timeout.
func runWithTimeout(ctx context.Context, timeout time.Duration, dir, name string, args ...string) (execResult, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return runExec(cctx, dir, name, args...)
}

func (p *MCPServerPlugin) registerCommandTool() {
	mcp.AddTool(p.server, newTool("command.run",
		"Run a shell command on the host with a timeout (admin only). Returns exit code, stdout and stderr"),
		func(ctx context.Context, req *mcp.CallToolRequest, in commandRunInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolCommandRun(ctx, in)
			return res, nil, err
		})
}

func (p *MCPServerPlugin) toolCommandRun(ctx context.Context, in commandRunInput) (*mcp.CallToolResult, error) {
	if in.Command == "" {
		return nil, fmt.Errorf("command must not be empty")
	}

	timeout := in.Timeout
	if timeout == 0 {
		timeout = 30
	}
	if timeout < 1 {
		timeout = 1
	}
	if timeout > 300 {
		timeout = 300
	}

	workDir := in.WorkDir
	if workDir == "" {
		workDir = "/"
	}

	res, err := runWithTimeout(ctx, time.Duration(timeout)*time.Second, workDir, "sh", "-c", in.Command)
	if err != nil {
		p.logEvent(ctx, events.EventMCPCommand, false, fmt.Sprintf("command=%q error=%v", in.Command, err))
		return nil, fmt.Errorf("failed to run command: %w", err)
	}

	p.logEvent(ctx, events.EventMCPCommand, res.ExitCode == 0, fmt.Sprintf("command=%q exit=%d", in.Command, res.ExitCode))

	return textResult(map[string]interface{}{
		"command":  in.Command,
		"workDir":  workDir,
		"exitCode": res.ExitCode,
		"stdout":   res.Stdout,
		"stderr":   res.Stderr,
	}), nil
}
