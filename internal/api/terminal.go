package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"podmanview/internal/auth"
	"podmanview/internal/events"
	"podmanview/internal/podman"
)

const (
	// WebSocket ping interval
	pingInterval = 30 * time.Second
	// Pong wait timeout (if no pong received within this time, connection is dead)
	pongWait = 60 * time.Second
	// Write wait timeout
	writeWait = 10 * time.Second
)

// TerminalHandler handles terminal WebSocket connections
type TerminalHandler struct {
	client         *podman.Client
	wsTokenStore   *auth.WSTokenStore
	eventStore     *events.Store
	historyHandler *HistoryHandler
	upgrader       websocket.Upgrader
}

// NewTerminalHandler creates new terminal handler
func NewTerminalHandler(client *podman.Client, wsTokenStore *auth.WSTokenStore, eventStore *events.Store, historyHandler *HistoryHandler) *TerminalHandler {
	h := &TerminalHandler{
		client:         client,
		wsTokenStore:   wsTokenStore,
		eventStore:     eventStore,
		historyHandler: historyHandler,
	}

	h.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     h.checkOrigin,
	}

	return h
}

// checkOrigin validates WebSocket connection using CSRF token
// This prevents Cross-Site WebSocket Hijacking (CSWSH) attacks
func (h *TerminalHandler) checkOrigin(r *http.Request) bool {
	// Get token from query parameter
	token := r.URL.Query().Get("ws_token")
	if token == "" {
		log.Printf("WebSocket rejected: missing ws_token")
		return false
	}

	// Validate token (allows reconnection within grace period)
	username, valid := h.wsTokenStore.Validate(token)
	if !valid {
		log.Printf("WebSocket rejected: invalid or expired ws_token (token may have exceeded max uses or grace period)")
		return false
	}

	log.Printf("WebSocket connection authorized for user: %s (reconnection supported for %v)", username, auth.WSTokenGracePeriod)
	return true
}

// ExecMessage represents a WebSocket message
type ExecMessage struct {
	Type    string `json:"type"` // "stdin", "resize", "save_command"
	Data    string `json:"data,omitempty"`
	Command string `json:"command,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

// HostTerminal handles WebSocket connection for host terminal
func (h *TerminalHandler) HostTerminal(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	// Upgrade HTTP to WebSocket
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer ws.Close()

	// Setup pong handler to keep connection alive
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Log terminal connection
	h.eventStore.Add(events.EventTerminalHost, user.Username, getClientIP(r), true, "")

	// Send command history as first message
	history := h.historyHandler.loadHistory()
	if len(history) > 0 {
		historyMsg := map[string]interface{}{
			"type":     "history",
			"commands": history,
		}
		if historyData, err := json.Marshal(historyMsg); err == nil {
			ws.WriteMessage(websocket.TextMessage, historyData)
		}
	}

	// Start shell process (use bash for better readline support)
	cmd := exec.Command("/bin/bash")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	// Get PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("Failed to start PTY: %v", err)
		ws.WriteMessage(websocket.TextMessage, []byte("Failed to start shell: "+err.Error()))
		return
	}
	defer func() {
		cmd.Process.Kill()
		ptmx.Close()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Ping ticker to keep connection alive
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Printf("Failed to send ping: %v", err)
					return
				}
			}
		}
	}()

	// Read from PTY -> write to WebSocket
	go func() {
		buf := make([]byte, 1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := ptmx.Read(buf)
				if err != nil {
					cancel()
					return
				}
				if n > 0 {
					if err := ws.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
						cancel()
						return
					}
				}
			}
		}
	}()

	// Read from WebSocket -> write to PTY
	for {
		select {
		case <-ctx.Done():
			return
		default:
			_, message, err := ws.ReadMessage()
			if err != nil {
				return
			}

			// Parse message
			var msg ExecMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				// Treat as raw stdin
				ptmx.Write(message)
				continue
			}

			switch msg.Type {
			case "stdin":
				ptmx.Write([]byte(msg.Data))
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					pty.Setsize(ptmx, &pty.Winsize{
						Rows: uint16(msg.Rows),
						Cols: uint16(msg.Cols),
					})
				}
			case "save_command":
				// Save command to history
				if msg.Command != "" {
					h.historyHandler.saveCommand(msg.Command)
				}
			}
		}
	}
}

// Connect handles WebSocket connection for container terminal
func (h *TerminalHandler) Connect(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	containerID := chi.URLParam(r, "id")

	// Create exec instance with TERM environment variable for proper terminal support
	// Try to use bash if available (better readline support), otherwise fallback to sh
	env := []string{"TERM=xterm-256color"}
	cmd := []string{"/bin/sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash || exec sh"}
	execResp, err := h.client.CreateExecWithEnv(r.Context(), containerID, cmd, env)
	if err != nil {
		log.Printf("Failed to create exec: %v", err)
		http.Error(w, "Failed to create exec: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Connect to Podman socket for exec start
	socketPath := h.client.GetSocketPath()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Printf("Failed to connect to socket: %v", err)
		http.Error(w, "Failed to connect to Podman", http.StatusInternalServerError)
		return
	}

	// Send exec start request (hijack connection)
	execStartReq := `{"Detach":false,"Tty":true}`
	httpReq := fmt.Sprintf("POST /v4.0.0/libpod/exec/%s/start HTTP/1.1\r\n"+
		"Host: localhost\r\n"+
		"Content-Type: application/json\r\n"+
		"Content-Length: %d\r\n"+
		"Connection: Upgrade\r\n"+
		"Upgrade: tcp\r\n"+
		"\r\n"+
		"%s", execResp.ID, len(execStartReq), execStartReq)

	_, err = conn.Write([]byte(httpReq))
	if err != nil {
		conn.Close()
		log.Printf("Failed to send exec start: %v", err)
		http.Error(w, "Failed to start exec", http.StatusInternalServerError)
		return
	}

	// Read response header
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		conn.Close()
		log.Printf("Failed to read response: %v", err)
		http.Error(w, "Failed to start exec", http.StatusInternalServerError)
		return
	}

	log.Printf("Exec start response: %d %s", resp.StatusCode, resp.Status)

	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		conn.Close()
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Exec start failed: %d %s", resp.StatusCode, string(body))
		http.Error(w, "Exec start failed", http.StatusInternalServerError)
		return
	}

	// Upgrade HTTP to WebSocket
	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		conn.Close()
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	// Setup pong handler to keep connection alive
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error {
		ws.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Log terminal connection
	h.eventStore.Add(events.EventTerminalContainer, user.Username, getClientIP(r), true, shortID(containerID))

	// Start proxying
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Ping ticker to keep connection alive
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ws.SetWriteDeadline(time.Now().Add(writeWait))
				if err := ws.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Printf("Container terminal: Failed to send ping: %v", err)
					return
				}
			}
		}
	}()

	// Read from container -> write to WebSocket
	go func() {
		defer cancel()
		buf := make([]byte, 1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				n, err := conn.Read(buf)
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					if err != io.EOF {
						log.Printf("Read from container error: %v", err)
					}
					return
				}
				if n > 0 {
					if err := ws.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
						log.Printf("WebSocket write error: %v", err)
						return
					}
				}
			}
		}
	}()

	// Read from WebSocket -> write to container
	for {
		select {
		case <-ctx.Done():
			ws.Close()
			conn.Close()
			return
		default:
			_, message, err := ws.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					log.Printf("WebSocket read error: %v", err)
				}
				ws.Close()
				conn.Close()
				return
			}

			// Parse message
			var msg ExecMessage
			if err := json.Unmarshal(message, &msg); err != nil {
				// Treat as raw stdin
				if _, err := conn.Write(message); err != nil {
					log.Printf("Container write error: %v", err)
					ws.Close()
					conn.Close()
					return
				}
				continue
			}

			switch msg.Type {
			case "stdin":
				if _, err := conn.Write([]byte(msg.Data)); err != nil {
					log.Printf("Container write error: %v", err)
					ws.Close()
					conn.Close()
					return
				}
			case "resize":
				// Resize is more complex with Podman, skip for now
				// Would need to send resize request to exec instance
			}
		}
	}
}
