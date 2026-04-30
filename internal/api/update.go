package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"podmanview/internal/auth"
	"podmanview/internal/events"
	"podmanview/internal/logger"
	"podmanview/internal/updater"
)

// UpdateHandler handles update-related API endpoints
type UpdateHandler struct {
	updater    *updater.Updater
	eventStore *events.Store
	logger     *logger.Logger

	// Update state
	updateMu     sync.RWMutex
	updating     bool
	updateStatus *updater.UpdateProgress
}

// NewUpdateHandler creates a new update handler
func NewUpdateHandler(u *updater.Updater, eventStore *events.Store, logger *logger.Logger) *UpdateHandler {
	return &UpdateHandler{
		updater:    u,
		eventStore: eventStore,
		logger:     logger,
	}
}

// Check handles GET /api/system/update/check
func (h *UpdateHandler) Check(w http.ResponseWriter, r *http.Request) {
	if h.updater == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Updater not available"})
		return
	}
	result, err := h.updater.CheckUpdate(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Status handles GET /api/system/update/status
func (h *UpdateHandler) Status(w http.ResponseWriter, r *http.Request) {
	h.updateMu.RLock()
	defer h.updateMu.RUnlock()

	if !h.updating {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"updating": false,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"updating": true,
		"progress": h.updateStatus,
	})
}

// Perform handles POST /api/system/update
func (h *UpdateHandler) Perform(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	// Check if already updating
	h.updateMu.Lock()
	if h.updating {
		h.updateMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Update already in progress"})
		return
	}
	h.updating = true
	h.updateStatus = &updater.UpdateProgress{Stage: "starting", Percent: 0}
	h.updateMu.Unlock()

	clientIP := getClientIP(r)

	// Run update in background
	go func() {
		defer func() {
			h.updateMu.Lock()
			h.updating = false
			h.updateMu.Unlock()
		}()

		err := h.updater.PerformUpdate(context.Background(), func(p updater.UpdateProgress) {
			h.updateMu.Lock()
			h.updateStatus = &p
			h.updateMu.Unlock()
			h.logger.Printf("Update progress: %s (%d%%)", p.Stage, p.Percent)
		})

		if err != nil {
			h.eventStore.Add(events.EventSystemUpdate, user.Username, clientIP, false, err.Error())
			h.logger.Printf("Update failed: %v", err)

			h.updateMu.Lock()
			h.updateStatus = &updater.UpdateProgress{
				Stage:   "failed",
				Percent: 0,
				Message: err.Error(),
			}
			h.updateMu.Unlock()
			return
		}

		h.eventStore.Add(events.EventSystemUpdate, user.Username, clientIP, true, "")
		h.logger.Println("Update completed successfully")

		// Wait a moment for clients to receive status
		time.Sleep(2 * time.Second)

		// Restart service
		h.logger.Println("Restarting service...")
		if err := updater.RestartService(); err != nil {
			h.logger.Printf("Failed to restart service: %v", err)
		}
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status":  "started",
		"message": "Update started. Check /api/system/update/status for progress.",
	})
}

// Version handles GET /api/system/version
func (h *UpdateHandler) Version(w http.ResponseWriter, r *http.Request) {
	if h.updater == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"version": "unknown",
			"isDev":   true,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"version": h.updater.GetCurrentVersion(),
		"isDev":   updater.IsDev(h.updater.GetCurrentVersion()),
	})
}
