package mcp

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"podmanview/internal/events"
)

type packagesListInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Optional substring to filter package names"`
}

type packagesInstallInput struct {
	Packages []string `json:"packages" jsonschema:"Package names to install"`
}

type packagesUpdateInput struct{}

func (p *MCPServerPlugin) registerPackageTools() {
	mcp.AddTool(p.server, newReadOnlyTool("packages.list",
		"List installed system packages (apt or dnf, auto-detected)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in packagesListInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPackagesList(ctx, in.Filter)
			return res, nil, err
		})

	mcp.AddTool(p.server, newTool("packages.install",
		"Install one or more system packages using the detected package manager (apt or dnf)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in packagesInstallInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPackagesInstall(ctx, in)
			return res, nil, err
		})

	mcp.AddTool(p.server, newTool("packages.update",
		"Update all system packages to the latest versions (apt or dnf, auto-detected)"),
		func(ctx context.Context, req *mcp.CallToolRequest, in packagesUpdateInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolPackagesUpdate(ctx)
			return res, nil, err
		})
}

// detectPackageManager returns "apt" or "dnf" based on what is available.
func detectPackageManager() string {
	if _, err := exec.LookPath("apt-get"); err == nil {
		return "apt"
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		return "dnf"
	}
	return ""
}

func (p *MCPServerPlugin) toolPackagesList(ctx context.Context, filter string) (*mcp.CallToolResult, error) {
	pm := detectPackageManager()
	if pm == "" {
		return nil, fmt.Errorf("no supported package manager found (apt-get or dnf)")
	}

	var res execResult
	var err error
	switch pm {
	case "apt":
		res, err = runWithTimeout(ctx, 30*time.Second, "", "dpkg-query", "-W", "-f=${Package}\\t${Version}\\n")
	case "dnf":
		res, err = runWithTimeout(ctx, 30*time.Second, "", "rpm", "-qa", "--qf", "%{NAME}\\t%{VERSION}-%{RELEASE}\\n")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list packages: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("package list failed: %s", strings.TrimSpace(res.Stderr))
	}

	packages := []map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		name := parts[0]
		version := ""
		if len(parts) == 2 {
			version = parts[1]
		}
		if filter != "" && !strings.Contains(name, filter) {
			continue
		}
		packages = append(packages, map[string]string{"name": name, "version": version})
	}

	sort.Slice(packages, func(i, j int) bool {
		return packages[i]["name"] < packages[j]["name"]
	})

	return textResult(map[string]interface{}{
		"packageManager": pm,
		"count":          len(packages),
		"packages":       packages,
	}), nil
}

func (p *MCPServerPlugin) toolPackagesInstall(ctx context.Context, in packagesInstallInput) (*mcp.CallToolResult, error) {
	if len(in.Packages) == 0 {
		return nil, fmt.Errorf("at least one package name is required")
	}

	pm := detectPackageManager()
	if pm == "" {
		return nil, fmt.Errorf("no supported package manager found (apt-get or dnf)")
	}

	var args []string
	switch pm {
	case "apt":
		args = append([]string{"install", "-y"}, in.Packages...)
	case "dnf":
		args = append([]string{"install", "-y"}, in.Packages...)
	}

	res, err := runWithTimeout(ctx, 10*time.Minute, "", packageManagerCommand(pm), args...)
	if err != nil {
		p.logEvent(ctx, events.EventMCPPackage, false, fmt.Sprintf("install=%v error=%v", in.Packages, err))
		return nil, fmt.Errorf("failed to install packages: %w", err)
	}

	p.logEvent(ctx, events.EventMCPPackage, res.ExitCode == 0, fmt.Sprintf("install=%v exit=%d", in.Packages, res.ExitCode))

	output := strings.TrimSpace(res.Stdout)
	if output == "" {
		output = strings.TrimSpace(res.Stderr)
	}

	return textResult(map[string]interface{}{
		"packageManager": pm,
		"packages":       in.Packages,
		"exitCode":       res.ExitCode,
		"output":         output,
	}), nil
}

func (p *MCPServerPlugin) toolPackagesUpdate(ctx context.Context) (*mcp.CallToolResult, error) {
	pm := detectPackageManager()
	if pm == "" {
		return nil, fmt.Errorf("no supported package manager found (apt-get or dnf)")
	}

	var res execResult
	var err error
	switch pm {
	case "apt":
		// Refresh indexes first, then upgrade.
		res, err = runWithTimeout(ctx, 10*time.Minute, "", "sh", "-c", "apt-get update && apt-get upgrade -y")
	case "dnf":
		res, err = runWithTimeout(ctx, 10*time.Minute, "", "dnf", "upgrade", "-y")
	}
	if err != nil {
		p.logEvent(ctx, events.EventMCPPackage, false, fmt.Sprintf("update error=%v", err))
		return nil, fmt.Errorf("failed to update packages: %w", err)
	}

	p.logEvent(ctx, events.EventMCPPackage, res.ExitCode == 0, fmt.Sprintf("update exit=%d", res.ExitCode))

	output := strings.TrimSpace(res.Stdout)
	if output == "" {
		output = strings.TrimSpace(res.Stderr)
	}

	return textResult(map[string]interface{}{
		"packageManager": pm,
		"exitCode":       res.ExitCode,
		"output":         output,
	}), nil
}

// packageManagerCommand returns the executable name for a package manager.
func packageManagerCommand(pm string) string {
	if pm == "apt" {
		return "apt-get"
	}
	return "dnf"
}
