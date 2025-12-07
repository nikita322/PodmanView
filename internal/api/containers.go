package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"podmanview/internal/auth"
	"podmanview/internal/events"
	"podmanview/internal/podman"
)

// ContainerHandler handles container endpoints
type ContainerHandler struct {
	client     *podman.Client
	eventStore *events.Store
}

// NewContainerHandler creates new container handler
func NewContainerHandler(client *podman.Client, eventStore *events.Store) *ContainerHandler {
	return &ContainerHandler{client: client, eventStore: eventStore}
}

// ContainerWithStats extends Container with resource stats
type ContainerWithStats struct {
	ID       string   `json:"Id"`
	Names    []string `json:"Names"`
	Image    string   `json:"Image"`
	State    string   `json:"State"`
	CPU      float64  `json:"CPU"`
	MemUsage uint64   `json:"MemUsage"`
}

// List handles GET /api/containers
func (h *ContainerHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	containers, err := h.client.ListContainers(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Get stats for running containers
	stats, _ := h.client.GetContainersStats(ctx)
	statsMap := make(map[string]*podman.ContainerStats)
	for i := range stats {
		statsMap[stats[i].ContainerID] = &stats[i]
	}

	// Build response with stats
	result := make([]ContainerWithStats, len(containers))
	for i, c := range containers {
		result[i] = ContainerWithStats{
			ID:    c.ID,
			Names: c.Names,
			Image: c.Image,
			State: c.State,
		}
		if stat := statsMap[c.ID]; stat != nil {
			result[i].CPU = stat.CPU
			result[i].MemUsage = stat.MemUsage
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// Inspect handles GET /api/containers/{id}
func (h *ContainerHandler) Inspect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	info, err := h.client.InspectContainer(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// Start handles POST /api/containers/{id}/start
func (h *ContainerHandler) Start(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	id := chi.URLParam(r, "id")

	if err := h.client.StartContainer(r.Context(), id); err != nil {
		h.eventStore.Add(events.EventContainerStart, user.Username, getClientIP(r), false, shortID(id))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.eventStore.Add(events.EventContainerStart, user.Username, getClientIP(r), true, shortID(id))
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// Stop handles POST /api/containers/{id}/stop
func (h *ContainerHandler) Stop(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	id := chi.URLParam(r, "id")

	if err := h.client.StopContainer(r.Context(), id); err != nil {
		h.eventStore.Add(events.EventContainerStop, user.Username, getClientIP(r), false, shortID(id))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.eventStore.Add(events.EventContainerStop, user.Username, getClientIP(r), true, shortID(id))
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// Restart handles POST /api/containers/{id}/restart
func (h *ContainerHandler) Restart(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	id := chi.URLParam(r, "id")

	if err := h.client.RestartContainer(r.Context(), id); err != nil {
		h.eventStore.Add(events.EventContainerRestart, user.Username, getClientIP(r), false, shortID(id))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.eventStore.Add(events.EventContainerRestart, user.Username, getClientIP(r), true, shortID(id))
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
}

// Remove handles DELETE /api/containers/{id}
func (h *ContainerHandler) Remove(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	id := chi.URLParam(r, "id")
	force := r.URL.Query().Get("force") == "true"

	if err := h.client.RemoveContainer(r.Context(), id, force); err != nil {
		h.eventStore.Add(events.EventContainerRemove, user.Username, getClientIP(r), false, shortID(id))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	h.eventStore.Add(events.EventContainerRemove, user.Username, getClientIP(r), true, shortID(id))
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// LogsResponse represents the response for container logs
type LogsResponse struct {
	Lines []string `json:"lines"`
}

// Logs handles GET /api/containers/{id}/logs
func (h *ContainerHandler) Logs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	tail := 100
	if t := r.URL.Query().Get("tail"); t != "" {
		if parsed, err := strconv.Atoi(t); err == nil {
			tail = parsed
		}
	}

	logs, err := h.client.GetContainerLogs(r.Context(), id, tail)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Split logs into lines
	var lines []string
	if logs != "" {
		lines = strings.Split(logs, "\n")
		// Remove empty trailing line if exists
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}

	writeJSON(w, http.StatusOK, LogsResponse{Lines: lines})
}

// CreateContainerRequest represents the request body for creating a container
type CreateContainerRequest struct {
	Image   string `json:"image"`
	Name    string `json:"name"`
	Ports   string `json:"ports"`
	Volumes string `json:"volumes"`
	Env     string `json:"env"`
	Command string `json:"command"`
	Start   bool   `json:"start"`
}

// Create handles POST /api/containers
func (h *ContainerHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	var req CreateContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if req.Image == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Image is required"})
		return
	}

	config := &podman.ContainerCreateConfig{
		Image: req.Image,
		Name:  req.Name,
	}

	// Parse command
	if req.Command != "" {
		config.Command = strings.Fields(req.Command)
	}

	// Parse environment variables
	if req.Env != "" {
		config.Env = parseEnvVars(req.Env)
	}

	// Parse port mappings
	if req.Ports != "" {
		config.PortMappings = parsePortMappings(req.Ports)
	}

	// Parse volume mounts
	if req.Volumes != "" {
		config.Mounts = parseVolumeMounts(req.Volumes)
	}

	result, err := h.client.CreateContainer(r.Context(), config)
	if err != nil {
		h.eventStore.Add(events.EventContainerCreate, user.Username, getClientIP(r), false, req.Image)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Start container if requested
	if req.Start {
		if err := h.client.StartContainer(r.Context(), result.ID); err != nil {
			h.eventStore.Add(events.EventContainerCreate, user.Username, getClientIP(r), true, shortID(result.ID))
			writeJSON(w, http.StatusOK, map[string]string{
				"id":      result.ID,
				"status":  "created",
				"warning": "Container created but failed to start: " + err.Error(),
			})
			return
		}
	}

	status := "created"
	if req.Start {
		status = "started"
	}

	h.eventStore.Add(events.EventContainerCreate, user.Username, getClientIP(r), true, shortID(result.ID))
	writeJSON(w, http.StatusCreated, map[string]string{"id": result.ID, "status": status})
}

// parsePortMappings parses port mappings from string like "80:80, 443:443"
func parsePortMappings(ports string) []podman.PortMapping {
	var mappings []podman.PortMapping
	parts := strings.Split(ports, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		portParts := strings.Split(part, ":")
		if len(portParts) != 2 {
			continue
		}
		hostPort, err1 := strconv.Atoi(strings.TrimSpace(portParts[0]))
		containerPort, err2 := strconv.Atoi(strings.TrimSpace(portParts[1]))
		if err1 != nil || err2 != nil {
			continue
		}
		mappings = append(mappings, podman.PortMapping{
			HostPort:      hostPort,
			ContainerPort: containerPort,
			Protocol:      "tcp",
		})
	}
	return mappings
}

// parseEnvVars parses environment variables from string like "KEY=value, DEBUG=true"
func parseEnvVars(env string) map[string]string {
	vars := make(map[string]string)
	parts := strings.Split(env, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		vars[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return vars
}

// parseVolumeMounts parses volume mounts from string like "/data:/app/data, /config:/etc/config"
func parseVolumeMounts(volumes string) []podman.Mount {
	var mounts []podman.Mount
	parts := strings.Split(volumes, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		volParts := strings.Split(part, ":")
		if len(volParts) != 2 {
			continue
		}
		mounts = append(mounts, podman.Mount{
			Type:        "bind",
			Source:      strings.TrimSpace(volParts[0]),
			Destination: strings.TrimSpace(volParts[1]),
		})
	}
	return mounts
}
