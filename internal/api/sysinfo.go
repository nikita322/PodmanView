package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// HostStats represents CPU, memory, temperature, uptime and disk info
type HostStats struct {
	CPUUsage        float64       `json:"cpuUsage"`
	MemTotal        uint64        `json:"memTotal"`                  // bytes
	MemFree         uint64        `json:"memFree"`                   // bytes (MemAvailable from /proc/meminfo)
	Temperatures    []Temperature `json:"temperatures"`              // CPU/SoC temperatures
	StorageTemps    []Temperature `json:"storageTemps,omitempty"`    // NVMe/Storage temperatures
	Uptime          int64         `json:"uptime"`                    // seconds
	DiskTotal       uint64        `json:"diskTotal"`                 // bytes
	DiskFree        uint64        `json:"diskFree"`                  // bytes
}

// Temperature represents a temperature sensor reading
type Temperature struct {
	Label string  `json:"label"`
	Temp  float64 `json:"temp"`
}

// GetHostStats reads CPU usage, memory, temperatures and uptime from /sys and /proc
func GetHostStats() *HostStats {
	stats := &HostStats{
		Temperatures: []Temperature{},
		StorageTemps: []Temperature{},
	}

	// Get CPU usage
	stats.CPUUsage = getCPUUsage()

	// Get memory info
	stats.MemTotal, stats.MemFree = getMemoryInfo()

	// Get CPU/SoC temperatures from hwmon
	stats.Temperatures = getCPUTemperatures()

	// Get NVMe/Storage temperatures
	stats.StorageTemps = getNVMeTemperatures()

	// Get uptime
	stats.Uptime = getUptime()

	// Get disk usage
	stats.DiskTotal, stats.DiskFree = getDiskUsage("/")

	return stats
}

// getMemoryInfo reads memory info from /proc/meminfo
// Returns MemTotal and MemAvailable (as "free" - more useful than actual MemFree)
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

// getDiskUsage returns total and free disk space for a path
func getDiskUsage(path string) (uint64, uint64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	return total, free
}

// getUptime reads system uptime from /proc/uptime
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

// getCPUUsage calculates real CPU usage from /proc/stat
// Returns percentage (0-100)
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

// readCPUStat reads CPU times from /proc/stat
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

// friendlyTempNames maps system sensor names to human-readable names
var friendlyTempNames = map[string]string{
	"cluster0_thermal": "CPU Cluster 0",
	"cluster1_thermal": "CPU Cluster 1",
}

// getCPUTemperatures reads CPU/SoC temperatures from /sys/class/hwmon
func getCPUTemperatures() []Temperature {
	temps := []Temperature{}

	// Scan hwmon devices
	hwmonPath := "/sys/class/hwmon"
	entries, err := os.ReadDir(hwmonPath)
	if err != nil {
		return temps
	}

	for _, entry := range entries {
		devicePath := filepath.Join(hwmonPath, entry.Name())

		// Get device name
		nameBytes, err := os.ReadFile(filepath.Join(devicePath, "name"))
		if err != nil {
			continue
		}
		deviceName := strings.TrimSpace(string(nameBytes))

		// Find temp inputs
		files, err := os.ReadDir(devicePath)
		if err != nil {
			continue
		}

		for _, f := range files {
			if !strings.HasPrefix(f.Name(), "temp") || !strings.HasSuffix(f.Name(), "_input") {
				continue
			}

			// Read temperature (in millidegrees)
			tempBytes, err := os.ReadFile(filepath.Join(devicePath, f.Name()))
			if err != nil {
				continue
			}

			tempMilliC, err := strconv.ParseInt(strings.TrimSpace(string(tempBytes)), 10, 64)
			if err != nil {
				continue
			}

			tempC := float64(tempMilliC) / 1000.0

			// Try to get label first, then use friendly name or device name
			labelFile := strings.Replace(f.Name(), "_input", "_label", 1)
			labelBytes, err := os.ReadFile(filepath.Join(devicePath, labelFile))
			var label string
			if err == nil {
				label = strings.TrimSpace(string(labelBytes))
			} else if friendly, ok := friendlyTempNames[deviceName]; ok {
				label = friendly
			} else {
				label = deviceName
			}

			temps = append(temps, Temperature{
				Label: label,
				Temp:  tempC,
			})
		}
	}

	return temps
}

// getNVMeTemperatures reads temperatures from NVMe devices using nvme-cli
func getNVMeTemperatures() []Temperature {
	temps := []Temperature{}

	// Scan /sys/block for nvme devices
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return temps
	}

	for _, entry := range entries {
		deviceName := entry.Name()
		if !strings.HasPrefix(deviceName, "nvme") {
			continue
		}

		// Skip partitions (nvme0n1p1, etc)
		if strings.Contains(deviceName, "p") {
			continue
		}

		devicePath := "/dev/" + deviceName
		if _, err := os.Stat(devicePath); err != nil {
			continue
		}

		cmd := exec.Command("nvme", "smart-log", devicePath)
		output, err := cmd.Output()
		if err != nil {
			continue
		}

		outputStr := string(output)

		// Parse main temperature: "temperature                             : 53 °C (326 K)"
		reMain := regexp.MustCompile(`(?m)^temperature\s*:\s*(\d+)\s*°?C`)
		if matches := reMain.FindStringSubmatch(outputStr); len(matches) >= 2 {
			if tempC, err := strconv.ParseFloat(matches[1], 64); err == nil {
				temps = append(temps, Temperature{
					Label: deviceName,
					Temp:  tempC,
				})
			}
		}

		// Parse temperature sensors: "Temperature Sensor 1           : 76 °C (349 K)"
		reSensors := regexp.MustCompile(`Temperature Sensor (\d+)\s*:\s*(\d+)\s*°C`)
		sensorMatches := reSensors.FindAllStringSubmatch(outputStr, -1)
		for _, match := range sensorMatches {
			if len(match) >= 3 {
				sensorNum := match[1]
				if tempC, err := strconv.ParseFloat(match[2], 64); err == nil {
					temps = append(temps, Temperature{
						Label: deviceName + " Sensor " + sensorNum,
						Temp:  tempC,
					})
				}
			}
		}
	}

	return temps
}
