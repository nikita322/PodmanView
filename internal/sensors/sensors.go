// Package sensors provides low-level system sensor readers (CPU/SoC
// temperatures from hwmon and NVMe storage temperatures). It is shared between
// the temperature plugin and other consumers such as the MCP server.
package sensors

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Temperature represents a temperature sensor reading
type Temperature struct {
	Label string  `json:"label"`
	Temp  float64 `json:"temp"`
}

// StorageTemp represents storage device temperatures grouped by device
type StorageTemp struct {
	Device  string        `json:"device"`  // Device name (e.g., nvme0n1)
	Sensors []Temperature `json:"sensors"` // Temperature sensors for this device
}

// GetFriendlyName converts system sensor names to human-readable names.
// Supports dynamic patterns like clusterN_thermal -> CPU Cluster N+1.
func GetFriendlyName(deviceName string) string {
	// Pattern: clusterN_thermal -> CPU Cluster N+1 (numbering starts from 1)
	if strings.HasPrefix(deviceName, "cluster") && strings.HasSuffix(deviceName, "_thermal") {
		clusterNum := strings.TrimPrefix(deviceName, "cluster")
		clusterNum = strings.TrimSuffix(clusterNum, "_thermal")
		if len(clusterNum) > 0 && isDigit(clusterNum[0]) {
			if num, err := strconv.Atoi(clusterNum); err == nil {
				return "CPU Cluster " + strconv.Itoa(num+1)
			}
		}
	}

	// Pattern: coreN -> CPU Core N+1 (numbering starts from 1)
	if strings.HasPrefix(deviceName, "core") {
		coreNum := strings.TrimPrefix(deviceName, "core")
		if len(coreNum) > 0 && isDigit(coreNum[0]) {
			if num, err := strconv.Atoi(coreNum); err == nil {
				return "CPU Core " + strconv.Itoa(num+1)
			}
		}
	}

	// Return original name if no pattern matches
	return deviceName
}

// GetFriendlyStorageName converts storage device names to human-readable names.
func GetFriendlyStorageName(deviceName string) string {
	// Pattern: nvmeXnY -> NVMe SSD X+1 (user-friendly numbering starting from 1)
	if strings.HasPrefix(deviceName, "nvme") {
		rest := strings.TrimPrefix(deviceName, "nvme")
		if len(rest) > 0 {
			// nvme0n1 -> rest = "0n1"
			nPos := strings.Index(rest, "n")
			if nPos > 0 {
				numStr := rest[:nPos]
				if num, err := strconv.Atoi(numStr); err == nil {
					return "NVMe SSD " + strconv.Itoa(num+1)
				}
			}
		}
	}

	// Pattern: sdX -> SATA Drive X (uppercase)
	if strings.HasPrefix(deviceName, "sd") && len(deviceName) >= 3 {
		letter := strings.ToUpper(string(deviceName[2]))
		return "SATA Drive " + letter
	}

	// Pattern: hdX -> IDE Drive X (uppercase)
	if strings.HasPrefix(deviceName, "hd") && len(deviceName) >= 3 {
		letter := strings.ToUpper(string(deviceName[2]))
		return "IDE Drive " + letter
	}

	// Pattern: mmcblkX -> SD Card X+1
	if strings.HasPrefix(deviceName, "mmcblk") {
		numStr := strings.TrimPrefix(deviceName, "mmcblk")
		if num, err := strconv.Atoi(numStr); err == nil {
			return "SD Card " + strconv.Itoa(num+1)
		}
	}

	// Return original name if no pattern matches
	return deviceName
}

// isDigit checks if a byte is a digit (0-9)
func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

// GetCPUTemperatures reads CPU/SoC temperatures from /sys/class/hwmon.
func GetCPUTemperatures() []Temperature {
	temps := []Temperature{}

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
			} else {
				label = GetFriendlyName(deviceName)
			}

			temps = append(temps, Temperature{
				Label: label,
				Temp:  tempC,
			})
		}
	}

	return temps
}

// NVMeDevice represents a physical (non-partition) NVMe block device.
type NVMeDevice struct {
	Name string // e.g. nvme0n1
	Path string // e.g. /dev/nvme0n1
}

// ListNVMeDevices returns physical NVMe block devices found in /sys/block.
// Partitions (e.g. nvme0n1p1) are skipped.
func ListNVMeDevices() []NVMeDevice {
	devices := []NVMeDevice{}

	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return devices
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

		devicePath := filepath.Join("/dev", deviceName)
		if _, err := os.Stat(devicePath); err != nil {
			continue
		}

		devices = append(devices, NVMeDevice{Name: deviceName, Path: devicePath})
	}

	return devices
}

// IsNVMeCLIAvailable reports whether the nvme utility from the nvme-cli package
// is present in PATH. NVMe temperatures cannot be read without it.
func IsNVMeCLIAvailable() bool {
	_, err := exec.LookPath("nvme")
	return err == nil
}

// GetNVMeTemperaturesGrouped reads temperatures from NVMe devices and groups by device.
func GetNVMeTemperaturesGrouped() []StorageTemp {
	result := []StorageTemp{}

	for _, dev := range ListNVMeDevices() {
		cmd := exec.Command("nvme", "smart-log", dev.Path)
		output, err := cmd.Output()
		if err != nil {
			continue
		}

		outputStr := string(output)
		deviceTemps := StorageTemp{
			Device:  GetFriendlyStorageName(dev.Name),
			Sensors: []Temperature{},
		}

		// Parse main temperature: "temperature                             : 53 °C (326 K)"
		reMain := regexp.MustCompile(`(?m)^temperature\s*:\s*(\d+)\s*°?C`)
		if matches := reMain.FindStringSubmatch(outputStr); len(matches) >= 2 {
			if tempC, err := strconv.ParseFloat(matches[1], 64); err == nil {
				deviceTemps.Sensors = append(deviceTemps.Sensors, Temperature{
					Label: "Composite",
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
					deviceTemps.Sensors = append(deviceTemps.Sensors, Temperature{
						Label: "Sensor " + sensorNum,
						Temp:  tempC,
					})
				}
			}
		}

		if len(deviceTemps.Sensors) > 0 {
			result = append(result, deviceTemps)
		}
	}

	return result
}
