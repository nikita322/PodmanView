package mcp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"podmanview/internal/events"
	"podmanview/internal/sensors"
	"podmanview/internal/sysinfo"
)

type systemInfoInput struct{}

type systemResourcesInput struct{}

type systemProcessesInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Optional substring to filter by process name or command"`
}

type systemKillProcessInput struct {
	PID    int `json:"pid" jsonschema:"Process ID to signal"`
	Signal int `json:"signal,omitempty" jsonschema:"Signal number (default: 15/SIGTERM)"`
}

func (p *MCPServerPlugin) registerSystemTools() {
	mcp.AddTool(p.server, newReadOnlyTool("system.info",
		"Get host information: OS, kernel, architecture, hostname, Podman version and uptime"),
		func(ctx context.Context, req *mcp.CallToolRequest, in systemInfoInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolSystemInfo(ctx)
			return res, nil, err
		})

	mcp.AddTool(p.server, newReadOnlyTool("system.resources",
		"Get current resource usage: CPU, memory, disks, uptime and temperatures"),
		func(ctx context.Context, req *mcp.CallToolRequest, in systemResourcesInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolSystemResources()
			return res, nil, err
		})

	mcp.AddTool(p.server, newReadOnlyTool("system.processes",
		"List running processes (PID, name, state, resident memory)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in systemProcessesInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolSystemProcesses(in.Filter)
			return res, nil, err
		})

	mcp.AddTool(p.server, newTool("system.kill_process",
		"Send a signal to a process by PID (default SIGTERM)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in systemKillProcessInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolSystemKillProcess(ctx, in)
			return res, nil, err
		})
}

func (p *MCPServerPlugin) toolSystemInfo(ctx context.Context) (*mcp.CallToolResult, error) {
	host := map[string]string{}
	podmanVersion := ""

	if deps := p.Deps(); deps != nil && deps.PodmanClient != nil {
		if info, err := deps.PodmanClient.GetSystemInfo(ctx); err == nil {
			host["hostname"] = info.Host.Hostname
			host["arch"] = info.Host.Arch
			host["kernel"] = info.Host.Kernel
			podmanVersion = info.Version.Version
		}
	}

	stats := sysinfo.GetHostStats()

	return textResult(map[string]interface{}{
		"os":            readOSRelease(),
		"host":          host,
		"podmanVersion": podmanVersion,
		"uptimeSeconds": stats.Uptime,
	}), nil
}

func (p *MCPServerPlugin) toolSystemResources() (*mcp.CallToolResult, error) {
	stats := sysinfo.GetHostStats()
	stats.Temperatures = sensors.GetCPUTemperatures()
	stats.StorageTemps = sensors.GetNVMeTemperaturesGrouped()

	return textResult(stats), nil
}

type processInfo struct {
	PID      int    `json:"pid"`
	Name     string `json:"name"`
	State    string `json:"state"`
	MemoryKB uint64 `json:"memoryKb"`
}

func (p *MCPServerPlugin) toolSystemProcesses(filter string) (*mcp.CallToolResult, error) {
	processes := listProcesses(filter)
	if processes == nil {
		processes = []processInfo{}
	}
	return textResult(map[string]interface{}{
		"count":     len(processes),
		"processes": processes,
	}), nil
}

func (p *MCPServerPlugin) toolSystemKillProcess(ctx context.Context, in systemKillProcessInput) (*mcp.CallToolResult, error) {
	if in.PID <= 0 {
		return nil, fmt.Errorf("pid must be a positive integer")
	}

	signal := in.Signal
	if signal == 0 {
		signal = int(syscall.SIGTERM)
	}
	if signal < 1 || signal > 64 {
		return nil, fmt.Errorf("signal must be between 1 and 64")
	}

	err := syscall.Kill(in.PID, syscall.Signal(signal))
	p.logEvent(ctx, events.EventMCPProcess, err == nil, fmt.Sprintf("kill pid=%d signal=%d", in.PID, signal))
	if err != nil {
		return nil, fmt.Errorf("failed to signal process %d: %w", in.PID, err)
	}

	return textResult(map[string]interface{}{
		"status": "signal sent",
		"pid":    in.PID,
		"signal": signal,
	}), nil
}

// readOSRelease parses /etc/os-release into a map.
func readOSRelease() map[string]string {
	values := map[string]string{}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return values
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		val := strings.Trim(parts[1], `"`)
		values[key] = val
	}

	return values
}

// listProcesses reads /proc and returns process information, optionally
// filtered by a substring in the process name.
func listProcesses(filter string) []processInfo {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	processes := make([]processInfo, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		name, state := readProcessStat(pid)
		if name == "" {
			continue
		}

		if filter != "" && !strings.Contains(name, filter) {
			continue
		}

		processes = append(processes, processInfo{
			PID:      pid,
			Name:     name,
			State:    state,
			MemoryKB: readProcessMemory(pid),
		})
	}

	sort.Slice(processes, func(i, j int) bool {
		return processes[i].PID < processes[j].PID
	})

	return processes
}

// readProcessStat reads the process name and state from /proc/<pid>/stat.
func readProcessStat(pid int) (name, state string) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", ""
	}

	content := string(data)

	// The comm field is the second field and is wrapped in parentheses.
	open := strings.Index(content, "(")
	closeIdx := strings.LastIndex(content, ")")
	if open < 0 || closeIdx <= open {
		return "", ""
	}
	name = content[open+1 : closeIdx]

	// The state is the field right after the closing parenthesis.
	rest := strings.TrimSpace(content[closeIdx+1:])
	fields := strings.Fields(rest)
	if len(fields) > 0 {
		state = fields[0]
	}

	return name, state
}

// readProcessMemory reads the resident memory (VmRSS) from /proc/<pid>/status.
func readProcessMemory(pid int) uint64 {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb
			}
		}
	}

	return 0
}
