package browser

import (
	"context"
	_ "embed"
	"fmt"
	"sync"
	"time"

	"podmanview/internal/plugins"
	"podmanview/internal/storage"
)

//go:embed index.html
var htmlContent []byte

const (
	// pluginName is the plugin identifier (also the storage bucket name).
	pluginName = "browser"
	// pluginVersion is reported in the plugin list.
	pluginVersion = "1.0.0"
	// idleCheckInterval is how often idle Chromium instances are reaped.
	idleCheckInterval = 15 * time.Second
	// browserInfoTTL caches binary detection (probing runs `--version`).
	browserInfoTTL = 60 * time.Second
	// browserMissingTTL caches a negative detection result (cheap to retry).
	browserMissingTTL = 5 * time.Second
)

// BrowserPlugin opens web pages in a headless Chromium running on the host.
type BrowserPlugin struct {
	*plugins.BasePlugin

	mu       sync.RWMutex
	settings *Settings
	manager  *sessionManager

	// checkMu serializes YouTube checks (one measurement at a time).
	checkMu sync.Mutex

	bgMu     sync.Mutex
	bgCancel context.CancelFunc
	bgDone   chan struct{}

	browserMu     sync.Mutex
	browserCached *browserInfo
	browserAt     time.Time
}

// New creates the browser plugin instance.
func New() *BrowserPlugin {
	p := &BrowserPlugin{
		BasePlugin: plugins.NewBasePlugin(
			pluginName,
			"Renders web pages with headless Chromium on the host (screenshots, input, network profiles, YouTube check)",
			pluginVersion,
			htmlContent,
		),
		settings: DefaultSettings(),
	}
	p.manager = newSessionManager(p)
	return p
}

// Init loads the persisted settings.
func (p *BrowserPlugin) Init(ctx context.Context, deps *plugins.PluginDependencies) error {
	p.SetDependencies(deps)

	settings := DefaultSettings()
	loaded := false
	if deps.Storage != nil {
		if err := deps.Storage.GetJSON(p.Name(), settingsKey, settings); err == nil {
			loaded = true
		}
	}
	settings.Normalize()
	if err := settings.Validate(); err != nil {
		p.logf("invalid stored settings (%v), falling back to defaults", err)
		settings = DefaultSettings()
		settings.Normalize()
		loaded = false
	}

	p.mu.Lock()
	p.settings = settings
	p.mu.Unlock()

	if !loaded {
		if err := saveSettings(deps.Storage, p.Name(), settings); err != nil {
			p.logf("failed to persist default settings: %v", err)
		}
	}

	p.logf("plugin initialized (profiles=%d, browser=%s)", len(settings.Profiles), p.describeBrowser())
	return nil
}

// Start enables lazy browser launches and cleans up processes left over from a
// previous run (e.g. after a hard kill of the service).
func (p *BrowserPlugin) Start(ctx context.Context) error {
	p.manager.reopen()
	if killed := terminateOrphans(); killed > 0 {
		p.logf("terminated %d leftover chromium process(es)", killed)
	}
	p.logf("plugin started (browser=%s)", p.describeBrowser())
	return nil
}

// Stop kills every Chromium instance and cancels background work.
func (p *BrowserPlugin) Stop(ctx context.Context) error {
	p.bgMu.Lock()
	cancel := p.bgCancel
	done := p.bgDone
	p.bgCancel = nil
	p.bgDone = nil
	p.bgMu.Unlock()

	if cancel != nil {
		cancel()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}

	p.manager.closeAll()
	p.logf("plugin stopped, all browser instances terminated")
	return nil
}

// StartBackgroundTasks reaps idle browser instances.
func (p *BrowserPlugin) StartBackgroundTasks(ctx context.Context) error {
	p.manager.reopen()

	bgCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	p.bgMu.Lock()
	if p.bgCancel != nil {
		p.bgCancel()
	}
	p.bgCancel = cancel
	p.bgDone = done
	p.bgMu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(idleCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-bgCtx.Done():
				return
			case <-ticker.C:
				p.manager.reapIdle(time.Duration(p.snapshot().IdleStopSec) * time.Second)
			}
		}
	}()

	return nil
}

// Routes returns the plugin HTTP routes. Every route requires authentication.
func (p *BrowserPlugin) Routes() []plugins.Route {
	return []plugins.Route{
		{Method: "GET", Path: "/api/plugins/browser/status", Handler: p.handleStatus, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/navigate", Handler: p.handleNavigate, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/history-step", Handler: p.handleHistoryStep, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/reload", Handler: p.handleReload, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/click", Handler: p.handleClick, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/scroll", Handler: p.handleScroll, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/type", Handler: p.handleType, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/key", Handler: p.handleKey, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/browser/screenshot", Handler: p.handleScreenshot, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/browser/page-text", Handler: p.handlePageText, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/stop", Handler: p.handleStop, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/youtube-check", Handler: p.handleYouTubeCheck, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/browser/history", Handler: p.handleHistory, RequireAuth: true},
		{Method: "DELETE", Path: "/api/plugins/browser/history", Handler: p.handleClearHistory, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/browser/settings", Handler: p.handleGetSettings, RequireAuth: true},
		{Method: "POST", Path: "/api/plugins/browser/settings", Handler: p.handleUpdateSettings, RequireAuth: true},
		{Method: "GET", Path: "/api/plugins/browser/diagnostics", Handler: p.handleDiagnostics, RequireAuth: true},
	}
}

// CanEnable implements plugins.EnableGuard: the plugin refuses to be enabled
// while no Chromium binary is available on the host, because nothing would work.
func (p *BrowserPlugin) CanEnable() error {
	info := p.browser()
	if info.Found {
		return nil
	}

	reason := "Chromium is not installed on the host"
	if info.Error != "" {
		reason += " (" + info.Error + ")"
	}
	return fmt.Errorf("%s: run `apt-get install -y chromium fonts-dejavu-core`, "+
		"or set the binary path in the plugin settings, then enable it again", reason)
}

// IsEnabled checks whether the plugin is enabled in storage.
func (p *BrowserPlugin) IsEnabled() bool {
	if p.Deps() == nil || p.Deps().Storage == nil {
		return false
	}
	enabled, err := p.Deps().Storage.IsPluginEnabled(p.Name())
	if err != nil {
		return false
	}
	return enabled
}

// snapshot returns a copy of the current settings.
func (p *BrowserPlugin) snapshot() *Settings {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.settings.Clone()
}

// browser returns cached browser detection results.
func (p *BrowserPlugin) browser() browserInfo {
	p.browserMu.Lock()
	defer p.browserMu.Unlock()

	if p.browserCached != nil {
		ttl := browserInfoTTL
		if !p.browserCached.Found {
			// Re-detect frequently while no browser is installed: the user may be
			// installing the package right now and must not wait a full minute.
			ttl = browserMissingTTL
		}
		if time.Since(p.browserAt) < ttl {
			return *p.browserCached
		}
	}

	info := detectBrowser(p.snapshot().BrowserPath)
	p.browserCached = &info
	p.browserAt = time.Now()
	return info
}

// invalidateBrowserCache forces a re-detection on the next call.
func (p *BrowserPlugin) invalidateBrowserCache() {
	p.browserMu.Lock()
	p.browserCached = nil
	p.browserAt = time.Time{}
	p.browserMu.Unlock()
}

// describeBrowser returns a short human readable browser state.
func (p *BrowserPlugin) describeBrowser() string {
	info := p.browser()
	if !info.Found {
		return "not found (" + info.Error + ")"
	}
	if info.Version == "" {
		return info.Path
	}
	return info.Version + " (" + info.Path + ")"
}

// updateSettings validates and persists new settings.
func (p *BrowserPlugin) updateSettings(settings *Settings) error {
	settings.Normalize()
	if err := settings.Validate(); err != nil {
		return err
	}

	p.mu.Lock()
	p.settings = settings
	p.mu.Unlock()

	p.invalidateBrowserCache()

	if err := saveSettings(p.storage(), p.Name(), settings); err != nil {
		return err
	}

	// Window geometry is applied at launch, so an explicit stop keeps the
	// active instances in sync with the settings.
	p.logf("settings updated (profiles=%d, window=%dx%d, idle=%ds)", len(settings.Profiles), settings.WindowWidth, settings.WindowHeight, settings.IdleStopSec)
	return nil
}

// storage returns the plugin storage (may be nil in tests).
func (p *BrowserPlugin) storage() storage.Storage {
	if p.Deps() == nil {
		return nil
	}
	return p.Deps().Storage
}

// logf logs through the plugin logger when it is available.
func (p *BrowserPlugin) logf(format string, args ...any) {
	if logger := p.Logger(); logger != nil {
		logger.Printf("[%s] "+format, append([]any{p.Name()}, args...)...)
	}
}
