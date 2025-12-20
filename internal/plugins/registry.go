package plugins

import (
	"context"
	"fmt"
	"sync"
)

// Registry is the registry of all plugins
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
	order   []string // registration order
	deps    *PluginDependencies
}

// NewRegistry creates a new plugin registry
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		order:   make([]string, 0),
	}
}

// SetDependencies sets the dependencies for all plugins
func (r *Registry) SetDependencies(deps *PluginDependencies) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deps = deps
}

// Deps returns the plugin dependencies
func (r *Registry) Deps() *PluginDependencies {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.deps
}

// Register registers a plugin in the registry
func (r *Registry) Register(p Plugin) error {
	if p == nil {
		return fmt.Errorf("plugin cannot be nil")
	}

	name := p.Name()
	if name == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %s is already registered", name)
	}

	r.plugins[name] = p
	r.order = append(r.order, name)

	return nil
}

// Unregister removes a plugin from the registry
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.plugins[name]; !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	delete(r.plugins, name)

	// Remove from order
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}

	return nil
}

// Get returns a plugin by name
func (r *Registry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.plugins[name]
	return p, ok
}

// All returns all registered plugins in registration order
func (r *Registry) All() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Plugin, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.plugins[name])
	}

	return result
}

// Enabled returns only enabled plugins
// The check is performed via the configuration passed to InitAll
func (r *Registry) Enabled() []Plugin {
	all := r.All()
	result := make([]Plugin, 0)

	for _, p := range all {
		if p.IsEnabled() {
			result = append(result, p)
		}
	}

	return result
}

// EnabledByConfig returns plugins enabled in configuration
func (r *Registry) EnabledByConfig(enabledNames []string) []Plugin {
	all := r.All()
	result := make([]Plugin, 0)

	enabledMap := make(map[string]bool)
	for _, name := range enabledNames {
		enabledMap[name] = true
	}

	for _, p := range all {
		if enabledMap[p.Name()] {
			result = append(result, p)
		}
	}

	return result
}

// Count returns the total number of registered plugins
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.plugins)
}

// EnabledCount returns the number of enabled plugins
func (r *Registry) EnabledCount() int {
	return len(r.Enabled())
}

// InitAll initializes all enabled plugins
// Rolls back already initialized plugins on error
func (r *Registry) InitAll(ctx context.Context, deps *PluginDependencies) error {
	enabled := r.Enabled()
	initialized := make([]Plugin, 0, len(enabled))

	for _, p := range enabled {
		if err := p.Init(ctx, deps); err != nil {
			// Rollback: stop all already initialized plugins
			for i := len(initialized) - 1; i >= 0; i-- {
				if stopErr := initialized[i].Stop(ctx); stopErr != nil {
					// Log but continue rollback
					if deps != nil && deps.Logger != nil {
						deps.Logger.Printf("Error stopping plugin %s during rollback: %v", initialized[i].Name(), stopErr)
					}
				}
			}
			return fmt.Errorf("failed to init plugin %s: %w", p.Name(), err)
		}
		initialized = append(initialized, p)
	}

	return nil
}

// StartAll starts all enabled plugins
// Rolls back already started plugins on error
func (r *Registry) StartAll(ctx context.Context) error {
	enabled := r.Enabled()
	started := make([]Plugin, 0, len(enabled))

	for _, p := range enabled {
		if err := p.Start(ctx); err != nil {
			// Rollback: stop all already started plugins
			for i := len(started) - 1; i >= 0; i-- {
				if stopErr := started[i].Stop(ctx); stopErr != nil {
					// Log but continue rollback
					// We can't access logger here easily, so just continue
				}
			}
			return fmt.Errorf("failed to start plugin %s: %w", p.Name(), err)
		}
		started = append(started, p)
	}

	return nil
}

// StopAll stops all enabled plugins in reverse order
func (r *Registry) StopAll(ctx context.Context) error {
	enabled := r.Enabled()

	// Stop in reverse order
	var lastErr error
	for i := len(enabled) - 1; i >= 0; i-- {
		p := enabled[i]
		if err := p.Stop(ctx); err != nil {
			lastErr = err
			// Continue stopping other plugins even on error
			// Logging will be done by the plugin itself
		}
	}

	return lastErr
}

// GetInfo returns information about a plugin
func (r *Registry) GetInfo(name string) (*PluginInfo, error) {
	p, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("plugin %s not found", name)
	}

	return &PluginInfo{
		Name:        p.Name(),
		Description: p.Description(),
		Version:     p.Version(),
		Enabled:     p.IsEnabled(),
		Status:      "unknown", // Can be extended for status tracking
	}, nil
}

// ListInfo returns information about all plugins
func (r *Registry) ListInfo() []*PluginInfo {
	all := r.All()
	result := make([]*PluginInfo, 0, len(all))

	for _, p := range all {
		status := "stopped"
		if p.IsEnabled() {
			status = "running"
		}

		result = append(result, &PluginInfo{
			Name:        p.Name(),
			Description: p.Description(),
			Version:     p.Version(),
			Enabled:     p.IsEnabled(),
			Status:      status,
		})
	}

	return result
}

// EnablePlugin dynamically enables and starts a plugin
func (r *Registry) EnablePlugin(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %s not found", name)
	}

	if plugin.IsEnabled() {
		return nil // Already enabled
	}

	if r.deps != nil {
		if err := plugin.Init(ctx, r.deps); err != nil {
			return fmt.Errorf("failed to init plugin %s: %w", name, err)
		}
	}

	if err := plugin.Start(ctx); err != nil {
		return fmt.Errorf("failed to start plugin %s: %w", name, err)
	}

	return nil
}

// DisablePlugin dynamically stops a plugin
func (r *Registry) DisablePlugin(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %s not found", name)
	}

	if !plugin.IsEnabled() {
		return nil // Already disabled
	}

	if err := plugin.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop plugin %s: %w", name, err)
	}

	return nil
}
