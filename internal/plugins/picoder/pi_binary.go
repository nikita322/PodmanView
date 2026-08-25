package picoder

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ResolvePiBinary locates the pi executable.
//
// Resolution order:
//  1. Explicit path from configuration (PODMANVIEW_PI_BIN)
//  2. pi on the current process PATH
//  3. Common npm / user-local install locations
func ResolvePiBinary(explicit string) (string, error) {
	if explicit != "" {
		if resolved, err := exec.LookPath(explicit); err == nil {
			return resolved, nil
		}
		if fileExists(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("pi executable not found at configured path %q (check PODMANVIEW_PI_BIN)", explicit)
	}

	if resolved, err := exec.LookPath(piCmd); err == nil {
		return resolved, nil
	}

	for _, candidate := range candidatePiPaths() {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("pi executable not found in $PATH; install @earendil-works/pi-coding-agent or set PODMANVIEW_PI_BIN to the full path")
}

// candidatePiPaths returns common install locations to check when pi is not on PATH.
func candidatePiPaths() []string {
	var paths []string
	add := func(p string) {
		if p != "" {
			paths = append(paths, p)
		}
	}

	home, _ := os.UserHomeDir()
	configDir, _ := os.UserConfigDir()

	if runtime.GOOS == "windows" {
		if configDir != "" {
			// npm global installs on Windows live in %APPDATA%\npm
			add(filepath.Join(configDir, "npm", "pi.cmd"))
			add(filepath.Join(configDir, "npm", "pi.exe"))
			add(filepath.Join(configDir, "npm", "pi.ps1"))
			add(filepath.Join(configDir, "npm", "pi"))
		}
		if home != "" {
			add(filepath.Join(home, ".local", "bin", "pi.cmd"))
			add(filepath.Join(home, ".local", "bin", "pi.exe"))
			add(filepath.Join(home, ".bun", "bin", "pi.cmd"))
			add(filepath.Join(home, ".bun", "bin", "pi.exe"))
		}
	} else {
		if home != "" {
			add(filepath.Join(home, ".local", "bin", "pi"))
			add(filepath.Join(home, ".bun", "bin", "pi"))
			add(filepath.Join(home, ".npm-global", "bin", "pi"))
		}
		add("/usr/local/bin/pi")
		add("/opt/homebrew/bin/pi")
		add("/usr/bin/pi")
	}

	return paths
}

// detectPiVersion returns the pi version string, or "" if it can't be determined.
func detectPiVersion(binary string) string {
	if binary == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// fileExists reports whether path exists and is not a directory.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
