// Package led provides LED control plugin
package led

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"podmanview/internal/plugins"
	"podmanview/internal/storage"
)

const (
	ledsPath = "/sys/class/leds"
)

// LEDStatus represents LED status
type LEDStatus string

const (
	LEDStatusEnabled  LEDStatus = "enabled"
	LEDStatusDisabled LEDStatus = "disabled"
)

// LEDInfo represents information about a single LED
type LEDInfo struct {
	Name      string `json:"name"`      // LED name (e.g., "led0")
	Path      string `json:"path"`      // Full path to LED directory
	Brightness int    `json:"brightness"` // Current brightness (0 or 1)
}

// LEDState represents the current state of all LEDs
type LEDState struct {
	Status         LEDStatus `json:"status"`         // Current status (enabled/disabled)
	TotalLEDs      int       `json:"totalLeds"`      // Total number of LEDs found
	EnabledCount   int       `json:"enabledCount"`   // Number of enabled LEDs
	DisabledCount  int       `json:"disabledCount"`  // Number of disabled LEDs
	LastUpdate     time.Time `json:"lastUpdate"`     // Last state update time
}

// Settings represents plugin settings
type Settings struct {
	AutoDisableOnStartup bool `json:"autoDisableOnStartup"` // Auto-disable LEDs on startup
}

// LEDPlugin manages system LEDs
type LEDPlugin struct {
	*plugins.BasePlugin
	mu       sync.RWMutex
	state    *LEDState
	settings *Settings
	leds     []LEDInfo // List of all available LEDs
}

// New creates a new LEDPlugin instance
func New() *LEDPlugin {
	htmlPath := filepath.Join("internal", "plugins", "led", "index.html")

	return &LEDPlugin{
		BasePlugin: plugins.NewBasePlugin(
			"led",
			"LED control and management",
			"1.0.0",
			htmlPath,
		),
		state: &LEDState{
			Status:     LEDStatusEnabled,
			LastUpdate: time.Now(),
		},
		settings: &Settings{
			AutoDisableOnStartup: false,
		},
		leds: []LEDInfo{},
	}
}

// Init initializes the plugin
func (p *LEDPlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)

	// Load settings from storage
	p.loadSettings(deps.Storage)

	// Discover all available LEDs
	if err := p.discoverLEDs(); err != nil {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] Warning: Failed to discover LEDs: %v", p.Name(), err)
		}
	}

	// Auto-disable on startup if enabled
	if p.settings.AutoDisableOnStartup {
		if err := p.setAllLEDs(false); err != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Warning: Failed to auto-disable LEDs: %v", p.Name(), err)
			}
		} else {
			if p.Logger() != nil {
				p.Logger().Printf("[%s] Auto-disabled %d LEDs on startup", p.Name(), len(p.leds))
			}
		}
	}

	// Update initial state
	p.updateState()

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin initialized (found %d LEDs)", p.Name(), len(p.leds))
	}

	return nil
}

// Start starts the plugin
func (p *LEDPlugin) Start(ctx context.Context) error {
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin started", p.Name())
	}
	return nil
}

// Stop stops the plugin
func (p *LEDPlugin) Stop(ctx context.Context) error {
	if p.Logger() != nil {
		p.Logger().Printf("[%s] Plugin stopped", p.Name())
	}
	return nil
}

// Routes returns the plugin's HTTP routes
func (p *LEDPlugin) Routes() []plugins.Route {
	return []plugins.Route{
		{
			Method:      "GET",
			Path:        "/api/plugins/led/status",
			Handler:     p.handleGetStatus,
			RequireAuth: true,
		},
		{
			Method:      "POST",
			Path:        "/api/plugins/led/toggle",
			Handler:     p.handleToggleLEDs,
			RequireAuth: true,
		},
		{
			Method:      "GET",
			Path:        "/api/plugins/led/settings",
			Handler:     p.handleGetSettings,
			RequireAuth: true,
		},
		{
			Method:      "POST",
			Path:        "/api/plugins/led/settings",
			Handler:     p.handleUpdateSettings,
			RequireAuth: true,
		},
	}
}

// IsEnabled checks if the plugin is enabled
func (p *LEDPlugin) IsEnabled() bool {
	if p.Deps() == nil || p.Deps().Storage == nil {
		return false
	}
	enabled, err := p.Deps().Storage.IsPluginEnabled(p.Name())
	if err != nil {
		return false
	}
	return enabled
}

// discoverLEDs scans /sys/class/leds for available LEDs
func (p *LEDPlugin) discoverLEDs() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.leds = []LEDInfo{}

	// Check if LEDs directory exists
	if _, err := os.Stat(ledsPath); os.IsNotExist(err) {
		if p.Logger() != nil {
			p.Logger().Printf("[%s] LEDs directory %s does not exist (not a Linux system or no LEDs available)", p.Name(), ledsPath)
		}
		return fmt.Errorf("LEDs directory %s does not exist", ledsPath)
	}

	entries, err := os.ReadDir(ledsPath)
	if err != nil {
		return fmt.Errorf("failed to read LEDs directory: %w", err)
	}

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Found %d entries in %s", p.Name(), len(entries), ledsPath)
	}

	for _, entry := range entries {
		ledPath := filepath.Join(ledsPath, entry.Name())
		brightnessPath := filepath.Join(ledPath, "brightness")
		triggerPath := filepath.Join(ledPath, "trigger")

		if p.Logger() != nil {
			p.Logger().Printf("[%s] Checking LED: %s (type: %s)", p.Name(), entry.Name(), entry.Type())
		}

		// Check if brightness file exists (follow symlinks with os.Stat)
		brightnessInfo, brightnessErr := os.Stat(brightnessPath)
		triggerInfo, triggerErr := os.Stat(triggerPath)

		if brightnessErr != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s]   Brightness file error: %v", p.Name(), brightnessErr)
			}
			continue
		}

		if triggerErr != nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s]   Trigger file error: %v", p.Name(), triggerErr)
			}
			continue
		}

		if p.Logger() != nil {
			p.Logger().Printf("[%s]   Brightness file: %s (mode: %s)", p.Name(), brightnessPath, brightnessInfo.Mode())
			p.Logger().Printf("[%s]   Trigger file: %s (mode: %s)", p.Name(), triggerPath, triggerInfo.Mode())
		}

		// Try to read current brightness
		brightness := 0
		if data, err := os.ReadFile(brightnessPath); err == nil {
			fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &brightness)
			if p.Logger() != nil {
				p.Logger().Printf("[%s]   Current brightness: %d", p.Name(), brightness)
			}
		} else {
			if p.Logger() != nil {
				p.Logger().Printf("[%s]   Cannot read brightness: %v", p.Name(), err)
			}
			continue
		}

		// Try to read trigger (for diagnostics)
		if data, err := os.ReadFile(triggerPath); err == nil {
			if p.Logger() != nil {
				p.Logger().Printf("[%s]   Current trigger: %s", p.Name(), strings.TrimSpace(string(data)))
			}
		}

		// Add LED to list
		p.leds = append(p.leds, LEDInfo{
			Name:       entry.Name(),
			Path:       ledPath,
			Brightness: brightness,
		})

		if p.Logger() != nil {
			p.Logger().Printf("[%s] ✓ Successfully added LED: %s", p.Name(), entry.Name())
		}
	}

	if len(p.leds) == 0 && p.Logger() != nil {
		p.Logger().Printf("[%s] Warning: No controllable LEDs found in %s", p.Name(), ledsPath)
	}

	return nil
}

// setAllLEDs enables or disables all LEDs
func (p *LEDPlugin) setAllLEDs(enable bool) error {
	p.mu.RLock()
	leds := p.leds
	p.mu.RUnlock()

	brightnessValue := "0"
	triggerValue := "none"

	if enable {
		brightnessValue = "1"
	}

	var lastErr error
	successCount := 0

	for _, led := range leds {
		brightnessPath := filepath.Join(led.Path, "brightness")
		triggerPath := filepath.Join(led.Path, "trigger")

		// Set trigger to "none" first
		if err := os.WriteFile(triggerPath, []byte(triggerValue), 0644); err != nil {
			lastErr = err
			continue
		}

		// Set brightness
		if err := os.WriteFile(brightnessPath, []byte(brightnessValue), 0644); err != nil {
			lastErr = err
			continue
		}

		successCount++
	}

	if p.Logger() != nil {
		if enable {
			p.Logger().Printf("[%s] Enabled %d/%d LEDs", p.Name(), successCount, len(leds))
		} else {
			p.Logger().Printf("[%s] Disabled %d/%d LEDs", p.Name(), successCount, len(leds))
		}
	}

	if lastErr != nil && successCount == 0 {
		return fmt.Errorf("failed to set LEDs: %w", lastErr)
	}

	return nil
}

// updateState updates the current state of all LEDs
func (p *LEDPlugin) updateState() {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Re-read brightness for all LEDs
	enabledCount := 0
	disabledCount := 0

	for i := range p.leds {
		brightnessPath := filepath.Join(p.leds[i].Path, "brightness")
		if data, err := os.ReadFile(brightnessPath); err == nil {
			brightness := 0
			fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &brightness)
			p.leds[i].Brightness = brightness

			if brightness > 0 {
				enabledCount++
			} else {
				disabledCount++
			}
		}
	}

	// Determine overall status based on majority
	// If more than half of LEDs are enabled -> status is "enabled"
	// Otherwise -> status is "disabled"
	status := LEDStatusDisabled
	if enabledCount > disabledCount {
		status = LEDStatusEnabled
	}

	p.state = &LEDState{
		Status:        status,
		TotalLEDs:     len(p.leds),
		EnabledCount:  enabledCount,
		DisabledCount: disabledCount,
		LastUpdate:    time.Now(),
	}
}

// GetState returns the current LED state
func (p *LEDPlugin) GetState() *LEDState {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Return a copy
	return &LEDState{
		Status:        p.state.Status,
		TotalLEDs:     p.state.TotalLEDs,
		EnabledCount:  p.state.EnabledCount,
		DisabledCount: p.state.DisabledCount,
		LastUpdate:    p.state.LastUpdate,
	}
}

// GetSettings returns current settings
func (p *LEDPlugin) GetSettings() *Settings {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return &Settings{
		AutoDisableOnStartup: p.settings.AutoDisableOnStartup,
	}
}

// UpdateSettings updates plugin settings
func (p *LEDPlugin) UpdateSettings(settings *Settings) error {
	p.mu.Lock()
	p.settings = settings
	p.mu.Unlock()

	// Save to storage
	if p.Deps() != nil && p.Deps().Storage != nil {
		if err := p.Deps().Storage.SetBool(p.Name(), "autoDisableOnStartup", settings.AutoDisableOnStartup); err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
	}

	if p.Logger() != nil {
		p.Logger().Printf("[%s] Settings updated: auto-disable=%v", p.Name(), settings.AutoDisableOnStartup)
	}

	return nil
}

// ToggleLEDs toggles all LEDs on or off
func (p *LEDPlugin) ToggleLEDs(enable bool) error {
	if err := p.setAllLEDs(enable); err != nil {
		return err
	}

	// Update state after toggling
	p.updateState()

	return nil
}

// loadSettings loads plugin settings from storage
func (p *LEDPlugin) loadSettings(storage storage.Storage) {
	if storage == nil {
		return
	}

	// Load auto-disable setting
	autoDisable, err := storage.GetBool(p.Name(), "autoDisableOnStartup")
	if err == nil {
		p.mu.Lock()
		p.settings.AutoDisableOnStartup = autoDisable
		p.mu.Unlock()

		if p.Logger() != nil {
			p.Logger().Printf("[%s] Loaded auto-disable setting: %v", p.Name(), autoDisable)
		}
	} else {
		// Save default if not set
		storage.SetBool(p.Name(), "autoDisableOnStartup", false)
	}
}
