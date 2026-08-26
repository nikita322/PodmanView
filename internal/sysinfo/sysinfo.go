// Package sysinfo provides host statistics readers (CPU, memory, disks, uptime)
// shared between the HTTP API and plugins such as the MCP server.
package sysinfo

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"podmanview/internal/sensors"
)

// HostStats represents CPU, memory, temperature, uptime and disk info
type HostStats struct {
	CPUUsage     float64       `json:"cpuUsage"`
	MemTotal     uint64        `json:"memTotal"`               // bytes
	MemFree      uint64        `json:"memFree"`                // bytes (MemAvailable from /proc/meminfo)
	Temperatures []Temperature `json:"temperatures"`           // CPU/SoC temperatures
	StorageTemps []StorageTemp `json:"storageTemps,omitempty"` // NVMe/Storage temperatures grouped by device
	Uptime       int64         `json:"uptime"`                 // seconds
	DiskTotal    uint64        `json:"diskTotal"`              // bytes (deprecated, kept for compatibility)
	DiskFree     uint64        `json:"diskFree"`               // bytes (deprecated, kept for compatibility)
	Disks        []DiskInfo    `json:"disks,omitempty"`        // All disks info
}

// DiskInfo represents disk usage information
type DiskInfo struct {
	Device     string `json:"device"`     // Device name (e.g., nvme0n1, sda)
	MountPoint string `json:"mountPoint"` // Mount point path
	Total      uint64 `json:"total"`      // Total size in bytes
	Free       uint64 `json:"free"`       // Free space in bytes
	Used       uint64 `json:"used"`       // Used space in bytes
}

// Temperature and StorageTemp are shared with the sensors package so that
// host statistics and sensor readers use a single set of types.
type Temperature = sensors.Temperature
type StorageTemp = sensors.StorageTemp

// GetHostStats reads CPU usage, memory, uptime and disk info from /sys and /proc.
// Temperature monitoring has been moved to the temperature plugin, so the
// temperature fields are returned empty here and populated by consumers that
// have access to the plugin registry.
func GetHostStats() *HostStats {
	stats := &HostStats{
		Temperatures: []Temperature{},
		StorageTemps: []StorageTemp{},
		Disks:        []DiskInfo{},
	}

	stats.CPUUsage = getCPUUsage()
	stats.MemTotal, stats.MemFree = getMemoryInfo()
	stats.Uptime = getUptime()
	stats.Disks = getAllDisksUsage()

	// Keep backward compatibility - use root disk for DiskTotal/DiskFree
	stats.DiskTotal, stats.DiskFree = getDiskUsage("/")

	return stats
}

// getMemoryInfo reads memory info from /proc/meminfo.
// Returns MemTotal and MemAvailable (as "free" - more useful than actual MemFree).
func getMemoryInfo() (uint64, uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}

	var memTotal, memAvailable uint64
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		// Values in /proc/meminfo are in kB
		value *= 1024

		switch fields[0] {
		case "MemTotal:":
			memTotal = value
		case "MemAvailable:":
			memAvailable = value
		}
	}

	return memTotal, memAvailable
}

// getDiskUsage returns total and free disk space for a path.
func getDiskUsage(path string) (uint64, uint64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return total, free
}

// getUptime reads system uptime from /proc/uptime.
func getUptime() int64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}

	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return 0
	}

	uptime, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}

	return int64(uptime)
}

// CPU stats for delta calculation
var (
	cpuMu        sync.Mutex
	prevTotal    int64
	prevIdle     int64
	prevTime     time.Time
	lastCPUUsage float64
)

// getCPUUsage calculates real CPU usage from /proc/stat.
// Returns percentage (0-100).
func getCPUUsage() float64 {
	total, idle := readCPUStat()
	if total == 0 {
		return lastCPUUsage
	}

	cpuMu.Lock()
	defer cpuMu.Unlock()

	now := time.Now()

	// Need previous reading to calculate delta
	if prevTime.IsZero() {
		prevTotal = total
		prevIdle = idle
		prevTime = now
		return 0
	}

	// Calculate delta since last reading
	totalDelta := total - prevTotal
	idleDelta := idle - prevIdle

	// Store current values for next call
	prevTotal = total
	prevIdle = idle
	prevTime = now

	if totalDelta <= 0 {
		return lastCPUUsage
	}

	// CPU usage = (total - idle) / total * 100
	lastCPUUsage = float64(totalDelta-idleDelta) / float64(totalDelta) * 100
	if lastCPUUsage < 0 {
		lastCPUUsage = 0
	} else if lastCPUUsage > 100 {
		lastCPUUsage = 100
	}

	return lastCPUUsage
}

// readCPUStat reads CPU times from /proc/stat.
func readCPUStat() (total, idle int64) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}

	// First line: cpu user nice system idle iowait irq softirq steal guest guest_nice
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0, 0
	}

	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}

	// Sum all CPU times
	for i := 1; i < len(fields); i++ {
		val, _ := strconv.ParseInt(fields[i], 10, 64)
		total += val
		// idle (index 4) + iowait (index 5) = total idle time
		if i == 4 || i == 5 {
			idle += val
		}
	}

	return total, idle
}

// getAllDisksUsage returns usage info for all mounted block devices.
func getAllDisksUsage() []DiskInfo {
	disks := []DiskInfo{}
	seen := make(map[string]bool)

	// Read /proc/mounts to find all mounted filesystems
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return disks
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		device := fields[0]
		mountPoint := fields[1]

		// Skip non-device mounts
		if !strings.HasPrefix(device, "/dev/") {
			continue
		}

		// Skip pseudo filesystems
		if strings.HasPrefix(device, "/dev/loop") {
			continue
		}

		// Get the base device name (e.g., nvme0n1 from /dev/nvme0n1p1)
		deviceName := strings.TrimPrefix(device, "/dev/")

		// For partitions, get the parent device
		baseDevice := deviceName
		if strings.HasPrefix(deviceName, "nvme") {
			// NVMe: nvme0n1p1 -> nvme0n1
			if idx := strings.Index(deviceName, "p"); idx > 0 {
				// Check if there's a number after 'p' (partition indicator)
				rest := deviceName[idx+1:]
				if len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
					baseDevice = deviceName[:idx]
				}
			}
		} else if strings.HasPrefix(deviceName, "sd") || strings.HasPrefix(deviceName, "vd") || strings.HasPrefix(deviceName, "hd") {
			// Traditional: sda1 -> sda
			for i := len(deviceName) - 1; i >= 0; i-- {
				if deviceName[i] < '0' || deviceName[i] > '9' {
					baseDevice = deviceName[:i+1]
					break
				}
			}
		}

		// Skip if we already have this device (use first mount point)
		if seen[baseDevice] {
			continue
		}

		// Get disk usage for this mount point
		var stat syscall.Statfs_t
		if err := syscall.Statfs(mountPoint, &stat); err != nil {
			continue
		}

		total := stat.Blocks * uint64(stat.Bsize)
		free := stat.Bfree * uint64(stat.Bsize)   // Total free (including reserved)
		avail := stat.Bavail * uint64(stat.Bsize) // Available for non-root users
		used := total - free

		// Skip tiny filesystems (< 100MB)
		if total < 100*1024*1024 {
			continue
		}

		seen[baseDevice] = true
		disks = append(disks, DiskInfo{
			Device:     baseDevice,
			MountPoint: mountPoint,
			Total:      total,
			Free:       avail, // Show available space (what user can actually use)
			Used:       used,
		})
	}

	return disks
}
