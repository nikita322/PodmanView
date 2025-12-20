package api

import (
	"strings"
	"sync"
	"time"

	"podmanview/internal/storage"
)

// HistoryHandler handles command history operations
type HistoryHandler struct {
	storage storage.Storage
	mu      sync.RWMutex
}

// NewHistoryHandler creates new history handler
func NewHistoryHandler(store storage.Storage) *HistoryHandler {
	return &HistoryHandler{
		storage: store,
	}
}

// loadHistory returns command history array (last 50 commands)
func (h *HistoryHandler) loadHistory() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	entries, err := h.storage.GetCommandHistory(50)
	if err != nil {
		return []string{}
	}

	commands := make([]string, 0, len(entries))
	for _, entry := range entries {
		commands = append(commands, entry.Command)
	}

	return commands
}

// saveCommand saves a command to history (called from WebSocket)
func (h *HistoryHandler) saveCommand(command string) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Save to storage (duplicate check is handled inside)
	if err := h.storage.SaveCommandHistory(command, time.Now()); err != nil {
		return err
	}

	// Keep only last 500 commands (trim if needed)
	go h.storage.TrimCommandHistory(500)

	return nil
}

