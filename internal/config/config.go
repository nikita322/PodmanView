package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Environment variable names
const (
	EnvAddr          = "PODMANVIEW_ADDR"
	EnvJWTSecret     = "PODMANVIEW_JWT_SECRET"
	EnvJWTExpiration = "PODMANVIEW_JWT_EXPIRATION"
	EnvNoAuth        = "PODMANVIEW_NO_AUTH"
	EnvSocket        = "PODMANVIEW_SOCKET"
)

// Default values
const (
	DefaultAddr          = ":80"
	DefaultJWTExpiration = 24 * time.Hour
	DefaultNoAuth        = false
	DefaultSocket        = "" // auto-detect
)

// Config holds all application configuration.
// All access should be through getter methods for thread safety.
type Config struct {
	mu       sync.RWMutex
	filePath string
	dirty    bool // tracks if config was modified

	// Server settings
	addr string

	// Security settings
	jwtSecret     string
	jwtExpiration time.Duration
	noAuth        bool

	// Podman settings
	socketPath string
}

// Load loads configuration from .env file or creates it with defaults.
// This is the main entry point for configuration initialization.
func Load(filePath string) (*Config, error) {
	cfg := &Config{
		filePath: filePath,
	}

	// Set defaults first
	cfg.setDefaults()

	// Try to load existing file
	if err := cfg.loadFromFile(); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		// File doesn't exist - will be created with defaults
		cfg.dirty = true
	}

	// Generate JWT secret if empty
	if cfg.jwtSecret == "" {
		secret, err := generateSecureSecret(32)
		if err != nil {
			return nil, fmt.Errorf("failed to generate JWT secret: %w", err)
		}
		cfg.jwtSecret = secret
		cfg.dirty = true
	}

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Save if config was modified (new file or generated secret)
	if cfg.dirty {
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("failed to save config: %w", err)
		}
	}

	return cfg, nil
}

// setDefaults initializes all fields with default values.
func (c *Config) setDefaults() {
	c.addr = DefaultAddr
	c.jwtSecret = ""
	c.jwtExpiration = DefaultJWTExpiration
	c.noAuth = DefaultNoAuth
	c.socketPath = DefaultSocket
}

// loadFromFile reads configuration from .env file.
func (c *Config) loadFromFile() error {
	file, err := os.Open(c.filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	values, err := ParseEnvFile(file)
	if err != nil {
		return err
	}

	c.applyValues(values)
	return nil
}

// applyValues applies parsed key-value pairs to config.
func (c *Config) applyValues(values map[string]string) {
	if v, ok := values[EnvAddr]; ok && v != "" {
		c.addr = v
	}

	if v, ok := values[EnvJWTSecret]; ok && v != "" {
		c.jwtSecret = v
	}

	if v, ok := values[EnvJWTExpiration]; ok && v != "" {
		if seconds, err := strconv.Atoi(v); err == nil && seconds > 0 {
			c.jwtExpiration = time.Duration(seconds) * time.Second
		}
	}

	if v, ok := values[EnvNoAuth]; ok {
		c.noAuth = parseBool(v)
	}

	if v, ok := values[EnvSocket]; ok {
		c.socketPath = v
	}
}

// validate checks if configuration is valid.
func (c *Config) validate() error {
	// Validate server address
	if c.addr == "" {
		return errors.New("server address cannot be empty")
	}

	// Check if address format is valid
	host, port, err := net.SplitHostPort(c.addr)
	if err != nil {
		// Try with default host
		if _, err := strconv.Atoi(strings.TrimPrefix(c.addr, ":")); err != nil {
			return fmt.Errorf("invalid server address format: %s", c.addr)
		}
	} else {
		if port == "" {
			return errors.New("port cannot be empty")
		}
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			return fmt.Errorf("invalid port number: %s", port)
		}
		_ = host // host can be empty (bind to all interfaces)
	}

	// Validate JWT expiration
	if c.jwtExpiration < time.Minute {
		return errors.New("JWT expiration must be at least 1 minute")
	}
	if c.jwtExpiration > 365*24*time.Hour {
		return errors.New("JWT expiration cannot exceed 1 year")
	}

	// Validate socket path if specified
	if c.socketPath != "" {
		// Just check it's not obviously invalid
		if strings.ContainsAny(c.socketPath, "\x00") {
			return errors.New("socket path contains invalid characters")
		}
	}

	return nil
}

// Save writes current configuration to .env file.
func (c *Config) Save() error {
	c.mu.RLock()
	values := c.toMap()
	filePath := c.filePath
	c.mu.RUnlock()

	if err := WriteEnvFile(filePath, values); err != nil {
		return err
	}

	c.mu.Lock()
	c.dirty = false
	c.mu.Unlock()

	return nil
}

// toMap converts config to key-value map for saving.
func (c *Config) toMap() map[string]string {
	return map[string]string{
		EnvAddr:          c.addr,
		EnvJWTSecret:     c.jwtSecret,
		EnvJWTExpiration: strconv.Itoa(int(c.jwtExpiration.Seconds())),
		EnvNoAuth:        strconv.FormatBool(c.noAuth),
		EnvSocket:        c.socketPath,
	}
}

// Getters (thread-safe)

// Addr returns the server address.
func (c *Config) Addr() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.addr
}

// JWTSecret returns the JWT secret key.
func (c *Config) JWTSecret() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.jwtSecret
}

// JWTExpiration returns the JWT token expiration duration.
func (c *Config) JWTExpiration() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.jwtExpiration
}

// NoAuth returns whether authentication is disabled.
func (c *Config) NoAuth() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.noAuth
}

// SocketPath returns the Podman socket path.
func (c *Config) SocketPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.socketPath
}

// FilePath returns the path to the .env file.
func (c *Config) FilePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.filePath
}

// Setters (thread-safe, auto-save)

// SetAddr sets the server address and saves to file.
func (c *Config) SetAddr(addr string) error {
	c.mu.Lock()
	c.addr = addr
	c.dirty = true
	c.mu.Unlock()

	if err := c.validate(); err != nil {
		return err
	}
	return c.Save()
}

// SetJWTSecret sets the JWT secret and saves to file.
func (c *Config) SetJWTSecret(secret string) error {
	if secret == "" {
		return errors.New("JWT secret cannot be empty")
	}

	c.mu.Lock()
	c.jwtSecret = secret
	c.dirty = true
	c.mu.Unlock()

	return c.Save()
}

// SetJWTExpiration sets the JWT expiration and saves to file.
func (c *Config) SetJWTExpiration(d time.Duration) error {
	c.mu.Lock()
	c.jwtExpiration = d
	c.dirty = true
	c.mu.Unlock()

	if err := c.validate(); err != nil {
		return err
	}
	return c.Save()
}

// SetNoAuth sets the no-auth flag and saves to file.
func (c *Config) SetNoAuth(noAuth bool) error {
	c.mu.Lock()
	c.noAuth = noAuth
	c.dirty = true
	c.mu.Unlock()

	return c.Save()
}

// SetSocketPath sets the Podman socket path and saves to file.
func (c *Config) SetSocketPath(path string) error {
	c.mu.Lock()
	c.socketPath = path
	c.dirty = true
	c.mu.Unlock()

	if err := c.validate(); err != nil {
		return err
	}
	return c.Save()
}

// Helper functions

// generateSecureSecret generates a cryptographically secure random hex string.
func generateSecureSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// parseBool parses a boolean string value.
// Accepts: true, false, 1, 0, yes, no (case-insensitive)
func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// Reload reloads configuration from file.
// Useful for hot-reloading configuration.
func (c *Config) Reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Save current JWT secret in case file doesn't have one
	currentSecret := c.jwtSecret

	// Reset to defaults
	c.setDefaults()

	// Load from file
	if err := c.loadFromFile(); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	}

	// Restore JWT secret if not in file
	if c.jwtSecret == "" {
		c.jwtSecret = currentSecret
	}

	return c.validate()
}

// String returns a string representation of the config (without secrets).
func (c *Config) String() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	secretDisplay := "[not set]"
	if c.jwtSecret != "" {
		secretDisplay = "[set]"
	}

	return fmt.Sprintf(
		"Config{Addr: %q, JWTSecret: %s, JWTExpiration: %v, NoAuth: %v, SocketPath: %q}",
		c.addr, secretDisplay, c.jwtExpiration, c.noAuth, c.socketPath,
	)
}
