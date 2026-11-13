package browser

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"podmanview/internal/plugins"
)

func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "bare host gets https", input: "example.com", want: "https://example.com"},
		{name: "host with path", input: "youtube.com/watch?v=1", want: "https://youtube.com/watch?v=1"},
		{name: "explicit http", input: "http://192.168.1.1/admin", want: "http://192.168.1.1/admin"},
		{name: "trims spaces", input: "  https://example.com/a  ", want: "https://example.com/a"},
		{name: "empty", input: "   ", wantErr: true},
		{name: "javascript", input: "javascript:alert(1)", wantErr: true},
		{name: "file", input: "file:///etc/passwd", wantErr: true},
		{name: "data", input: "data:text/html,<h1>x</h1>", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %q", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseMajorVersion(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{input: "Chromium 120.0.6099.224", want: 120},
		{input: "Google Chrome 103.0.5060.134", want: 103},
		{input: "Chromium 119.0.6045.199 built on Debian 12.2", want: 119},
		{input: "Chromium 108.0.5359.94 snap", want: 108},
		{input: "garbage", want: 0},
		{input: "", want: 0},
	}

	for _, tc := range cases {
		if got := parseMajorVersion(tc.input); got != tc.want {
			t.Errorf("parseMajorVersion(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestHeadlessFlagFor(t *testing.T) {
	if got := headlessFlagFor(120); got != "new" {
		t.Errorf("modern chromium should use new headless mode, got %q", got)
	}
	if got := headlessFlagFor(105); got != "old" {
		t.Errorf("old chromium should use old headless mode, got %q", got)
	}
	if got := headlessFlagFor(0); got != "new" {
		t.Errorf("unknown version should assume new headless mode, got %q", got)
	}
}

func TestProxyHandling(t *testing.T) {
	direct := Profile{ID: "direct", Name: "Direct"}
	if direct.ProxyAddr() != "" {
		t.Errorf("direct profile must have no proxy address")
	}

	socks := Profile{ID: "bypass", Proxy: "socks5://127.0.0.1:1080"}
	if got := socks.ProxyAddr(); got != "127.0.0.1:1080" {
		t.Errorf("ProxyAddr() = %q, want 127.0.0.1:1080", got)
	}
	if got := proxyServerArg(socks.Proxy); got != "--proxy-server=socks5://127.0.0.1:1080" {
		t.Errorf("proxyServerArg() = %q", got)
	}

	noPort := Profile{ID: "p", Proxy: "socks5://127.0.0.1"}
	if got := noPort.ProxyAddr(); got != "127.0.0.1:1080" {
		t.Errorf("default socks port missing: %q", got)
	}

	alias := Profile{ID: "p", Proxy: "socks5h://127.0.0.1:9050"}
	if got := proxyServerArg(alias.Proxy); got != "--proxy-server=socks5://127.0.0.1:9050" {
		t.Errorf("socks5h alias not converted: %q", got)
	}

	if got := proxyServerArg(""); got != "" {
		t.Errorf("direct profile must not add a proxy flag, got %q", got)
	}
}

func TestSettingsNormalizeAndValidate(t *testing.T) {
	settings := DefaultSettings()
	settings.WindowWidth = 10
	settings.WindowHeight = 100000
	settings.ImageQuality = 5
	settings.MaxInstances = 99
	settings.Normalize()

	if settings.WindowWidth != 320 {
		t.Errorf("window width not clamped: %d", settings.WindowWidth)
	}
	if settings.WindowHeight != 1600 {
		t.Errorf("window height not clamped: %d", settings.WindowHeight)
	}
	if settings.ImageQuality != 20 {
		t.Errorf("image quality not clamped: %d", settings.ImageQuality)
	}
	if settings.MaxInstances != 4 {
		t.Errorf("max instances not clamped: %d", settings.MaxInstances)
	}
	if err := settings.Validate(); err != nil {
		t.Fatalf("default settings must be valid: %v", err)
	}

	duplicate := DefaultSettings()
	duplicate.Profiles = append(duplicate.Profiles, Profile{ID: "direct", Name: "again"})
	if err := duplicate.Validate(); err == nil {
		t.Error("duplicate profile ids must be rejected")
	}

	noDirect := DefaultSettings()
	noDirect.Profiles = []Profile{{ID: "bypass", Proxy: "socks5://127.0.0.1:1080"}}
	if err := noDirect.Validate(); err == nil {
		t.Error("missing direct profile must be rejected")
	}

	badScheme := DefaultSettings()
	badScheme.Profiles = append(badScheme.Profiles, Profile{ID: "ftp", Proxy: "ftp://127.0.0.1:21"})
	if err := badScheme.Validate(); err == nil {
		t.Error("unsupported proxy scheme must be rejected")
	}

	badCheckURL := DefaultSettings()
	badCheckURL.CheckURL = "not a url"
	if err := badCheckURL.Validate(); err == nil {
		t.Error("invalid check url must be rejected")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Direct (host network)": "direct-host-network",
		"  BYPASS  ":            "bypass",
		"socks_proxy":           "socks-proxy",
		"!!":                    "",
	}
	for input, want := range cases {
		if got := slugify(input); got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestIsGoogleHostAndBenignErrors(t *testing.T) {
	google := []string{"www.youtube.com", "youtube.com", "rr1---sn-abc.googlevideo.com", "i.ytimg.com", "www.google.com"}
	for _, host := range google {
		if !isGoogleHost(host) {
			t.Errorf("%q must be treated as a Google host", host)
		}
	}
	for _, host := range []string{"example.com", "notyoutube.com", "youtube.com.evil.tld"} {
		if isGoogleHost(host) {
			t.Errorf("%q must not be treated as a Google host", host)
		}
	}

	benign := []string{"net::ERR_ABORTED", "net::ERR_BLOCKED_BY_CLIENT", "cancelled"}
	for _, text := range benign {
		if !isBenignNetError(text) {
			t.Errorf("%q must be treated as benign", text)
		}
	}
	for _, text := range []string{"net::ERR_CONNECTION_RESET", "net::ERR_TIMED_OUT", "net::ERR_PROXY_CONNECTION_FAILED"} {
		if isBenignNetError(text) {
			t.Errorf("%q must not be treated as benign", text)
		}
	}
}

// healthySamples builds a sample sequence where playback keeps up with the wall clock.
func healthySamples(count int) []playbackSample {
	samples := make([]playbackSample, 0, count)
	for i := 0; i < count; i++ {
		samples = append(samples, playbackSample{
			HasVideo:   true,
			T:          float64(i),
			Paused:     false,
			ReadyState: 4,
			Width:      1280,
			Height:     720,
			Buffered:   12,
			Dropped:    2,
			Total:      600,
		})
	}
	return samples
}

func TestComputeMetricsHealthy(t *testing.T) {
	samples := healthySamples(20)
	collector := newNetCollector()
	collector.mediaBytes = 8 * 1024 * 1024

	metrics := computeMetrics(samples, 19, collector, playAttempt{Found: true})
	if metrics.PlayedSec != 19 {
		t.Errorf("played seconds = %v, want 19", metrics.PlayedSec)
	}
	if metrics.PlayedRatio < 0.99 {
		t.Errorf("played ratio = %v, want ~1", metrics.PlayedRatio)
	}
	if metrics.DownloadedMB < 7.9 || metrics.DownloadedMB > 8.1 {
		t.Errorf("downloaded MB = %v, want ~8", metrics.DownloadedMB)
	}
	if metrics.ThroughputKBps < 400 {
		t.Errorf("throughput = %v, want > 400 KB/s", metrics.ThroughputKBps)
	}
	if metrics.DroppedPercent > 1 {
		t.Errorf("dropped percent = %v, want < 1", metrics.DroppedPercent)
	}
}

func TestDecideVerdicts(t *testing.T) {
	t.Run("works", func(t *testing.T) {
		result := &CheckResult{}
		result.Metrics = computeMetrics(healthySamples(20), 19, newNetCollector(), playAttempt{Found: true})
		decide(result, healthySamples(20), pageFacts{}, playAttempt{Found: true})
		if result.Verdict != VerdictWorks {
			t.Fatalf("verdict = %s (%s), want works", result.Verdict, result.Summary)
		}
	})

	t.Run("slow playback", func(t *testing.T) {
		samples := healthySamples(20)
		for i := range samples {
			samples[i].T = float64(i) * 0.3
			samples[i].Buffered = 0.4
		}
		result := &CheckResult{}
		result.Metrics = computeMetrics(samples, 19, newNetCollector(), playAttempt{Found: true})
		decide(result, samples, pageFacts{}, playAttempt{Found: true})
		if result.Verdict != VerdictSlow {
			t.Fatalf("verdict = %s (%s), want slow", result.Verdict, result.Summary)
		}
	})

	t.Run("low resolution is slow", func(t *testing.T) {
		samples := healthySamples(20)
		for i := range samples {
			samples[i].Width, samples[i].Height = 256, 144
		}
		result := &CheckResult{}
		result.Metrics = computeMetrics(samples, 19, newNetCollector(), playAttempt{Found: true})
		decide(result, samples, pageFacts{}, playAttempt{Found: true})
		if result.Verdict != VerdictSlow {
			t.Fatalf("verdict = %s (%s), want slow", result.Verdict, result.Summary)
		}
	})

	t.Run("blocked by network without video", func(t *testing.T) {
		result := &CheckResult{NetworkFailures: []networkFailure{{Host: "rr1.googlevideo.com", Error: "net::ERR_CONNECTION_RESET", Count: 4}}}
		result.Metrics = computeMetrics(nil, 19, newNetCollector(), playAttempt{Found: false})
		decide(result, nil, pageFacts{}, playAttempt{Found: false})
		if result.Verdict != VerdictBlocked {
			t.Fatalf("verdict = %s (%s), want blocked", result.Verdict, result.Summary)
		}
		if len(result.Reasons) == 0 {
			t.Fatal("blocked verdict must carry reasons")
		}
	})

	t.Run("blocked by bot page", func(t *testing.T) {
		result := &CheckResult{}
		result.Metrics = computeMetrics(nil, 19, newNetCollector(), playAttempt{Found: false})
		decide(result, nil, pageFacts{Title: "YouTube", Text: "Sign in to confirm you're not a bot"}, playAttempt{Found: false})
		if result.Verdict != VerdictBlocked {
			t.Fatalf("verdict = %s (%s), want blocked", result.Verdict, result.Summary)
		}
	})

	t.Run("media error", func(t *testing.T) {
		samples := healthySamples(10)
		for i := range samples {
			samples[i].T = 0
			samples[i].Paused = true
			samples[i].Err = &mediaError{Code: 3, Message: "decode error"}
		}
		result := &CheckResult{}
		result.Metrics = computeMetrics(samples, 19, newNetCollector(), playAttempt{Found: true})
		decide(result, samples, pageFacts{}, playAttempt{Found: true})
		if result.Verdict != VerdictBlocked {
			t.Fatalf("verdict = %s (%s), want blocked", result.Verdict, result.Summary)
		}
	})

	t.Run("paused player is an error", func(t *testing.T) {
		samples := healthySamples(10)
		for i := range samples {
			samples[i].T = 0
			samples[i].Paused = true
		}
		result := &CheckResult{}
		result.Metrics = computeMetrics(samples, 19, newNetCollector(), playAttempt{Found: true})
		decide(result, samples, pageFacts{}, playAttempt{Found: true})
		if result.Verdict != VerdictError {
			t.Fatalf("verdict = %s (%s), want error", result.Verdict, result.Summary)
		}
	})
}

func TestDecideNavigationFailure(t *testing.T) {
	blocked := &CheckResult{}
	decideNavigationFailure(blocked, errors.New("navigation failed: net::ERR_CONNECTION_RESET"))
	if blocked.Verdict != VerdictBlocked {
		t.Fatalf("network navigation failure must be blocked, got %s", blocked.Verdict)
	}

	pluginErr := &CheckResult{}
	decideNavigationFailure(pluginErr, errors.New("devtools error: something internal"))
	if pluginErr.Verdict != VerdictError {
		t.Fatalf("internal navigation failure must be an error, got %s", pluginErr.Verdict)
	}
}

func TestBuildReport(t *testing.T) {
	samples := healthySamples(20)
	collector := newNetCollector()
	collector.mediaBytes = 5 * 1024 * 1024
	result := &CheckResult{
		StartedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ProfileName: "Direct",
		URL:         "https://www.youtube.com/watch?v=x",
		Metrics:     computeMetrics(samples, 19, collector, playAttempt{Found: true}),
		Reasons:     []string{"played 19.0s of 19.0s"},
		NetworkFailures: []networkFailure{
			{Host: "rr1.googlevideo.com", Error: "net::ERR_CONNECTION_RESET", Count: 3},
		},
		ConsoleErrors: []string{"log: boom"},
	}
	result.Verdict = VerdictWorks
	result.Summary = "Playback keeps up with the wall clock"
	report := buildReport(result)

	for _, want := range []string{
		"Browser check — 2026-01-02 03:04:05 UTC",
		"Verdict: WORKS",
		"Played: 19.0 s",
		"rr1.googlevideo.com: net::ERR_CONNECTION_RESET x3",
		"log: boom",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
}

func TestKeySpecsAreComplete(t *testing.T) {
	for _, name := range []string{"Enter", "Tab", "Escape", "Backspace", "ArrowDown", "ArrowUp", "PageDown", "PageUp", "Home", "End"} {
		if _, ok := keySpecs[name]; !ok {
			t.Errorf("missing key spec for %s", name)
		}
	}
	for name, spec := range keySpecs {
		if spec.Key == "" || spec.Code == "" || spec.VK == 0 {
			t.Errorf("incomplete key spec for %s: %+v", name, spec)
		}
	}
}

func TestChromiumArgsSafety(t *testing.T) {
	args := buildChromiumArgs(launchConfig{Bin: "chromium", Width: 1024, Height: 768, Headless: "new"}, "/tmp/profile")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--headless=new",
		"--remote-debugging-port=0",
		"--user-data-dir=/tmp/profile",
		"--window-size=1024,768",
		"--disable-dev-shm-usage",
		"--mute-audio",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("chromium args are missing %q: %v", want, args)
		}
	}

	if strings.Contains(joined, "--proxy-server") {
		t.Errorf("direct launch must not pass a proxy: %v", args)
	}

	proxied := strings.Join(buildChromiumArgs(launchConfig{Proxy: "socks5://127.0.0.1:1080", Width: 800, Height: 600}, "/tmp/p"), " ")
	if !strings.Contains(proxied, "--proxy-server=socks5://127.0.0.1:1080") {
		t.Errorf("proxy flag missing: %s", proxied)
	}

	old := strings.Join(buildChromiumArgs(launchConfig{Headless: "old", Width: 800, Height: 600}, "/tmp/p"), " ")
	if !strings.Contains(old, "--headless ") {
		t.Errorf("old headless flag missing: %s", old)
	}
}

func TestCanEnableRequiresBrowser(t *testing.T) {
	p := New()
	if err := p.Init(context.Background(), &plugins.PluginDependencies{}); err != nil {
		t.Fatalf("init: %v", err)
	}

	// A path that does not exist must block enabling with an actionable message.
	broken := p.snapshot()
	broken.BrowserPath = "/definitely/not/here/chromium"
	p.mu.Lock()
	p.settings = broken
	p.mu.Unlock()
	p.invalidateBrowserCache()

	err := p.CanEnable()
	if err == nil {
		t.Fatal("enabling must be refused when no browser is available")
	}
	if !strings.Contains(err.Error(), "apt-get install") {
		t.Errorf("reason must explain the fix, got %q", err.Error())
	}

	// Any executable stands in for a real browser here: detection only requires
	// an executable file (the version probe is allowed to fail).
	self, lookErr := os.Executable()
	if lookErr != nil {
		t.Skipf("cannot resolve own executable: %v", lookErr)
	}
	ok := p.snapshot()
	ok.BrowserPath = self
	p.mu.Lock()
	p.settings = ok
	p.mu.Unlock()
	p.invalidateBrowserCache()

	if err := p.CanEnable(); err != nil {
		t.Fatalf("enabling must be allowed for an existing executable: %v", err)
	}
	if info := p.browser(); !info.Found || info.Source != "settings" {
		t.Errorf("detection must use the configured path: %+v", info)
	}
}
