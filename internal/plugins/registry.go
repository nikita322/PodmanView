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
	order   []string        // registration order
	running map[string]bool // runtime state: which plugins are currently started
	deps    *PluginDependencies
}

// NewRegistry creates a new plugin registry
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		order:   make([]string, 0),
		running: make(map[string]bool),
	}
}

// SetDependencies sets the dependencies for all plugins
func (r *Registry) SetDependencies(deps *PluginDependencies) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deps = deps
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

// SetRunning marks a plugin as running (or not running) in the registry.
// This should be called after manually starting/stopping a plugin outside of EnablePlugin/DisablePlugin.
func (r *Registry) SetRunning(name string, running bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running[name] = running
}

// EnablePlugin dynamically enables and starts a plugin
func (r *Registry) EnablePlugin(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	plugin, ok := r.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %s not found", name)
	}

	if r.running[name] {
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

	r.running[name] = true

	// Start background tasks if the plugin supports them
	if runner, ok := plugin.(BackgroundTaskRunner); ok {
		bgCtx := context.Background()
		if err := runner.StartBackgroundTasks(bgCtx); err != nil {
			delete(r.running, name)
			return fmt.Errorf("failed to start background tasks for plugin %s: %w", name, err)
		}
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

	if !r.running[name] {
		return nil // Already disabled
	}

	if err := plugin.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop plugin %s: %w", name, err)
	}

	delete(r.running, name)

	return nil
}
