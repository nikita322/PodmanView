package storage

import (
	"encoding/json"
	"fmt"
	"os"
)

// MigrateFromJSON loads a plugins.json file and migrates its data to BoltStorage
// This function is useful for migrating from the old JSON-based configuration
// to the new bbolt-based storage system
func MigrateFromJSON(jsonPath string, storage Storage) error {
	// Read the JSON file
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist - nothing to migrate
			return nil
		}
		return fmt.Errorf("failed to read JSON file: %w", err)
	}

	// Parse the JSON
	var configs map[string]PluginConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Migrate each plugin configuration to storage
	for name, cfg := range configs {
		if err := storage.SetPluginConfig(name, &cfg); err != nil {
			return fmt.Errorf("failed to migrate plugin %s: %w", name, err)
		}
	}

	return nil
}

// ExportToJSON exports all plugin configurations from storage to a JSON file
// This is useful for backup or debugging purposes
func ExportToJSON(storage Storage, jsonPath string) error {
	// Get all plugin configurations
	configs, err := storage.ListAllPlugins()
	if err != nil {
		return fmt.Errorf("failed to list plugins: %w", err)
	}

	// Convert to simple map for JSON
	jsonConfigs := make(map[string]PluginConfig)
	for name, cfg := range configs {
		jsonConfigs[name] = *cfg
	}

	// Marshal to JSON with pretty printing
	data, err := json.MarshalIndent(jsonConfigs, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Write to file
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write JSON file: %w", err)
	}

	return nil
}
