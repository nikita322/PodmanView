package storage_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"podmanview/internal/storage"
)

func TestBoltStorage(t *testing.T) {
	// Create temporary database file in testdata
	tmpFile := filepath.Join("testdata", "temp", "test_plugins.db")

	// Ensure temp directory exists
	os.MkdirAll(filepath.Dir(tmpFile), 0755)
	defer os.Remove(tmpFile)

	// Create storage
	store, err := storage.NewBoltStorage(tmpFile)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}
	defer store.Close()

	// Test plugin configuration
	t.Run("PluginConfig", func(t *testing.T) {
		// Set plugin config
		cfg := &storage.PluginConfig{
			Enabled: true,
			Name:    "Test Plugin",
		}
		err := store.SetPluginConfig("test", cfg)
		if err != nil {
			t.Fatalf("Failed to set plugin config: %v", err)
		}

		// Get plugin config
		retrieved, err := store.GetPluginConfig("test")
		if err != nil {
			t.Fatalf("Failed to get plugin config: %v", err)
		}

		if retrieved.Enabled != cfg.Enabled {
			t.Errorf("Expected Enabled=%v, got %v", cfg.Enabled, retrieved.Enabled)
		}
		if retrieved.Name != cfg.Name {
			t.Errorf("Expected Name=%s, got %s", cfg.Name, retrieved.Name)
		}

		// Check if enabled
		enabled, err := store.IsPluginEnabled("test")
		if err != nil {
			t.Fatalf("Failed to check if plugin enabled: %v", err)
		}
		if !enabled {
			t.Error("Expected plugin to be enabled")
		}

		// List enabled plugins
		enabledList, err := store.ListEnabledPlugins()
		if err != nil {
			t.Fatalf("Failed to list enabled plugins: %v", err)
		}
		if len(enabledList) != 1 || enabledList[0] != "test" {
			t.Errorf("Expected [test], got %v", enabledList)
		}

		// Disable plugin
		err = store.DisablePlugin("test")
		if err != nil {
			t.Fatalf("Failed to disable plugin: %v", err)
		}

		enabled, err = store.IsPluginEnabled("test")
		if err != nil {
			t.Fatalf("Failed to check if plugin enabled: %v", err)
		}
		if enabled {
			t.Error("Expected plugin to be disabled")
		}
	})

	// Test plugin data
	t.Run("PluginData", func(t *testing.T) {
		// Set string data
		err := store.SetString("test", "key1", "value1")
		if err != nil {
			t.Fatalf("Failed to set string: %v", err)
		}

		// Get string data
		value, err := store.GetString("test", "key1")
		if err != nil {
			t.Fatalf("Failed to get string: %v", err)
		}
		if value != "value1" {
			t.Errorf("Expected value1, got %s", value)
		}

		// Set int data
		err = store.SetInt("test", "counter", 42)
		if err != nil {
			t.Fatalf("Failed to set int: %v", err)
		}

		// Get int data
		counter, err := store.GetInt("test", "counter")
		if err != nil {
			t.Fatalf("Failed to get int: %v", err)
		}
		if counter != 42 {
			t.Errorf("Expected 42, got %d", counter)
		}

		// Set JSON data
		type TestData struct {
			Field1 string `json:"field1"`
			Field2 int    `json:"field2"`
		}
		testData := TestData{
			Field1: "test",
			Field2: 123,
		}
		err = store.SetJSON("test", "json", testData)
		if err != nil {
			t.Fatalf("Failed to set JSON: %v", err)
		}

		// Get JSON data
		var retrieved TestData
		err = store.GetJSON("test", "json", &retrieved)
		if err != nil {
			t.Fatalf("Failed to get JSON: %v", err)
		}
		if retrieved.Field1 != testData.Field1 || retrieved.Field2 != testData.Field2 {
			t.Errorf("Expected %+v, got %+v", testData, retrieved)
		}

		// List all data
		all, err := store.List("test")
		if err != nil {
			t.Fatalf("Failed to list data: %v", err)
		}
		if len(all) != 3 { // key1, counter, json
			t.Errorf("Expected 3 keys, got %d", len(all))
		}

		// Delete data
		err = store.Delete("test", "key1")
		if err != nil {
			t.Fatalf("Failed to delete data: %v", err)
		}

		// Verify deletion
		_, err = store.GetString("test", "key1")
		if err != storage.ErrNotFound {
			t.Errorf("Expected ErrNotFound, got %v", err)
		}
	})

	// Test non-existent data
	t.Run("NotFound", func(t *testing.T) {
		_, err := store.GetPluginConfig("nonexistent")
		if err != storage.ErrPluginNotFound {
			t.Errorf("Expected ErrPluginNotFound, got %v", err)
		}

		_, err = store.Get("nonexistent", "key")
		if err != storage.ErrNotFound {
			t.Errorf("Expected ErrNotFound, got %v", err)
		}
	})

	// Test command history
	t.Run("CommandHistory", func(t *testing.T) {
		// Save some commands
		now := time.Now()
		err := store.SaveCommandHistory("ls -la", now)
		if err != nil {
			t.Fatalf("Failed to save command: %v", err)
		}

		err = store.SaveCommandHistory("cd /tmp", now.Add(1*time.Second))
		if err != nil {
			t.Fatalf("Failed to save command: %v", err)
		}

		err = store.SaveCommandHistory("pwd", now.Add(2*time.Second))
		if err != nil {
			t.Fatalf("Failed to save command: %v", err)
		}

		// Try to save duplicate (should be skipped)
		err = store.SaveCommandHistory("pwd", now.Add(3*time.Second))
		if err != nil {
			t.Fatalf("Failed to save duplicate command: %v", err)
		}

		// Get history
		history, err := store.GetCommandHistory(10)
		if err != nil {
			t.Fatalf("Failed to get history: %v", err)
		}

		// Should have 3 commands (duplicate was skipped)
		if len(history) != 3 {
			t.Errorf("Expected 3 commands, got %d", len(history))
		}

		// Check order (oldest to newest)
		if history[0].Command != "ls -la" {
			t.Errorf("Expected first command 'ls -la', got %s", history[0].Command)
		}
		if history[1].Command != "cd /tmp" {
			t.Errorf("Expected second command 'cd /tmp', got %s", history[1].Command)
		}
		if history[2].Command != "pwd" {
			t.Errorf("Expected third command 'pwd', got %s", history[2].Command)
		}

		// Get last command
		lastCmd, err := store.GetLastCommand()
		if err != nil {
			t.Fatalf("Failed to get last command: %v", err)
		}
		if lastCmd != "pwd" {
			t.Errorf("Expected last command 'pwd', got %s", lastCmd)
		}

		// Test history limit
		history, err = store.GetCommandHistory(2)
		if err != nil {
			t.Fatalf("Failed to get limited history: %v", err)
		}
		if len(history) != 2 {
			t.Errorf("Expected 2 commands, got %d", len(history))
		}

		// Add more commands to test trim
		for i := 0; i < 10; i++ {
			cmd := fmt.Sprintf("echo %d", i)
			err := store.SaveCommandHistory(cmd, now.Add(time.Duration(i+10)*time.Second))
			if err != nil {
				t.Fatalf("Failed to save command %d: %v", i, err)
			}
		}

		// Trim to last 5 commands
		err = store.TrimCommandHistory(5)
		if err != nil {
			t.Fatalf("Failed to trim history: %v", err)
		}

		// Check that only 5 commands remain
		history, err = store.GetCommandHistory(100)
		if err != nil {
			t.Fatalf("Failed to get history after trim: %v", err)
		}
		if len(history) != 5 {
			t.Errorf("Expected 5 commands after trim, got %d", len(history))
		}
	})
}
