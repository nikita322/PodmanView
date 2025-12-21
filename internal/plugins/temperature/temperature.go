// Package temperature provides temperature monitoring plugin
package temperature

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"podmanview/internal/plugins"
)

// TemperaturePlugin monitors system temperatures
type TemperaturePlugin struct {
	*plugins.BasePlugin
	mu               sync.RWMutex
	cachedData       *TemperatureData
	lastUpdate       time.Time
	updatePeriod     time.Duration
	backgroundCtx    context.Context
	backgroundCancel context.CancelFunc
	bgMutex          sync.Mutex
}

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

// TemperatureData represents all temperature data
type TemperatureData struct {
	Temperatures []Temperature `json:"temperatures"`           // CPU/SoC temperatures
	StorageTemps []StorageTemp `json:"storageTemps,omitempty"` // NVMe/Storage temperatures grouped by device
}

// New creates a new TemperaturePlugin instance
func New() *TemperaturePlugin {
	// Get the path to the HTML file relative to this plugin's directory
	htmlPath := filepath.Join("internal", "plugins", "temperature", "index.html")

	return &TemperaturePlugin{
		BasePlugin: plugins.NewBasePlugin(
			"temperature",
			"System temperature monitoring",
			"1.0.0",
			htmlPath,
		),
		updatePeriod: 15 * time.Second, // Update every 15 seconds
		cachedData: &TemperatureData{
			Temperatures: []Temperature{},
			StorageTemps: []StorageTemp{},
		},
	}
}

// Init initializes the plugin
func (p *TemperaturePlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)

	// Load update interval from storage
	if deps.Storage != nil {
		interval, err := deps.Storage.GetInt(p.Name(), "updateInterval")
		if err == nil && interval >= 5 && interval <= 60 {
			p.mu.Lock()
			p.updatePeriod = time.Duration(interval) * time.Second
			p.mu.Unlock()
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Loaded update interval from storage: %d seconds", p.Name(), interval)
			}
		} else {
			// Save default interval if not set
			if err != nil {
				deps.Storage.SetInt(p.Name(), "updateInterval", 15)
			}
		}
	}

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin initialized", p.Name())
	}
	return nil
}

// Start starts the plugin
func (p *TemperaturePlugin) Start(ctx context.Context) error {
	// Perform initial temperature update
	p.updateTemperatureData()

	// Log start (check if logger is available)
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin started with initial temperature data", p.Name())
	}
	return nil
}

// Stop stops the plugin
func (p *TemperaturePlugin) Stop(ctx context.Context) error {
	// Cancel background task
	p.bgMutex.Lock()
	if p.backgroundCancel != nil {
		p.backgroundCancel()
		p.backgroundCancel = nil
	}
	p.bgMutex.Unlock()

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin stopped", p.Name())
	}
	return nil
}

// Routes returns the plugin's HTTP routes
func (p *TemperaturePlugin) Routes() []plugins.Route {
	return []plugins.Route{
		{
			Method:      "GET",
			Path:        "/api/plugins/temperature/data",
			Handler:     p.handleGetTemperatures,
			RequireAuth: true,
		},
		{
			Method:      "GET",
			Path:        "/api/plugins/temperature/settings",
			Handler:     p.handleGetSettings,
			RequireAuth: true,
		},
		{
			Method:      "POST",
			Path:        "/api/plugins/temperature/settings",
			Handler:     p.handleUpdateSettings,
			RequireAuth: true,
		},
	}
}

// IsEnabled checks if the plugin is enabled
func (p *TemperaturePlugin) IsEnabled() bool {
	if p.Deps() == nil || p.Deps().Storage == nil {
		return false
	}
	enabled, err := p.Deps().Storage.IsPluginEnabled(p.Name())
	if err != nil {
		return false
	}
	return enabled
}

// StartBackgroundTasks starts the background temperature monitoring task
func (p *TemperaturePlugin) StartBackgroundTasks(ctx context.Context) error {
	p.bgMutex.Lock()
	defer p.bgMutex.Unlock()

	// Create a child context that we can cancel independently
	p.backgroundCtx, p.backgroundCancel = context.WithCancel(ctx)

	// Log start (check if logger is available)
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Starting background temperature monitoring (update interval: %v)", p.Name(), p.updatePeriod)
	}

	// Run periodic temperature updates
	go plugins.RunPeriodic(p.backgroundCtx, p.updatePeriod, p.Logger(), p.Name(), func(ctx context.Context) error {
		p.updateTemperatureData()
		return nil
	})

	return nil
}

// RestartBackgroundTasks restarts the background task with new interval
func (p *TemperaturePlugin) RestartBackgroundTasks() error {
	p.bgMutex.Lock()

	// Cancel existing background task
	if p.backgroundCancel != nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Stopping background task for restart", p.Name())
		}
		p.backgroundCancel()
	}

	// Create new context
	// Use context.Background() as parent since the original parent context is long-lived
	p.backgroundCtx, p.backgroundCancel = context.WithCancel(context.Background())

	p.bgMutex.Unlock()

	// Log restart
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Restarting background temperature monitoring (new interval: %v)", p.Name(), p.updatePeriod)
	}

	// Run periodic temperature updates with new interval
	go plugins.RunPeriodic(p.backgroundCtx, p.updatePeriod, p.Logger(), p.Name(), func(ctx context.Context) error {
		p.updateTemperatureData()
		return nil
	})

	return nil
}

// updateTemperatureData updates the cached temperature data
func (p *TemperaturePlugin) updateTemperatureData() {
	// Collect fresh temperature data
	newData := &TemperatureData{
		Temperatures: getCPUTemperatures(),
		StorageTemps: getNVMeTemperaturesGrouped(),
	}

	// Update cache with lock
	p.mu.Lock()
	p.cachedData = newData
	p.lastUpdate = time.Now()
	p.mu.Unlock()

	// Log update (check if logger is available)
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Temperature data updated: %d CPU sensors, %d storage devices",
			p.Name(), len(newData.Temperatures), len(newData.StorageTemps))
	}
}

// HTTP Handlers

func (p *TemperaturePlugin) handleGetTemperatures(w http.ResponseWriter, r *http.Request) {
	data := p.GetTemperatureData()
	plugins.WriteJSON(w, http.StatusOK, data)
}

// PluginSettings represents plugin configuration
type PluginSettings struct {
	UpdateInterval int `json:"updateInterval"` // Update interval in seconds
}

func (p *TemperaturePlugin) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	interval := int(p.updatePeriod.Seconds())
	p.mu.RUnlock()

	settings := PluginSettings{
		UpdateInterval: interval,
	}

	plugins.WriteJSON(w, http.StatusOK, settings)
}

func (p *TemperaturePlugin) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings PluginSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	// Validate interval (5-60 seconds)
	if settings.UpdateInterval < 5 || settings.UpdateInterval > 60 {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Update interval must be between 5 and 60 seconds"})
		return
	}

	// Update in-memory interval
	p.mu.Lock()
	p.updatePeriod = time.Duration(settings.UpdateInterval) * time.Second
	p.mu.Unlock()

	// Save to storage
	if p.Deps() != nil && p.Deps().Storage != nil {
		if err := p.Deps().Storage.SetInt(p.Name(), "updateInterval", settings.UpdateInterval); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Failed to save update interval to storage: %v", p.Name(), err)
			}
			plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to save settings"})
			return
		}
	}

	// Restart background task with new interval
	if err := p.RestartBackgroundTasks(); err != nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Failed to restart background tasks: %v", p.Name(), err)
		}
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to restart background tasks"})
		return
	}

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Update interval changed to %d seconds and background task restarted", p.Name(), settings.UpdateInterval)
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "Settings updated successfully"})
}

// GetTemperatureData returns cached temperature data
func (p *TemperaturePlugin) GetTemperatureData() *TemperatureData {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy of cached data to prevent external modifications
	return &TemperatureData{
		Temperatures: p.cachedData.Temperatures,
		StorageTemps: p.cachedData.StorageTemps,
	}
}

// GetLastUpdateTime returns the time of the last temperature data update
func (p *TemperaturePlugin) GetLastUpdateTime() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastUpdate
}

// GetFriendlyName converts system sensor names to human-readable names
// Supports dynamic patterns like clusterN_thermal -> CPU Cluster N+1 (user-friendly numbering)
func GetFriendlyName(deviceName string) string {
	// Pattern: clusterN_thermal -> CPU Cluster N+1 (numbering starts from 1)
	if strings.HasPrefix(deviceName, "cluster") && strings.HasSuffix(deviceName, "_thermal") {
		// Extract cluster number
		clusterNum := strings.TrimPrefix(deviceName, "cluster")
		clusterNum = strings.TrimSuffix(clusterNum, "_thermal")
		// Verify it's a number and convert
		if len(clusterNum) > 0 && isDigit(clusterNum[0]) {
			if num, err := strconv.Atoi(clusterNum); err == nil {
				return "CPU Cluster " + strconv.Itoa(num+1)
			}
		}
	}

	// Pattern: coreN -> CPU Core N+1 (numbering starts from 1)
	if strings.HasPrefix(deviceName, "core") {
		coreNum := strings.TrimPrefix(deviceName, "core")
		// Check if the next character is a digit
		if len(coreNum) > 0 && isDigit(coreNum[0]) {
			if num, err := strconv.Atoi(coreNum); err == nil {
				return "CPU Core " + strconv.Itoa(num+1)
			}
		}
	}

	// Add more patterns as needed
	// For example: nvme, ssd, etc.

	// Return original name if no pattern matches
	return deviceName
}

// GetFriendlyStorageName converts storage device names to human-readable names
// Supports patterns like nvme0n1 -> NVMe SSD 1, sda -> SATA Drive A
func GetFriendlyStorageName(deviceName string) string {
	// Pattern: nvmeXnY -> NVMe SSD X+1 (user-friendly numbering starting from 1)
	if strings.HasPrefix(deviceName, "nvme") {
		// Extract number after "nvme"
		rest := strings.TrimPrefix(deviceName, "nvme")
		// Find the digit part before 'n'
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
			} else {
				// Use dynamic friendly name conversion
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

// getNVMeTemperaturesGrouped reads temperatures from NVMe devices and groups by device
func getNVMeTemperaturesGrouped() []StorageTemp {
	result := []StorageTemp{}

	// Scan /sys/block for nvme devices
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return result
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
		deviceTemps := StorageTemp{
			Device:  GetFriendlyStorageName(deviceName),
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
