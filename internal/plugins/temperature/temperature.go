// Package temperature provides temperature monitoring plugin
package temperature

import (
	"context"
	_ "embed"
	"sync"
	"time"

	"podmanview/internal/plugins"
	"podmanview/internal/sensors"
	"podmanview/internal/storage"
)

//go:embed index.html
var htmlContent []byte

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

// Temperature and StorageTemp are re-exported from the shared sensors package
// so consumers (API, MCP) and this plugin use a single source of truth.
type Temperature = sensors.Temperature
type StorageTemp = sensors.StorageTemp

// TemperatureData represents all temperature data
type TemperatureData struct {
	Temperatures []Temperature `json:"temperatures"`           // CPU/SoC temperatures
	StorageTemps []StorageTemp `json:"storageTemps,omitempty"` // NVMe/Storage temperatures grouped by device
}

// New creates a new TemperaturePlugin instance
func New() *TemperaturePlugin {
	return &TemperaturePlugin{
		BasePlugin: plugins.NewBasePlugin(
			"temperature",
			"System temperature monitoring. Data: GET /api/plugins/temperature/data (session or PODMANVIEW_API_TOKEN)",
			"2.0.0",
			htmlContent,
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

	// Load settings from storage (update interval)
	p.loadSettings(deps.Storage)

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
		p.Logger().Printf("[%s] Plugin stopped gracefully", p.Name())
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
		Temperatures: sensors.GetCPUTemperatures(),
		StorageTemps: sensors.GetNVMeTemperaturesGrouped(),
	}

	// Update cache with lock
	p.mu.Lock()
	p.cachedData = newData
	p.lastUpdate = time.Now()
	p.mu.Unlock()

	// Log update
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Temperature data updated: %d CPU sensors, %d storage devices",
			p.Name(), len(newData.Temperatures), len(newData.StorageTemps))
	}
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

// loadSettings loads plugin settings from storage
func (p *TemperaturePlugin) loadSettings(st storage.Storage) {
	if st == nil {
		return
	}

	// Load update interval
	interval, err := st.GetInt(p.Name(), "updateInterval")
	if err == nil && interval >= 5 && interval <= 60 {
		p.mu.Lock()
		p.updatePeriod = time.Duration(interval) * time.Second
		p.mu.Unlock()
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Loaded update interval: %d seconds", p.Name(), interval)
		}
	} else if err != nil {
		// Save default interval if not set
		st.SetInt(p.Name(), "updateInterval", 15)
	}
}

// GetFriendlyName converts system sensor names to human-readable names.
// Delegates to the shared sensors package.
func GetFriendlyName(deviceName string) string {
	return sensors.GetFriendlyName(deviceName)
}

// GetFriendlyStorageName converts storage device names to human-readable names.
// Delegates to the shared sensors package.
func GetFriendlyStorageName(deviceName string) string {
	return sensors.GetFriendlyStorageName(deviceName)
}
