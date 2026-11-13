//go:build integration

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"podmanview/internal/plugins"
)

// TestIntegrationChrome exercises the real DevTools flow against a locally
// installed Chromium/Chrome. Run with:
//
//	BROWSER_BIN="C:/Program Files/Google/Chrome/Application/chrome.exe" go test -tags integration -run TestIntegrationChrome -v ./internal/plugins/browser
func TestIntegrationChrome(t *testing.T) {
	bin := os.Getenv("BROWSER_BIN")
	if bin == "" {
		t.Skip("BROWSER_BIN not set")
	}

	logger := log.New(os.Stdout, "[it] ", log.Ltime)
	p := New()
	if err := p.Init(context.Background(), &plugins.PluginDependencies{Logger: logger}); err != nil {
		t.Fatalf("init: %v", err)
	}

	settings := p.snapshot()
	settings.BrowserPath = bin
	settings.CheckDurationSec = 8
	settings.NavigateTimeoutSec = 30
	p.mu.Lock()
	p.settings = settings
	p.mu.Unlock()
	p.invalidateBrowserCache()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><head><title>Local test page</title>
<style>body{font-family:sans-serif} #btn{position:absolute;left:40px;top:120px;width:200px;height:60px;font-size:18px}
#inp{position:absolute;left:40px;top:220px;width:300px;height:40px;font-size:16px}#out{position:absolute;left:40px;top:280px;font-size:18px}</style></head>
<body><h1>Rendered by the host browser</h1><p>Some visible text for the text extraction check.</p>
<button id="btn" onclick="document.getElementById('out').textContent='clicked!'">Click me</button>
<input id="inp"><div id="out">idle</div></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sess, err := p.manager.acquire(ctx, "direct")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Logf("chromium pid=%d", sess.proc.pid())

	state, err := sess.navigate(ctx, srv.URL)
	if err != nil {
		t.Fatalf("navigate: %v", err)
	}
	t.Logf("state: url=%s title=%q ready=%s", state.URL, state.Title, state.ReadyState)
	if !strings.Contains(state.Title, "Local test page") {
		t.Fatalf("unexpected title: %q", state.Title)
	}
	if !strings.Contains(state.TextPreview, "Rendered by the host browser") {
		t.Fatalf("text not rendered: %q", state.TextPreview)
	}

	image, err := sess.screenshot(ctx)
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if len(image) < 2000 || image[0] != 0xFF || image[1] != 0xD8 {
		t.Fatalf("screenshot does not look like a JPEG: len=%d", len(image))
	}
	t.Logf("screenshot ok: %d bytes", len(image))
	_ = os.WriteFile(fmt.Sprintf("%s/podmanview-shot-local.jpg", os.TempDir()), image, 0o644)

	// Click the button (its box is fixed at 40,120 200x60 in CSS pixels).
	if err := sess.click(ctx, 140, 150); err != nil {
		t.Fatalf("click: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	text, err := sess.pageText(ctx)
	if err != nil {
		t.Fatalf("page text: %v", err)
	}
	if !strings.Contains(text, "clicked!") {
		t.Fatalf("click did not reach the page: %q", text)
	}

	// Type into the input.
	if err := sess.click(ctx, 140, 240); err != nil {
		t.Fatalf("focus input: %v", err)
	}
	if err := sess.insertText(ctx, "hello host", false); err != nil {
		t.Fatalf("type: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	value, err := sess.evalString(ctx, `document.getElementById('inp').value`)
	if err != nil {
		t.Fatalf("read input: %v", err)
	}
	if value != "hello host" {
		t.Fatalf("typed text mismatch: %q", value)
	}

	// History: navigate to a second page and go back.
	if _, err := sess.navigate(ctx, srv.URL+"?second=1"); err != nil {
		t.Fatalf("second navigate: %v", err)
	}
	if state, err = sess.back(ctx); err != nil {
		t.Fatalf("back: %v", err)
	}
	if strings.Contains(state.URL, "second=1") {
		t.Fatalf("back did not navigate: %s", state.URL)
	}

	// Scroll must not error.
	if err := sess.scroll(ctx, 400, 400, 300); err != nil {
		t.Fatalf("scroll: %v", err)
	}

	// The check on a page without a video must produce an actionable result.
	result, err := p.runYouTubeCheck(ctx, "direct", srv.URL)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	t.Logf("local-page check verdict=%s summary=%q", result.Verdict, result.Summary)
	if result.Verdict != VerdictError {
		t.Fatalf("expected error verdict for a page without video, got %s", result.Verdict)
	}
	if result.Report == "" {
		t.Fatal("empty report")
	}

	// Real YouTube check (validates the full metric pipeline; the verdict
	// depends on the network this test runs in).
	ytCtx, ytCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer ytCancel()
	yt, err := p.runYouTubeCheck(ytCtx, "direct", "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("youtube check: %v", err)
	}
	t.Logf("youtube verdict=%s summary=%q", yt.Verdict, yt.Summary)
	t.Logf("report:\n%s", yt.Report)
	if shotSess, ok := p.manager.existing("direct"); ok {
		if shot, err := shotSess.screenshot(ctx); err == nil {
			_ = os.WriteFile(fmt.Sprintf("%s/podmanview-shot-youtube.jpg", os.TempDir()), shot, 0o644)
		}
	}
	if yt.Metrics.Samples == 0 && yt.Verdict == VerdictWorks {
		t.Fatal("works verdict without samples")
	}

	// A dead proxy must produce the "blocked" verdict, not a generic error.
	deadSettings := p.snapshot()
	deadSettings.Profiles = append(deadSettings.Profiles, Profile{
		ID:    "deadproxy",
		Name:  "Dead local proxy",
		Proxy: "socks5://127.0.0.1:1",
	})
	deadSettings.CheckDurationSec = 5
	p.mu.Lock()
	p.settings = deadSettings
	p.mu.Unlock()
	p.invalidateBrowserCache()

	blocked, err := p.runYouTubeCheck(ctx, "deadproxy", "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil {
		t.Fatalf("blocked check: %v", err)
	}
	t.Logf("dead-proxy verdict=%s summary=%q", blocked.Verdict, blocked.Summary)
	t.Logf("dead-proxy report:\n%s", blocked.Report)
	if blocked.Verdict != VerdictBlocked {
		t.Fatalf("expected blocked verdict with a dead proxy, got %s", blocked.Verdict)
	}

	// Idle reaping must terminate the process.
	proc := sess.proc
	p.manager.reapIdle(1 * time.Nanosecond)
	time.Sleep(500 * time.Millisecond)
	if !proc.exited() {
		t.Fatal("chromium still running after idle reap")
	}
	if len(p.manager.list()) != 0 {
		t.Fatalf("sessions still present after idle reap: %+v", p.manager.list())
	}

	p.manager.closeAll()
}

// TestIntegrationUI loads the plugin page with a mocked API in a real browser
// and checks that the interface boots, renders and reacts to clicks.
func TestIntegrationUI(t *testing.T) {
	bin := os.Getenv("BROWSER_BIN")
	if bin == "" {
		t.Skip("BROWSER_BIN not set")
	}

	sectionHTML, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	styleCSS, err := os.ReadFile("../../../web/static/css/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}

	var (
		mu     sync.Mutex
		counts = map[string]int{}
	)
	count := func(name string) {
		mu.Lock()
		counts[name]++
		mu.Unlock()
	}
	countsSnapshot := func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string]int, len(counts))
		for k, v := range counts {
			out[k] = v
		}
		return out
	}

	settings := DefaultSettings()
	status := map[string]any{
		"browser":  map[string]any{"found": true, "path": "/usr/bin/chromium", "version": "Chromium 120.0.6099.224", "major": 120},
		"settings": settings,
		"profiles": []map[string]any{
			{"id": "direct", "name": "Direct (host network)", "proxy": "", "running": true},
			{"id": "bypass", "name": "Local SOCKS proxy (byedevpi)", "proxy": "socks5://127.0.0.1:1080", "reachable": false, "proxyAddr": "127.0.0.1:1080"},
		},
		"sessions": []map[string]any{
			{"profile": "direct", "url": "http://example.com/", "title": "Example", "pid": 4242, "idleForSec": 3},
		},
		"page": map[string]any{
			"profile": "direct", "browserRunning": true, "url": "http://example.com/",
			"title": "Example Domain", "readyState": "complete",
			"textPreview": "Example Domain This domain is for use in illustrative examples.",
		},
		"host": map[string]any{"arch": "arm64", "memTotalMb": 3000, "memAvailableMb": 1200, "fontFamilies": 12, "fontCommandOk": true},
	}
	checkResult := map[string]any{
		"id": "1", "startedAt": time.Now().UTC(), "profile": "bypass", "profileName": "Local SOCKS proxy",
		"proxy": "socks5://127.0.0.1:1080", "url": "https://www.youtube.com/watch?v=x",
		"verdict": VerdictWorks, "summary": "Playback keeps up with the wall clock",
		"reasons": []string{"played 19.0s of 19.5s (97%)"},
		"metrics": map[string]any{"playedSec": 19.0, "wallSec": 19.5, "playedRatio": 0.97, "bufferedAheadSec": 8.0,
			"maxBufferedAhead": 9.0, "downloadedMb": 6.4, "throughputKbps": 336, "videoWidth": 1280, "videoHeight": 720,
			"droppedFrames": 1, "totalFrames": 580, "droppedPercent": 0.2, "readyState": 4, "samples": 20, "playingSamples": 20},
		"networkFailures": []any{}, "consoleErrors": []string{},
		"report": "Browser check — test\nVerdict: WORKS",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/static/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(styleCSS)
	})
	jsonHandler := func(payload any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(payload)
		}
	}
	mux.HandleFunc("/api/plugins/browser/status", jsonHandler(status))
	mux.HandleFunc("/api/plugins/browser/settings", jsonHandler(settings))
	mux.HandleFunc("/api/plugins/browser/history", jsonHandler([]any{checkResult}))
	mux.HandleFunc("/api/plugins/browser/diagnostics", jsonHandler(map[string]any{
		"browser": status["browser"], "host": status["host"], "profiles": status["profiles"],
		"sessions": status["sessions"], "hints": []string{"Profile bypass: nothing is listening on 127.0.0.1:1080"},
	}))
	mux.HandleFunc("/api/plugins/browser/screenshot", func(w http.ResponseWriter, r *http.Request) {
		count("screenshot")
		img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
		for x := 0; x < 1024; x++ {
			for y := 0; y < 768; y++ {
				img.Set(x, y, color.RGBA{R: 32, G: 40, B: 52, A: 255})
			}
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_ = jpeg.Encode(w, img, &jpeg.Options{Quality: 60})
	})
	mux.HandleFunc("/api/plugins/browser/navigate", func(w http.ResponseWriter, r *http.Request) {
		count("navigate")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status["page"])
	})
	mux.HandleFunc("/api/plugins/browser/youtube-check", func(w http.ResponseWriter, r *http.Request) {
		count("check")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(checkResult)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">
<link rel="stylesheet" href="/static/style.css">
<script>
window.__errors = [];
window.addEventListener('error', function (e) { window.__errors.push(String(e.message)); });
window.addEventListener('unhandledrejection', function (e) { window.__errors.push('rejection: ' + String(e.reason)); });
localStorage.setItem('token', 'test-token');
window.__toasts = [];
window.App = { navigateTo: function (p) { window.__lastPage = p; }, showToast: function (m, t) { window.__toasts.push(t + ': ' + m); } };
function showToast(m, t) { window.App.showToast(m, t); }
</script></head><body><div class="main-content">%s</div>
<script>document.getElementById('page-plugin-browser').classList.remove('hidden');</script>
</body></html>`, string(sectionHTML))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	logger := log.New(os.Stdout, "[ui] ", log.Ltime)
	p := New()
	if err := p.Init(context.Background(), &plugins.PluginDependencies{Logger: logger}); err != nil {
		t.Fatalf("init: %v", err)
	}
	uiSettings := p.snapshot()
	uiSettings.BrowserPath = bin
	p.mu.Lock()
	p.settings = uiSettings
	p.mu.Unlock()
	p.invalidateBrowserCache()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sess, err := p.manager.acquire(ctx, "direct")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer p.manager.closeAll()

	if _, err := sess.navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	time.Sleep(2 * time.Second) // let the inline plugin script boot and fetch

	read := func(expression string) string {
		value, err := sess.evalString(ctx, expression)
		if err != nil {
			t.Fatalf("evaluate %q: %v", expression, err)
		}
		return value
	}

	if errors := read(`JSON.stringify(window.__errors || [])`); errors != "[]" {
		t.Fatalf("javascript errors on the plugin page: %s", errors)
	}
	if badge := read(`document.getElementById('browser-version-badge').textContent`); !strings.Contains(badge, "120") {
		t.Errorf("version badge not rendered: %q", badge)
	}
	if options := read(`String(document.getElementById('browser-profile').options.length)`); options != "2" {
		t.Errorf("profile options = %s, want 2", options)
	}
	if width := read(`document.getElementById('browser-set-width').value`); width != "1024" {
		t.Errorf("settings not rendered, window width = %q", width)
	}
	if profiles := read(`document.getElementById('browser-set-profiles').value`); !strings.Contains(profiles, "socks5://127.0.0.1:1080") {
		t.Errorf("profiles textarea not rendered: %q", profiles)
	}
	if warning := read(`document.getElementById('browser-warning').style.display`); warning != "block" {
		t.Errorf("proxy warning must be visible when the proxy port is closed, display=%q", warning)
	}
	if history := read(`document.getElementById('browser-history-list').textContent`); !strings.Contains(history, "WORKS") {
		t.Errorf("history not rendered: %q", history)
	}

	// Click "Go" in the address bar and check that the API was called.
	if err := sess.evalJSON(ctx, `(() => {
		document.getElementById('browser-url').value = 'example.com';
		document.getElementById('browser-go-btn').click();
		return true;
	})()`, new(bool)); err != nil {
		t.Fatalf("click Go: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if countsSnapshot()["navigate"] == 0 {
		t.Error("the Go button did not call the navigate endpoint")
	}
	if shots := countsSnapshot()["screenshot"]; shots == 0 {
		t.Error("no screenshot was requested after navigation")
	}

	// Run the check through the UI.
	if err := sess.evalJSON(ctx, `(() => { document.getElementById('browser-check-btn').click(); return true; })()`, new(bool)); err != nil {
		t.Fatalf("click Run check: %v", err)
	}
	time.Sleep(2500 * time.Millisecond)
	if countsSnapshot()["check"] == 0 {
		t.Error("the Run check button did not call the check endpoint")
	}
	if verdict := read(`document.getElementById('browser-check-verdict').textContent`); !strings.Contains(verdict, "WORKS") {
		t.Errorf("verdict badge not rendered: %q", verdict)
	}

	if errors := read(`JSON.stringify(window.__errors || [])`); errors != "[]" {
		t.Fatalf("javascript errors after interactions: %s", errors)
	}

	if shot, err := sess.screenshot(ctx); err == nil {
		_ = os.WriteFile(fmt.Sprintf("%s/podmanview-shot-ui.jpg", os.TempDir()), shot, 0o644)
	}
	t.Logf("api calls: %+v", countsSnapshot())
}

// TestIntegrationPluginsList verifies that the Plugins page blocks enabling a
// plugin whose host dependency is missing.
func TestIntegrationPluginsList(t *testing.T) {
	bin := os.Getenv("BROWSER_BIN")
	if bin == "" {
		t.Skip("BROWSER_BIN not set")
	}

	pluginsJS, err := os.ReadFile("../../../web/static/js/plugins.js")
	if err != nil {
		t.Fatalf("read plugins.js: %v", err)
	}
	styleCSS, err := os.ReadFile("../../../web/static/css/style.css")
	if err != nil {
		t.Fatalf("read style.css: %v", err)
	}

	var (
		mu     sync.Mutex
		toggle int
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/static/style.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write(styleCSS)
	})
	mux.HandleFunc("/static/plugins.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write(pluginsJS)
	})
	mux.HandleFunc("/api/plugins", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"name": "temperature", "description": "Temperature", "version": "2.0.0", "enabled": true, "ready": true},
			{"name": "browser", "description": "Host browser", "version": "1.0.0", "enabled": false, "ready": false,
				"not_ready_reason": "Chromium is not installed on the host: run apt-get install -y chromium"},
		})
	})
	mux.HandleFunc("/api/plugins/browser/toggle", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		toggle++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("/api/plugins/temperature/toggle", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8">
<link rel="stylesheet" href="/static/style.css">
<script>
window.__errors = [];
window.addEventListener('error', function (e) { window.__errors.push(String(e.message)); });
localStorage.setItem('token', 'test-token');
window.App = { showToast: function (m, t) { (window.__toasts = window.__toasts || []).push(t + ': ' + m); }, navigateTo: function () {} };
function showToast(m, t) { window.App.showToast(m, t); }
</script></head><body>
<section id="page-plugins" class="content-page">
  <table><tbody id="plugins-list"></tbody></table>
</section>
<script src="/static/plugins.js"></script>
</body></html>`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	logger := log.New(os.Stdout, "[ui] ", log.Ltime)
	p := New()
	if err := p.Init(context.Background(), &plugins.PluginDependencies{Logger: logger}); err != nil {
		t.Fatalf("init: %v", err)
	}
	settings := p.snapshot()
	settings.BrowserPath = bin
	p.mu.Lock()
	p.settings = settings
	p.mu.Unlock()
	p.invalidateBrowserCache()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sess, err := p.manager.acquire(ctx, "direct")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer p.manager.closeAll()

	if _, err := sess.navigate(ctx, srv.URL); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	read := func(expression string) string {
		value, err := sess.evalString(ctx, expression)
		if err != nil {
			t.Fatalf("evaluate %q: %v", expression, err)
		}
		return value
	}

	if errors := read(`JSON.stringify(window.__errors || [])`); errors != "[]" {
		t.Fatalf("javascript errors on the plugins page: %s", errors)
	}
	if rows := read(`String(document.querySelectorAll('tr[data-plugin]').length)`); rows != "2" {
		t.Fatalf("rendered rows = %s, want 2", rows)
	}
	if disabled := read(`String(document.querySelector('tr[data-plugin="browser"] input.plugin-toggle').disabled)`); disabled != "true" {
		t.Error("the toggle of a plugin with missing dependencies must be disabled")
	}
	if hint := read(`document.querySelector('tr[data-plugin="browser"] td').parentElement.textContent`); !strings.Contains(hint, "Chromium is not installed") {
		t.Errorf("missing dependency hint not rendered: %q", hint)
	}
	if enabledToggle := read(`String(document.querySelector('tr[data-plugin="temperature"] input.plugin-toggle').disabled)`); enabledToggle != "false" {
		t.Error("a ready plugin must stay toggleable")
	}

	// Clicking the disabled toggle must not reach the API.
	_ = sess.evalJSON(ctx, `(() => { document.querySelector('tr[data-plugin="browser"] input.plugin-toggle').click(); return true; })()`, new(bool))
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	calls := toggle
	mu.Unlock()
	if calls != 0 {
		t.Errorf("toggle endpoint was called %d time(s) for a blocked plugin", calls)
	}

	if shot, err := sess.screenshot(ctx); err == nil {
		_ = os.WriteFile(fmt.Sprintf("%s/podmanview-shot-plugins.jpg", os.TempDir()), shot, 0o644)
	}
}
