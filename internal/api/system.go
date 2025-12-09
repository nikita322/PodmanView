package api

import (
	"context"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"podmanview/internal/auth"
	"podmanview/internal/events"
	"podmanview/internal/podman"
)

// Cache for system info and resource counts
var (
	cachedSystemInfo    *podman.SystemInfo
	systemInfoCacheTime time.Time
	systemInfoMu        sync.RWMutex

	// Cache for images/volumes/networks (change rarely)
	cachedImagesCount    int
	cachedVolumesCount   int
	cachedNetworksCount  int
	resourcesCacheTime   time.Time
	resourcesCacheMu     sync.RWMutex
	resourcesCacheTTL    = 30 * time.Second
)

// SystemHandler handles system endpoints
type SystemHandler struct {
	client     *podman.Client
	eventStore *events.Store
}

// NewSystemHandler creates new system handler
func NewSystemHandler(client *podman.Client, eventStore *events.Store) *SystemHandler {
	return &SystemHandler{client: client, eventStore: eventStore}
}

// DashboardInfo represents dashboard summary
type DashboardInfo struct {
	System     *DashboardSystemInfo `json:"system"`
	HostStats  *HostStats           `json:"hostStats"`
	Containers ContainerCounts      `json:"containers"`
	Images     int                  `json:"images"`
	Volumes    int                  `json:"volumes"`
	Networks   int                  `json:"networks"`
}

// DashboardSystemInfo contains only used system fields
type DashboardSystemInfo struct {
	Host    DashboardHostInfo    `json:"host"`
	Version DashboardVersionInfo `json:"version"`
}

// DashboardHostInfo contains only used host fields
type DashboardHostInfo struct {
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
	Kernel   string `json:"kernel"`
}

// DashboardVersionInfo contains only used version fields
type DashboardVersionInfo struct {
	Version string `json:"Version"`
}

// ContainerCounts represents container count statistics
type ContainerCounts struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Stopped int `json:"stopped"`
}

// Dashboard handles GET /api/system/dashboard
func (h *SystemHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get cached or fresh system info (static data, cache for 5 minutes)
	sysInfo := h.getCachedSystemInfo(ctx)
	if sysInfo == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to get system info"})
		return
	}

	// Get cached or fresh resource counts
	imagesCount, volumesCount, networksCount := h.getCachedResourceCounts(ctx)

	// Only containers need fresh data (state changes frequently)
	containers, err := h.client.ListContainers(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Get host stats (reads /proc, /sys)
	hostStats := GetHostStats()

	containerCounts := ContainerCounts{Total: len(containers)}
	for _, c := range containers {
		if c.State == "running" {
			containerCounts.Running++
		} else {
			containerCounts.Stopped++
		}
	}

	// Build optimized system info with only used fields
	systemInfo := &DashboardSystemInfo{
		Host: DashboardHostInfo{
			Arch:     sysInfo.Host.Arch,
			Hostname: sysInfo.Host.Hostname,
			Kernel:   sysInfo.Host.Kernel,
		},
		Version: DashboardVersionInfo{
			Version: sysInfo.Version.Version,
		},
	}

	dashboard := DashboardInfo{
		System:     systemInfo,
		HostStats:  hostStats,
		Containers: containerCounts,
		Images:     imagesCount,
		Volumes:    volumesCount,
		Networks:   networksCount,
	}

	writeJSON(w, http.StatusOK, dashboard)
}

// getCachedSystemInfo returns cached system info or fetches fresh
func (h *SystemHandler) getCachedSystemInfo(ctx context.Context) *podman.SystemInfo {
	systemInfoMu.RLock()
	if cachedSystemInfo != nil && time.Since(systemInfoCacheTime) < 5*time.Minute {
		info := cachedSystemInfo
		systemInfoMu.RUnlock()
		return info
	}
	systemInfoMu.RUnlock()

	// Fetch fresh
	info, err := h.client.GetSystemInfo(ctx)
	if err != nil {
		return cachedSystemInfo // Return stale cache on error
	}

	systemInfoMu.Lock()
	cachedSystemInfo = info
	systemInfoCacheTime = time.Now()
	systemInfoMu.Unlock()

	return info
}

// getCachedResourceCounts returns cached or fresh counts for images, volumes, networks
func (h *SystemHandler) getCachedResourceCounts(ctx context.Context) (int, int, int) {
	resourcesCacheMu.RLock()
	if time.Since(resourcesCacheTime) < resourcesCacheTTL {
		images, volumes, networks := cachedImagesCount, cachedVolumesCount, cachedNetworksCount
		resourcesCacheMu.RUnlock()
		return images, volumes, networks
	}
	resourcesCacheMu.RUnlock()

	// Fetch fresh counts in parallel
	var imagesCount, volumesCount, networksCount int
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		if images, err := h.client.ListImages(ctx); err == nil {
			imagesCount = len(images)
		}
	}()

	go func() {
		defer wg.Done()
		if volumes, err := h.client.ListVolumes(ctx); err == nil {
			volumesCount = len(volumes)
		}
	}()

	go func() {
		defer wg.Done()
		if networks, err := h.client.ListNetworks(ctx); err == nil {
			networksCount = len(networks)
		}
	}()

	wg.Wait()

	// Update cache
	resourcesCacheMu.Lock()
	cachedImagesCount = imagesCount
	cachedVolumesCount = volumesCount
	cachedNetworksCount = networksCount
	resourcesCacheTime = time.Now()
	resourcesCacheMu.Unlock()

	return imagesCount, volumesCount, networksCount
}

// Info handles GET /api/system/info
func (h *SystemHandler) Info(w http.ResponseWriter, r *http.Request) {
	info, err := h.client.GetSystemInfo(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, info)
}

// DiskUsage handles GET /api/system/df
func (h *SystemHandler) DiskUsage(w http.ResponseWriter, r *http.Request) {
	df, err := h.client.GetSystemDF(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, df)
}

// Reboot handles POST /api/system/reboot
func (h *SystemHandler) Reboot(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	// Log reboot event
	h.eventStore.Add(events.EventSystemReboot, user.Username, getClientIP(r), true, "")

	// Send response before rebooting
	writeJSON(w, http.StatusOK, map[string]string{"status": "rebooting"})

	// Reboot in background
	go func() {
		exec.Command("systemctl", "reboot").Run()
	}()
}

// Shutdown handles POST /api/system/shutdown
func (h *SystemHandler) Shutdown(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromContext(r.Context())
	if !user.IsAdmin() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Admin access required"})
		return
	}

	// Log shutdown event
	h.eventStore.Add(events.EventSystemShutdown, user.Username, getClientIP(r), true, "")

	// Send response before shutdown
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})

	// Shutdown in background
	go func() {
		exec.Command("systemctl", "poweroff").Run()
	}()
}
