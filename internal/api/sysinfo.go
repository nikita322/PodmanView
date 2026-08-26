package api

import "podmanview/internal/sysinfo"

// Type aliases preserve the existing HTTP API surface while the actual
// implementations live in internal/sysinfo so they can be reused by plugins
// (e.g. the MCP plugin) without importing the api package.
type (
	HostStats   = sysinfo.HostStats
	DiskInfo    = sysinfo.DiskInfo
	StorageTemp = sysinfo.StorageTemp
	Temperature = sysinfo.Temperature
)

// GetHostStats returns current host statistics (CPU, memory, disks, uptime).
func GetHostStats() *HostStats {
	return sysinfo.GetHostStats()
}
