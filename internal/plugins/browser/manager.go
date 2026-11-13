package browser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// errTooManyInstances is returned when all instance slots are busy/starting.
var errTooManyInstances = errors.New("browser instance limit reached")

// errPluginStopped is returned when the plugin is shutting down.
var errPluginStopped = errors.New("browser plugin is stopped")

// sessionSlot is one profile slot: either starting or ready.
type sessionSlot struct {
	ready chan struct{}
	sess  *session
	err   error
}

// sessionManager keeps at most one Chromium instance per network profile and
// enforces the global instance limit plus the idle timeout.
type sessionManager struct {
	plugin *BrowserPlugin

	mu     sync.Mutex
	slots  map[string]*sessionSlot
	closed bool
}

// newSessionManager creates an empty manager.
func newSessionManager(p *BrowserPlugin) *sessionManager {
	return &sessionManager{
		plugin: p,
		slots:  make(map[string]*sessionSlot),
	}
}

// acquire returns a running session for the profile, launching Chromium lazily.
func (m *sessionManager) acquire(ctx context.Context, profileID string) (*session, error) {
	settings := m.plugin.snapshot()
	profile, ok := settings.Profile(profileID)
	if !ok {
		return nil, fmt.Errorf("unknown network profile %q", profileID)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errPluginStopped
	}
	if slot, exists := m.slots[profileID]; exists {
		m.mu.Unlock()
		select {
		case <-slot.ready:
			return slot.sess, slot.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Respect the instance limit: stop the least recently used session.
	var victimID string
	var victim *sessionSlot
	if len(m.slots) >= settings.MaxInstances {
		victimID, victim = m.oldestLocked()
		if victim == nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: %d instance(s) are still starting", errTooManyInstances, len(m.slots))
		}
		delete(m.slots, victimID)
	}

	slot := &sessionSlot{ready: make(chan struct{})}
	m.slots[profileID] = slot
	m.mu.Unlock()

	if victim != nil {
		m.stopSlot(victimID, victim, "instance limit reached")
	}

	return m.launch(ctx, profileID, profile, slot)
}

// launch starts Chromium and publishes the result into the slot.
func (m *sessionManager) launch(ctx context.Context, profileID string, profile Profile, slot *sessionSlot) (*session, error) {
	sess, err := m.start(ctx, profile, profileID)

	m.mu.Lock()
	// The plugin may have been stopped while Chromium was starting: in that case
	// the instance is not tracked by the manager anymore and must be killed here.
	if m.closed && sess != nil {
		m.mu.Unlock()
		m.plugin.logf("plugin stopped during launch, terminating chromium profile=%q", profileID)
		sess.stop(m.plugin.Logger())
		slot.sess, slot.err = nil, errPluginStopped
		close(slot.ready)
		return nil, errPluginStopped
	}
	slot.sess, slot.err = sess, err
	close(slot.ready)
	if err != nil {
		delete(m.slots, profileID)
	}
	m.mu.Unlock()

	return sess, err
}

// start spawns Chromium and attaches to a page target.
func (m *sessionManager) start(ctx context.Context, profile Profile, profileID string) (*session, error) {
	settings := m.plugin.snapshot()
	logger := m.plugin.Logger()

	info := m.plugin.browser()
	if !info.Found {
		return nil, fmt.Errorf("%w: %s", errBrowserNotFound, info.Error)
	}
	if reachable := proxyReachable(profile); reachable != nil && !*reachable {
		m.plugin.logf("profile %q: proxy %s is not listening, launching anyway", profileID, profile.Proxy)
	}

	launchCtx, cancel := context.WithTimeout(ctx, chromiumStartupTimeout+5*time.Second)
	defer cancel()

	proc, err := launchChromium(launchCtx, launchConfig{
		Bin:       info.Path,
		Proxy:     profile.Proxy,
		Width:     settings.WindowWidth,
		Height:    settings.WindowHeight,
		ExtraArgs: settings.ExtraArgs,
		Headless:  headlessFlagFor(info.Major),
		Logger:    logger,
	})
	if err != nil {
		return nil, err
	}

	client, err := dialCDP(proc.wsURL, settings.actionTimeout())
	if err != nil {
		proc.stop(logger)
		return nil, err
	}

	sess, err := newSession(m.plugin, profile, proc, client, settings)
	if err != nil {
		_ = client.Call(context.Background(), "", "Browser.close", nil, nil)
		client.Close()
		proc.stop(logger)
		return nil, err
	}

	m.plugin.logf("started chromium pid=%d profile=%q proxy=%q", proc.pid(), profile.ID, profile.Proxy)
	return sess, nil
}

// oldestLocked returns the session slot that was used least recently.
func (m *sessionManager) oldestLocked() (string, *sessionSlot) {
	var (
		oldestID   string
		oldestSlot *sessionSlot
		oldestTime time.Time
	)
	for id, slot := range m.slots {
		select {
		case <-slot.ready:
			if slot.sess == nil {
				continue
			}
			used := slot.sess.lastUse()
			if oldestSlot == nil || used.Before(oldestTime) {
				oldestID, oldestSlot, oldestTime = id, slot, used
			}
		default:
			// Still starting: cannot be evicted safely.
		}
	}
	return oldestID, oldestSlot
}

// stopSlot stops a detached slot (already removed from the map).
func (m *sessionManager) stopSlot(profileID string, slot *sessionSlot, reason string) {
	if slot == nil {
		return
	}
	select {
	case <-slot.ready:
		if slot.sess == nil {
			return
		}
		m.plugin.logf("stopping chromium profile=%q (%s)", profileID, reason)
		slot.sess.stop(m.plugin.Logger())
	case <-time.After(5 * time.Second):
		m.plugin.logf("profile=%q is still starting, cannot stop it now", profileID)
	}
}

// release stops the session of one profile (the "stop browser" button).
func (m *sessionManager) release(profileID string) {
	m.mu.Lock()
	slot, exists := m.slots[profileID]
	if exists {
		delete(m.slots, profileID)
	}
	m.mu.Unlock()

	if exists {
		m.stopSlot(profileID, slot, "requested by user")
	}
}

// reapIdle stops sessions that were unused for longer than the idle timeout.
func (m *sessionManager) reapIdle(idleTimeout time.Duration) {
	var victims []*sessionSlot
	var victimIDs []string

	m.mu.Lock()
	for id, slot := range m.slots {
		select {
		case <-slot.ready:
			if slot.sess == nil {
				continue
			}
			if time.Since(slot.sess.lastUse()) > idleTimeout {
				victims = append(victims, slot)
				victimIDs = append(victimIDs, id)
				delete(m.slots, id)
			}
		default:
		}
	}
	m.mu.Unlock()

	for i, slot := range victims {
		m.stopSlot(victimIDs[i], slot, "idle timeout")
	}
}

// closeAll stops every session and prevents new launches.
func (m *sessionManager) closeAll() {
	m.mu.Lock()
	m.closed = true
	slots := make(map[string]*sessionSlot, len(m.slots))
	for id, slot := range m.slots {
		slots[id] = slot
	}
	m.slots = make(map[string]*sessionSlot)
	m.mu.Unlock()

	for id, slot := range slots {
		m.stopSlot(id, slot, "plugin stopped")
	}
}

// reopen allows launches again after a plugin restart.
func (m *sessionManager) reopen() {
	m.mu.Lock()
	m.closed = false
	m.mu.Unlock()
}

// sessionStatus is the per-profile runtime state exposed by the status endpoint.
type sessionStatus struct {
	Profile   string    `json:"profile"`
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	LastUsed  time.Time `json:"lastUsed"`
	IdleForS  int       `json:"idleForSec"`
}

// list returns the running sessions.
func (m *sessionManager) list() []sessionStatus {
	m.mu.Lock()
	slots := make(map[string]*sessionSlot, len(m.slots))
	for id, slot := range m.slots {
		slots[id] = slot
	}
	m.mu.Unlock()

	result := make([]sessionStatus, 0, len(slots))
	for id, slot := range slots {
		select {
		case <-slot.ready:
			if slot.sess == nil {
				continue
			}
			url, title := slot.sess.pageMeta()
			result = append(result, sessionStatus{
				Profile:   id,
				URL:       url,
				Title:     title,
				PID:       slot.sess.proc.pid(),
				StartedAt: slot.sess.started,
				LastUsed:  slot.sess.lastUse(),
				IdleForS:  int(time.Since(slot.sess.lastUse()).Seconds()),
			})
		default:
		}
	}
	return result
}

// existing returns the ready session for a profile (if any).
func (m *sessionManager) existing(profileID string) (*session, bool) {
	m.mu.Lock()
	slot, ok := m.slots[profileID]
	m.mu.Unlock()
	if !ok {
		return nil, false
	}
	select {
	case <-slot.ready:
		if slot.sess == nil {
			return nil, false
		}
		return slot.sess, true
	default:
		return nil, false
	}
}

// releaseRemovedOrStale stops instances whose profile was deleted or whose
// window geometry changed, so that new settings take effect on the next open.
func (m *sessionManager) releaseRemovedOrStale(settings *Settings) {
	type stale struct {
		profileID string
		slot      *sessionSlot
		reason    string
	}
	var victims []stale

	m.mu.Lock()
	for id, slot := range m.slots {
		select {
		case <-slot.ready:
			if slot.sess == nil {
				continue
			}
			profile, exists := settings.Profile(id)
			reason := ""
			switch {
			case !exists:
				reason = "profile removed"
			case slot.sess.width != settings.WindowWidth || slot.sess.height != settings.WindowHeight:
				reason = "window size changed"
			case !profile.IsDirect() && profile.Proxy != slot.sess.profile.Proxy:
				reason = "proxy changed"
			}
			if reason != "" {
				victims = append(victims, stale{id, slot, reason})
				delete(m.slots, id)
			}
		default:
		}
	}
	m.mu.Unlock()

	for _, victim := range victims {
		m.stopSlot(victim.profileID, victim.slot, victim.reason)
	}
}

// running reports whether a session exists for the profile.
func (m *sessionManager) running(profileID string) bool {
	m.mu.Lock()
	slot, exists := m.slots[profileID]
	m.mu.Unlock()
	if !exists {
		return false
	}
	select {
	case <-slot.ready:
		return slot.sess != nil
	default:
		return false
	}
}
