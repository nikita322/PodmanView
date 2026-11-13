package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"podmanview/internal/plugins"
)

// profileStatus is a profile plus its runtime state.
type profileStatus struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Proxy     string `json:"proxy"`
	ProxyAddr string `json:"proxyAddr,omitempty"`
	Reachable *bool  `json:"reachable,omitempty"`
	Running   bool   `json:"running"`
}

// statusResponse is everything the UI needs to render the page.
type statusResponse struct {
	Browser  browserInfo     `json:"browser"`
	Settings *Settings       `json:"settings"`
	Profiles []profileStatus `json:"profiles"`
	Sessions []sessionStatus `json:"sessions"`
	Page     *pageState      `json:"page,omitempty"`
	Host     hostInfo        `json:"host"`
}

// diagnosticsResponse is the extended host/requirement report.
type diagnosticsResponse struct {
	Browser  browserInfo     `json:"browser"`
	Host     hostInfo        `json:"host"`
	Profiles []profileStatus `json:"profiles"`
	Sessions []sessionStatus `json:"sessions"`
	Hints    []string        `json:"hints"`
}

// navigateRequest is shared by navigation-like actions.
type navigateRequest struct {
	Profile string `json:"profile"`
	URL     string `json:"url"`
	Dir     string `json:"dir"`
}

// pointRequest is a click/scroll request in screenshot coordinates.
type pointRequest struct {
	Profile string  `json:"profile"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	DeltaY  float64 `json:"deltaY"`
}

// typeRequest inserts text into the focused element.
type typeRequest struct {
	Profile string `json:"profile"`
	Text    string `json:"text"`
	Submit  bool   `json:"submit"`
}

// keyRequest presses a single named key.
type keyRequest struct {
	Profile string `json:"profile"`
	Key     string `json:"key"`
}

// stopRequest stops one profile or all instances.
type stopRequest struct {
	Profile string `json:"profile"`
	All     bool   `json:"all"`
}

// checkRequest starts a YouTube check.
type checkRequest struct {
	Profile string `json:"profile"`
	URL     string `json:"url"`
}

// profileFromRequest resolves the profile ID, defaulting to the first profile.
func (p *BrowserPlugin) profileFromRequest(requested string) (string, error) {
	settings := p.snapshot()
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = settings.DefaultProfileID()
	}
	if _, ok := settings.Profile(requested); !ok {
		return "", fmt.Errorf("unknown network profile %q", requested)
	}
	return requested, nil
}

// decodeJSON decodes a JSON request body into dst.
func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return decoder.Decode(dst)
}

// writeError writes a JSON error with an optional machine readable code.
func writeError(w http.ResponseWriter, status int, code string, err error) {
	payload := map[string]string{"error": err.Error()}
	if code != "" {
		payload["code"] = code
	}
	plugins.WriteJSON(w, status, payload)
}

// writeSessionError maps plugin errors onto HTTP statuses.
func writeSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBrowserNotFound):
		writeError(w, http.StatusServiceUnavailable, "browser_missing", err)
	case errors.Is(err, errTooManyInstances), errors.Is(err, errPluginStopped):
		writeError(w, http.StatusConflict, "browser_busy", err)
	default:
		writeError(w, http.StatusInternalServerError, "browser_error", err)
	}
}

// handleStatus returns the plugin state, settings and current page info.
func (p *BrowserPlugin) handleStatus(w http.ResponseWriter, r *http.Request) {
	settings := p.snapshot()
	profileID, err := p.profileFromRequest(r.URL.Query().Get("profile"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}

	response := p.buildStatus(r.Context(), settings, profileID)
	plugins.WriteJSON(w, http.StatusOK, response)
}

// buildStatus assembles the status response.
func (p *BrowserPlugin) buildStatus(ctx context.Context, settings *Settings, profileID string) *statusResponse {
	response := &statusResponse{
		Browser:  p.browser(),
		Settings: settings,
		Profiles: p.profileStatuses(settings),
		Sessions: p.manager.list(),
		Host:     readHostInfo(),
	}
	if sess, ok := p.manager.existing(profileID); ok {
		stateCtx, cancel := context.WithTimeout(ctx, settings.actionTimeout())
		defer cancel()
		if state, err := sess.snapshotState(stateCtx, ""); err == nil {
			response.Page = state
		}
	}
	return response
}

// handleDiagnostics returns host checks and installation hints.
func (p *BrowserPlugin) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	settings := p.snapshot()
	info := p.browser()
	host := readHostInfo()

	response := &diagnosticsResponse{
		Browser:  info,
		Host:     host,
		Profiles: p.profileStatuses(settings),
		Sessions: p.manager.list(),
		Hints:    buildHints(info, host, settings),
	}
	plugins.WriteJSON(w, http.StatusOK, response)
}

// profileStatuses enriches profiles with reachability and running state.
func (p *BrowserPlugin) profileStatuses(settings *Settings) []profileStatus {
	result := make([]profileStatus, 0, len(settings.Profiles))
	for _, profile := range settings.Profiles {
		status := profileStatus{
			ID:        profile.ID,
			Name:      profile.Name,
			Proxy:     profile.Proxy,
			ProxyAddr: profile.ProxyAddr(),
			Reachable: proxyReachable(profile),
			Running:   p.manager.running(profile.ID),
		}
		result = append(result, status)
	}
	return result
}

// buildHints explains what is missing on the host.
func buildHints(info browserInfo, host hostInfo, settings *Settings) []string {
	hints := make([]string, 0, 4)

	if !info.Found {
		hints = append(hints,
			"Chromium is not installed. On Debian/Ubuntu arm64: apt-get install -y chromium",
			"The plugin never downloads or bundles a browser, so this package must exist on the host.",
			"Alternatively set the binary path explicitly in the settings (e.g. /usr/lib/chromium/chromium).",
		)
	} else if info.Error != "" {
		hints = append(hints, "Configured browser path is unusable: "+info.Error)
	}

	if info.Found && host.FontCommandOK && host.FontFamilies == 0 {
		hints = append(hints, "No fonts found (fc-list). Install fonts-dejavu-core, otherwise pages render boxes instead of text.")
	}
	if info.Found && !host.FontCommandOK {
		hints = append(hints, "fontconfig (fc-list) is not available; font presence cannot be verified.")
	}

	for _, profile := range settings.Profiles {
		if profile.IsDirect() {
			continue
		}
		if reachable := proxyReachable(profile); reachable != nil && !*reachable {
			hints = append(hints, fmt.Sprintf("Profile %q: nothing is listening on %s — start your local proxy first.",
				profile.ID, profile.ProxyAddr()))
		}
	}

	if host.MemAvailableMB > 0 && host.MemAvailableMB < 500 {
		hints = append(hints, fmt.Sprintf("Only %d MB of RAM is available: Chromium needs roughly 250-400 MB per instance.",
			host.MemAvailableMB))
	}
	if host.BrowserProcs > 0 && host.TempProfileDirs > 0 && host.MemAvailableMB == 0 {
		hints = append(hints, "Browser processes are running, but host memory could not be read.")
	}
	return hints
}

// handleNavigate opens a URL in the profile browser.
func (p *BrowserPlugin) handleNavigate(w http.ResponseWriter, r *http.Request) {
	var req navigateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	profileID, err := p.profileFromRequest(req.Profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}

	sess, err := p.manager.acquire(r.Context(), profileID)
	if err != nil {
		writeSessionError(w, err)
		return
	}

	state, err := sess.navigate(r.Context(), req.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "navigation_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, state)
}

// handleHistoryStep moves back or forward in the page history.
func (p *BrowserPlugin) handleHistoryStep(w http.ResponseWriter, r *http.Request) {
	var req navigateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	profileID, err := p.profileFromRequest(req.Profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}

	sess, ok := p.manager.existing(profileID)
	if !ok {
		writeError(w, http.StatusConflict, "browser_not_running", fmt.Errorf("browser is not running for profile %q", profileID))
		return
	}

	var state *pageState
	switch req.Dir {
	case "forward":
		state, err = sess.forward(r.Context())
	case "back", "":
		state, err = sess.back(r.Context())
	default:
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Errorf("dir must be back or forward"))
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "navigation_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, state)
}

// handleReload reloads the current page.
func (p *BrowserPlugin) handleReload(w http.ResponseWriter, r *http.Request) {
	var req navigateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	profileID, err := p.profileFromRequest(req.Profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}

	sess, ok := p.manager.existing(profileID)
	if !ok {
		writeError(w, http.StatusConflict, "browser_not_running", fmt.Errorf("browser is not running for profile %q", profileID))
		return
	}
	state, err := sess.reload(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "navigation_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, state)
}

// handleClick sends a click at screenshot coordinates.
func (p *BrowserPlugin) handleClick(w http.ResponseWriter, r *http.Request) {
	var req pointRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	sess, ok := p.existingSession(w, req.Profile)
	if !ok {
		return
	}
	if err := sess.click(r.Context(), req.X, req.Y); err != nil {
		writeError(w, http.StatusBadGateway, "action_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleScroll sends a wheel event at screenshot coordinates.
func (p *BrowserPlugin) handleScroll(w http.ResponseWriter, r *http.Request) {
	var req pointRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	sess, ok := p.existingSession(w, req.Profile)
	if !ok {
		return
	}
	if err := sess.scroll(r.Context(), req.X, req.Y, req.DeltaY); err != nil {
		writeError(w, http.StatusBadGateway, "action_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleType inserts text into the focused element.
func (p *BrowserPlugin) handleType(w http.ResponseWriter, r *http.Request) {
	var req typeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	sess, ok := p.existingSession(w, req.Profile)
	if !ok {
		return
	}
	if err := sess.insertText(r.Context(), req.Text, req.Submit); err != nil {
		writeError(w, http.StatusBadGateway, "action_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleKey presses a single named key.
func (p *BrowserPlugin) handleKey(w http.ResponseWriter, r *http.Request) {
	var req keyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	sess, ok := p.existingSession(w, req.Profile)
	if !ok {
		return
	}
	if err := sess.pressKey(r.Context(), req.Key); err != nil {
		writeError(w, http.StatusBadGateway, "action_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleScreenshot returns the current viewport as a JPEG image.
func (p *BrowserPlugin) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	profileID, err := p.profileFromRequest(r.URL.Query().Get("profile"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}
	sess, ok := p.manager.existing(profileID)
	if !ok {
		writeError(w, http.StatusConflict, "browser_not_running", fmt.Errorf("browser is not running for profile %q", profileID))
		return
	}

	image, err := sess.screenshot(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "screenshot_failed", err)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(image)
}

// handlePageText returns the visible page text (proves that text really renders).
func (p *BrowserPlugin) handlePageText(w http.ResponseWriter, r *http.Request) {
	profileID, err := p.profileFromRequest(r.URL.Query().Get("profile"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}
	sess, ok := p.manager.existing(profileID)
	if !ok {
		writeError(w, http.StatusConflict, "browser_not_running", fmt.Errorf("browser is not running for profile %q", profileID))
		return
	}

	text, err := sess.pageText(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "action_failed", err)
		return
	}
	url, title := sess.pageMeta()
	plugins.WriteJSON(w, http.StatusOK, map[string]string{
		"profile": profileID,
		"url":     url,
		"title":   title,
		"text":    text,
	})
}

// handleStop stops one profile browser or all of them.
func (p *BrowserPlugin) handleStop(w http.ResponseWriter, r *http.Request) {
	var req stopRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err)
			return
		}
	}

	if req.All {
		p.manager.closeAll()
		p.manager.reopen()
		plugins.WriteJSON(w, http.StatusOK, map[string]bool{"stopped": true})
		return
	}

	profileID, err := p.profileFromRequest(req.Profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}
	p.manager.release(profileID)
	plugins.WriteJSON(w, http.StatusOK, map[string]bool{"stopped": true})
}

// handleYouTubeCheck runs the automated playback measurement.
func (p *BrowserPlugin) handleYouTubeCheck(w http.ResponseWriter, r *http.Request) {
	var req checkRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err)
			return
		}
	}
	profileID, err := p.profileFromRequest(req.Profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return
	}

	if !p.checkMu.TryLock() {
		writeError(w, http.StatusConflict, "check_running", fmt.Errorf("another check is already running"))
		return
	}
	defer p.checkMu.Unlock()

	result, err := p.runYouTubeCheck(r.Context(), profileID, req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "check_failed", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, result)
}

// handleHistory returns stored check results.
func (p *BrowserPlugin) handleHistory(w http.ResponseWriter, r *http.Request) {
	history := p.history()
	plugins.WriteJSON(w, http.StatusOK, history)
}

// handleClearHistory removes stored check results.
func (p *BrowserPlugin) handleClearHistory(w http.ResponseWriter, r *http.Request) {
	if err := p.clearHistory(); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", err)
		return
	}
	plugins.WriteJSON(w, http.StatusOK, map[string]bool{"cleared": true})
}

// handleGetSettings returns the plugin settings.
func (p *BrowserPlugin) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	plugins.WriteJSON(w, http.StatusOK, p.snapshot())
}

// handleUpdateSettings validates and stores new settings.
func (p *BrowserPlugin) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var settings Settings
	if err := decodeJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err)
		return
	}
	if err := p.updateSettings(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "bad_settings", err)
		return
	}

	// Running instances would otherwise keep stale geometry/profiles.
	p.manager.releaseRemovedOrStale(&settings)
	plugins.WriteJSON(w, http.StatusOK, map[string]any{"status": "Settings updated successfully", "settings": settings})
}

// existingSession resolves the profile and returns its running session.
func (p *BrowserPlugin) existingSession(w http.ResponseWriter, requested string) (*session, bool) {
	profileID, err := p.profileFromRequest(requested)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_profile", err)
		return nil, false
	}
	sess, ok := p.manager.existing(profileID)
	if !ok {
		writeError(w, http.StatusConflict, "browser_not_running",
			fmt.Errorf("browser is not running for profile %q, open a page first", profileID))
		return nil, false
	}
	return sess, true
}
