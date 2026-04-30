package events

import (
	"sync"
	"time"
)

// EventType represents the type of security event
type EventType string

const (
	// Auth events
	EventLogin       EventType = "login"
	EventLoginFailed EventType = "login_failed"
	EventLogout      EventType = "logout"

	// Terminal events
	EventTerminalHost      EventType = "terminal_host"
	EventTerminalContainer EventType = "terminal_container"

	// Container events
	EventContainerStart   EventType = "container_start"
	EventContainerStop    EventType = "container_stop"
	EventContainerRestart EventType = "container_restart"
	EventContainerRemove  EventType = "container_remove"
	EventContainerCreate  EventType = "container_create"

	// Image events
	EventImagePull   EventType = "image_pull"
	EventImageRemove EventType = "image_remove"

	// System events
	EventSystemReboot   EventType = "system_reboot"
	EventSystemShutdown EventType = "system_shutdown"
	EventSystemUpdate   EventType = "system_update"

	// File manager events
	EventFileBrowse   EventType = "file_browse"
	EventFileDownload EventType = "file_download"
	EventFileUpload   EventType = "file_upload"
	EventFileDelete   EventType = "file_delete"
	EventFileMkdir    EventType = "file_mkdir"
	EventFileRename   EventType = "file_rename"
	EventFileRead     EventType = "file_read"
	EventFileWrite    EventType = "file_write"
)

// Event represents a security/audit event
type Event struct {
	ID        int64     `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Username  string    `json:"username"`
	IP        string    `json:"ip"`
	Success   bool      `json:"success"`
	Details   string    `json:"details,omitempty"`
}

// Store holds events in memory with a fixed capacity (ring buffer)
type Store struct {
	mu      sync.RWMutex
	events  []Event
	maxSize int
	nextID  int64
}

// NewStore creates a new event store with specified max capacity
func NewStore(maxSize int) *Store {
	return &Store{
		events:  make([]Event, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add adds a new event to the store
func (s *Store) Add(eventType EventType, username, ip string, success bool, details string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	event := Event{
		ID:        s.nextID,
		Type:      eventType,
		Timestamp: time.Now(),
		Username:  username,
		IP:        ip,
		Success:   success,
		Details:   details,
	}

	// Ring buffer: remove oldest if at max capacity
	if len(s.events) >= s.maxSize {
		s.events = s.events[1:]
	}
	s.events = append(s.events, event)
}

// GetLast returns the last N events (newest first)
func (s *Store) GetLast(n int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n > len(s.events) {
		n = len(s.events)
	}

	result := make([]Event, n)
	for i := 0; i < n; i++ {
		result[i] = s.events[len(s.events)-1-i]
	}
	return result
}

// GetSince returns events newer than the given ID (newest first)
func (s *Store) GetSince(lastID int64) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Event
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].ID > lastID {
			result = append(result, s.events[i])
		} else {
			break
		}
	}
	return result
}

// LastID returns the ID of the most recent event
func (s *Store) LastID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextID
}
