package updater

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	maxDownloadRetries = 3
	retryBackoffBase   = 1 * time.Second
)

// ProgressFunc is called during download with bytes downloaded and total size
type ProgressFunc func(downloaded, total int64)

// downloadFile downloads a file from URL to local path
func (u *Updater) downloadFile(ctx context.Context, client *http.Client, url, destPath string) error {
	return u.downloadWithRetry(ctx, client, url, destPath, nil)
}

// downloadFileWithProgress downloads a file with progress callback
func (u *Updater) downloadFileWithProgress(ctx context.Context, client *http.Client, url, destPath string, progress ProgressFunc) error {
	return u.downloadWithRetry(ctx, client, url, destPath, progress)
}

// downloadWithRetry attempts to download a file up to maxDownloadRetries times
func (u *Updater) downloadWithRetry(ctx context.Context, client *http.Client, url, destPath string, progress ProgressFunc) error {
	var lastErr error
	for attempt := 1; attempt <= maxDownloadRetries; attempt++ {
		if attempt > 1 {
			backoff := retryBackoffBase * time.Duration(1<<(attempt-2)) // 1s, 2s, 4s
			u.logf("Download attempt %d/%d for %s waiting %v before retry...", attempt, maxDownloadRetries, url, backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fmt.Errorf("download cancelled during retry: %w", ctx.Err())
			}
		}
		u.logf("Download attempt %d/%d for %s", attempt, maxDownloadRetries, url)
		if err := u.doDownload(ctx, client, url, destPath, progress); err != nil {
			lastErr = err
			u.logf("Download attempt %d/%d failed: %v", attempt, maxDownloadRetries, err)
			continue
		}
		return nil
	}
	return fmt.Errorf("download failed after %d attempts: %w", maxDownloadRetries, lastErr)
}

// doDownload performs a single download attempt
func (u *Updater) doDownload(ctx context.Context, client *http.Client, url, destPath string, progress ProgressFunc) error {
	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Set User-Agent
	req.Header.Set("User-Agent", "PodmanView-Updater/1.0")

	u.logf("Starting download from %s (timeout: %v)", url, client.Timeout)
	startTime := time.Now()

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		u.logf("Download request failed after %v: %v", time.Since(startTime), err)
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	u.logf("Download response received: HTTP %d, Content-Length: %s", resp.StatusCode, formatBytes(resp.ContentLength))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Create temporary file
	tmpPath := destPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	// Ensure cleanup on error
	success := false
	defer func() {
		out.Close()
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Copy with progress
	var written int64
	total := resp.ContentLength

	if progress != nil && total > 0 {
		// Wrap reader to track progress
		reader := &progressReader{
			reader:   resp.Body,
			total:    total,
			progress: progress,
		}
		written, err = io.Copy(out, reader)
	} else {
		written, err = io.Copy(out, resp.Body)
	}

	if err != nil {
		u.logf("Download write failed after %v: %v", time.Since(startTime), err)
		return fmt.Errorf("write file: %w", err)
	}

	// Verify size if known
	if total > 0 && written != total {
		return fmt.Errorf("incomplete download: got %d bytes, expected %d", written, total)
	}

	elapsed := time.Since(startTime)
	if total > 0 {
		speed := float64(total) / elapsed.Seconds() / 1024 // KB/s
		u.logf("Download completed: %s in %v (%.1f KB/s)", formatBytes(written), elapsed, speed)
	} else {
		u.logf("Download completed: %s in %v", formatBytes(written), elapsed)
	}

	// Close before rename
	out.Close()

	// Rename temp file to final destination
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("rename file: %w", err)
	}

	success = true
	return nil
}

// progressReader wraps an io.Reader to track download progress
type progressReader struct {
	reader     io.Reader
	total      int64
	downloaded int64
	progress   ProgressFunc
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.downloaded += int64(n)
		if r.progress != nil {
			r.progress(r.downloaded, r.total)
		}
	}
	return n, err
}

// formatBytes formats bytes as human-readable string
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
