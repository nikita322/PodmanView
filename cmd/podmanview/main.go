package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"

	"podmanview/internal/api"
	"podmanview/internal/config"
	"podmanview/internal/podman"
)

func main() {
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
	if err := client.Ping(context.Background()); err != nil {
		log.Fatalf("Failed to ping Podman: %v", err)
	}

	// Create API server
	server := api.NewServer(client, cfg)

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

	if err := http.ListenAndServe(addr, server.Router()); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
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
