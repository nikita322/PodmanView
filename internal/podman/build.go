package podman

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// buildRecipes lists supported build recipe file names
var buildRecipes = []string{"Dockerfile", "Containerfile", "dockerfile", "containerfile"}

// BuildImage rebuilds an image from a local build context. It mirrors the
// manual update cycle:
//
//	podman build --pull --no-cache --build-arg CACHEBUST=<ts> -t <image> <contextDir>
//
// Pull plus no-cache is intentional: RUN steps that fetch remote sources
// (git clone, wget from GitHub releases) are otherwise served from the layer
// cache and the image keeps stale code/binaries.
func (c *Client) BuildImage(ctx context.Context, image, contextDir string) error {
	if image == "" {
		return fmt.Errorf("image reference is required")
	}

	podmanBin, err := exec.LookPath("podman")
	if err != nil {
		return fmt.Errorf("podman CLI not found: %w", err)
	}

	if err := validateBuildContext(contextDir); err != nil {
		return err
	}

	cacheBust := strconv.FormatInt(time.Now().Unix(), 10)
	_, err = runPodman(ctx, podmanBin, "build",
		"--pull=always",
		"--no-cache",
		"--build-arg", "CACHEBUST="+cacheBust,
		"-t", image,
		contextDir,
	)
	if err != nil {
		return fmt.Errorf("build image %s: %w", image, err)
	}

	return nil
}

// validateBuildContext checks that the context is a directory with a Dockerfile
func validateBuildContext(contextDir string) error {
	if contextDir == "" {
		return fmt.Errorf("build context is not set")
	}

	info, err := os.Stat(contextDir)
	if err != nil {
		return fmt.Errorf("build context %s: %w", contextDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("build context %s is not a directory", contextDir)
	}

	for _, name := range buildRecipes {
		if _, err := os.Stat(filepath.Join(contextDir, name)); err == nil {
			return nil
		}
	}

	return fmt.Errorf("no Dockerfile or Containerfile in %s", contextDir)
}

// RebuildContainerFromSource rebuilds the container image from a local build
// context and then recreates the container from the freshly built image.
func (c *Client) RebuildContainerFromSource(ctx context.Context, id, contextDir string) (*RebuildResult, error) {
	info, err := c.InspectContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", err)
	}

	image := info.imageRef()
	if image == "" {
		return nil, fmt.Errorf("container %s has no image reference", id)
	}

	if err := c.BuildImage(ctx, image, contextDir); err != nil {
		return nil, err
	}

	// The image was just built locally, so there is nothing to pull
	result, err := c.RebuildContainer(ctx, id, false)
	if result != nil {
		result.Built = true
		result.BuildContext = contextDir
	}
	return result, err
}
