package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"podmanview/internal/events"
)

type systemdListUnitsInput struct {
	Type   string `json:"type,omitempty" jsonschema:"Unit type filter: service, timer, socket, mount, target, ..."`
	Filter string `json:"filter,omitempty" jsonschema:"Optional substring to filter unit names"`
}

type systemdManageUnitInput struct {
	Unit   string `json:"unit" jsonschema:"Systemd unit name, e.g. sshd.service"`
	Action string `json:"action" jsonschema:"Action: start, stop, restart, reload, enable, disable or status"`
}

func (p *MCPServerPlugin) registerSystemdTools() {
	mcp.AddTool(p.server, newReadOnlyTool("systemd.list_units",
		"List systemd units and their states"),
		func(ctx context.Context, req *mcp.CallToolRequest, in systemdListUnitsInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolSystemdListUnits(ctx, in)
			return res, nil, err
		})

	mcp.AddTool(p.server, newTool("systemd.manage_unit",
		"Manage a systemd unit: start, stop, restart, reload, enable, disable or show status"),
		func(ctx context.Context, req *mcp.CallToolRequest, in systemdManageUnitInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolSystemdManageUnit(ctx, in)
			return res, nil, err
		})
}

func (p *MCPServerPlugin) toolSystemdListUnits(ctx context.Context, in systemdListUnitsInput) (*mcp.CallToolResult, error) {
	args := []string{"list-units", "--all", "--no-legend", "--no-pager", "--plain"}
	if in.Type != "" {
		args = append(args, "--type="+in.Type)
	}

	res, err := runWithTimeout(ctx, 30*time.Second, "", "systemctl", args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list systemd units: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("systemctl exited with code %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}

	units := parseSystemctlUnits(res.Stdout, in.Filter)

	return textResult(map[string]interface{}{
		"count": len(units),
		"units": units,
	}), nil
}

var allowedSystemdActions = map[string]bool{
	"start": true, "stop": true, "restart": true, "reload": true,
	"enable": true, "disable": true, "status": true,
}

func (p *MCPServerPlugin) toolSystemdManageUnit(ctx context.Context, in systemdManageUnitInput) (*mcp.CallToolResult, error) {
	if in.Unit == "" {
		return nil, fmt.Errorf("unit must not be empty")
	}
	if !allowedSystemdActions[in.Action] {
		return nil, fmt.Errorf("action must be one of: start, stop, restart, reload, enable, disable, status")
	}

	var args []string
	if in.Action == "status" {
		args = []string{"--no-pager", "status", in.Unit}
	} else {
		args = []string{in.Action, in.Unit}
	}

	res, err := runWithTimeout(ctx, 120*time.Second, "", "systemctl", args...)
	if err != nil {
		p.logEvent(ctx, events.EventMCPSystemd, false, fmt.Sprintf("unit=%s action=%s error=%v", in.Unit, in.Action, err))
		return nil, fmt.Errorf("failed to %s unit %s: %w", in.Action, in.Unit, err)
	}

	// "status" is a read-only query; only log mutating actions.
	if in.Action != "status" {
		p.logEvent(ctx, events.EventMCPSystemd, res.ExitCode == 0, fmt.Sprintf("unit=%s action=%s exit=%d", in.Unit, in.Action, res.ExitCode))
	}

	output := strings.TrimSpace(res.Stdout)
	if output == "" {
		output = strings.TrimSpace(res.Stderr)
	}

	return textResult(map[string]interface{}{
		"unit":     in.Unit,
		"action":   in.Action,
		"exitCode": res.ExitCode,
		"output":   output,
	}), nil
}

// systemdUnit represents a single line from `systemctl list-units`.
type systemdUnit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

// parseSystemctlUnits parses `systemctl list-units --no-legend --plain` output.
// Columns are: UNIT LOAD ACTIVE SUB DESCRIPTION (description may contain spaces).
func parseSystemctlUnits(output, filter string) []systemdUnit {
	units := []systemdUnit{}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		unit := systemdUnit{
			Unit:   fields[0],
			Load:   fields[1],
			Active: fields[2],
			Sub:    fields[3],
		}
		if len(fields) > 4 {
			unit.Description = strings.Join(fields[4:], " ")
		}

		if filter != "" && !strings.Contains(unit.Unit, filter) {
			continue
		}

		units = append(units, unit)
	}

	return units
}
