package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"podmanview/internal/plugins"
)

// newTestPlugin builds a plugin without storage (defaults only).
func newTestPlugin(t *testing.T) *BrowserPlugin {
	t.Helper()
	p := New()
	if err := p.Init(context.Background(), &plugins.PluginDependencies{}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return p
}

func TestHandleStatus(t *testing.T) {
	p := newTestPlugin(t)

	recorder := httptest.NewRecorder()
	p.handleStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/plugins/browser/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var status statusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.Settings == nil {
		t.Fatal("settings missing in status response")
	}
	if len(status.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(status.Profiles))
	}
	if status.Profiles[0].ID != "direct" {
		t.Errorf("first profile = %q, want direct", status.Profiles[0].ID)
	}
	if status.Page != nil {
		t.Error("no page must be reported while no browser is running")
	}
}

func TestHandleStatusUnknownProfile(t *testing.T) {
	p := newTestPlugin(t)
	recorder := httptest.NewRecorder()
	p.handleStatus(recorder, httptest.NewRequest(http.MethodGet, "/api/plugins/browser/status?profile=nope", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400", recorder.Code)
	}
}

func TestHandleScreenshotWithoutSession(t *testing.T) {
	p := newTestPlugin(t)
	recorder := httptest.NewRecorder()
	p.handleScreenshot(recorder, httptest.NewRequest(http.MethodGet, "/api/plugins/browser/screenshot?profile=direct", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status code = %d, want 409", recorder.Code)
	}
	var payload map[string]string
	_ = json.Unmarshal(recorder.Body.Bytes(), &payload)
	if payload["code"] != "browser_not_running" {
		t.Errorf("error code = %q, want browser_not_running", payload["code"])
	}
}

func TestHandleUpdateSettingsValidation(t *testing.T) {
	p := newTestPlugin(t)

	body := `{"profiles":[{"id":"direct","name":"Direct","proxy":""},{"id":"bad","name":"Bad","proxy":"ftp://127.0.0.1:21"}]}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/plugins/browser/settings", strings.NewReader(body))
	p.handleUpdateSettings(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status code = %d, want 400, body = %s", recorder.Code, recorder.Body.String())
	}

	body = `{"windowWidth":640,"windowHeight":480,"profiles":[{"id":"direct","name":"Direct","proxy":""},{"id":"bypass","name":"Proxy","proxy":"socks5://127.0.0.1:1080"}]}`
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/plugins/browser/settings", strings.NewReader(body))
	p.handleUpdateSettings(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200, body = %s", recorder.Code, recorder.Body.String())
	}

	settings := p.snapshot()
	if settings.WindowWidth != 640 || settings.WindowHeight != 480 {
		t.Errorf("settings not applied: %dx%d", settings.WindowWidth, settings.WindowHeight)
	}
	if len(settings.Profiles) != 2 || settings.Profiles[1].Proxy != "socks5://127.0.0.1:1080" {
		t.Errorf("profiles not applied: %+v", settings.Profiles)
	}
}

func TestHandleHistoryEmpty(t *testing.T) {
	p := newTestPlugin(t)
	recorder := httptest.NewRecorder()
	p.handleHistory(recorder, httptest.NewRequest(http.MethodGet, "/api/plugins/browser/history", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d", recorder.Code)
	}
	var history []CheckResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("history must be empty, got %d entries", len(history))
	}
}
