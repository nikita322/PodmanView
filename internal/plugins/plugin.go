package plugins

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"podmanview/internal/config"
	"podmanview/internal/events"
	"podmanview/internal/mqtt"
	"podmanview/internal/podman"
	"podmanview/internal/storage"
)

// Plugin is the base interface for all plugins
type Plugin interface {
	// Name returns the unique plugin name (lowercase, no spaces)
	Name() string

	// Description returns the plugin description
	Description() string

	// Version returns the plugin version (semver)
	Version() string

	// Init initializes the plugin
	// Called during application startup before Start
	Init(ctx context.Context, deps *PluginDependencies) error

	// Start starts the plugin
	// Called after successful initialization of all plugins
	Start(ctx context.Context) error

	// Stop stops the plugin
	// Called during application shutdown
	Stop(ctx context.Context) error

	// Routes returns the plugin's HTTP routes
	// Can be nil if the plugin doesn't add any routes
	Routes() []Route

	// IsEnabled checks if the plugin should be enabled
	IsEnabled() bool

	// GetHTML returns the plugin's HTML interface
	// This HTML will be embedded into the main index.html
	GetHTML() (string, error)
}

// BackgroundTaskRunner is an optional interface for plugins that need to run background tasks
// Plugins can implement this interface to run periodic tasks (monitoring, checks, updates, etc.)
type BackgroundTaskRunner interface {
	// StartBackgroundTasks starts the plugin's background tasks
	// This method is called after Start() and should launch goroutines for background work
	// The provided context will be cancelled when the plugin should stop its background tasks
	// Example: monitoring, periodic checks, data updates, etc.
	StartBackgroundTasks(ctx context.Context) error
}

// PluginDependencies contains dependencies available to plugins
type PluginDependencies struct {
	// PodmanClient is the client for working with Podman API
	PodmanClient *podman.Client

	// Config is the application configuration
	Config *config.Config

	// EventStore is the event storage for logging actions
	EventStore *events.Store

	// Logger is the application logger
	Logger *log.Logger

	// Storage is the storage for plugin configurations and data
	Storage storage.Storage

	// MQTT services (can be nil if MQTT is not configured)
	MQTTClient    *mqtt.Client           // MQTT client for direct publishing
	MQTTPublisher *mqtt.Publisher        // Publisher for sensor data
	MQTTDiscovery *mqtt.DiscoveryManager // Home Assistant discovery manager
}

// Route represents a plugin's HTTP route
type Route struct {
	// Method is the HTTP method (GET, POST, DELETE, PUT, PATCH)
	Method string

	// Path is the route path (e.g., "/api/plugins/fans/status")
	// Recommended to use prefix /api/plugins/{plugin-name}/
	Path string

	// Handler is the request handler
	Handler http.HandlerFunc

	// RequireAuth indicates whether authentication is required for this route
	RequireAuth bool
}

// GetMethod returns the HTTP method
func (r Route) GetMethod() string {
	return r.Method
}

// GetPath returns the route path
func (r Route) GetPath() string {
	return r.Path
}

// PluginInfo contains plugin information for API responses
type PluginInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"` // "running", "stopped", "error"
}

// BasePlugin is a base structure that plugins can embed
type BasePlugin struct {
	name        string
	description string
	version     string
	deps        *PluginDependencies
	logger      *log.Logger
	htmlContent []byte // Embedded HTML content
}

// NewBasePlugin creates a new BasePlugin
func NewBasePlugin(name, description, version string, htmlContent []byte) *BasePlugin {
	return &BasePlugin{
		name:        name,
		description: description,
		version:     version,
		htmlContent: htmlContent,
	}
}

// Name implements Plugin.Name
func (p *BasePlugin) Name() string {
	return p.name
}

// Description implements Plugin.Description
func (p *BasePlugin) Description() string {
	return p.description
}

// Version implements Plugin.Version
func (p *BasePlugin) Version() string {
	return p.version
}

// SetDependencies sets the plugin's dependencies
func (p *BasePlugin) SetDependencies(deps *PluginDependencies) {
	p.deps = deps
	p.logger = deps.Logger
}

// Deps returns the plugin's dependencies
func (p *BasePlugin) Deps() *PluginDependencies {
	return p.deps
}

// Logger returns the plugin's logger
func (p *BasePlugin) Logger() *log.Logger {
	return p.logger
}

// LogError logs an error message
func (p *BasePlugin) LogError(format string, v ...interface{}) {
	if p.logger != nil {
		p.logger.Printf("["+p.name+"] "+format, v...)
	}
}

// GetHTML returns the plugin's HTML interface
func (p *BasePlugin) GetHTML() (string, error) {
	if len(p.htmlContent) == 0 {
		return "", nil
	}
	return string(p.htmlContent), nil
}

// WriteJSON is a shared helper function for writing JSON responses
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Log encoding error but can't change response at this point
		log.Printf("ERROR: Failed to encode JSON response: %v", err)
	}
}

// RunPeriodic runs a function periodically until the context is cancelled
// This is a helper for plugins that need to run background tasks
// Usage example:
//
//	go RunPeriodic(ctx, 30*time.Second, p.logger, p.name, func(ctx context.Context) error {
//	    // Your periodic task here
//	    return p.checkStatus()
//	})
func RunPeriodic(ctx context.Context, interval time.Duration, logger *log.Logger, pluginName string, task func(context.Context) error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run task immediately on start
	if err := task(ctx); err != nil {
		if logger != nil {
			logger.Printf("[%s] Background task error: %v", pluginName, err)
		}
	}

	// Then run periodically
	for {
		select {
		case <-ctx.Done():
			if logger != nil {
				logger.Printf("[%s] Background task stopped", pluginName)
			}
			return
		case <-ticker.C:
			if err := task(ctx); err != nil {
				if logger != nil {
					logger.Printf("[%s] Background task error: %v", pluginName, err)
				}
			}
		}
	}
}

// RunOnce runs a function once after a delay, unless the context is cancelled
// This is useful for delayed initialization or one-time background tasks
func RunOnce(ctx context.Context, delay time.Duration, logger *log.Logger, pluginName string, task func(context.Context) error) {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		if logger != nil {
			logger.Printf("[%s] Delayed task cancelled", pluginName)
		}
		return
	case <-timer.C:
		if err := task(ctx); err != nil {
			if logger != nil {
				logger.Printf("[%s] Delayed task error: %v", pluginName, err)
			}
		}
	}
}
