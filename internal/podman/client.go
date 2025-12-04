package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Client represents a Podman API client
type Client struct {
	httpClient *http.Client
	socketPath string
}

// NewClient creates a new Podman client
// It tries rootless socket first, then falls back to rootful
func NewClient() (*Client, error) {
	socketPaths := []string{
		fmt.Sprintf("/run/user/%d/podman/podman.sock", os.Getuid()),
		"/run/podman/podman.sock",
	}

	for _, path := range socketPaths {
		if _, err := os.Stat(path); err == nil {
			client := &Client{
				socketPath: path,
				httpClient: &http.Client{
					Transport: &http.Transport{
						DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
							return net.Dial("unix", path)
						},
					},
					Timeout: 30 * time.Second,
				},
			}
			return client, nil
		}
	}

	return nil, fmt.Errorf("podman socket not found")
}

// NewClientWithSocket creates a client with specific socket path
func NewClientWithSocket(socketPath string) (*Client, error) {
	if _, err := os.Stat(socketPath); err != nil {
		return nil, fmt.Errorf("socket not found: %s", socketPath)
	}

	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
			Timeout: 30 * time.Second,
		},
	}, nil
}

// request makes HTTP request to Podman API
func (c *Client) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	url := "http://localhost" + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// get performs GET request and decodes JSON response
func (c *Client) get(ctx context.Context, path string, result interface{}) error {
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// post performs POST request
func (c *Client) post(ctx context.Context, path string, body interface{}) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(data))
	}

	resp, err := c.request(ctx, http.MethodPost, path, reader)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// delete performs DELETE request
func (c *Client) delete(ctx context.Context, path string) error {
	resp, err := c.request(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Container types
type Container struct {
	ID      string   `json:"Id"`
	Names   []string `json:"Names"`
	Image   string   `json:"Image"`
	ImageID string   `json:"ImageID"`
	Command []string `json:"Command"`
	State   string   `json:"State"`
	Status  string   `json:"Status"`
	Ports   []Port   `json:"Ports"`
}

type Port struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

type ContainerInspect struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	State   struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
	} `json:"State"`
	Image  string `json:"Image"`
	Config struct {
		Hostname string            `json:"Hostname"`
		Env      []string          `json:"Env"`
		Cmd      []string          `json:"Cmd"`
		Labels   map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

// ListContainers returns list of all containers (running and stopped)
func (c *Client) ListContainers(ctx context.Context) ([]Container, error) {
	var containers []Container
	err := c.get(ctx, "/v4.0.0/libpod/containers/json?all=true", &containers)
	return containers, err
}

// InspectContainer returns detailed info about container
func (c *Client) InspectContainer(ctx context.Context, id string) (*ContainerInspect, error) {
	var info ContainerInspect
	err := c.get(ctx, fmt.Sprintf("/v4.0.0/libpod/containers/%s/json", id), &info)
	return &info, err
}

// ContainerStats represents resource usage statistics for a container
type ContainerStats struct {
	ContainerID string  `json:"ContainerID"`
	Name        string  `json:"Name"`
	CPU         float64 `json:"CPU"`
	MemUsage    uint64  `json:"MemUsage"`
	MemLimit    uint64  `json:"MemLimit"`
	MemPerc     float64 `json:"MemPerc"`
}

// GetContainersStats returns stats for all running containers
func (c *Client) GetContainersStats(ctx context.Context) ([]ContainerStats, error) {
	resp, err := c.request(ctx, http.MethodGet, "/v4.0.0/libpod/containers/stats?stream=false", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d", resp.StatusCode)
	}

	var result struct {
		Stats []ContainerStats `json:"Stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Stats, nil
}

// StartContainer starts a container
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.post(ctx, fmt.Sprintf("/v4.0.0/libpod/containers/%s/start", id), nil)
}

// StopContainer stops a container
func (c *Client) StopContainer(ctx context.Context, id string) error {
	return c.post(ctx, fmt.Sprintf("/v4.0.0/libpod/containers/%s/stop", id), nil)
}

// RestartContainer restarts a container
func (c *Client) RestartContainer(ctx context.Context, id string) error {
	return c.post(ctx, fmt.Sprintf("/v4.0.0/libpod/containers/%s/restart", id), nil)
}

// RemoveContainer removes a container
func (c *Client) RemoveContainer(ctx context.Context, id string, force bool) error {
	path := fmt.Sprintf("/v4.0.0/libpod/containers/%s", id)
	if force {
		path += "?force=true"
	}
	return c.delete(ctx, path)
}

// ContainerCreateConfig represents container creation options
type ContainerCreateConfig struct {
	Name         string            `json:"name,omitempty"`
	Image        string            `json:"image"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	PortMappings []PortMapping     `json:"portmappings,omitempty"`
	Mounts       []Mount           `json:"mounts,omitempty"`
}

// PortMapping represents a port mapping
type PortMapping struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Protocol      string `json:"protocol,omitempty"`
}

// Mount represents a volume mount
type Mount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// CreateContainerResponse represents the response from container creation
type CreateContainerResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// CreateContainer creates a new container
func (c *Client) CreateContainer(ctx context.Context, config *ContainerCreateConfig) (*CreateContainerResponse, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	resp, err := c.request(ctx, http.MethodPost, "/v4.0.0/libpod/containers/create", strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result CreateContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetContainerLogs returns container logs
func (c *Client) GetContainerLogs(ctx context.Context, id string, tail int) (string, error) {
	path := fmt.Sprintf("/v4.0.0/libpod/containers/%s/logs?stdout=true&stderr=true&tail=%d", id, tail)
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Parse multiplexed stream and reverse order (newest first)
	lines := parseContainerLogs(body)

	// Reverse lines order (newest first)
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	// Strip ANSI escape codes
	result := strings.Join(lines, "\n")
	result = stripAnsiCodes(result)

	return result, nil
}

// stripAnsiCodes removes ANSI escape sequences from string
func stripAnsiCodes(s string) string {
	// Match ANSI escape sequences: ESC[ ... m (colors, styles)
	// and ESC[ ... other control codes
	result := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '\x1b' && s[i+1] == '[' {
			// Skip until we find the terminating character
			j := i + 2
			for j < len(s) {
				c := s[j]
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
					j++
					break
				}
				j++
			}
			i = j
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}

// parseContainerLogs parses multiplexed log stream
// Each frame: [1 byte type][3 bytes padding][4 bytes size BE][payload]
func parseContainerLogs(data []byte) []string {
	var lines []string
	pos := 0

	for pos < len(data) {
		// Need at least 8 bytes for header
		if pos+8 > len(data) {
			// Remaining data without header - treat as plain text
			remaining := strings.TrimSpace(string(data[pos:]))
			if remaining != "" {
				lines = append(lines, strings.Split(remaining, "\n")...)
			}
			break
		}

		// Check if this looks like a multiplexed header (type byte 0-2)
		streamType := data[pos]
		if streamType > 2 {
			// Not a valid header - treat rest as plain text
			remaining := strings.TrimSpace(string(data[pos:]))
			if remaining != "" {
				lines = append(lines, strings.Split(remaining, "\n")...)
			}
			break
		}

		// Read payload size (big endian uint32 at offset 4)
		size := int(data[pos+4])<<24 | int(data[pos+5])<<16 | int(data[pos+6])<<8 | int(data[pos+7])

		// Sanity check size
		if size < 0 || pos+8+size > len(data) {
			remaining := strings.TrimSpace(string(data[pos:]))
			if remaining != "" {
				lines = append(lines, strings.Split(remaining, "\n")...)
			}
			break
		}

		// Extract payload
		payload := string(data[pos+8 : pos+8+size])
		payload = strings.TrimRight(payload, "\n\r")
		if payload != "" {
			lines = append(lines, payload)
		}

		pos += 8 + size
	}

	return lines
}

// Image types
type Image struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     int64    `json:"Created"`
	Size        int64    `json:"Size"`
	VirtualSize int64    `json:"VirtualSize"`
}

type ImageInspect struct {
	ID            string   `json:"Id"`
	RepoTags      []string `json:"RepoTags"`
	RepoDigests   []string `json:"RepoDigests"`
	Created       string   `json:"Created"`
	Size          int64    `json:"Size"`
	Architecture  string   `json:"Architecture"`
	Os            string   `json:"Os"`
	Config        struct {
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
}

// ListImages returns list of all images
func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	var images []Image
	err := c.get(ctx, "/v4.0.0/libpod/images/json", &images)
	return images, err
}

// InspectImage returns detailed info about image
func (c *Client) InspectImage(ctx context.Context, id string) (*ImageInspect, error) {
	var info ImageInspect
	err := c.get(ctx, fmt.Sprintf("/v4.0.0/libpod/images/%s/json", id), &info)
	return &info, err
}

// PullImage pulls an image from registry
func (c *Client) PullImage(ctx context.Context, reference string) error {
	path := fmt.Sprintf("/v4.0.0/libpod/images/pull?reference=%s", reference)
	resp, err := c.request(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Read the streaming response
	_, err = io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("pull failed with status %d", resp.StatusCode)
	}
	return err
}

// RemoveImage removes an image
func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	path := fmt.Sprintf("/v4.0.0/libpod/images/%s", id)
	if force {
		path += "?force=true"
	}
	return c.delete(ctx, path)
}

// Volume types
type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
}

// ListVolumes returns list of all volumes
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	var result struct {
		Volumes []Volume `json:"Volumes"`
	}
	err := c.get(ctx, "/v4.0.0/libpod/volumes/json", &result)
	if err != nil {
		// Try alternative format
		var volumes []Volume
		err = c.get(ctx, "/v4.0.0/libpod/volumes/json", &volumes)
		return volumes, err
	}
	return result.Volumes, nil
}

// CreateVolume creates a new volume
func (c *Client) CreateVolume(ctx context.Context, name string) (*Volume, error) {
	body := map[string]string{"Name": name}
	data, _ := json.Marshal(body)

	resp, err := c.request(ctx, http.MethodPost, "/v4.0.0/libpod/volumes/create", strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var volume Volume
	err = json.NewDecoder(resp.Body).Decode(&volume)
	return &volume, err
}

// InspectVolume returns info about volume
func (c *Client) InspectVolume(ctx context.Context, name string) (*Volume, error) {
	var volume Volume
	err := c.get(ctx, fmt.Sprintf("/v4.0.0/libpod/volumes/%s/json", name), &volume)
	return &volume, err
}

// RemoveVolume removes a volume
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	path := fmt.Sprintf("/v4.0.0/libpod/volumes/%s", name)
	if force {
		path += "?force=true"
	}
	return c.delete(ctx, path)
}

// Network types
type Network struct {
	Name        string            `json:"name"`
	ID          string            `json:"id"`
	Driver      string            `json:"driver"`
	Created     string            `json:"created"`
	Subnets     []Subnet          `json:"subnets"`
	IPv6Enabled bool              `json:"ipv6_enabled"`
	Internal    bool              `json:"internal"`
	Labels      map[string]string `json:"labels"`
}

type Subnet struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
}

// ListNetworks returns list of all networks
func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var networks []Network
	err := c.get(ctx, "/v4.0.0/libpod/networks/json", &networks)
	return networks, err
}

// InspectNetwork returns info about network
func (c *Client) InspectNetwork(ctx context.Context, name string) (*Network, error) {
	var network Network
	err := c.get(ctx, fmt.Sprintf("/v4.0.0/libpod/networks/%s/json", name), &network)
	return &network, err
}

// CreateNetwork creates a new network
func (c *Client) CreateNetwork(ctx context.Context, name string) (*Network, error) {
	body := map[string]string{"name": name}
	data, _ := json.Marshal(body)

	resp, err := c.request(ctx, http.MethodPost, "/v4.0.0/libpod/networks/create", strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var network Network
	err = json.NewDecoder(resp.Body).Decode(&network)
	return &network, err
}

// RemoveNetwork removes a network
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	return c.delete(ctx, fmt.Sprintf("/v4.0.0/libpod/networks/%s", name))
}

// Pod types
type Pod struct {
	ID         string   `json:"Id"`
	Name       string   `json:"Name"`
	Status     string   `json:"Status"`
	Created    string   `json:"Created"`
	Containers []string `json:"Containers"`
}

type PodInspect struct {
	ID         string `json:"Id"`
	Name       string `json:"Name"`
	State      string `json:"State"`
	Created    string `json:"Created"`
	Hostname   string `json:"Hostname"`
	Containers []struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		State string `json:"State"`
	} `json:"Containers"`
}

// ListPods returns list of all pods
func (c *Client) ListPods(ctx context.Context) ([]Pod, error) {
	var pods []Pod
	err := c.get(ctx, "/v4.0.0/libpod/pods/json", &pods)
	return pods, err
}

// PodCreateConfig represents pod creation options
type PodCreateConfig struct {
	Name         string        `json:"name"`
	PortMappings []PortMapping `json:"portmappings,omitempty"`
}

// PodCreateResponse represents the response from pod creation
type PodCreateResponse struct {
	ID string `json:"Id"`
}

// CreatePod creates a new pod
func (c *Client) CreatePod(ctx context.Context, config *PodCreateConfig) (*PodCreateResponse, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	resp, err := c.request(ctx, http.MethodPost, "/v4.0.0/libpod/pods/create", strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result PodCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// InspectPod returns info about pod
func (c *Client) InspectPod(ctx context.Context, id string) (*PodInspect, error) {
	var pod PodInspect
	err := c.get(ctx, fmt.Sprintf("/v4.0.0/libpod/pods/%s/json", id), &pod)
	return &pod, err
}

// StartPod starts a pod
func (c *Client) StartPod(ctx context.Context, id string) error {
	return c.post(ctx, fmt.Sprintf("/v4.0.0/libpod/pods/%s/start", id), nil)
}

// StopPod stops a pod
func (c *Client) StopPod(ctx context.Context, id string) error {
	return c.post(ctx, fmt.Sprintf("/v4.0.0/libpod/pods/%s/stop", id), nil)
}

// RemovePod removes a pod
func (c *Client) RemovePod(ctx context.Context, id string, force bool) error {
	path := fmt.Sprintf("/v4.0.0/libpod/pods/%s", id)
	if force {
		path += "?force=true"
	}
	return c.delete(ctx, path)
}

// System types
type SystemInfo struct {
	Host struct {
		Arch     string `json:"arch"`
		Hostname string `json:"hostname"`
		Kernel   string `json:"kernel"`
	} `json:"host"`
	Version struct {
		Version string `json:"Version"`
	} `json:"version"`
}

type SystemDF struct {
	Containers []struct {
		ContainerID string `json:"ContainerID"`
		Size        int64  `json:"Size"`
		RWSize      int64  `json:"RWSize"`
	} `json:"Containers"`
	Images []struct {
		ImageID string `json:"ImageID"`
		Size    int64  `json:"Size"`
	} `json:"Images"`
	Volumes []struct {
		VolumeName string `json:"VolumeName"`
		Size       int64  `json:"Size"`
	} `json:"Volumes"`
}

// GetSystemInfo returns system information
func (c *Client) GetSystemInfo(ctx context.Context) (*SystemInfo, error) {
	var info SystemInfo
	err := c.get(ctx, "/v4.0.0/libpod/info", &info)
	return &info, err
}

// GetSystemDF returns disk usage
func (c *Client) GetSystemDF(ctx context.Context) (*SystemDF, error) {
	var df SystemDF
	err := c.get(ctx, "/v4.0.0/libpod/system/df", &df)
	return &df, err
}

// SystemPrune cleans up unused resources
func (c *Client) SystemPrune(ctx context.Context) error {
	return c.post(ctx, "/v4.0.0/libpod/system/prune", nil)
}

// Ping checks if Podman API is available
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.request(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ExecConfig represents exec configuration
type ExecConfig struct {
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
	Cmd          []string `json:"Cmd"`
}

// ExecCreateResponse represents exec create response
type ExecCreateResponse struct {
	ID string `json:"Id"`
}

// CreateExec creates an exec instance in a container
func (c *Client) CreateExec(ctx context.Context, containerID string, cmd []string) (*ExecCreateResponse, error) {
	config := ExecConfig{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          cmd,
	}

	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	resp, err := c.request(ctx, http.MethodPost, fmt.Sprintf("/v4.0.0/libpod/containers/%s/exec", containerID), strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result ExecCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetSocketPath returns the socket path
func (c *Client) GetSocketPath() string {
	return c.socketPath
}
