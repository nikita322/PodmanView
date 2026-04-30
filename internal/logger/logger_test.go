package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	// Create temp directory for logs
	tempDir := t.TempDir()

	// Create logger
	logger, err := New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// Check if log files were created
	appLogPath := filepath.Join(tempDir, "app.log")
	errorLogPath := filepath.Join(tempDir, "error.log")

	if _, err := os.Stat(appLogPath); os.IsNotExist(err) {
		t.Errorf("app.log was not created")
	}

	if _, err := os.Stat(errorLogPath); os.IsNotExist(err) {
		t.Errorf("error.log was not created")
	}
}

func TestLogging(t *testing.T) {
	// Create temp directory for logs
	tempDir := t.TempDir()

	// Create logger
	logger, err := New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	// Test logging methods
	logger.Printf("Test message: %s", "hello")
	logger.Print("Test print")
	logger.Println("Test println")

	logger.Errorf("Test error: %s", "error")
	logger.Error("Test error")
	logger.Errorln("Test errorln")

	// Check if files contain data
	appLogPath := filepath.Join(tempDir, "app.log")
	errorLogPath := filepath.Join(tempDir, "error.log")

	appInfo, err := os.Stat(appLogPath)
	if err != nil {
		t.Errorf("Failed to stat app.log: %v", err)
	}
	if appInfo.Size() == 0 {
		t.Errorf("app.log is empty")
	}

	errorInfo, err := os.Stat(errorLogPath)
	if err != nil {
		t.Errorf("Failed to stat error.log: %v", err)
	}
	if errorInfo.Size() == 0 {
		t.Errorf("error.log is empty")
	}
}

func TestRotation(t *testing.T) {
	tempDir := t.TempDir()

	// Create a rotating writer with a very small max size (100 bytes)
	w, err := newRotatingWriter(filepath.Join(tempDir, "test.log"), 100, 3)
	if err != nil {
		t.Fatalf("Failed to create rotating writer: %v", err)
	}
	defer w.Close()

	// Write data that exceeds the limit to trigger rotation
	data := []byte("this is a test log line that should trigger rotation when written multiple times\n")
	for i := 0; i < 5; i++ {
		if _, err := w.Write(data); err != nil {
			t.Fatalf("Failed to write: %v", err)
		}
	}

	// Check that backup files were created
	for i := 1; i <= 3; i++ {
		backupPath := filepath.Join(tempDir, fmt.Sprintf("test.log.%d", i))
		if _, err := os.Stat(backupPath); os.IsNotExist(err) {
			t.Errorf("Expected backup file %s to exist", backupPath)
		}
	}

	// test.log.4 should not exist because maxBackups is 3
	if _, err := os.Stat(filepath.Join(tempDir, "test.log.4")); !os.IsNotExist(err) {
		t.Errorf("Expected test.log.4 to not exist (maxBackups=3)")
	}
}
