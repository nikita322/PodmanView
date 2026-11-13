package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// pageState is what the UI shows above the screenshot.
// It also carries the extracted page text so the user can verify that the host
// really rendered text (and not just empty boxes).
type pageState struct {
	Profile        string `json:"profile"`
	BrowserRunning bool   `json:"browserRunning"`
	URL            string `json:"url"`
	Title          string `json:"title"`
	ReadyState     string `json:"readyState"`
	CanGoBack      bool   `json:"canGoBack"`
	CanGoForward   bool   `json:"canGoForward"`
	TextPreview    string `json:"textPreview"`
	Warning        string `json:"warning,omitempty"`
}

// stateJS collects the page facts used by pageState in one round trip.
const stateJS = `(() => ({
	url: location.href,
	title: document.title || '',
	readyState: document.readyState,
	text: ((document.body && document.body.innerText) || '').slice(0, 2000)
}))()`

// session is one headless Chromium instance bound to one network profile,
// with exactly one page target used by the UI.
type session struct {
	plugin  *BrowserPlugin
	profile Profile

	proc   *chromiumProcess
	client *cdpClient

	targetID  string
	sessionID string
	width     int
	height    int

	// opMu serializes browser operations on the single page target.
	opMu sync.Mutex

	mu        sync.Mutex
	started   time.Time
	lastUsed  time.Time
	pageURL   string
	pageTitle string
}

// newSession attaches to a fresh page target and enables the required domains.
func newSession(p *BrowserPlugin, profile Profile, proc *chromiumProcess, client *cdpClient, settings *Settings) (*session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), chromiumStartupTimeout/2)
	defer cancel()

	s := &session{
		plugin:   p,
		profile:  profile,
		proc:     proc,
		client:   client,
		width:    settings.WindowWidth,
		height:   settings.WindowHeight,
		started:  time.Now(),
		lastUsed: time.Now(),
	}

	var created struct {
		TargetID string `json:"targetId"`
	}
	// width/height are intentionally omitted: per CDP they are only valid for
	// a new window. The viewport is set by --window-size and the Emulation domain.
	if err := client.Call(ctx, "", "Target.createTarget", map[string]any{
		"url": "about:blank",
	}, &created); err != nil {
		return nil, fmt.Errorf("create page target: %w", err)
	}
	s.targetID = created.TargetID

	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := client.Call(ctx, "", "Target.attachToTarget", map[string]any{
		"targetId": s.targetID,
		"flatten":  true,
	}, &attached); err != nil {
		return nil, fmt.Errorf("attach to page target: %w", err)
	}
	s.sessionID = attached.SessionID

	for _, domain := range []string{"Page.enable", "Runtime.enable", "Network.enable", "Log.enable"} {
		if err := client.Call(ctx, s.sessionID, domain, nil, nil); err != nil {
			return nil, fmt.Errorf("enable %s: %w", domain, err)
		}
	}

	// 1 device pixel per CSS pixel keeps screenshot coordinates == click coordinates.
	if err := client.Call(ctx, s.sessionID, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width":             s.width,
		"height":            s.height,
		"deviceScaleFactor": 1,
		"mobile":            false,
	}, nil); err != nil {
		return nil, fmt.Errorf("set viewport: %w", err)
	}

	_ = client.Call(ctx, "", "Target.activateTarget", map[string]any{"targetId": s.targetID}, nil)
	return s, nil
}

// settings returns a snapshot of the current plugin settings.
func (s *session) settings() *Settings { return s.plugin.snapshot() }

// touch marks the session as recently used (drives the idle reaper).
func (s *session) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

// lastUse returns the last time the session was used.
func (s *session) lastUse() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

// pageMeta returns the cached URL/title.
func (s *session) pageMeta() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pageURL, s.pageTitle
}

// call performs a page-level protocol call with the action timeout.
func (s *session) call(ctx context.Context, method string, params, out any) error {
	callCtx, cancel := context.WithTimeout(ctx, s.settings().actionTimeout())
	defer cancel()
	return s.client.Call(callCtx, s.sessionID, method, params, out)
}

// evalRaw evaluates a JS expression and returns its JSON value.
func (s *session) evalRaw(ctx context.Context, expression string) (json.RawMessage, error) {
	var res struct {
		Result struct {
			Value       json.RawMessage `json:"value"`
			Type        string          `json:"type"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := s.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"userGesture":   true,
		"awaitPromise":  true,
	}, &res); err != nil {
		return nil, err
	}
	if res.ExceptionDetails != nil {
		msg := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil && res.ExceptionDetails.Exception.Description != "" {
			msg = res.ExceptionDetails.Exception.Description
		}
		return nil, fmt.Errorf("javascript error: %s", msg)
	}
	return res.Result.Value, nil
}

// evalJSON evaluates an expression that yields an object and decodes it into out.
func (s *session) evalJSON(ctx context.Context, expression string, out any) error {
	raw, err := s.evalRaw(ctx, expression)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("expression returned no value")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode expression result: %w", err)
	}
	return nil
}

// evalString evaluates an expression that yields a string.
func (s *session) evalString(ctx context.Context, expression string) (string, error) {
	var value string
	if err := s.evalJSON(ctx, expression, &value); err != nil {
		return "", err
	}
	return value, nil
}

// back navigates one entry back in the history.
func (s *session) back(ctx context.Context) (*pageState, error) {
	return s.historyStep(ctx, -1)
}

// forward navigates one entry forward in the history.
func (s *session) forward(ctx context.Context) (*pageState, error) {
	return s.historyStep(ctx, 1)
}

// historyStep walks the DevTools navigation history by offset.
func (s *session) historyStep(ctx context.Context, offset int) (*pageState, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.touch()

	var history struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID  int64  `json:"id"`
			URL string `json:"url"`
		} `json:"entries"`
	}
	if err := s.call(ctx, "Page.getNavigationHistory", nil, &history); err != nil {
		return nil, err
	}
	index := history.CurrentIndex + offset
	if index < 0 || index >= len(history.Entries) {
		return s.snapshotState(ctx, "already at the edge of history")
	}
	if err := s.call(ctx, "Page.navigateToHistoryEntry", map[string]any{
		"entryId": history.Entries[index].ID,
	}, nil); err != nil {
		return nil, err
	}
	warning := s.waitForLoad(ctx)
	return s.snapshotState(ctx, warning)
}

// reload re-navigates to the current URL.
func (s *session) reload(ctx context.Context) (*pageState, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.touch()

	if err := s.call(ctx, "Page.reload", map[string]any{"ignoreCache": true}, nil); err != nil {
		return nil, err
	}
	warning := s.waitForLoad(ctx)
	return s.snapshotState(ctx, warning)
}

// navigate opens an http(s) URL and waits until the page finishes loading.
func (s *session) navigate(ctx context.Context, rawURL string) (*pageState, error) {
	target, err := normalizeURL(rawURL)
	if err != nil {
		return nil, err
	}

	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.touch()

	var res struct {
		ErrorText string `json:"errorText"`
	}
	if err := s.call(ctx, "Page.navigate", map[string]any{"url": target}, &res); err != nil {
		return nil, err
	}
	if res.ErrorText != "" {
		return nil, fmt.Errorf("navigation failed: %s", res.ErrorText)
	}

	warning := s.waitForLoad(ctx)
	return s.snapshotState(ctx, warning)
}

// waitForLoad polls document.readyState until the page is complete or the
// navigation timeout elapses. It returns a human readable warning (if any).
func (s *session) waitForLoad(ctx context.Context) string {
	timeout := s.settings().navigateTimeout()
	deadline := time.Now().Add(timeout)

	for {
		state, err := s.evalString(ctx, "document.readyState")
		if err == nil && state == "complete" {
			return ""
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("page did not finish loading within %s (readyState=%q)", timeout, state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err().Error()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// snapshotState builds the current pageState.
func (s *session) snapshotState(ctx context.Context, warning string) (*pageState, error) {
	var page struct {
		URL        string `json:"url"`
		Title      string `json:"title"`
		ReadyState string `json:"readyState"`
		Text       string `json:"text"`
	}
	evalErr := s.evalJSON(ctx, stateJS, &page)

	state := &pageState{
		Profile:        s.profile.ID,
		BrowserRunning: true,
		URL:            page.URL,
		Title:          page.Title,
		ReadyState:     page.ReadyState,
		TextPreview:    truncateRunes(page.Text, 400),
		Warning:        warning,
	}

	if evalErr != nil && state.Warning == "" {
		state.Warning = evalErr.Error()
	}

	var history struct {
		CurrentIndex int `json:"currentIndex"`
		Entries      []struct {
			ID int64 `json:"id"`
		} `json:"entries"`
	}
	if err := s.call(ctx, "Page.getNavigationHistory", nil, &history); err == nil {
		state.CanGoBack = history.CurrentIndex > 0
		state.CanGoForward = history.CurrentIndex >= 0 && history.CurrentIndex < len(history.Entries)-1
	}

	s.mu.Lock()
	s.pageURL = state.URL
	s.pageTitle = state.Title
	s.mu.Unlock()

	return state, nil
}

// click dispatches a trusted left click at viewport coordinates.
func (s *session) click(ctx context.Context, x, y float64) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.touch()

	events := []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y, "button": "none", "buttons": 0},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
	}
	for _, event := range events {
		if err := s.call(ctx, "Input.dispatchMouseEvent", event, nil); err != nil {
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}

// scroll dispatches a wheel event at viewport coordinates.
func (s *session) scroll(ctx context.Context, x, y, deltaY float64) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.touch()

	return s.call(ctx, "Input.dispatchMouseEvent", map[string]any{
		"type":   "mouseWheel",
		"x":      x,
		"y":      y,
		"deltaX": 0,
		"deltaY": deltaY,
	}, nil)
}

// insertText inserts text into the focused element.
func (s *session) insertText(ctx context.Context, text string, submit bool) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.touch()

	if text != "" {
		if err := s.call(ctx, "Input.insertText", map[string]any{"text": text}, nil); err != nil {
			return err
		}
	}
	if submit {
		return s.pressKeyLocked(ctx, "Enter")
	}
	return nil
}

// pressKey dispatches a single named key press.
func (s *session) pressKey(ctx context.Context, name string) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.touch()
	return s.pressKeyLocked(ctx, name)
}

// keySpec describes how a named key is dispatched.
type keySpec struct {
	Key     string
	Code    string
	VK      int
	Text    string
	RawOnly bool
}

// keySpecs maps the UI key buttons onto DevTools key events.
var keySpecs = map[string]keySpec{
	"Enter":      {Key: "Enter", Code: "Enter", VK: 13, Text: "\r"},
	"Tab":        {Key: "Tab", Code: "Tab", VK: 9, RawOnly: true},
	"Escape":     {Key: "Escape", Code: "Escape", VK: 27, RawOnly: true},
	"Backspace":  {Key: "Backspace", Code: "Backspace", VK: 8, RawOnly: true},
	"ArrowDown":  {Key: "ArrowDown", Code: "ArrowDown", VK: 40, RawOnly: true},
	"ArrowUp":    {Key: "ArrowUp", Code: "ArrowUp", VK: 38, RawOnly: true},
	"ArrowLeft":  {Key: "ArrowLeft", Code: "ArrowLeft", VK: 37, RawOnly: true},
	"ArrowRight": {Key: "ArrowRight", Code: "ArrowRight", VK: 39, RawOnly: true},
	"PageDown":   {Key: "PageDown", Code: "PageDown", VK: 34, RawOnly: true},
	"PageUp":     {Key: "PageUp", Code: "PageUp", VK: 33, RawOnly: true},
	"Home":       {Key: "Home", Code: "Home", VK: 36, RawOnly: true},
	"End":        {Key: "End", Code: "End", VK: 35, RawOnly: true},
}

// pressKeyLocked dispatches a named key; the caller must hold opMu.
func (s *session) pressKeyLocked(ctx context.Context, name string) error {
	spec, ok := keySpecs[name]
	if !ok {
		return fmt.Errorf("unsupported key %q", name)
	}

	down := map[string]any{
		"type":                  "rawKeyDown",
		"key":                   spec.Key,
		"code":                  spec.Code,
		"windowsVirtualKeyCode": spec.VK,
		"nativeVirtualKeyCode":  spec.VK,
	}
	if spec.Text != "" {
		down["type"] = "keyDown"
		down["text"] = spec.Text
		down["unmodifiedText"] = spec.Text
	}
	if err := s.call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	return s.call(ctx, "Input.dispatchKeyEvent", map[string]any{
		"type":                  "keyUp",
		"key":                   spec.Key,
		"code":                  spec.Code,
		"windowsVirtualKeyCode": spec.VK,
		"nativeVirtualKeyCode":  spec.VK,
	}, nil)
}

// screenshot returns a JPEG screenshot of the current viewport.
func (s *session) screenshot(ctx context.Context) ([]byte, error) {
	s.touch()

	var res struct {
		Data string `json:"data"`
	}
	if err := s.call(ctx, "Page.captureScreenshot", map[string]any{
		"format":                "jpeg",
		"quality":               s.settings().ImageQuality,
		"captureBeyondViewport": false,
	}, &res); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(res.Data)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return data, nil
}

// pageText returns the visible text of the page (up to 4000 characters).
func (s *session) pageText(ctx context.Context) (string, error) {
	s.touch()
	text, err := s.evalString(ctx, `(((document.body && document.body.innerText) || '').slice(0, 4000))`)
	if err != nil {
		return "", err
	}
	return text, nil
}

// stop closes the DevTools connection, kills Chromium and removes its profile dir.
func (s *session) stop(logger *log.Logger) {
	if s.client != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.client.Call(closeCtx, "", "Browser.close", nil, nil)
		cancel()
		s.client.Close()
	}
	s.proc.stop(logger)
}

// truncateRunes shortens a string to at most limit runes.
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
