//go:build unix

package browser

import (
	"os"
	"os/exec"
	"syscall"
)

// configureProcess starts Chromium in its own process group so that the whole
// renderer/zygote tree can be killed at once.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree kills the Chromium process group (best effort).
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// Negative pid targets the whole process group created by Setpgid.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

// runningAsRoot reports whether the service runs as root (Chromium then needs
// --no-sandbox).
func runningAsRoot() bool {
	return os.Geteuid() == 0
}

// terminateOrphans kills Chromium processes left over from a previous run of the
// plugin or from a hard kill of the service. Only processes started by this
// plugin (their profile directory contains the marker) are touched.
func terminateOrphans() int {
	pids := listBrowserPIDs()
	killed := 0
	for _, pid := range pids {
		if pid == os.Getpid() {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	return killed
}
