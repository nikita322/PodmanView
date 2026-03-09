// Package mqtt provides MQTT client functionality
package mqtt

import (
	"crypto/tls"
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// Config holds MQTT client configuration
type Config struct {
	Broker   string // MQTT broker address (e.g., "tcp://localhost:1883")
	ClientID string // Unique client ID
	Username string // MQTT username (optional)
	Password string // MQTT password (optional)
	Prefix   string // Topic prefix for all messages
	UseTLS   bool   // Enable TLS connection
}

// MessageHandler is a callback for incoming MQTT messages
type MessageHandler func(topic string, payload []byte)

// subscription holds info for re-subscribing on reconnect
type subscription struct {
	topic   string
	qos     byte
	handler mqtt.MessageHandler
}

// Client wraps the MQTT client with additional functionality
type Client struct {
	client        mqtt.Client
	config        Config
	mu            sync.RWMutex
	logger        *log.Logger
	isActive      bool
	subscriptions map[string]subscription // active subscriptions for re-subscribe on reconnect
}

// New creates a new MQTT client
func New(cfg Config, logger *log.Logger) (*Client, error) {
	if cfg.Broker == "" {
		return nil, fmt.Errorf("MQTT broker address is required")
	}

	if cfg.ClientID == "" {
		cfg.ClientID = fmt.Sprintf("podmanview-%d", time.Now().Unix())
	}

	c := &Client{
		config:        cfg,
		logger:        logger,
		subscriptions: make(map[string]subscription),
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	opts.SetClientID(cfg.ClientID)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
	}
	if cfg.Password != "" {
		opts.SetPassword(cfg.Password)
	}

	// Configure TLS if enabled
	if cfg.UseTLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: false,
		}
		opts.SetTLSConfig(tlsConfig)
	}

	// Set connection handlers
	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		if c.logger != nil {
			c.logger.Printf("[MQTT] Connection lost: %v", err)
		}
	})

	opts.SetOnConnectHandler(func(client mqtt.Client) {
		if c.logger != nil {
			c.logger.Printf("[MQTT] Connected to broker: %s", cfg.Broker)
		}
		// Re-subscribe to all active subscriptions on reconnect
		c.mu.RLock()
		subs := make([]subscription, 0, len(c.subscriptions))
		for _, s := range c.subscriptions {
			subs = append(subs, s)
		}
		c.mu.RUnlock()

		for _, s := range subs {
			token := client.Subscribe(s.topic, s.qos, s.handler)
			if token.Wait() && token.Error() != nil {
				if c.logger != nil {
					c.logger.Printf("[MQTT] Failed to re-subscribe to %s: %v", s.topic, token.Error())
				}
			} else if c.logger != nil {
				c.logger.Printf("[MQTT] Re-subscribed to %s", s.topic)
			}
		}
	})

	opts.SetReconnectingHandler(func(client mqtt.Client, options *mqtt.ClientOptions) {
		if c.logger != nil {
			c.logger.Printf("[MQTT] Attempting to reconnect...")
		}
	})

	// Auto-reconnect settings
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)

	// Keep alive settings
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(10 * time.Second)

	// Clean session
	opts.SetCleanSession(true)

	c.client = mqtt.NewClient(opts)
	return c, nil
}

// Connect establishes connection to MQTT broker
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isActive {
		return nil // Already connected
	}

	if c.logger != nil {
		c.logger.Printf("[MQTT] Connecting to broker: %s", c.config.Broker)
	}

	token := c.client.Connect()
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to connect to MQTT broker: %w", token.Error())
	}

	c.isActive = true
	if c.logger != nil {
		c.logger.Printf("[MQTT] Successfully connected")
	}

	return nil
}

// Disconnect closes connection to MQTT broker
func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isActive {
		return
	}

	c.client.Disconnect(250) // Wait up to 250ms for graceful disconnect
	c.isActive = false

	if c.logger != nil {
		c.logger.Printf("[MQTT] Disconnected from broker")
	}
}

// Publish publishes a message to the specified topic with QoS 0 (default for telemetry)
func (c *Client) Publish(topic string, payload interface{}) error {
	return c.PublishWithQoS(topic, 0, false, payload)
}

// PublishWithQoS publishes a message with explicit QoS and retained settings
func (c *Client) PublishWithQoS(topic string, qos byte, retained bool, payload interface{}) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isActive {
		return fmt.Errorf("MQTT client is not connected")
	}

	// Add prefix to topic
	fullTopic := c.buildTopic(topic)

	// Publish with specified QoS and retained flag
	token := c.client.Publish(fullTopic, qos, retained, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish message: %w", token.Error())
	}

	if c.logger != nil {
		c.logger.Printf("[MQTT] Published to %s (QoS %d, retained %v)", fullTopic, qos, retained)
	}

	return nil
}

// PublishRaw publishes a message without adding prefix (for discovery topics)
func (c *Client) PublishRaw(topic string, payload interface{}, retained bool) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.isActive {
		return fmt.Errorf("MQTT client is not connected")
	}

	// Publish with QoS 1 without prefix
	token := c.client.Publish(topic, 1, retained, payload)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to publish message: %w", token.Error())
	}

	if c.logger != nil {
		c.logger.Printf("[MQTT] Published (raw) to %s", topic)
	}

	return nil
}

// Subscribe subscribes to a topic with prefix and calls handler on messages
func (c *Client) Subscribe(topic string, qos byte, handler MessageHandler) error {
	fullTopic := c.buildTopic(topic)
	return c.SubscribeRaw(fullTopic, qos, handler)
}

// SubscribeRaw subscribes to a topic without adding prefix
func (c *Client) SubscribeRaw(topic string, qos byte, handler MessageHandler) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isActive {
		return fmt.Errorf("MQTT client is not connected")
	}

	// Wrap handler to match paho signature
	pahoHandler := func(client mqtt.Client, msg mqtt.Message) {
		handler(msg.Topic(), msg.Payload())
	}

	token := c.client.Subscribe(topic, qos, pahoHandler)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", topic, token.Error())
	}

	// Store for re-subscribe on reconnect
	c.subscriptions[topic] = subscription{
		topic:   topic,
		qos:     qos,
		handler: pahoHandler,
	}

	if c.logger != nil {
		c.logger.Printf("[MQTT] Subscribed to %s (QoS %d)", topic, qos)
	}

	return nil
}

// Unsubscribe removes subscription from a topic
func (c *Client) Unsubscribe(topics ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.isActive {
		return fmt.Errorf("MQTT client is not connected")
	}

	token := c.client.Unsubscribe(topics...)
	if token.Wait() && token.Error() != nil {
		return fmt.Errorf("failed to unsubscribe: %w", token.Error())
	}

	for _, t := range topics {
		delete(c.subscriptions, t)
	}

	if c.logger != nil {
		c.logger.Printf("[MQTT] Unsubscribed from %v", topics)
	}

	return nil
}

// buildTopic constructs full topic path with prefix
func (c *Client) buildTopic(topic string) string {
	if c.config.Prefix == "" {
		return topic
	}
	return c.config.Prefix + "/" + topic
}

// IsConnected returns true if client is connected to broker
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isActive && c.client.IsConnected()
}

// GetConfig returns the current MQTT configuration
func (c *Client) GetConfig() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.config
}
