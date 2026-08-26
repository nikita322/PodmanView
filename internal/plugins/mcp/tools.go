package mcp

// registerTools registers all MCP tools exposed by the plugin.
func (p *MCPServerPlugin) registerTools() {
	p.registerSystemTools()
	p.registerSystemdTools()
	p.registerNetworkTools()
	p.registerPackageTools()
	p.registerCommandTool()
	p.registerPowerTools()
	p.registerPodmanTools()
	p.registerLogsTool()
}
