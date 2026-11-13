// Package browser drives a headless Chromium on the PodmanView host and shows
// rendered pages in the PodmanView UI as screenshots (no iframes, no HTML
// proxying). It is a diagnostic tool: it answers "what does the host network
// actually see", including a YouTube playback check that distinguishes
// "works", "slow", "blocked" and "error".
package browser

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"podmanview/internal/storage"
)

const (
	// settingsKey is the storage key holding the JSON-encoded settings.
	settingsKey = "settings"
	// historyKey is the storage key holding the JSON-encoded check history.
	historyKey = "history"
	// historyLimit caps the number of stored check results.
	historyLimit = 20
)

// Profile is a named way to reach the internet from the host.
// An empty Proxy means "use the host network directly", otherwise the value is
// a proxy URL accepted by Chromium (socks5://host:port or http://host:port).
type Profile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Proxy string `json:"proxy"`
}

// IsDirect reports whether the profile uses the host network directly.
func (p Profile) IsDirect() bool { return strings.TrimSpace(p.Proxy) == "" }

// ProxyAddr returns the host:port of the proxy, or an empty string for direct profiles.
func (p Profile) ProxyAddr() string {
	if p.IsDirect() {
		return ""
	}
	u, err := url.Parse(p.Proxy)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Host
	if u.Port() == "" {
		switch u.Scheme {
		case "socks5", "socks5h", "socks4":
			host += ":1080"
		default:
			host += ":8080"
		}
	}
	return host
}

// Settings holds everything the plugin persists in the plugin storage.
type Settings struct {
	// BrowserPath overrides Chromium auto-detection. Empty means auto-detect.
	BrowserPath string `json:"browserPath"`
	// Profiles are the selectable network profiles (at least one direct profile is enforced).
	Profiles []Profile `json:"profiles"`

	// WindowWidth/WindowHeight define the viewport (and therefore the screenshot geometry).
	WindowWidth  int `json:"windowWidth"`
	WindowHeight int `json:"windowHeight"`
	// ImageQuality is the JPEG quality (20-95) for screenshots.
	ImageQuality int `json:"imageQuality"`
	// LiveIntervalMs is the default refresh interval of the "live" mode.
	LiveIntervalMs int `json:"liveIntervalMs"`

	// NavigateTimeoutSec limits a navigation + page load.
	NavigateTimeoutSec int `json:"navigateTimeoutSec"`
	// ActionTimeoutSec limits a single browser action (click, screenshot, evaluate).
	ActionTimeoutSec int `json:"actionTimeoutSec"`
	// CheckDurationSec is how long the YouTube check measures playback.
	CheckDurationSec int `json:"checkDurationSec"`
	// CheckURL is the page opened by the YouTube check.
	CheckURL string `json:"checkUrl"`
	// IdleStopSec stops a Chromium instance that has not been used for that long.
	IdleStopSec int `json:"idleStopSec"`
	// MaxInstances caps the number of simultaneously running Chromium instances.
	MaxInstances int `json:"maxInstances"`
	// ExtraArgs are extra Chromium command line flags.
	ExtraArgs []string `json:"extraArgs"`
}

// DefaultSettings returns the settings used on first start.
func DefaultSettings() *Settings {
	return &Settings{
		BrowserPath:        "",
		Profiles:           defaultProfiles(),
		WindowWidth:        1024,
		WindowHeight:       768,
		ImageQuality:       70,
		LiveIntervalMs:     2000,
		NavigateTimeoutSec: 25,
		ActionTimeoutSec:   10,
		CheckDurationSec:   20,
		CheckURL:           "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		IdleStopSec:        120,
		MaxInstances:       2,
		ExtraArgs:          []string{},
	}
}

// defaultProfiles returns the built-in profiles: host network and the usual
// local SOCKS port used by DPI-bypass tools.
func defaultProfiles() []Profile {
	return []Profile{
		{ID: "direct", Name: "Direct (host network)", Proxy: ""},
		{ID: "bypass", Name: "Local SOCKS proxy (byedpi)", Proxy: "socks5://127.0.0.1:1080"},
	}
}

// Profile returns the profile with the given ID.
func (s *Settings) Profile(id string) (Profile, bool) {
	for _, p := range s.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// DefaultProfileID returns the ID of the first profile (used when the UI sends no profile).
func (s *Settings) DefaultProfileID() string {
	if len(s.Profiles) == 0 {
		return ""
	}
	return s.Profiles[0].ID
}

// Clone returns a deep copy safe to use outside the plugin lock.
func (s *Settings) Clone() *Settings {
	cp := *s
	cp.Profiles = make([]Profile, len(s.Profiles))
	copy(cp.Profiles, s.Profiles)
	cp.ExtraArgs = make([]string, len(s.ExtraArgs))
	copy(cp.ExtraArgs, s.ExtraArgs)
	return &cp
}

// Normalize clamps out-of-range values and fills empty ones with defaults.
func (s *Settings) Normalize() {
	def := DefaultSettings()

	s.BrowserPath = strings.TrimSpace(s.BrowserPath)
	s.CheckURL = strings.TrimSpace(s.CheckURL)
	if s.CheckURL == "" {
		s.CheckURL = def.CheckURL
	}

	s.WindowWidth = clampInt(s.WindowWidth, 320, 1920, def.WindowWidth)
	s.WindowHeight = clampInt(s.WindowHeight, 240, 1600, def.WindowHeight)
	s.ImageQuality = clampInt(s.ImageQuality, 20, 95, def.ImageQuality)
	s.LiveIntervalMs = clampInt(s.LiveIntervalMs, 500, 10000, def.LiveIntervalMs)

	s.NavigateTimeoutSec = clampInt(s.NavigateTimeoutSec, 5, 120, def.NavigateTimeoutSec)
	s.ActionTimeoutSec = clampInt(s.ActionTimeoutSec, 2, 60, def.ActionTimeoutSec)
	s.CheckDurationSec = clampInt(s.CheckDurationSec, 5, 120, def.CheckDurationSec)
	s.IdleStopSec = clampInt(s.IdleStopSec, 30, 3600, def.IdleStopSec)
	s.MaxInstances = clampInt(s.MaxInstances, 1, 4, def.MaxInstances)

	if len(s.Profiles) == 0 {
		s.Profiles = def.Profiles
	}
	profiles := make([]Profile, 0, len(s.Profiles))
	for _, p := range s.Profiles {
		p.ID = slugify(p.ID)
		p.Name = strings.TrimSpace(p.Name)
		p.Proxy = strings.TrimSpace(p.Proxy)
		if p.ID == "" {
			continue
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		profiles = append(profiles, p)
	}
	if len(profiles) == 0 {
		profiles = def.Profiles
	}
	s.Profiles = profiles

	args := make([]string, 0, len(s.ExtraArgs))
	for _, a := range s.ExtraArgs {
		if a = strings.TrimSpace(a); a != "" {
			args = append(args, a)
		}
	}
	s.ExtraArgs = args
}

// Validate checks settings that cannot be silently normalized.
func (s *Settings) Validate() error {
	seen := make(map[string]bool, len(s.Profiles))
	for _, p := range s.Profiles {
		if p.ID == "" {
			return fmt.Errorf("profile id cannot be empty")
		}
		if seen[p.ID] {
			return fmt.Errorf("duplicate profile id %q", p.ID)
		}
		seen[p.ID] = true
		if p.IsDirect() {
			continue
		}
		u, err := url.Parse(p.Proxy)
		if err != nil {
			return fmt.Errorf("profile %q: invalid proxy url: %w", p.ID, err)
		}
		switch u.Scheme {
		case "socks5", "socks5h", "socks4", "http", "https":
		default:
			return fmt.Errorf("profile %q: unsupported proxy scheme %q (use socks5:// or http://)", p.ID, u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("profile %q: proxy url has no host", p.ID)
		}
	}
	if _, ok := s.Profile("direct"); !ok {
		// A direct profile is not mandatory, but the UI and the check rely on a
		// predictable "no proxy" baseline, so it must exist.
		return fmt.Errorf("a profile with id \"direct\" is required")
	}
	if u, err := url.Parse(s.CheckURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("check url must be an absolute http(s) url")
	}
	return nil
}

// navigateTimeout returns the navigation timeout.
func (s *Settings) navigateTimeout() time.Duration {
	return time.Duration(s.NavigateTimeoutSec) * time.Second
}

// actionTimeout returns the per-action timeout.
func (s *Settings) actionTimeout() time.Duration {
	return time.Duration(s.ActionTimeoutSec) * time.Second
}

// loadSettings reads settings from the plugin storage, falling back to defaults.
func loadSettings(st storage.Storage, pluginName string) *Settings {
	settings := DefaultSettings()
	if st != nil {
		loaded := DefaultSettings()
		if err := st.GetJSON(pluginName, settingsKey, loaded); err == nil {
			loaded.Normalize()
			if err := loaded.Validate(); err == nil {
				return loaded
			}
		}
	}
	settings.Normalize()
	return settings
}

// saveSettings persists settings in the plugin storage.
func saveSettings(st storage.Storage, pluginName string, settings *Settings) error {
	if st == nil {
		return nil
	}
	return st.SetJSON(pluginName, settingsKey, settings)
}

func clampInt(value, min, max, def int) int {
	if value == 0 {
		return def
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// slugify makes a lowercase identifier safe for use as a profile ID.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// normalizeURL turns user input into an absolute http(s) URL.
// Only http/https are allowed: the browser must never open file:// or javascript:.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url is empty")
	}
	candidate := raw
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	u, err := url.Parse(candidate)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("only http and https urls are allowed, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("url has no host")
	}
	return u.String(), nil
}

// intToString is a tiny helper used by the copy-report builder.
func intToString(v int) string { return strconv.Itoa(v) }
