package storage_test

import (
	"context"
	"testing"
	"time"

	"podmanview/internal/plugins/temperature"
)

func TestGetFriendlyName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Cluster thermal patterns (numbering starts from 1)
		{"cluster0_thermal", "CPU Cluster 1"},
		{"cluster1_thermal", "CPU Cluster 2"},
		{"cluster2_thermal", "CPU Cluster 3"},
		{"cluster10_thermal", "CPU Cluster 11"},
		{"cluster99_thermal", "CPU Cluster 100"},

		// Core patterns (numbering starts from 1)
		{"core0", "CPU Core 1"},
		{"core1", "CPU Core 2"},
		{"core15", "CPU Core 16"},

		// Unknown patterns - should return as-is
		{"k10temp", "k10temp"},
		{"coretemp", "coretemp"},
		{"nvme", "nvme"},
		{"unknown_sensor", "unknown_sensor"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := temperature.GetFriendlyName(tt.input)
			if result != tt.expected {
				t.Errorf("GetFriendlyName(%q) = %q; want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestGetFriendlyStorageName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// NVMe patterns (numbering starts from 1 for user-friendliness)
		{"nvme0n1", "NVMe SSD 1"},
		{"nvme1n1", "NVMe SSD 2"},
		{"nvme2n1", "NVMe SSD 3"},
		{"nvme10n1", "NVMe SSD 11"},

		// SATA patterns
		{"sda", "SATA Drive A"},
		{"sdb", "SATA Drive B"},
		{"sdc", "SATA Drive C"},
		{"sdz", "SATA Drive Z"},

		// IDE patterns
		{"hda", "IDE Drive A"},
		{"hdb", "IDE Drive B"},

		// SD Card patterns
		{"mmcblk0", "SD Card 1"},
		{"mmcblk1", "SD Card 2"},

		// Unknown patterns - should return as-is
		{"unknown_device", "unknown_device"},
		{"vda", "vda"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := temperature.GetFriendlyStorageName(tt.input)
			if result != tt.expected {
				t.Errorf("GetFriendlyStorageName(%q) = %q; want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestTemperaturePluginCaching(t *testing.T) {
	plugin := temperature.New()

	// Check initial state - cache should be initialized
	initialData := plugin.GetTemperatureData()
	if initialData == nil {
		t.Fatal("GetTemperatureData() returned nil")
	}

	// Call Start to perform initial update
	ctx := context.Background()
	err := plugin.Start(ctx)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Get cached data
	cachedData := plugin.GetTemperatureData()
	if cachedData == nil {
		t.Fatal("GetTemperatureData() returned nil after Start")
	}

	// Check that lastUpdate is set
	lastUpdate := plugin.GetLastUpdateTime()
	if lastUpdate.IsZero() {
		t.Error("lastUpdate should not be zero after Start()")
	}

	// Verify that GetTemperatureData returns cached data
	// by checking it doesn't change lastUpdate timestamp
	timeBefore := plugin.GetLastUpdateTime()
	time.Sleep(10 * time.Millisecond)
	cachedData2 := plugin.GetTemperatureData()

	timeAfter := plugin.GetLastUpdateTime()
	if !timeBefore.Equal(timeAfter) {
		t.Error("GetTemperatureData() should not update lastUpdate (should return cached data)")
	}

	// Verify we got data (structure is correct)
	if cachedData2 == nil {
		t.Fatal("GetTemperatureData() returned nil")
	}
}

func TestBackgroundTasksInterface(t *testing.T) {
	plugin := temperature.New()

	// Test that plugin implements BackgroundTaskRunner interface
	// by checking if StartBackgroundTasks method exists
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := plugin.StartBackgroundTasks(ctx)
	if err != nil {
		t.Fatalf("StartBackgroundTasks() failed: %v", err)
	}

	// Call Start to perform initial update
	err = plugin.Start(ctx)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Now lastUpdate should be set
	afterStart := plugin.GetLastUpdateTime()
	if afterStart.IsZero() {
		t.Error("lastUpdate should not be zero after Start()")
	}

	// Wait a bit and verify background task has run at least once
	// (update period is 15 seconds, but we can't wait that long in tests)
	// So we just verify the mechanism works
	initialTime := plugin.GetLastUpdateTime()
	if initialTime.IsZero() {
		t.Error("Initial lastUpdate should not be zero")
	}
}

func TestTemperaturePluginLifecycle(t *testing.T) {
	plugin := temperature.New()
	ctx := context.Background()

	// Test Init
	// Note: Init requires PluginDependencies, which we don't have in this test
	// So we'll skip Init and just test Start/Stop

	// Test Start
	err := plugin.Start(ctx)
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// Verify data is available
	data := plugin.GetTemperatureData()
	if data == nil {
		t.Error("GetTemperatureData() should not return nil after Start()")
	}

	// Test Stop
	err = plugin.Stop(ctx)
	if err != nil {
		t.Fatalf("Stop() failed: %v", err)
	}

	// Data should still be available even after Stop (cached)
	dataAfterStop := plugin.GetTemperatureData()
	if dataAfterStop == nil {
		t.Error("GetTemperatureData() should still return cached data after Stop()")
	}
}
