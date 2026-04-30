package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/jedisct1/go-minisign"
	"podmanview/internal/logger"
)

const (
	githubRepo      = "nikita322/PodmanView"
	githubAPIURL    = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	cacheTTL        = 15 * time.Minute
	requestTimeout  = 30 * time.Second
	downloadTimeout = 10 * time.Minute
)

// Updater handles checking and performing updates
type Updater struct {
	currentVersion string
	workDir        string
	pubKey         minisign.PublicKey
	httpClient     *http.Client
	logger         *logger.Logger

	// Cache for update checks
	lastCheck     *UpdateCheckResult
	lastCheckTime time.Time
	checkMu       sync.RWMutex
}

// GitHubRelease represents GitHub release API response
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a release asset
type GitHubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// UpdateCheckResult contains update check information
type UpdateCheckResult struct {
	UpdateAvailable bool      `json:"updateAvailable"`
	CurrentVersion  string    `json:"currentVersion"`
	LatestVersion   string    `json:"latestVersion"`
	ReleaseNotes    string    `json:"releaseNotes,omitempty"`
	ReleaseURL      string    `json:"releaseUrl,omitempty"`
	PublishedAt     time.Time `json:"publishedAt,omitempty"`
	DownloadSize    int64     `json:"downloadSize,omitempty"`
	CurrentArch     string    `json:"currentArch"`
	IsDev           bool      `json:"isDev"`
}

// UpdateProgress represents current update progress
type UpdateProgress struct {
	Stage   string `json:"stage"`
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
}

// New creates a new Updater instance
func New(currentVersion, workDir string) (*Updater, error) {
	pubKey, err := ParsePublicKey(PublicKeyStr)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	return &Updater{
		currentVersion: currentVersion,
		workDir:        workDir,
		pubKey:         pubKey,
		httpClient: &http.Client{
			Timeout: requestTimeout,
		},
	}, nil
}

// CheckUpdate checks if a new version is available
func (u *Updater) CheckUpdate(ctx context.Context) (*UpdateCheckResult, error) {
	arch := runtime.GOARCH
	isDev := IsDev(u.currentVersion)

	u.logf("Checking for updates (current: %s, arch: %s)", u.currentVersion, arch)

	// Check cache first
	u.checkMu.RLock()
	if u.lastCheck != nil && time.Since(u.lastCheckTime) < cacheTTL {
		result := *u.lastCheck
		u.checkMu.RUnlock()
		u.logf("Using cached update check result (age: %v)", time.Since(u.lastCheckTime))
		return &result, nil
	}
	u.checkMu.RUnlock()

	u.logf("Fetching latest release from GitHub API: %s", githubAPIURL)

	// Fetch latest release from GitHub
	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		u.logf("Failed to fetch latest release: %v", err)
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	u.logf("Latest release: %s (published: %s)", release.TagName, release.PublishedAt.Format(time.RFC3339))

	// Find download size for current architecture
	var downloadSize int64
	archiveName := fmt.Sprintf("podmanview-linux-%s.tar.gz", arch)
	for _, asset := range release.Assets {
		if asset.Name == archiveName {
			downloadSize = asset.Size
			break
		}
	}

	// Check if update is available
	updateAvailable := false
	if !isDev {
		updateAvailable, _ = IsNewer(u.currentVersion, release.TagName)
	}

	u.logf("Update available: %v (current: %s -> latest: %s)", updateAvailable, u.currentVersion, release.TagName)

	result := &UpdateCheckResult{
		UpdateAvailable: updateAvailable,
		CurrentVersion:  u.currentVersion,
		LatestVersion:   release.TagName,
		ReleaseNotes:    release.Body,
		ReleaseURL:      release.HTMLURL,
		PublishedAt:     release.PublishedAt,
		DownloadSize:    downloadSize,
		CurrentArch:     arch,
		IsDev:           isDev,
	}

	// Update cache
	u.checkMu.Lock()
	u.lastCheck = result
	u.lastCheckTime = time.Now()
	u.checkMu.Unlock()

	return result, nil
}

// fetchLatestRelease fetches the latest release from GitHub API
func (u *Updater) fetchLatestRelease(ctx context.Context) (*GitHubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "PodmanView-Updater/1.0")

	u.logf("Sending request to GitHub API (timeout: %v)", requestTimeout)

	resp, err := u.httpClient.Do(req)
	if err != nil {
		u.logf("GitHub API request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	u.logf("GitHub API response: HTTP %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &release, nil
}

// PerformUpdate downloads and installs the update
func (u *Updater) PerformUpdate(ctx context.Context, progress func(UpdateProgress)) error {
	u.logf("Starting update process from version %s", u.currentVersion)

	// Check if dev version
	if IsDev(u.currentVersion) {
		u.logf("Update aborted: cannot update dev version")
		return fmt.Errorf("cannot update dev version")
	}

	// Step 1: Check for updates
	u.logf("Step 1/11: Checking for updates...")
	progress(UpdateProgress{Stage: "preparing", Percent: 0, Message: "Checking for updates..."})

	check, err := u.CheckUpdate(ctx)
	if err != nil {
		u.logf("Update check failed: %v", err)
		return fmt.Errorf("check update: %w", err)
	}
	if !check.UpdateAvailable {
		u.logf("No update available (current: %s, latest: %s)", check.CurrentVersion, check.LatestVersion)
		return fmt.Errorf("no update available")
	}
	u.logf("Update available: %s -> %s (size: %s)", check.CurrentVersion, check.LatestVersion, formatBytes(check.DownloadSize))

	// Step 2: Prepare directories
	arch := runtime.GOARCH
	updateDir := filepath.Join(u.workDir, ".update")
	backupDir := filepath.Join(u.workDir, ".backup", u.currentVersion)

	u.logf("Step 2/11: Preparing directories (updateDir=%s, backupDir=%s)", updateDir, backupDir)
	os.RemoveAll(updateDir)
	if err := os.MkdirAll(updateDir, 0755); err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}

	// Step 3: Get download URLs
	u.logf("Step 3/11: Resolving download URLs from GitHub API")
	archiveName := fmt.Sprintf("podmanview-linux-%s.tar.gz", arch)
	archiveURL, sigURL, err := u.getDownloadURLs(ctx, archiveName)
	if err != nil {
		os.RemoveAll(updateDir)
		u.logf("Failed to get download URLs: %v", err)
		return fmt.Errorf("get download URLs: %w", err)
	}
	u.logf("Archive URL: %s", archiveURL)
	u.logf("Signature URL: %s", sigURL)

	// Step 4: Download archive
	u.logf("Step 4/11: Downloading archive %s (timeout: %v)", archiveName, downloadTimeout)
	progress(UpdateProgress{Stage: "downloading", Percent: 5, Message: "Downloading update..."})

	archivePath := filepath.Join(updateDir, archiveName)
	downloadClient := &http.Client{Timeout: downloadTimeout}

	err = u.downloadFileWithProgress(ctx, downloadClient, archiveURL, archivePath, func(downloaded, total int64) {
		pct := 5 + int(float64(downloaded)/float64(total)*40) // 5-45%
		progress(UpdateProgress{
			Stage:   "downloading",
			Percent: pct,
			Message: fmt.Sprintf("Downloaded %s / %s", formatBytes(downloaded), formatBytes(total)),
		})
	})
	if err != nil {
		os.RemoveAll(updateDir)
		u.logf("Archive download failed: %v", err)
		return fmt.Errorf("download archive: %w", err)
	}
	u.logf("Archive downloaded successfully: %s", archivePath)

	// Step 5: Download signature
	u.logf("Step 5/11: Downloading signature from %s", sigURL)
	progress(UpdateProgress{Stage: "downloading", Percent: 48, Message: "Downloading signature..."})

	sigPath := archivePath + ".minisig"
	if err := u.downloadFile(ctx, downloadClient, sigURL, sigPath); err != nil {
		os.RemoveAll(updateDir)
		u.logf("Signature download failed: %v", err)
		return fmt.Errorf("download signature: %w", err)
	}
	u.logf("Signature downloaded successfully: %s", sigPath)

	// Step 6: Verify signature
	u.logf("Step 6/11: Verifying archive signature")
	progress(UpdateProgress{Stage: "verifying", Percent: 50, Message: "Verifying signature..."})

	if err := VerifySignature(archivePath, sigPath, u.pubKey); err != nil {
		os.RemoveAll(updateDir)
		u.logf("Signature verification failed: %v", err)
		return fmt.Errorf("signature verification failed: %w", err)
	}
	u.logf("Signature verified successfully")

	// Step 7: Create backup
	u.logf("Step 7/11: Creating backup in %s", backupDir)
	progress(UpdateProgress{Stage: "backup", Percent: 55, Message: "Creating backup..."})

	if err := createBackup(u.workDir, backupDir); err != nil {
		os.RemoveAll(updateDir)
		u.logf("Backup creation failed: %v", err)
		return fmt.Errorf("create backup: %w", err)
	}
	u.logf("Backup created successfully")

	// Step 8: Extract archive
	u.logf("Step 8/11: Extracting archive to %s", filepath.Join(updateDir, "extracted"))
	progress(UpdateProgress{Stage: "extracting", Percent: 65, Message: "Extracting files..."})

	extractDir := filepath.Join(updateDir, "extracted")
	if err := extractTarGz(archivePath, extractDir); err != nil {
		os.RemoveAll(updateDir)
		u.logf("Archive extraction failed: %v", err)
		return fmt.Errorf("extract archive: %w", err)
	}
	u.logf("Archive extracted successfully")

	// Step 9: Install update
	u.logf("Step 9/11: Installing update to %s", u.workDir)
	progress(UpdateProgress{Stage: "installing", Percent: 80, Message: "Installing update..."})

	if err := u.installUpdate(extractDir); err != nil {
		// Try to rollback
		u.logf("Installation failed: %v", err)
		progress(UpdateProgress{Stage: "rollback", Percent: 85, Message: "Rolling back..."})
		u.logf("Step 10/11: Rolling back from backup")
		if rbErr := restoreBackup(u.workDir, backupDir); rbErr != nil {
			os.RemoveAll(updateDir)
			u.logf("Rollback failed: %v", rbErr)
			return fmt.Errorf("install failed: %w, rollback also failed: %v", err, rbErr)
		}
		os.RemoveAll(updateDir)
		u.logf("Rollback completed successfully")
		return fmt.Errorf("install failed (rolled back): %w", err)
	}
	u.logf("Update installed successfully")

	// Step 10: Cleanup
	u.logf("Step 10/11: Cleaning up temporary files")
	progress(UpdateProgress{Stage: "cleanup", Percent: 95, Message: "Cleaning up..."})
	os.RemoveAll(updateDir)

	// Step 11: Done - caller should restart service
	u.logf("Step 11/11: Update complete, restarting service")
	progress(UpdateProgress{Stage: "restarting", Percent: 100, Message: "Restarting service..."})

	return nil
}

// getDownloadURLs returns archive and signature download URLs
func (u *Updater) getDownloadURLs(ctx context.Context, archiveName string) (archiveURL, sigURL string, err error) {
	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		return "", "", err
	}

	sigName := archiveName + ".minisig"

	for _, asset := range release.Assets {
		if asset.Name == archiveName {
			archiveURL = asset.BrowserDownloadURL
		}
		if asset.Name == sigName {
			sigURL = asset.BrowserDownloadURL
		}
	}

	if archiveURL == "" {
		return "", "", fmt.Errorf("no release asset for architecture: %s", archiveName)
	}
	if sigURL == "" {
		return "", "", fmt.Errorf("no signature file for: %s", archiveName)
	}

	return archiveURL, sigURL, nil
}

// installUpdate copies new files to working directory
func (u *Updater) installUpdate(extractDir string) error {
	// Replace binary
	newBinary := filepath.Join(extractDir, "podmanview")
	dstBinary := filepath.Join(u.workDir, "podmanview")

	// In Linux we can replace running binary
	if err := copyFile(newBinary, dstBinary); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	if err := os.Chmod(dstBinary, 0755); err != nil {
		return fmt.Errorf("chmod binary: %w", err)
	}

	// Replace web/ directory
	newWeb := filepath.Join(extractDir, "web")
	dstWeb := filepath.Join(u.workDir, "web")

	if _, err := os.Stat(newWeb); err == nil {
		// Remove old web directory
		if err := os.RemoveAll(dstWeb); err != nil {
			return fmt.Errorf("remove old web: %w", err)
		}
		// Copy new web directory
		if err := copyDir(newWeb, dstWeb); err != nil {
			return fmt.Errorf("copy new web: %w", err)
		}
	}

	return nil
}

// RestartService restarts the podmanview systemd service
func RestartService() error {
	return exec.Command("systemctl", "restart", "podmanview").Run()
}

// GetCurrentVersion returns the current version
func (u *Updater) GetCurrentVersion() string {
	return u.currentVersion
}

// GetWorkDir returns the working directory
func (u *Updater) GetWorkDir() string {
	return u.workDir
}

// SetLogger sets the logger for update operations
func (u *Updater) SetLogger(l *logger.Logger) {
	u.logger = l
}

// logf logs a formatted message if logger is set
func (u *Updater) logf(format string, v ...interface{}) {
	if u.logger != nil {
		u.logger.Printf("[Updater] "+format, v...)
	}
}
