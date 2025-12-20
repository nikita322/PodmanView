package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"podmanview/internal/api"
	"podmanview/internal/config"
	"podmanview/internal/events"
	"podmanview/internal/podman"
	"podmanview/internal/plugins"
	"podmanview/internal/plugins/demo"
	"podmanview/internal/storage"
)

const (
	pluginInitTimeout  = 30 * time.Second
	pluginStartTimeout = 10 * time.Second
	shutdownTimeout    = 10 * time.Second
	pluginsDBFile      = "plugins.db"
)

// Version is set at build time via -ldflags "-X main.Version=vX.Y.Z"
var Version = "dev"

func main() {
	ctx := context.Background()

	// Generate static files version (timestamp for cache busting)
	staticVersion := fmt.Sprintf("%d", time.Now().Unix())
	log.Printf("Static files version: %s", staticVersion)

	// Load configuration from .env file
	cfg, err := config.Load(".env")
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Configuration loaded: %s", cfg)

	// Create Podman client
	var client *podman.Client

	socketPath := cfg.SocketPath()
	if socketPath != "" {
		client, err = podman.NewClientWithSocket(socketPath)
	} else {
		client, err = podman.NewClient()
	}

	if err != nil {
		log.Fatalf("Failed to connect to Podman: %v", err)
	}

	// Test connection
	if err := client.Ping(ctx); err != nil {
		log.Fatalf("Failed to ping Podman: %v", err)
	}

	// Create event store
	eventStore := events.NewStore(100)

	// Create or open BoltDB storage for plugins
	pluginStorage, err := storage.NewBoltStorage(pluginsDBFile)
	if err != nil {
		log.Fatalf("Failed to create plugin storage: %v", err)
	}
	defer pluginStorage.Close()

	// Initialize default plugin configurations if not present
	// Check if demo plugin exists in storage
	_, err = pluginStorage.GetPluginConfig("demo")
	if err == storage.ErrPluginNotFound {
		// Set default configuration for demo plugin
		log.Printf("Initializing default configuration for demo plugin")
		if err := pluginStorage.SetPluginConfig("demo", &storage.PluginConfig{
			Enabled: true,
			Name:    "Demo Plugin",
		}); err != nil {
			log.Printf("Warning: Failed to set default demo plugin config: %v", err)
		}
	}

	// Create plugin registry
	pluginRegistry := plugins.NewRegistry()

	// Register all available plugins
	// Add your plugins here
	if err := pluginRegistry.Register(demo.New()); err != nil {
		log.Fatalf("Failed to register demo plugin: %v", err)
	}

	log.Printf("Registered %d plugins", pluginRegistry.Count())

	// Get enabled plugin names from storage
	enabledPluginNames, err := pluginStorage.ListEnabledPlugins()
	if err != nil {
		log.Fatalf("Failed to list enabled plugins: %v", err)
	}
	log.Printf("Enabled plugins from storage: %v", enabledPluginNames)

	// Get enabled plugins by config (before Init, so we can't use IsEnabled())
	enabledPlugins := pluginRegistry.EnabledByConfig(enabledPluginNames)
	log.Printf("Found %d/%d enabled plugins", len(enabledPlugins), pluginRegistry.Count())

	// Initialize enabled plugins with timeout
	pluginDeps := &plugins.PluginDependencies{
		PodmanClient: client,
		Config:       cfg,
		EventStore:   eventStore,
		Logger:       log.Default(),
		Storage:      pluginStorage,
	}

	// Set dependencies in registry
	pluginRegistry.SetDependencies(pluginDeps)

	// Initialize plugins
	for _, p := range enabledPlugins {
		initCtx, cancel := context.WithTimeout(ctx, pluginInitTimeout)
		if err := p.Init(initCtx, pluginDeps); err != nil {
			cancel()
			log.Fatalf("Failed to initialize plugin %s: %v", p.Name(), err)
		}
		cancel()
	}

	// Start plugins
	for _, p := range enabledPlugins {
		startCtx, cancel := context.WithTimeout(ctx, pluginStartTimeout)
		if err := p.Start(startCtx); err != nil {
			cancel()
			log.Fatalf("Failed to start plugin %s: %v", p.Name(), err)
		}
		cancel()
	}

	// Create API server with ALL plugins (not just enabled)
	// This allows the API to show all available plugins with their enabled status
	allPlugins := pluginRegistry.All()
	server := api.NewServerWithPlugins(client, cfg, Version, staticVersion, allPlugins, pluginRegistry, pluginStorage)

	// Start server
	addr := cfg.Addr()
	fmt.Printf("PodmanView starting on %s\n", addr)

	if cfg.NoAuth() {
		fmt.Println("WARNING: Authentication is DISABLED!")
	}

	// Print access URLs
	port := addr
	if strings.HasPrefix(port, ":") {
		port = port[1:]
	} else if idx := strings.LastIndex(port, ":"); idx != -1 {
		port = port[idx+1:]
	}
	printAccessURLs(port)

	// Setup graceful shutdown
	httpServer := &http.Server{
		Addr:    addr,
		Handler: server.Router(),
	}

	// Channel to listen for interrupt signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start HTTP server in goroutine
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	log.Println("Server started. Press Ctrl+C to stop.")

	// Wait for interrupt signal
	<-stop

	log.Println("Shutting down gracefully...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Stop all enabled plugins in reverse order
	for i := len(enabledPlugins) - 1; i >= 0; i-- {
		p := enabledPlugins[i]
		if err := p.Stop(shutdownCtx); err != nil {
			log.Printf("Error stopping plugin %s: %v", p.Name(), err)
		} else {
			log.Printf("Stopped plugin: %s", p.Name())
		}
	}

	log.Println("Server stopped")
}

// getLocalIPs returns all local IP addresses
func getLocalIPs() []string {
	var ips []string

	interfaces, err := net.Interfaces()
	if err != nil {
		return ips
	}

	for _, iface := range interfaces {
		// Skip down or loopback interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Skip loopback and IPv6
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}

			ips = append(ips, ip.String())
		}
	}

	return ips
}

// printAccessURLs prints all available access URLs
func printAccessURLs(port string) {
	ips := getLocalIPs()
	if len(ips) == 0 {
		fmt.Printf("\nOpen http://localhost:%s in your browser\n", port)
		return
	}

	fmt.Println("\nAccess URLs:")
	for _, ip := range ips {
		fmt.Printf("  http://%s:%s\n", ip, port)
	}
	fmt.Println()
}
