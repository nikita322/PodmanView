package updater

import (
	"fmt"
	"strconv"
	"strings"
)

// Version represents a semantic version
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
}

// ParseVersion parses version string like "v1.2.3" or "v1.2.3-beta.1"
func ParseVersion(s string) (Version, error) {
	v := Version{}

	// Remove "v" prefix if present
	s = strings.TrimPrefix(s, "v")

	// Split by "-" to separate prerelease
	parts := strings.SplitN(s, "-", 2)
	if len(parts) == 2 {
		v.Prerelease = parts[1]
	}

	// Parse major.minor.patch
	versionParts := strings.Split(parts[0], ".")
	if len(versionParts) < 1 || len(versionParts) > 3 {
		return v, fmt.Errorf("invalid version format: %s", s)
	}

	var err error
	v.Major, err = strconv.Atoi(versionParts[0])
	if err != nil {
		return v, fmt.Errorf("invalid major version: %s", versionParts[0])
	}

	if len(versionParts) >= 2 {
		v.Minor, err = strconv.Atoi(versionParts[1])
		if err != nil {
			return v, fmt.Errorf("invalid minor version: %s", versionParts[1])
		}
	}

	if len(versionParts) >= 3 {
		v.Patch, err = strconv.Atoi(versionParts[2])
		if err != nil {
			return v, fmt.Errorf("invalid patch version: %s", versionParts[2])
		}
	}

	return v, nil
}

// Compare compares two versions
// Returns: -1 if v < other, 0 if v == other, 1 if v > other
func (v Version) Compare(other Version) int {
	// Compare major
	if v.Major < other.Major {
		return -1
	}
	if v.Major > other.Major {
		return 1
	}

	// Compare minor
	if v.Minor < other.Minor {
		return -1
	}
	if v.Minor > other.Minor {
		return 1
	}

	// Compare patch
	if v.Patch < other.Patch {
		return -1
	}
	if v.Patch > other.Patch {
		return 1
	}

	// Compare prerelease
	// No prerelease > with prerelease (1.0.0 > 1.0.0-beta)
	if v.Prerelease == "" && other.Prerelease != "" {
		return 1
	}
	if v.Prerelease != "" && other.Prerelease == "" {
		return -1
	}

	// Both have prerelease - compare alphabetically
	if v.Prerelease < other.Prerelease {
		return -1
	}
	if v.Prerelease > other.Prerelease {
		return 1
	}

	return 0
}

// IsNewer returns true if latest is newer than current
func IsNewer(current, latest string) (bool, error) {
	// "dev" version is never updateable
	if current == "dev" {
		return false, nil
	}

	currentV, err := ParseVersion(current)
	if err != nil {
		return false, fmt.Errorf("parse current version: %w", err)
	}

	latestV, err := ParseVersion(latest)
	if err != nil {
		return false, fmt.Errorf("parse latest version: %w", err)
	}

	return currentV.Compare(latestV) < 0, nil
}

// IsDev returns true if version is development version
func IsDev(version string) bool {
	return version == "dev" || version == ""
}
