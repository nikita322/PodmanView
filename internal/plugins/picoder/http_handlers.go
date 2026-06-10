package picoder

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"podmanview/internal/auth"
	"podmanview/internal/plugins"
)

// Handlers holds HTTP handlers for the picoder plugin
type Handlers struct {
	sessions *SessionManager
	logger   *log.Logger
	upgrader websocket.Upgrader
}

// NewHandlers creates new HTTP handlers
func NewHandlers(sessions *SessionManager, logger *log.Logger) *Handlers {
	return &Handlers{
		sessions: sessions,
		logger:   logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// ContainerInfo extends podman container with picoder-specific fields
type ContainerInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Image        string   `json:"image"`
	State        string   `json:"state"`
	Status       string   `json:"status"`
	Binds        []string `json:"binds"` // host paths from bind mounts
	SessionCount int      `json:"session_count"`
}

// ListContainers returns all containers with their bind mounts and session counts
func (h *Handlers) ListContainers(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		plugins.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	if h.sessions.podman == nil {
		plugins.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Podman client not available"})
		return
	}

	containers, err := h.sessions.podman.ListContainers(r.Context())
	if err != nil {
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	result := make([]ContainerInfo, 0, len(containers))
	for _, c := range containers {
		info := ContainerInfo{
			ID:     c.ID,
			Name:   "",
			Image:  c.Image,
			State:  c.State,
			Status: c.Status,
			Binds:  []string{},
		}
		if len(c.Names) > 0 {
			info.Name = c.Names[0]
		}

		// inspect to get mounts
		inspect, err := h.sessions.podman.InspectContainer(r.Context(), c.ID)
		if err == nil && inspect != nil {
			for _, m := range inspect.Mounts {
				if m.Type == "bind" {
					info.Binds = append(info.Binds, m.Source)
				}
			}
		}

		info.SessionCount = len(h.sessions.ListByContainer(c.ID))
		result = append(result, info)
	}

	plugins.WriteJSON(w, http.StatusOK, result)
}

// ContainerSessions returns sessions for a specific container
func (h *Handlers) ContainerSessions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		plugins.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	containerID := chi.URLParam(r, "id")
	sessions := h.sessions.ListByContainer(containerID)
	plugins.WriteJSON(w, http.StatusOK, sessions)
}

// ListSessions returns all sessions
func (h *Handlers) ListSessions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		plugins.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	sessions := h.sessions.List()
	plugins.WriteJSON(w, http.StatusOK, sessions)
}

// CreateSessionRequest is the body for creating a session
type CreateSessionRequest struct {
	ContainerID string `json:"container_id"` // optional, "" for host
	Name        string `json:"name"`
	CWD         string `json:"cwd"` // working directory
}

// CreateSession creates and starts a new pi session
func (h *Handlers) CreateSession(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		plugins.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		plugins.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if req.Name == "" {
		req.Name = "Untitled Session"
	}

	// Auto-detect CWD from container binds if not provided
	if req.CWD == "" && req.ContainerID != "" && h.sessions.podman != nil {
		inspect, err := h.sessions.podman.InspectContainer(r.Context(), req.ContainerID)
		if err == nil && inspect != nil {
			for _, m := range inspect.Mounts {
				if m.Type == "bind" {
					req.CWD = m.Source
					break
				}
			}
		}
	}
	if req.CWD == "" {
		req.CWD = "/root"
	}

	meta := &SessionMetadata{
		ID:          fmt.Sprintf("pi-%d", time.Now().UnixNano()),
		ContainerID: req.ContainerID,
		Name:        req.Name,
		CWD:         req.CWD,
		Status:      "starting",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	pp, err := h.sessions.Create(meta)
	if err != nil {
		h.logf("Failed to create session: %v", err)
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	plugins.WriteJSON(w, http.StatusCreated, pp.Meta)
}

// DeleteSession kills a session
func (h *Handlers) DeleteSession(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		plugins.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	id := chi.URLParam(r, "id")
	if err := h.sessions.Kill(id); err != nil {
		plugins.WriteJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// CompactSession sends compact command to pi
func (h *Handlers) CompactSession(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		plugins.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	id := chi.URLParam(r, "id")
	pp := h.sessions.Get(id)
	if pp == nil {
		plugins.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Session not found"})
		return
	}

	cmd := map[string]string{"type": "compact"}
	if err := h.sessions.SendCommand(id, cmd); err != nil {
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "compaction requested"})
}

// ExportSession sends export_html command to pi
func (h *Handlers) ExportSession(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		plugins.WriteJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	id := chi.URLParam(r, "id")
	pp := h.sessions.Get(id)
	if pp == nil {
		plugins.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "Session not found"})
		return
	}

	cmd := map[string]string{"type": "export_html"}
	if err := h.sessions.SendCommand(id, cmd); err != nil {
		plugins.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	plugins.WriteJSON(w, http.StatusOK, map[string]string{"status": "export requested"})
}

// WSProxy handles WebSocket connections and proxies JSONL to/from pi
func (h *Handlers) WSProxy(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	id := chi.URLParam(r, "id")
	pp := h.sessions.Get(id)
	if pp == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logf("WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	clientID := fmt.Sprintf("%s-%d", user.Username, time.Now().UnixNano())
	client := &WSClient{
		ID:   clientID,
		Conn: ws,
		Send: make(chan []byte, 64),
	}
	pp.AddClient(client)
	defer pp.RemoveClient(clientID)

	h.logf("WS client %s connected to session %s", clientID, id)

	// writer goroutine: sends messages from Send channel to WebSocket
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range client.Send {
			if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// reader goroutine: reads from WebSocket and forwards to pi stdin
	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logf("WS read error for %s: %v", clientID, err)
			}
			break
		}

		// Validate JSON before forwarding
		var cmd map[string]interface{}
		if err := json.Unmarshal(message, &cmd); err != nil {
			// forward raw anyway? pi expects JSONL
			continue
		}

		if err := h.sessions.SendCommand(id, cmd); err != nil {
			h.logf("Failed to send command to session %s: %v", id, err)
			break
		}
	}

	close(client.Send)
	<-done
	h.logf("WS client %s disconnected from session %s", clientID, id)
}

func (h *Handlers) logf(format string, v ...interface{}) {
	if h.logger != nil {
		h.logger.Printf("[picoder] "+format, v...)
	}
}
