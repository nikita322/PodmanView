package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// envTemplate defines the structure of the generated .env file.
// Each entry contains: key, default value, and comment.
type envEntry struct {
	Key     string
	Comment string
}

// envTemplate defines the order and comments for .env file generation.
var envTemplate = []envEntry{
	{"", "# PodmanView Configuration"},
	{"", "# Generated automatically on first run"},
	{"", ""},
	{"", "# ==================="},
	{"", "# Server Settings"},
	{"", "# ==================="},
	{"", ""},
	{"PODMANVIEW_ADDR", "# Server address (host:port)"},
	{"", ""},
	{"", "# ==================="},
	{"", "# Security Settings"},
	{"", "# ==================="},
	{"", ""},
	{"PODMANVIEW_JWT_SECRET", "# JWT secret key (auto-generated, do not share!)"},
	{"PODMANVIEW_JWT_EXPIRATION", "# JWT token expiration in seconds (default: 24 hours)"},
	{"PODMANVIEW_NO_AUTH", "# Disable authentication (true/false, for development only!)"},
	{"", ""},
	{"", "# ==================="},
	{"", "# Podman Settings"},
	{"", "# ==================="},
	{"", ""},
	{"PODMANVIEW_SOCKET", "# Podman socket path (leave empty for auto-detection)"},
}

// WriteEnvFile writes configuration to .env file with comments.
// Uses atomic write (write to temp file, then rename).
func WriteEnvFile(filePath string, values map[string]string) error {
	var content strings.Builder

	for _, entry := range envTemplate {
		if entry.Key == "" {
			// This is a comment or empty line
			content.WriteString(entry.Comment)
			content.WriteString("\n")
		} else {
			// This is a config entry
			if entry.Comment != "" {
				content.WriteString(entry.Comment)
				content.WriteString("\n")
			}

			value := values[entry.Key]
			content.WriteString(formatEnvLine(entry.Key, value))
			content.WriteString("\n")
		}
	}

	// Add any extra values not in template
	extraKeys := findExtraKeys(values)
	if len(extraKeys) > 0 {
		content.WriteString("\n")
		content.WriteString("# ===================\n")
		content.WriteString("# Custom Settings\n")
		content.WriteString("# ===================\n")
		content.WriteString("\n")

		for _, key := range extraKeys {
			content.WriteString(formatEnvLine(key, values[key]))
			content.WriteString("\n")
		}
	}

	return atomicWrite(filePath, content.String())
}

// formatEnvLine formats a single KEY=value line.
// Quotes values that contain special characters.
func formatEnvLine(key, value string) string {
	if needsQuoting(value) {
		// Escape special characters and wrap in quotes
		escaped := escapeValue(value)
		return fmt.Sprintf("%s=\"%s\"", key, escaped)
	}
	return fmt.Sprintf("%s=%s", key, value)
}

// needsQuoting returns true if the value needs to be quoted.
func needsQuoting(value string) bool {
	if value == "" {
		return false
	}

	// Check for characters that need quoting
	for _, r := range value {
		switch r {
		case ' ', '\t', '\n', '\r', '"', '\'', '#', '\\':
			return true
		}
	}

	return false
}

// escapeValue escapes special characters for double-quoted values.
func escapeValue(value string) string {
	var result strings.Builder
	result.Grow(len(value) + 10)

	for _, r := range value {
		switch r {
		case '"':
			result.WriteString("\\\"")
		case '\\':
			result.WriteString("\\\\")
		case '\n':
			result.WriteString("\\n")
		case '\t':
			result.WriteString("\\t")
		case '\r':
			result.WriteString("\\r")
		default:
			result.WriteRune(r)
		}
	}

	return result.String()
}

// findExtraKeys returns keys that are not in the template.
func findExtraKeys(values map[string]string) []string {
	templateKeys := make(map[string]bool)
	for _, entry := range envTemplate {
		if entry.Key != "" {
			templateKeys[entry.Key] = true
		}
	}

	var extra []string
	for key := range values {
		if !templateKeys[key] {
			extra = append(extra, key)
		}
	}

	sort.Strings(extra)
	return extra
}

// atomicWrite writes content to file atomically.
// Writes to temp file first, then renames.
func atomicWrite(filePath, content string) error {
	// Create directory if not exists
	dir := filepath.Dir(filePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	// Create temp file in the same directory
	tmpFile := filePath + ".tmp"

	// Write to temp file
	if err := os.WriteFile(tmpFile, []byte(content), 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Rename temp file to target (atomic on most filesystems)
	if err := os.Rename(tmpFile, filePath); err != nil {
		// Clean up temp file on failure
		os.Remove(tmpFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
