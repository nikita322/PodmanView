package picoder

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"podmanview/internal/podman"
	"podmanview/internal/storage"
)

const (
	storagePluginName = "picoder"
	piCmd             = "pi"
)

// SessionMetadata holds persisted info about a pi session
type SessionMetadata struct {
	ID           string    `json:"id"`
	ContainerID  string    `json:"container_id"` // "" for host
	Name         string    `json:"name"`
	CWD          string    `json:"cwd"`
	Status       string    `json:"status"` // running, stopped, error
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
}

// WSClient represents a browser client connected to a session
type WSClient struct {
	ID   string
	Conn *websocket.Conn
	Send chan []byte
}

// PiProcess wraps a running pi --mode rpc process
type PiProcess struct {
	ID      string
	Cmd     *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  io.ReadCloser
	Scanner *bufio.Scanner
	Meta    *SessionMetadata

	mu      sync.RWMutex
	clients map[string]*WSClient
	done    chan struct{}
}

// Broadcast sends a JSON message to all connected WebSocket clients
func (pp *PiProcess) Broadcast(msg []byte) {
	pp.mu.RLock()
	defer pp.mu.RUnlock()
	for _, client := range pp.clients {
		select {
		case client.Send <- msg:
		default:
			// channel full, drop
		}
	}
}

// AddClient registers a new WebSocket client
func (pp *PiProcess) AddClient(c *WSClient) {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	pp.clients[c.ID] = c
}

// RemoveClient unregisters a WebSocket client
func (pp *PiProcess) RemoveClient(id string) {
	pp.mu.Lock()
	defer pp.mu.Unlock()
	delete(pp.clients, id)
}

// ClientCount returns number of connected WS clients
func (pp *PiProcess) ClientCount() int {
	pp.mu.RLock()
	defer pp.mu.RUnlock()
	return len(pp.clients)
}

// SessionManager manages pi coding agent sessions
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*PiProcess // sessionID -> process
	logger   *log.Logger
	storage  storage.Storage
	podman   *podman.Client
}

// NewSessionManager creates a new session manager
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*PiProcess),
	}
}

// SetLogger sets the logger
func (sm *SessionManager) SetLogger(l *log.Logger) { sm.logger = l }

// SetStorage sets the storage backend
func (sm *SessionManager) SetStorage(s storage.Storage) { sm.storage = s }

// SetPodmanClient sets the podman client
func (sm *SessionManager) SetPodmanClient(c *podman.Client) { sm.podman = c }

// Count returns number of tracked sessions
func (sm *SessionManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// List returns all session metadata
func (sm *SessionManager) List() []*SessionMetadata {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make([]*SessionMetadata, 0, len(sm.sessions))
	for _, pp := range sm.sessions {
		result = append(result, pp.Meta)
	}
	return result
}

// ListByContainer returns sessions for a specific container
func (sm *SessionManager) ListByContainer(containerID string) []*SessionMetadata {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	result := make([]*SessionMetadata, 0)
	for _, pp := range sm.sessions {
		if pp.Meta.ContainerID == containerID {
			result = append(result, pp.Meta)
		}
	}
	return result
}

// Get returns a session process by ID
func (sm *SessionManager) Get(id string) *PiProcess {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[id]
}

// Create starts a new pi --mode rpc process for the given session
func (sm *SessionManager) Create(meta *SessionMetadata) (*PiProcess, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.sessions[meta.ID]; exists {
		return nil, fmt.Errorf("session %s already exists", meta.ID)
	}

	args := []string{"--mode", "rpc", "--name", meta.Name}
	if meta.CWD != "" {
		args = append(args, "--cwd", meta.CWD)
	}

	cmd := exec.Command(piCmd, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout for logging

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start pi: %w", err)
	}

	pp := &PiProcess{
		ID:      meta.ID,
		Cmd:     cmd,
		Stdin:   stdin,
		Stdout:  stdout,
		Scanner: bufio.NewScanner(stdout),
		Meta:    meta,
		clients: make(map[string]*WSClient),
		done:    make(chan struct{}),
	}

	sm.sessions[meta.ID] = pp
	meta.Status = "running"
	meta.CreatedAt = time.Now()
	meta.UpdatedAt = time.Now()

	// persist
	if err := sm.saveMetadata(meta); err != nil {
		sm.logf("Warning: failed to save session metadata: %v", err)
	}

	// start stdout reader goroutine
	go sm.readLoop(pp)

	// start process waiter
	go sm.waitLoop(pp)

	sm.logf("Started pi session %s (cwd=%s, pid=%d)", meta.ID, meta.CWD, cmd.Process.Pid)
	return pp, nil
}

// Kill terminates a pi process and removes the session
func (sm *SessionManager) Kill(id string) error {
	sm.mu.Lock()
	pp, ok := sm.sessions[id]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("session %s not found", id)
	}
	delete(sm.sessions, id)
	sm.mu.Unlock()

	// close all WS clients
	pp.mu.Lock()
	for _, c := range pp.clients {
		close(c.Send)
		c.Conn.Close()
	}
	pp.clients = make(map[string]*WSClient)
	pp.mu.Unlock()

	close(pp.done)

	if pp.Cmd != nil && pp.Cmd.Process != nil {
		pp.Cmd.Process.Kill()
		pp.Cmd.Wait()
	}
	pp.Stdin.Close()
	pp.Stdout.Close()

	// update metadata
	pp.Meta.Status = "stopped"
	pp.Meta.UpdatedAt = time.Now()
	sm.saveMetadata(pp.Meta)

	sm.logf("Killed pi session %s", id)
	return nil
}

// StopAll kills all running sessions
func (sm *SessionManager) StopAll() error {
	sm.mu.RLock()
	ids := make([]string, 0, len(sm.sessions))
	for id := range sm.sessions {
		ids = append(ids, id)
	}
	sm.mu.RUnlock()

	var errs []string
	for _, id := range ids {
		if err := sm.Kill(id); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("stop errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// LoadAll restores session metadata from storage (does NOT restart processes)
func (sm *SessionManager) LoadAll() error {
	if sm.storage == nil {
		return nil
	}
	data, err := sm.storage.List(storagePluginName)
	if err != nil {
		return err
	}

	for key, value := range data {
		if !strings.HasPrefix(key, "session:") {
			continue
		}
		var meta SessionMetadata
		if err := json.Unmarshal(value, &meta); err != nil {
			sm.logf("Warning: corrupt session metadata %s: %v", key, err)
			continue
		}
		// loaded sessions are marked stopped until explicitly started
		if meta.Status == "running" {
			meta.Status = "stopped"
		}
		sm.mu.Lock()
		sm.sessions[meta.ID] = &PiProcess{
			ID:      meta.ID,
			Meta:    &meta,
			clients: make(map[string]*WSClient),
			done:    make(chan struct{}),
		}
		sm.mu.Unlock()
	}
	return nil
}

// SendCommand writes a JSON command to pi's stdin
func (sm *SessionManager) SendCommand(id string, cmd interface{}) error {
	sm.mu.RLock()
	pp := sm.sessions[id]
	sm.mu.RUnlock()
	if pp == nil {
		return fmt.Errorf("session %s not found", id)
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = pp.Stdin.Write(data)
	return err
}

// readLoop reads JSONL from pi stdout and broadcasts to WS clients
func (sm *SessionManager) readLoop(pp *PiProcess) {
	for pp.Scanner.Scan() {
		line := pp.Scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// try to parse to update metadata
		var envelope map[string]interface{}
		if err := json.Unmarshal(line, &envelope); err == nil {
			if t, _ := envelope["type"].(string); t == "agent_end" {
				pp.Meta.MessageCount++
				pp.Meta.UpdatedAt = time.Now()
				sm.saveMetadata(pp.Meta)
			}
		}
		pp.Broadcast(line)
	}
	if err := pp.Scanner.Err(); err != nil {
		sm.logf("Session %s scanner error: %v", pp.ID, err)
	}
	pp.Meta.Status = "stopped"
	pp.Meta.UpdatedAt = time.Now()
	sm.saveMetadata(pp.Meta)
}

// waitLoop waits for the pi process to exit
func (sm *SessionManager) waitLoop(pp *PiProcess) {
	if pp.Cmd == nil {
		return
	}
	err := pp.Cmd.Wait()
	if err != nil {
		sm.logf("Session %s process exited: %v", pp.ID, err)
	} else {
		sm.logf("Session %s process exited cleanly", pp.ID)
	}
	pp.Meta.Status = "stopped"
	pp.Meta.UpdatedAt = time.Now()
	sm.saveMetadata(pp.Meta)
}

// saveMetadata persists session metadata to storage
func (sm *SessionManager) saveMetadata(meta *SessionMetadata) error {
	if sm.storage == nil {
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return sm.storage.Set(storagePluginName, "session:"+meta.ID, data)
}

func (sm *SessionManager) logf(format string, v ...interface{}) {
	if sm.logger != nil {
		sm.logger.Printf("[picoder] "+format, v...)
	}
}
