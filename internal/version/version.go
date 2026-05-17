package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Current defines the CLI semantic version following https://semver.org/.
const Current = "v0.9.0"

// Parsed represents a parsed semantic version.
type Parsed struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
}

// Parse parses a version string like "v1.2.3" or "1.2.3-rc" into components.
func Parse(v string) (Parsed, error) {
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return Parsed{}, fmt.Errorf("empty version string")
	}

	// Split off prerelease suffix
	var prerelease string
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		prerelease = v[idx+1:]
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return Parsed{}, fmt.Errorf("invalid version format %q: expected major.minor.patch", v)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Parsed{}, fmt.Errorf("invalid major version %q: %w", parts[0], err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Parsed{}, fmt.Errorf("invalid minor version %q: %w", parts[1], err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Parsed{}, fmt.Errorf("invalid patch version %q: %w", parts[2], err)
	}

	return Parsed{
		Major:      major,
		Minor:      minor,
		Patch:      patch,
		Prerelease: prerelease,
	}, nil
}

// Compare returns -1 if a < b, 0 if a == b, 1 if a > b.
// Prerelease versions sort before their release (1.0.0-rc < 1.0.0).
func Compare(a, b string) (int, error) {
	pa, err := Parse(a)
	if err != nil {
		return 0, fmt.Errorf("parsing version a: %w", err)
	}
	pb, err := Parse(b)
	if err != nil {
		return 0, fmt.Errorf("parsing version b: %w", err)
	}

	if pa.Major != pb.Major {
		return cmpInt(pa.Major, pb.Major), nil
	}
	if pa.Minor != pb.Minor {
		return cmpInt(pa.Minor, pb.Minor), nil
	}
	if pa.Patch != pb.Patch {
		return cmpInt(pa.Patch, pb.Patch), nil
	}

	// Same major.minor.patch — compare prerelease.
	// No prerelease > any prerelease (release beats pre-release).
	if pa.Prerelease == pb.Prerelease {
		return 0, nil
	}
	if pa.Prerelease == "" {
		return 1, nil // a is release, b is pre-release
	}
	if pb.Prerelease == "" {
		return -1, nil // a is pre-release, b is release
	}
	// Both have prerelease — lexicographic comparison
	if pa.Prerelease < pb.Prerelease {
		return -1, nil
	}
	return 1, nil
}

// IsNewer returns true if candidate is strictly newer than current.
func IsNewer(candidate, current string) (bool, error) {
	cmp, err := Compare(candidate, current)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	return 1
}
