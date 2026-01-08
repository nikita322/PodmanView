package logger

import (
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
