package storage

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a key is not found
	ErrNotFound = errors.New("key not found")

	// ErrPluginNotFound is returned when a plugin is not found
	ErrPluginNotFound = errors.New("plugin not found")
)

// PluginConfig represents the configuration for a single plugin
type PluginConfig struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
}

// CommandHistoryEntry represents a single command in history
type CommandHistoryEntry struct {
	Command   string    `json:"command"`
	Timestamp time.Time `json:"timestamp"`
}

// Storage is the interface for plugin configuration and data storage
type Storage interface {
	// Plugin Configuration Methods

	// EnablePlugin enables a plugin by name
	EnablePlugin(name string) error

	// DisablePlugin disables a plugin by name
	DisablePlugin(name string) error

	// IsPluginEnabled checks if a plugin is enabled
	IsPluginEnabled(name string) (bool, error)

	// GetPluginConfig returns the configuration for a plugin
	GetPluginConfig(name string) (*PluginConfig, error)

	// SetPluginConfig sets the configuration for a plugin
	SetPluginConfig(name string, cfg *PluginConfig) error

	// ListEnabledPlugins returns a list of all enabled plugin names
	ListEnabledPlugins() ([]string, error)

	// ListAllPlugins returns all plugin configurations
	ListAllPlugins() (map[string]*PluginConfig, error)

	// Plugin Data Methods

	// Get retrieves data for a plugin by key
	// Returns ErrNotFound if the key doesn't exist
	Get(pluginName, key string) ([]byte, error)

	// GetString retrieves string data for a plugin by key
	GetString(pluginName, key string) (string, error)

	// GetInt retrieves int data for a plugin by key
	GetInt(pluginName, key string) (int, error)

	// GetJSON retrieves and unmarshals JSON data for a plugin by key
	GetJSON(pluginName, key string, v interface{}) error

	// Set stores data for a plugin by key
	Set(pluginName, key string, value []byte) error

	// SetString stores string data for a plugin by key
	SetString(pluginName, key string, value string) error

	// SetInt stores int data for a plugin by key
	SetInt(pluginName, key string, value int) error

	// SetJSON marshals and stores JSON data for a plugin by key
	SetJSON(pluginName, key string, v interface{}) error

	// Delete removes data for a plugin by key
	Delete(pluginName, key string) error

	// List returns all keys and values for a plugin
	List(pluginName string) (map[string][]byte, error)

	// DeleteAll removes all data for a plugin
	DeleteAll(pluginName string) error

	// Command History Methods

	// SaveCommandHistory saves a command to history
	// Automatically prevents duplicate consecutive commands
	SaveCommandHistory(command string, timestamp time.Time) error

	// GetCommandHistory returns the last N commands from history
	// Returns up to limit commands, ordered from oldest to newest
	GetCommandHistory(limit int) ([]CommandHistoryEntry, error)

	// GetLastCommand returns the most recent command from history
	// Returns empty string if no history exists
	GetLastCommand() (string, error)

	// TrimCommandHistory keeps only the last maxCommands in history
	// Older commands are automatically removed
	TrimCommandHistory(maxCommands int) error

	// Lifecycle Methods

	// Close closes the storage
	Close() error
}

// Helper functions for JSON marshaling/unmarshaling

func marshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func unmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}
