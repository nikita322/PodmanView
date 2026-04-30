package updater

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// createBackup copies current binary and web/ to backup directory
func createBackup(workDir, backupDir string) error {
	// Ensure backup directory exists
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	// Backup binary
	binaryPath := filepath.Join(workDir, "podmanview")
	if _, err := os.Stat(binaryPath); err == nil {
		if err := copyFile(binaryPath, filepath.Join(backupDir, "podmanview")); err != nil {
			return fmt.Errorf("backup binary: %w", err)
		}
	}

	// Backup web/ directory
	webSrc := filepath.Join(workDir, "web")
	webDst := filepath.Join(backupDir, "web")
	if _, err := os.Stat(webSrc); err == nil {
		if err := copyDir(webSrc, webDst); err != nil {
			return fmt.Errorf("backup web directory: %w", err)
		}
	}

	return nil
}

// restoreBackup restores files from backup directory
func restoreBackup(workDir, backupDir string) error {
	// Restore binary
	backupBinary := filepath.Join(backupDir, "podmanview")
	if _, err := os.Stat(backupBinary); err == nil {
		dstBinary := filepath.Join(workDir, "podmanview")
		if err := replaceBinary(backupBinary, dstBinary); err != nil {
			return fmt.Errorf("restore binary: %w", err)
		}
	}

	// Restore web/ directory
	backupWeb := filepath.Join(backupDir, "web")
	if _, err := os.Stat(backupWeb); err == nil {
		dstWeb := filepath.Join(workDir, "web")
		// Remove current web/ first
		os.RemoveAll(dstWeb)
		if err := copyDir(backupWeb, dstWeb); err != nil {
			return fmt.Errorf("restore web directory: %w", err)
		}
	}

	return nil
}

// replaceBinary atomically replaces dst with src using a temp file + rename.
// This works even when dst is a currently-running executable on Linux.
func replaceBinary(src, dst string) error {
	tmp := dst + ".new"

	if err := copyFile(src, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("copy to temp: %w", err)
	}

	if err := os.Chmod(tmp, 0755); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("chmod temp: %w", err)
	}

	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp: %w", err)
	}

	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Get source file info for permissions
	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}

	// Create destination file
	destFile, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, sourceInfo.Mode())
	if err != nil {
		return err
	}
	defer destFile.Close()

	// Copy contents
	_, err = io.Copy(destFile, sourceFile)
	return err
}

// copyDir recursively copies a directory from src to dst
func copyDir(src, dst string) error {
	// Get source info
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// Create destination directory
	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	// Read source directory
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}
