package podman

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RebuildResult describes the outcome of a container rebuild
type RebuildResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Image        string `json:"image"`
	WasRunning   bool   `json:"wasRunning"`
	Built        bool   `json:"built"`
	BuildContext string `json:"buildContext,omitempty"`
	Warning      string `json:"warning,omitempty"`
}

// RebuildContainer recreates a container from its image, keeping its full
// configuration (mounts, ports, env, restart policy, networks, devices, ...).
//
// Rebuilding the create spec by hand from inspect output would silently drop
// config we do not model (devices, capabilities, labels, security options), so
// the recreation is delegated to `podman container clone`, which copies the
// whole container config. Steps:
//  1. clone the container under a temporary name
//  2. remove the original (frees its name)
//  3. rename the clone to the original name
//  4. start it again if the original was running
//
// When pullLatest is true the image is refreshed first. A failed pull is not
// fatal: the local image is used and the reason is reported in Warning.
func (c *Client) RebuildContainer(ctx context.Context, id string, pullLatest bool) (*RebuildResult, error) {
	podmanBin, err := exec.LookPath("podman")
	if err != nil {
		return nil, fmt.Errorf("podman CLI not found: %w", err)
	}

	info, err := c.InspectContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}

	result := &RebuildResult{
		Name:       strings.TrimPrefix(info.Name, "/"),
		Image:      info.imageRef(),
		WasRunning: info.State.Running,
	}

	if pullLatest && result.Image != "" {
		if err := c.PullImage(ctx, result.Image); err != nil {
			result.Warning = fmt.Sprintf("failed to pull %s, used local image: %v", result.Image, err)
		}
	}

	tmpName := fmt.Sprintf("%s-rebuild-%d", result.Name, time.Now().Unix())

	// Step 1: create a stopped copy of the container
	//
	// The image must be passed explicitly: with only two positional arguments
	// `podman container clone` inherits the image ID of the original container
	// (ConfigToSpec), so a re-tagged/rebuilt image would be ignored and the
	// clone would keep running the old one. --pull=never keeps podman from
	// pulling the (locally built) reference from a registry.
	cloneArgs := []string{"container", "clone", "--pull=never", id, tmpName}
	if result.Image != "" {
		cloneArgs = append(cloneArgs, result.Image)
	}

	out, err := runPodman(ctx, podmanBin, cloneArgs...)
	if err != nil {
		if strings.Contains(err.Error(), "arg(s)") {
			return nil, fmt.Errorf("podman is too old for rebuild, upgrade to podman >= 4.4: %w", err)
		}
		return nil, fmt.Errorf("clone container: %w", err)
	}
	result.ID = firstLine(out)

	// Step 2: remove the original container so its name becomes free
	if err := c.RemoveContainer(ctx, id, true); err != nil {
		// Roll back: drop the clone, the original is still in place
		_, _ = runPodman(ctx, podmanBin, "rm", "-f", tmpName)
		return nil, fmt.Errorf("remove original container: %w", err)
	}

	// Step 3: give the rebuilt container the original name
	if _, err := runPodman(ctx, podmanBin, "rename", tmpName, result.Name); err != nil {
		return nil, fmt.Errorf("rename %s to %s: %w", tmpName, result.Name, err)
	}

	// Step 4: restore the previous running state (by name, the ID may be unknown)
	if result.WasRunning {
		name := result.Name
		if name == "" {
			name = result.ID
		}
		if err := c.StartContainer(ctx, name); err != nil {
			return result, fmt.Errorf("container rebuilt but failed to start: %w", err)
		}
	}

	return result, nil
}

// imageRef returns the image reference to pull, falling back to the image ID
func (i *ContainerInspect) imageRef() string {
	if i.ImageName != "" {
		return i.ImageName
	}
	return i.Image
}

// runPodman runs the podman CLI and returns its stdout
func runPodman(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			msg = strings.TrimSpace(string(exitErr.Stderr))
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("podman %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// firstLine returns the first non-empty line of s
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
