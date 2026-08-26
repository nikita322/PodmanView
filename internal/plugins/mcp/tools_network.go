package mcp

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type networkInterfacesInput struct{}

type networkInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	State     string   `json:"state"`
	MTU       string   `json:"mtu,omitempty"`
	Addresses []string `json:"addresses"`
}

func (p *MCPServerPlugin) registerNetworkTools() {
	mcp.AddTool(p.server, newReadOnlyTool("network.interfaces",
		"List network interfaces, IP addresses and listening TCP/UDP sockets"),
		func(ctx context.Context, req *mcp.CallToolRequest, in networkInterfacesInput) (*mcp.CallToolResult, any, error) {
			res, err := p.toolNetworkInterfaces(ctx)
			return res, nil, err
		})
}

func (p *MCPServerPlugin) toolNetworkInterfaces(ctx context.Context) (*mcp.CallToolResult, error) {
	ifaces := readNetworkInterfaces()
	addresses := readIPAddresses(ctx)

	// Attach addresses to the matching interface by name.
	for i := range ifaces {
		iface := &ifaces[i]
		iface.Addresses = addresses[iface.Name]
		if iface.Addresses == nil {
			iface.Addresses = []string{}
		}
	}

	// Listening sockets (TCP/UDP) as a convenience overview.
	listening := ""
	if res, err := runWithTimeout(ctx, 15*time.Second, "", "ss", "-tulpn"); err == nil && res.ExitCode == 0 {
		listening = strings.TrimSpace(res.Stdout)
	}

	return textResult(map[string]interface{}{
		"interfaces":       ifaces,
		"listeningSockets": listening,
	}), nil
}

// readNetworkInterfaces reads basic interface info from /sys/class/net.
func readNetworkInterfaces() []networkInterface {
	interfaces := []networkInterface{}

	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return interfaces
	}

	for _, entry := range entries {
		name := entry.Name()

		iface := networkInterface{
			Name:      name,
			Addresses: []string{},
		}

		if data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/address", name)); err == nil {
			iface.MAC = strings.TrimSpace(string(data))
		}
		if data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/operstate", name)); err == nil {
			iface.State = strings.TrimSpace(string(data))
		}
		if data, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/mtu", name)); err == nil {
			iface.MTU = strings.TrimSpace(string(data))
		}

		interfaces = append(interfaces, iface)
	}

	return interfaces
}

// readIPAddresses uses `ip -o addr show` to collect addresses per interface.
func readIPAddresses(ctx context.Context) map[string][]string {
	result := map[string][]string{}

	res, err := runWithTimeout(ctx, 15*time.Second, "", "ip", "-o", "addr", "show")
	if err != nil || res.ExitCode != 0 {
		return result
	}

	// Example line:
	// 2: eth0    inet 192.168.1.10/24 brd 192.168.1.255 scope global dynamic eth0
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		// fields[0] = "N:", fields[1] = ifname, fields[2] = family, fields[3] = address
		if fields[2] != "inet" && fields[2] != "inet6" {
			continue
		}

		iface := fields[1]
		result[iface] = append(result[iface], fields[3])
	}

	return result
}
