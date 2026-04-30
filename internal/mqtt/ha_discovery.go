package mqtt

import (
	"encoding/json"
	"log"
	"sync"

	"podmanview/internal/storage"
)

// DiscoveryManager manages Home Assistant MQTT Discovery
type DiscoveryManager struct {
	mqttClient *Client
	logger     *log.Logger
	storage    storage.Storage
	pluginName string

	// Cache of pre-generated discovery configs
	discoveryConfigs map[string][]byte
	discoveryMu      sync.RWMutex

	// State tracking
	lastSensorCount int
	mu              sync.RWMutex
}

// NewDiscoveryManager creates a new DiscoveryManager instance
func NewDiscoveryManager(client *Client, logger *log.Logger, storage storage.Storage, pluginName string) *DiscoveryManager {
	return &DiscoveryManager{
		mqttClient:       client,
		logger:           logger,
		storage:          storage,
		pluginName:       pluginName,
		discoveryConfigs: make(map[string][]byte),
		lastSensorCount:  0,
	}
}

// ShouldRepublishDiscovery checks if discovery configs should be republished
func (d *DiscoveryManager) ShouldRepublishDiscovery(currentSensorCount int) bool {
	// Check if discovery was published before
	published, err := d.storage.GetBool(d.pluginName, "discoveryPublished")
	if err != nil {
		published = false // First time
	}

	d.mu.RLock()
	lastCount := d.lastSensorCount
	d.mu.RUnlock()

	// Republish if:
	// 1. Never published before
	// 2. Sensor count changed (hotplug/unplug)
	shouldPublish := !published || currentSensorCount != lastCount

	if shouldPublish {
		d.mu.Lock()
		d.lastSensorCount = currentSensorCount
		d.mu.Unlock()
	}

	return shouldPublish
}

// PublishDiscoveryConfig publishes discovery config for a single sensor
func (d *DiscoveryManager) PublishDiscoveryConfig(cfg *SensorConfig) error {
	if cfg == nil {
		return nil
	}

	configJSON := d.generateDiscoveryConfig(cfg)
	if configJSON == nil {
		return nil
	}

	// Topic: homeassistant/sensor/{domain}/{sensor_id}/config
	discoveryTopic := "homeassistant/sensor/podmanview/" + cfg.SensorID + "/config"

	return d.mqttClient.PublishRaw(discoveryTopic, configJSON, true)
}

// PublishMultipleDiscoveryConfigs publishes discovery configs for multiple sensors
func (d *DiscoveryManager) PublishMultipleDiscoveryConfigs(configs []*SensorConfig) error {
	for _, cfg := range configs {
		if err := d.PublishDiscoveryConfig(cfg); err != nil {
			if d.logger != nil {
				d.logger.Printf("[%s] Failed to publish discovery for %s: %v",
					d.pluginName, cfg.SensorID, err)
			}
		}
	}

	// Mark as published
	d.markDiscoveryPublished()

	if d.logger != nil {
		d.logger.Printf("[%s] Published MQTT discovery config for %d sensors",
			d.pluginName, len(configs))
	}

	return nil
}

// generateDiscoveryConfig generates and caches Home Assistant discovery config
func (d *DiscoveryManager) generateDiscoveryConfig(cfg *SensorConfig) []byte {
	// Check cache first
	d.discoveryMu.RLock()
	if config, ok := d.discoveryConfigs[cfg.SensorID]; ok {
		d.discoveryMu.RUnlock()
		return config
	}
	d.discoveryMu.RUnlock()

	// Build configuration
	mqttCfg := d.mqttClient.GetConfig()

	discoveryConfig := map[string]interface{}{
		"name":                cfg.Name,
		"unique_id":           "podmanview_" + cfg.SensorID,
		"state_topic":         mqttCfg.Prefix + "/" + cfg.StateTopic,
		"unit_of_measurement": cfg.Unit,
	}

	// Add optional fields
	if cfg.AttributesTopic != "" {
		discoveryConfig["json_attributes_topic"] = mqttCfg.Prefix + "/" + cfg.AttributesTopic
	}

	if cfg.DeviceClass != "" {
		discoveryConfig["device_class"] = cfg.DeviceClass
	}

	if cfg.StateClass != "" {
		discoveryConfig["state_class"] = cfg.StateClass
	}

	if cfg.AvailabilityTopic != "" {
		discoveryConfig["availability_topic"] = mqttCfg.Prefix + "/" + cfg.AvailabilityTopic
		discoveryConfig["payload_available"] = "online"
		discoveryConfig["payload_not_available"] = "offline"
	}

	// Device information for grouping in Home Assistant
	if cfg.DeviceInfo != nil {
		discoveryConfig["device"] = map[string]interface{}{
			"identifiers":  cfg.DeviceInfo.Identifiers,
			"name":         cfg.DeviceInfo.Name,
			"model":        cfg.DeviceInfo.Model,
			"manufacturer": cfg.DeviceInfo.Manufacturer,
		}
	}

	configJSON, err := json.Marshal(discoveryConfig)
	if err != nil {
		if d.logger != nil {
			d.logger.Printf("[%s] Failed to marshal discovery config: %v", d.pluginName, err)
		}
		return nil
	}

	// Cache for future use
	d.discoveryMu.Lock()
	d.discoveryConfigs[cfg.SensorID] = configJSON
	d.discoveryMu.Unlock()

	return configJSON
}

// markDiscoveryPublished marks discovery as published in storage
func (d *DiscoveryManager) markDiscoveryPublished() {
	if d.storage != nil {
		if err := d.storage.SetBool(d.pluginName, "discoveryPublished", true); err != nil {
			if d.logger != nil {
				d.logger.Printf("[%s] Failed to mark discovery as published: %v",
					d.pluginName, err)
			}
		}
	}
}


