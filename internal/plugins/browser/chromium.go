package browser

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// chromiumStartupTimeout bounds "spawn process -> DevTools endpoint is up".
	chromiumStartupTimeout = 30 * time.Second
	// chromiumStopTimeout bounds "SIGKILL process group -> wait for exit".
	chromiumStopTimeout = 5 * time.Second
	// devToolsActivePortFile is written by Chromium when --remote-debugging-port=0 is used.
	devToolsActivePortFile = "DevToolsActivePort"
	// stderrTailLimit caps the captured Chromium stderr (used in error messages).
	stderrTailLimit = 8 << 10
)

// errBrowserNotFound means no Chromium binary could be located on the host.
var errBrowserNotFound = errors.New("chromium binary not found")

// launchConfig describes a Chromium instance to start.
type launchConfig struct {
	Bin       string
	Proxy     string // empty for direct network
	Width     int
	Height    int
	ExtraArgs []string
	Headless  string // "new" or "old"
	Logger    *log.Logger
}

// chromiumProcess is a running headless Chromium bound to a temporary profile dir.
type chromiumProcess struct {
	cmd   *exec.Cmd
	dir   string
	wsURL string
	port  string

	stderr   *tailBuffer
	exit     chan error
	gone     atomic.Bool
	stopOnce sync.Once
}

// tailBuffer keeps only the last N bytes of Chromium stderr.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newTailBuffer(capacity int) *tailBuffer {
	return &tailBuffer{cap: capacity}
}

// Write implements io.Writer and keeps the buffer bounded.
func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.cap {
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
	return len(p), nil
}

// String returns the captured tail.
func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.buf))
}

// launchChromium starts headless Chromium and waits until its DevTools endpoint
// is reachable. On failure the process and temp dir are cleaned up.
func launchChromium(ctx context.Context, cfg launchConfig) (*chromiumProcess, error) {
	dir, err := os.MkdirTemp("", "podmanview-browser-")
	if err != nil {
		return nil, fmt.Errorf("create temp profile dir: %w", err)
	}

	stderr := newTailBuffer(stderrTailLimit)
	cmd := exec.Command(cfg.Bin, buildChromiumArgs(cfg, dir)...)
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	configureProcess(cmd)

	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("start %s: %w", cfg.Bin, err)
	}

	proc := &chromiumProcess{
		cmd:    cmd,
		dir:    dir,
		stderr: stderr,
		exit:   make(chan error, 1),
	}
	go func() {
		err := cmd.Wait()
		proc.gone.Store(true)
		proc.exit <- err
	}()

	port, wsPath, err := waitDevToolsPort(ctx, proc)
	if err != nil {
		proc.stop(cfg.Logger)
		return nil, err
	}

	proc.port = port
	proc.wsURL = fmt.Sprintf("ws://127.0.0.1:%s%s", port, wsPath)
	return proc, nil
}

// buildChromiumArgs builds the command line for a diagnostic headless instance.
func buildChromiumArgs(cfg launchConfig, profileDir string) []string {
	headless := "--headless=new"
	if cfg.Headless == "old" {
		headless = "--headless"
	}

	args := []string{
		headless,
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + profileDir,
		fmt.Sprintf("--window-size=%d,%d", cfg.Width, cfg.Height),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-extensions",
		"--disable-sync",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--disable-features=Translate,MediaRouter,OptimizationHints,CalculateNativeWinOcclusion",
		"--metrics-recording-only",
		"--mute-audio",
		"--autoplay-policy=no-user-gesture-required",
		"--lang=en-US",
	}

	// Chromium refuses to run its sandbox as root, which is how the PodmanView
	// service normally runs on the host.
	if runningAsRoot() {
		args = append(args, "--no-sandbox")
	}
	if proxyArg := proxyServerArg(cfg.Proxy); proxyArg != "" {
		args = append(args, proxyArg)
	}
	args = append(args, cfg.ExtraArgs...)
	args = append(args, "about:blank")
	return args
}

// proxyServerArg converts a profile proxy URL into a Chromium flag.
func proxyServerArg(proxy string) string {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return ""
	}
	// Chromium understands socks5:// and http:// directly; socks5h is our alias.
	proxy = strings.Replace(proxy, "socks5h://", "socks5://", 1)
	return "--proxy-server=" + proxy
}

// waitDevToolsPort polls the profile directory for the DevToolsActivePort file.
func waitDevToolsPort(ctx context.Context, proc *chromiumProcess) (string, string, error) {
	portFile := filepath.Join(proc.dir, devToolsActivePortFile)
	deadline := time.Now().Add(chromiumStartupTimeout)

	for {
		if data, err := os.ReadFile(portFile); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 2 {
				port := strings.TrimSpace(lines[0])
				wsPath := strings.TrimSpace(lines[1])
				if port != "" && strings.HasPrefix(wsPath, "/devtools/") {
					return port, wsPath, nil
				}
			}
		}

		select {
		case err := <-proc.exit:
			msg := proc.stderr.String()
			if msg == "" {
				return "", "", fmt.Errorf("chromium exited during startup: %v", err)
			}
			return "", "", fmt.Errorf("chromium exited during startup: %v: %s", err, msg)
		case <-ctx.Done():
			return "", "", ctx.Err()
		default:
		}

		if time.Now().After(deadline) {
			return "", "", fmt.Errorf("chromium did not expose a devtools port within %s: %s",
				chromiumStartupTimeout, proc.stderr.String())
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// stop kills the process group and removes the temporary profile directory.
// It is safe to call more than once.
func (p *chromiumProcess) stop(logger *log.Logger) {
	if p == nil {
		return
	}

	p.stopOnce.Do(func() {
		killProcessTree(p.cmd)
		if !p.gone.Load() {
			select {
			case <-p.exit:
			case <-time.After(chromiumStopTimeout):
				if logger != nil {
					logger.Printf("[browser] chromium pid %d did not exit in %s", p.pid(), chromiumStopTimeout)
				}
			}
		}

		if err := os.RemoveAll(p.dir); err != nil && logger != nil {
			logger.Printf("[browser] failed to remove temp profile dir %s: %v", p.dir, err)
		}
	})
}

// pid returns the process ID (0 when the process never started).
func (p *chromiumProcess) pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// exited reports whether the process has already terminated.
func (p *chromiumProcess) exited() bool { return p.gone.Load() }

// probeBrowserVersion asks the binary for its version and derives the major number.
func probeBrowserVersion(bin string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", 0
	}
	version := strings.TrimSpace(string(out))
	major := parseMajorVersion(version)
	return version, major
}

// parseMajorVersion extracts the leading major version number from
// "Chromium 120.0.6099.224" style strings.
func parseMajorVersion(version string) int {
	for _, field := range strings.Fields(version) {
		field = strings.TrimPrefix(field, "v")
		digits := field
		if idx := strings.IndexAny(digits, ".<"); idx >= 0 {
			digits = digits[:idx]
		}
		if digits == "" {
			continue
		}
		if major, err := strconv.Atoi(digits); err == nil {
			return major
		}
	}
	return 0
}
