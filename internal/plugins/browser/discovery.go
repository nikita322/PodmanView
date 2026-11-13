package browser

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// browserCandidates are the binary names and paths checked during auto-detection.
// The first existing entry wins. Debian/Ubuntu arm64 packages install "chromium".
var browserCandidates = []string{
	"chromium",
	"chromium-browser",
	"chromium-headless-shell",
	"google-chrome",
	"google-chrome-stable",
	"headless_shell",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/usr/lib/chromium/chromium",
	"/snap/bin/chromium",
	"/opt/google/chrome/chrome",
}

// browserInfo describes the browser available on the host.
type browserInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
	Major   int    `json:"major"`
	Source  string `json:"source"` // "settings", "PATH" or "known-path"
	Error   string `json:"error,omitempty"`
}

// hostInfo is the host state relevant for this plugin.
type hostInfo struct {
	Arch            string `json:"arch"`
	MemTotalMB      uint64 `json:"memTotalMb"`
	MemAvailableMB  uint64 `json:"memAvailableMb"`
	FontFamilies    int    `json:"fontFamilies"`
	FontCommandOK   bool   `json:"fontCommandOk"`
	BrowserProcs    int    `json:"browserProcesses"`
	TempProfileDirs int    `json:"tempProfileDirs"`
}

// detectBrowser finds a usable Chromium binary, honouring the configured path.
func detectBrowser(configured string) browserInfo {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		info := browserInfo{Path: configured, Source: "settings"}
		if err := checkExecutable(configured); err != nil {
			info.Error = err.Error()
			return info
		}
		info.Found = true
		info.Version, info.Major = probeBrowserVersion(configured)
		return info
	}

	for _, candidate := range browserCandidates {
		path := candidate
		source := "PATH"
		if strings.Contains(candidate, "/") {
			source = "known-path"
		} else {
			resolved, err := exec.LookPath(candidate)
			if err != nil {
				continue
			}
			path = resolved
		}
		if err := checkExecutable(path); err != nil {
			continue
		}
		version, major := probeBrowserVersion(path)
		return browserInfo{Found: true, Path: path, Version: version, Major: major, Source: source}
	}

	return browserInfo{Error: "chromium binary not found in PATH or known locations"}
}

// checkExecutable verifies that path is an executable file.
func checkExecutable(path string) error {
	stat, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if stat.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if runtime.GOOS != "windows" && stat.Mode()&0o111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}

// headlessFlagFor returns the headless command line flag supported by the binary.
func headlessFlagFor(major int) string {
	// "--headless=new" exists since Chromium 109/112; older builds only know "--headless".
	if major != 0 && major < 112 {
		return "old"
	}
	return "new"
}

// reachabilityCache memoizes "is the local proxy port open" checks, so that
// status polling does not dial (and do not stall on) a dead proxy every time.
var reachabilityCache struct {
	sync.Mutex
	entries map[string]reachabilityEntry
}

type reachabilityEntry struct {
	reachable bool
	checked   time.Time
}

// reachabilityTTL is how long a proxy reachability result is reused.
const reachabilityTTL = 10 * time.Second

// proxyReachable reports whether something listens on the proxy address locally.
func proxyReachable(profile Profile) *bool {
	addr := profile.ProxyAddr()
	if addr == "" {
		return nil
	}

	reachabilityCache.Lock()
	defer reachabilityCache.Unlock()
	if reachabilityCache.entries == nil {
		reachabilityCache.entries = make(map[string]reachabilityEntry)
	}
	if entry, ok := reachabilityCache.entries[addr]; ok && time.Since(entry.checked) < reachabilityTTL {
		value := entry.reachable
		return &value
	}

	conn, err := net.DialTimeout("tcp", addr, 700*time.Millisecond)
	reachable := err == nil
	if reachable {
		_ = conn.Close()
	}
	reachabilityCache.entries[addr] = reachabilityEntry{reachable: reachable, checked: time.Now()}
	return &reachable
}

// hostInfoCache memoizes the (relatively expensive) /proc scan.
var hostInfoCache struct {
	sync.Mutex
	value   hostInfo
	checked time.Time
}

// hostInfoTTL is how long host facts are reused by the status endpoint.
const hostInfoTTL = 10 * time.Second

// readHostInfo collects light-weight host facts used by the diagnostics panel.
func readHostInfo() hostInfo {
	hostInfoCache.Lock()
	defer hostInfoCache.Unlock()

	if !hostInfoCache.checked.IsZero() && time.Since(hostInfoCache.checked) < hostInfoTTL {
		return hostInfoCache.value
	}

	info := hostInfo{Arch: runtime.GOARCH}
	info.MemTotalMB, info.MemAvailableMB = readMemInfo()
	info.FontFamilies, info.FontCommandOK = countFontFamilies()
	info.BrowserProcs = countBrowserProcesses()
	info.TempProfileDirs = countTempProfileDirs()

	hostInfoCache.value = info
	hostInfoCache.checked = time.Now()
	return info
}

// readMemInfo reads memory totals from /proc/meminfo (Linux only, zeros elsewhere).
func readMemInfo() (totalMB, availableMB uint64) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			totalMB = value / 1024
		case "MemAvailable:":
			availableMB = value / 1024
		}
	}
	return totalMB, availableMB
}

// fontCache memoizes the font family count (fc-list is slow on weak hosts).
var fontCache struct {
	sync.Mutex
	count    int
	ok       bool
	checked  time.Time
	lifetime time.Duration
}

// countFontFamilies counts installed font families via fc-list.
// Without fonts Chromium renders boxes instead of text.
func countFontFamilies() (int, bool) {
	fontCache.Lock()
	defer fontCache.Unlock()

	if fontCache.lifetime == 0 {
		fontCache.lifetime = 5 * time.Minute
	}
	if !fontCache.checked.IsZero() && time.Since(fontCache.checked) < fontCache.lifetime {
		return fontCache.count, fontCache.ok
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "fc-list", "--format", "%{family}\n").Output()
	fontCache.checked = time.Now()
	if err != nil {
		fontCache.count = 0
		fontCache.ok = false
		return 0, false
	}

	families := make(map[string]struct{})
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// fc-list returns comma separated aliases per font file.
		for _, family := range strings.Split(line, ",") {
			if family = strings.TrimSpace(family); family != "" {
				families[family] = struct{}{}
			}
		}
	}
	fontCache.count = len(families)
	fontCache.ok = true
	return fontCache.count, true
}

// browserProfileMarker identifies Chromium processes started by this plugin
// (they all use a temporary "podmanview-browser-*" profile directory).
const browserProfileMarker = "podmanview-browser-"

// listBrowserPIDs returns PIDs of Chromium processes started by this plugin.
func listBrowserPIDs() []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	pids := make([]int, 0, 4)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile("/proc/" + entry.Name() + "/cmdline")
		if err != nil {
			continue
		}
		if strings.Contains(string(data), browserProfileMarker) {
			pids = append(pids, pid)
		}
	}
	return pids
}

// countBrowserProcesses counts Chromium processes owned by this plugin.
func countBrowserProcesses() int { return len(listBrowserPIDs()) }

// countTempProfileDirs counts leftover temporary profile directories.
func countTempProfileDirs() int {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), browserProfileMarker) {
			count++
		}
	}
	return count
}
