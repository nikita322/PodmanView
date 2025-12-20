package storage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"go.etcd.io/bbolt"
)

const (
	// configBucket stores plugin configurations (enabled/disabled)
	configBucket = "_config"

	// dataBucket stores plugin-specific data
	dataBucket = "_data"

	// historyBucket stores command history
	historyBucket = "_history"
)

// BoltStorage is a bbolt implementation of the Storage interface
type BoltStorage struct {
	db *bbolt.DB
}

// NewBoltStorage creates a new BoltStorage instance
// The database file will be created if it doesn't exist
func NewBoltStorage(path string) (*BoltStorage, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{
		Timeout: 1 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open bolt database: %w", err)
	}

	// Create the main buckets if they don't exist
	err = db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(configBucket)); err != nil {
			return fmt.Errorf("failed to create config bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(dataBucket)); err != nil {
			return fmt.Errorf("failed to create data bucket: %w", err)
		}
		if _, err := tx.CreateBucketIfNotExists([]byte(historyBucket)); err != nil {
			return fmt.Errorf("failed to create history bucket: %w", err)
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &BoltStorage{db: db}, nil
}

// Plugin Configuration Methods

// EnablePlugin enables a plugin by name
func (s *BoltStorage) EnablePlugin(name string) error {
	return s.updatePluginEnabled(name, true)
}

// DisablePlugin disables a plugin by name
func (s *BoltStorage) DisablePlugin(name string) error {
	return s.updatePluginEnabled(name, false)
}

// updatePluginEnabled updates the enabled status of a plugin
func (s *BoltStorage) updatePluginEnabled(name string, enabled bool) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(configBucket))
		if bucket == nil {
			return fmt.Errorf("config bucket not found")
		}

		// Get existing config or create new one
		var cfg PluginConfig
		data := bucket.Get([]byte(name))
		if data != nil {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("failed to unmarshal plugin config: %w", err)
			}
		} else {
			cfg.Name = name
		}

		cfg.Enabled = enabled

		// Marshal and save
		newData, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal plugin config: %w", err)
		}

		return bucket.Put([]byte(name), newData)
	})
}

// IsPluginEnabled checks if a plugin is enabled
func (s *BoltStorage) IsPluginEnabled(name string) (bool, error) {
	var enabled bool
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(configBucket))
		if bucket == nil {
			return fmt.Errorf("config bucket not found")
		}

		data := bucket.Get([]byte(name))
		if data == nil {
			// Plugin not found - default to false
			enabled = false
			return nil
		}

		var cfg PluginConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("failed to unmarshal plugin config: %w", err)
		}

		enabled = cfg.Enabled
		return nil
	})

	return enabled, err
}

// GetPluginConfig returns the configuration for a plugin
func (s *BoltStorage) GetPluginConfig(name string) (*PluginConfig, error) {
	var cfg *PluginConfig
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(configBucket))
		if bucket == nil {
			return fmt.Errorf("config bucket not found")
		}

		data := bucket.Get([]byte(name))
		if data == nil {
			return ErrPluginNotFound
		}

		cfg = &PluginConfig{}
		if err := json.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("failed to unmarshal plugin config: %w", err)
		}

		return nil
	})

	return cfg, err
}

// SetPluginConfig sets the configuration for a plugin
func (s *BoltStorage) SetPluginConfig(name string, cfg *PluginConfig) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(configBucket))
		if bucket == nil {
			return fmt.Errorf("config bucket not found")
		}

		data, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("failed to marshal plugin config: %w", err)
		}

		return bucket.Put([]byte(name), data)
	})
}

// ListEnabledPlugins returns a list of all enabled plugin names
func (s *BoltStorage) ListEnabledPlugins() ([]string, error) {
	var enabled []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(configBucket))
		if bucket == nil {
			return fmt.Errorf("config bucket not found")
		}

		return bucket.ForEach(func(k, v []byte) error {
			var cfg PluginConfig
			if err := json.Unmarshal(v, &cfg); err != nil {
				return fmt.Errorf("failed to unmarshal plugin config: %w", err)
			}

			if cfg.Enabled {
				enabled = append(enabled, string(k))
			}

			return nil
		})
	})

	return enabled, err
}

// ListAllPlugins returns all plugin configurations
func (s *BoltStorage) ListAllPlugins() (map[string]*PluginConfig, error) {
	configs := make(map[string]*PluginConfig)
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(configBucket))
		if bucket == nil {
			return fmt.Errorf("config bucket not found")
		}

		return bucket.ForEach(func(k, v []byte) error {
			var cfg PluginConfig
			if err := json.Unmarshal(v, &cfg); err != nil {
				return fmt.Errorf("failed to unmarshal plugin config: %w", err)
			}

			configs[string(k)] = &cfg
			return nil
		})
	})

	return configs, err
}

// Plugin Data Methods

// Get retrieves data for a plugin by key
func (s *BoltStorage) Get(pluginName, key string) ([]byte, error) {
	var value []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(dataBucket))
		if bucket == nil {
			return fmt.Errorf("data bucket not found")
		}

		pluginBucket := bucket.Bucket([]byte(pluginName))
		if pluginBucket == nil {
			return ErrNotFound
		}

		data := pluginBucket.Get([]byte(key))
		if data == nil {
			return ErrNotFound
		}

		value = make([]byte, len(data))
		copy(value, data)
		return nil
	})

	return value, err
}

// GetString retrieves string data for a plugin by key
func (s *BoltStorage) GetString(pluginName, key string) (string, error) {
	data, err := s.Get(pluginName, key)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GetInt retrieves int data for a plugin by key
func (s *BoltStorage) GetInt(pluginName, key string) (int, error) {
	data, err := s.Get(pluginName, key)
	if err != nil {
		return 0, err
	}

	value, err := strconv.Atoi(string(data))
	if err != nil {
		return 0, fmt.Errorf("failed to parse int: %w", err)
	}

	return value, nil
}

// GetJSON retrieves and unmarshals JSON data for a plugin by key
func (s *BoltStorage) GetJSON(pluginName, key string, v interface{}) error {
	data, err := s.Get(pluginName, key)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("failed to unmarshal JSON: %w", err)
	}

	return nil
}

// Set stores data for a plugin by key
func (s *BoltStorage) Set(pluginName, key string, value []byte) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(dataBucket))
		if bucket == nil {
			return fmt.Errorf("data bucket not found")
		}

		// Create plugin bucket if it doesn't exist
		pluginBucket, err := bucket.CreateBucketIfNotExists([]byte(pluginName))
		if err != nil {
			return fmt.Errorf("failed to create plugin bucket: %w", err)
		}

		return pluginBucket.Put([]byte(key), value)
	})
}

// SetString stores string data for a plugin by key
func (s *BoltStorage) SetString(pluginName, key string, value string) error {
	return s.Set(pluginName, key, []byte(value))
}

// SetInt stores int data for a plugin by key
func (s *BoltStorage) SetInt(pluginName, key string, value int) error {
	return s.Set(pluginName, key, []byte(strconv.Itoa(value)))
}

// SetJSON marshals and stores JSON data for a plugin by key
func (s *BoltStorage) SetJSON(pluginName, key string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return s.Set(pluginName, key, data)
}

// Delete removes data for a plugin by key
func (s *BoltStorage) Delete(pluginName, key string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(dataBucket))
		if bucket == nil {
			return fmt.Errorf("data bucket not found")
		}

		pluginBucket := bucket.Bucket([]byte(pluginName))
		if pluginBucket == nil {
			return ErrNotFound
		}

		return pluginBucket.Delete([]byte(key))
	})
}

// List returns all keys and values for a plugin
func (s *BoltStorage) List(pluginName string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(dataBucket))
		if bucket == nil {
			return fmt.Errorf("data bucket not found")
		}

		pluginBucket := bucket.Bucket([]byte(pluginName))
		if pluginBucket == nil {
			// Plugin has no data yet - return empty map
			return nil
		}

		return pluginBucket.ForEach(func(k, v []byte) error {
			value := make([]byte, len(v))
			copy(value, v)
			result[string(k)] = value
			return nil
		})
	})

	return result, err
}

// DeleteAll removes all data for a plugin
func (s *BoltStorage) DeleteAll(pluginName string) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(dataBucket))
		if bucket == nil {
			return fmt.Errorf("data bucket not found")
		}

		return bucket.DeleteBucket([]byte(pluginName))
	})
}

// Command History Methods

// SaveCommandHistory saves a command to history
func (s *BoltStorage) SaveCommandHistory(command string, timestamp time.Time) error {
	// Check if this is a duplicate of the last command
	lastCmd, err := s.GetLastCommand()
	if err == nil && lastCmd == command {
		// Skip duplicate consecutive command
		return nil
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(historyBucket))
		if bucket == nil {
			return fmt.Errorf("history bucket not found")
		}

		entry := CommandHistoryEntry{
			Command:   command,
			Timestamp: timestamp,
		}

		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("failed to marshal history entry: %w", err)
		}

		// Use timestamp as key (formatted as Unix nano for sorting)
		key := []byte(fmt.Sprintf("%020d", timestamp.UnixNano()))
		return bucket.Put(key, data)
	})
}

// GetCommandHistory returns the last N commands from history
func (s *BoltStorage) GetCommandHistory(limit int) ([]CommandHistoryEntry, error) {
	var entries []CommandHistoryEntry

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(historyBucket))
		if bucket == nil {
			return fmt.Errorf("history bucket not found")
		}

		// Collect all entries first
		var allEntries []CommandHistoryEntry
		cursor := bucket.Cursor()

		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var entry CommandHistoryEntry
			if err := json.Unmarshal(v, &entry); err != nil {
				continue // Skip corrupted entries
			}
			allEntries = append(allEntries, entry)
		}

		// Return only last N entries
		if len(allEntries) > limit {
			entries = allEntries[len(allEntries)-limit:]
		} else {
			entries = allEntries
		}

		return nil
	})

	return entries, err
}

// GetLastCommand returns the most recent command from history
func (s *BoltStorage) GetLastCommand() (string, error) {
	var lastCommand string

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(historyBucket))
		if bucket == nil {
			return fmt.Errorf("history bucket not found")
		}

		// Get the last entry (bucket is sorted by key)
		cursor := bucket.Cursor()
		k, v := cursor.Last()

		if k == nil {
			// No history yet
			return nil
		}

		var entry CommandHistoryEntry
		if err := json.Unmarshal(v, &entry); err != nil {
			return fmt.Errorf("failed to unmarshal history entry: %w", err)
		}

		lastCommand = entry.Command
		return nil
	})

	return lastCommand, err
}

// TrimCommandHistory keeps only the last maxCommands in history
func (s *BoltStorage) TrimCommandHistory(maxCommands int) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(historyBucket))
		if bucket == nil {
			return fmt.Errorf("history bucket not found")
		}

		// Count total entries
		var count int
		cursor := bucket.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			count++
		}

		// If we're under the limit, nothing to do
		if count <= maxCommands {
			return nil
		}

		// Delete oldest entries
		toDelete := count - maxCommands
		cursor = bucket.Cursor()
		for k, _ := cursor.First(); k != nil && toDelete > 0; k, _ = cursor.Next() {
			if err := bucket.Delete(k); err != nil {
				return fmt.Errorf("failed to delete old entry: %w", err)
			}
			toDelete--
		}

		return nil
	})
}

// Close closes the storage
func (s *BoltStorage) Close() error {
	return s.db.Close()
}
