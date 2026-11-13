//go:build !unix

package browser

import "os/exec"

// configureProcess is a no-op outside Unix (development builds only; the plugin
// is meant to run on the Linux host).
func configureProcess(cmd *exec.Cmd) {}

// killProcessTree kills the Chromium process (no process groups outside Unix).
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}

// runningAsRoot always reports false outside Unix.
func runningAsRoot() bool { return false }

// terminateOrphans is a no-op outside Unix (no /proc scan available).
func terminateOrphans() int { return 0 }
