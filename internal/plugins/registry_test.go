package plugins

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// fakePlugin is a minimal Plugin used to exercise the registry.
type fakePlugin struct {
	name        string
	guardReason string
	started     bool
	stopped     bool
}

func (f *fakePlugin) Name() string                                    { return f.name }
func (f *fakePlugin) Description() string                             { return "fake plugin" }
func (f *fakePlugin) Version() string                                 { return "1.0.0" }
func (f *fakePlugin) Init(context.Context, *PluginDependencies) error { return nil }
func (f *fakePlugin) Start(context.Context) error                     { f.started = true; return nil }
func (f *fakePlugin) Stop(context.Context) error                      { f.stopped = true; return nil }
func (f *fakePlugin) Routes() []Route                                 { return nil }
func (f *fakePlugin) IsEnabled() bool                                 { return false }
func (f *fakePlugin) GetHTML() (string, error)                        { return "", nil }

// guardedPlugin implements EnableGuard and refuses to be enabled while the
// configured reason is set.
type guardedPlugin struct {
	fakePlugin
}

func (g *guardedPlugin) CanEnable() error {
	if g.guardReason == "" {
		return nil
	}
	return errors.New(g.guardReason)
}

func TestEnablePluginRespectsEnableGuard(t *testing.T) {
	registry := NewRegistry()
	plugin := &guardedPlugin{fakePlugin: fakePlugin{name: "guarded", guardReason: "chromium is missing"}}
	if err := registry.Register(plugin); err != nil {
		t.Fatalf("register: %v", err)
	}

	err := registry.EnablePlugin(context.Background(), "guarded")
	if err == nil {
		t.Fatal("expected the plugin to refuse enabling")
	}

	var notReady *PluginNotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("error must be a PluginNotReadyError, got %T: %v", err, err)
	}
	if !strings.Contains(notReady.Reason, "chromium is missing") {
		t.Errorf("reason = %q", notReady.Reason)
	}
	if plugin.started {
		t.Error("a plugin with unmet prerequisites must not be started")
	}
	if err := registry.CanEnable("guarded"); err == nil {
		t.Error("CanEnable must report the missing prerequisite")
	}
}

func TestEnablePluginRunsWhenPrerequisitesAreMet(t *testing.T) {
	registry := NewRegistry()
	plugin := &guardedPlugin{fakePlugin: fakePlugin{name: "guarded"}}
	if err := registry.Register(plugin); err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := registry.CanEnable("guarded"); err != nil {
		t.Fatalf("CanEnable: %v", err)
	}
	if err := registry.EnablePlugin(context.Background(), "guarded"); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}
	if !plugin.started {
		t.Error("plugin was not started")
	}
}

func TestEnablePluginWithoutGuard(t *testing.T) {
	registry := NewRegistry()
	plugin := &fakePlugin{name: "plain"}
	if err := registry.Register(plugin); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.CanEnable("plain"); err != nil {
		t.Fatalf("CanEnable: %v", err)
	}
	if err := registry.EnablePlugin(context.Background(), "plain"); err != nil {
		t.Fatalf("EnablePlugin: %v", err)
	}
	if !plugin.started {
		t.Error("plugin was not started")
	}
	if err := registry.DisablePlugin(context.Background(), "plain"); err != nil {
		t.Fatalf("DisablePlugin: %v", err)
	}
	if !plugin.stopped {
		t.Error("plugin was not stopped")
	}
}

func TestCanEnableUnknownPlugin(t *testing.T) {
	registry := NewRegistry()
	if err := registry.CanEnable("nope"); err == nil {
		t.Fatal("unknown plugin must produce an error")
	}
}

// Ensure the guard interface stays optional: a plain Plugin must satisfy Plugin.
var _ Plugin = (*fakePlugin)(nil)
var _ EnableGuard = (*guardedPlugin)(nil)
var _ http.Handler = http.NotFoundHandler()
