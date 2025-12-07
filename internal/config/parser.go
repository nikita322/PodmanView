package config

import (
	"bufio"
	"io"
	"strings"
	"unicode"
)

// ParseEnvFile parses .env file content and returns key-value pairs.
// Supports:
// - KEY=value
// - KEY="value with spaces"
// - KEY='value with spaces'
// - KEY="value with \"escaped\" quotes"
// - # comments
// - Empty lines
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	result := make(map[string]string)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()

		key, value, ok := parseLine(line)
		if ok {
			result[key] = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// parseLine parses a single line from .env file.
// Returns key, value, and whether the line was valid.
func parseLine(line string) (key, value string, ok bool) {
	// Trim leading/trailing whitespace
	line = strings.TrimSpace(line)

	// Skip empty lines and comments
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	// Find the first '=' sign
	eqIndex := strings.Index(line, "=")
	if eqIndex == -1 {
		return "", "", false
	}

	// Extract key
	key = strings.TrimSpace(line[:eqIndex])
	if key == "" || !isValidKey(key) {
		return "", "", false
	}

	// Extract value
	rawValue := line[eqIndex+1:]
	value = parseValue(rawValue)

	return key, value, true
}

// isValidKey checks if the key contains only valid characters.
// Valid: A-Z, a-z, 0-9, _
func isValidKey(key string) bool {
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// parseValue parses the value part, handling quotes and escapes.
func parseValue(raw string) string {
	raw = strings.TrimSpace(raw)

	if len(raw) == 0 {
		return ""
	}

	// Check for inline comments (only if not quoted)
	if raw[0] != '"' && raw[0] != '\'' {
		// Find comment that's not inside the value
		if idx := strings.Index(raw, " #"); idx != -1 {
			raw = strings.TrimSpace(raw[:idx])
		}
	}

	// Handle quoted values
	if len(raw) >= 2 {
		first := raw[0]
		last := raw[len(raw)-1]

		if first == '"' && last == '"' {
			return parseQuotedValue(raw[1:len(raw)-1], '"')
		}
		if first == '\'' && last == '\'' {
			// Single quotes - no escape processing
			return raw[1 : len(raw)-1]
		}
	}

	return raw
}

// parseQuotedValue handles escape sequences in double-quoted values.
func parseQuotedValue(s string, quote byte) string {
	var result strings.Builder
	result.Grow(len(s))

	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			switch next {
			case 'n':
				result.WriteByte('\n')
			case 't':
				result.WriteByte('\t')
			case 'r':
				result.WriteByte('\r')
			case '\\':
				result.WriteByte('\\')
			case '"':
				result.WriteByte('"')
			case '\'':
				result.WriteByte('\'')
			default:
				// Unknown escape - keep both characters
				result.WriteByte('\\')
				result.WriteByte(next)
			}
			i += 2
		} else {
			result.WriteByte(s[i])
			i++
		}
	}

	return result.String()
}
