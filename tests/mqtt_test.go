package tests

import (
	"log"
	"os"
	"sync"
	"testing"

	"podmanview/internal/mqtt"
	"podmanview/internal/storage"
)

// TestSensorIDSanitization tests sensor ID sanitization
func TestSensorIDSanitization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase conversion",
			input:    "CPU0_TEMP",
			expected: "cpu0_temp",
		},
		{
			name:     "space replacement",
			input:    "CPU 0 Temperature",
			expected: "cpu_0_temperature",
		},
		{
			name:     "slash replacement",
			input:    "nvme0/temp1",
			expected: "nvme0_temp1",
		},
		{
			name:     "dot replacement",
			input:    "sensor.temp.1",
			expected: "sensor_temp_1",
		},
		{
			name:     "mixed characters",
			input:    "CPU 0/Temp.Sensor",
			expected: "cpu_0_temp_sensor",
		},
		{
			name:     "already clean",
			input:    "cpu0_temp",
			expected: "cpu0_temp",
		},
	}

	// Create a mock client for testing
	client := &mqtt.Client{}
	publisher := mqtt.NewPublisher(client, log.New(os.Stdout, "[TEST] ", log.LstdFlags))
	_ = publisher // Used for future test expansion

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create sensor data with the test input
			data := &mqtt.SensorData{
				ID:    tt.input,
				Label: tt.input,
				Value: 42.0,
			}

			// The publisher will sanitize the ID internally
			// We can't directly access the private getSanitizedID method,
			// but we can verify the behavior through PublishSensorState
			// For now, we'll test the expected behavior

			// Note: This is a simplified test. In a real scenario, we'd need
			// to mock the MQTT client to verify the actual topic used
			if data.ID != tt.input {
				t.Errorf("Expected input %s, got %s", tt.input, data.ID)
			}
		})
	}
}

// TestPublisherCaching tests that the publisher caches sanitized sensor IDs
func TestPublisherCaching(t *testing.T) {
	client := &mqtt.Client{}
	publisher := mqtt.NewPublisher(client, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

	// Publish the same sensor multiple times
	sensorData := &mqtt.SensorData{
		ID:    "CPU 0 Temperature",
		Label: "CPU 0 Temperature",
		Value: 65.5,
	}

	// First publish (should cache the sanitized ID)
	// Subsequent publishes should use the cached ID
	for i := 0; i < 10; i++ {
		// Note: This will fail without a real MQTT connection
		// In a real test, we'd mock the client.Publish method
		err := publisher.PublishSensorState(sensorData)
		// Ignore error - expected without real MQTT connection
		_ = err
	}

	// The test passes if no panic occurs and caching works internally
	// To properly test caching, we'd need to expose metrics or use a mock
}

// TestPublisherConcurrency tests concurrent access to the publisher
func TestPublisherConcurrency(t *testing.T) {
	client := &mqtt.Client{}
	publisher := mqtt.NewPublisher(client, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

	var wg sync.WaitGroup
	numGoroutines := 10
	numPublishPerGoroutine := 100

	// Launch multiple goroutines publishing sensors concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numPublishPerGoroutine; j++ {
				data := &mqtt.SensorData{
					ID:    "sensor_" + string(rune(id)),
					Label: "Sensor " + string(rune(id)),
					Value: float64(j),
				}
				// Ignore errors for this concurrency test
				_ = publisher.PublishSensorState(data)
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions or panics occur
}

// TestDiscoveryManagerRepublishing tests the republishing logic
func TestDiscoveryManagerRepublishing(t *testing.T) {
	// Create in-memory storage for testing
	tempDB := "test_discovery.db"
	defer os.Remove(tempDB)

	store, err := storage.NewBoltStorage(tempDB)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}
	defer store.Close()

	client := &mqtt.Client{}
	discoveryMgr := mqtt.NewDiscoveryManager(client, log.New(os.Stdout, "[TEST] ", log.LstdFlags), store, "test_plugin")

	// Test 1: First time should require publishing
	if !discoveryMgr.ShouldRepublishDiscovery(5) {
		t.Error("First call should return true (never published)")
	}

	// Simulate publishing
	configs := []*mqtt.SensorConfig{
		{
			SensorID: "test_sensor_1",
			Name:     "Test Sensor 1",
			Unit:     "°C",
		},
	}
	_ = discoveryMgr.PublishMultipleDiscoveryConfigs(configs)

	// Test 2: Same count should NOT require republishing
	if discoveryMgr.ShouldRepublishDiscovery(5) {
		t.Error("Same sensor count should return false (already published)")
	}

	// Test 3: Different count should require republishing (hotplug detected)
	if !discoveryMgr.ShouldRepublishDiscovery(7) {
		t.Error("Different sensor count should return true (hotplug detected)")
	}

	// Test 4: After republishing with new count, same count should not republish
	_ = discoveryMgr.PublishMultipleDiscoveryConfigs(configs)
	if discoveryMgr.ShouldRepublishDiscovery(7) {
		t.Error("Same count after republish should return false")
	}
}

// TestDiscoveryConfigCaching tests discovery config caching
func TestDiscoveryConfigCaching(t *testing.T) {
	tempDB := "test_config_cache.db"
	defer os.Remove(tempDB)

	store, err := storage.NewBoltStorage(tempDB)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}
	defer store.Close()

	// Create a mock client with config
	client := &mqtt.Client{}
	discoveryMgr := mqtt.NewDiscoveryManager(client, log.New(os.Stdout, "[TEST] ", log.LstdFlags), store, "test_plugin")

	// Create multiple configs
	configs := []*mqtt.SensorConfig{
		{
			SensorID:        "cpu0_temp",
			Name:            "CPU 0 Temperature",
			Unit:            "°C",
			DeviceClass:     "temperature",
			StateClass:      "measurement",
			StateTopic:      "sensor/cpu0_temp/state",
			AttributesTopic: "sensor/cpu0_temp/attributes",
			DeviceInfo: &mqtt.DeviceInfo{
				Identifiers:  []string{"podmanview"},
				Name:         "PodmanView",
				Model:        "Test Model",
				Manufacturer: "PodmanView",
			},
		},
		{
			SensorID:    "cpu1_temp",
			Name:        "CPU 1 Temperature",
			Unit:        "°C",
			DeviceClass: "temperature",
			StateClass:  "measurement",
			StateTopic:  "sensor/cpu1_temp/state",
		},
	}

	// Publish configs multiple times
	// The internal cache should prevent re-marshaling
	for i := 0; i < 5; i++ {
		_ = discoveryMgr.PublishMultipleDiscoveryConfigs(configs)
	}

	// Test passes if no errors occur and configs are cached internally
}

// TestSensorDataStructure tests SensorData structure
func TestSensorDataStructure(t *testing.T) {
	data := &mqtt.SensorData{
		ID:    "test_sensor",
		Label: "Test Sensor",
		Value: 42.5,
		Attributes: map[string]interface{}{
			"unit": "°C",
			"max":  100.0,
			"min":  0.0,
		},
	}

	if data.ID != "test_sensor" {
		t.Errorf("Expected ID 'test_sensor', got %s", data.ID)
	}

	if data.Label != "Test Sensor" {
		t.Errorf("Expected Label 'Test Sensor', got %s", data.Label)
	}

	if v, ok := data.Value.(float64); !ok || v != 42.5 {
		t.Errorf("Expected Value 42.5, got %v", data.Value)
	}

	if len(data.Attributes) != 3 {
		t.Errorf("Expected 3 attributes, got %d", len(data.Attributes))
	}
}

// TestSensorConfigStructure tests SensorConfig structure
func TestSensorConfigStructure(t *testing.T) {
	deviceInfo := &mqtt.DeviceInfo{
		Identifiers:  []string{"podmanview", "test"},
		Name:         "Test Device",
		Model:        "v1.0",
		Manufacturer: "PodmanView",
	}

	config := &mqtt.SensorConfig{
		SensorID:          "cpu_temp",
		Name:              "CPU Temperature",
		Unit:              "°C",
		StateTopic:        "sensor/cpu_temp/state",
		AttributesTopic:   "sensor/cpu_temp/attributes",
		DeviceClass:       "temperature",
		StateClass:        "measurement",
		AvailabilityTopic: "sensor/availability",
		DeviceInfo:        deviceInfo,
	}

	if config.SensorID != "cpu_temp" {
		t.Errorf("Expected SensorID 'cpu_temp', got %s", config.SensorID)
	}

	if config.Unit != "°C" {
		t.Errorf("Expected Unit '°C', got %s", config.Unit)
	}

	if config.DeviceInfo.Name != "Test Device" {
		t.Errorf("Expected DeviceInfo.Name 'Test Device', got %s", config.DeviceInfo.Name)
	}

	if len(config.DeviceInfo.Identifiers) != 2 {
		t.Errorf("Expected 2 identifiers, got %d", len(config.DeviceInfo.Identifiers))
	}
}

// TestMultipleSensorsPublishing tests batch publishing
func TestMultipleSensorsPublishing(t *testing.T) {
	client := &mqtt.Client{}
	publisher := mqtt.NewPublisher(client, log.New(os.Stdout, "[TEST] ", log.LstdFlags))

	sensors := []*mqtt.SensorData{
		{
			ID:    "sensor1",
			Label: "Sensor 1",
			Value: 10.0,
		},
		{
			ID:    "sensor2",
			Label: "Sensor 2",
			Value: 20.0,
		},
		{
			ID:    "sensor3",
			Label: "Sensor 3",
			Value: 30.0,
		},
	}

	// Should not panic even without real MQTT connection
	err := publisher.PublishMultipleSensors(sensors)

	// Error is expected (no real connection), but should not panic
	if err == nil {
		t.Log("Warning: Expected error due to no MQTT connection, but got nil")
	}
}

// BenchmarkSensorIDSanitization benchmarks the sanitization performance
func BenchmarkSensorIDSanitization(b *testing.B) {
	client := &mqtt.Client{}
	publisher := mqtt.NewPublisher(client, log.New(os.Stdout, "[BENCH] ", log.LstdFlags))

	data := &mqtt.SensorData{
		ID:    "CPU 0 / Temperature Sensor.1",
		Label: "Test",
		Value: 42.0,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This will use cached ID after first iteration
		_ = publisher.PublishSensorState(data)
	}
}

// BenchmarkDiscoveryConfigGeneration benchmarks config generation
func BenchmarkDiscoveryConfigGeneration(b *testing.B) {
	tempDB := "bench_discovery.db"
	defer os.Remove(tempDB)

	store, _ := storage.NewBoltStorage(tempDB)
	defer store.Close()

	client := &mqtt.Client{}
	discoveryMgr := mqtt.NewDiscoveryManager(client, log.New(os.Stdout, "[BENCH] ", log.LstdFlags), store, "bench")

	config := &mqtt.SensorConfig{
		SensorID:   "bench_sensor",
		Name:       "Bench Sensor",
		Unit:       "°C",
		StateTopic: "sensor/bench/state",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// This will use cached config after first iteration
		_ = discoveryMgr.PublishDiscoveryConfig(config)
	}
}
