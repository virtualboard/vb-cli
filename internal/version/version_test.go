package version

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMajor int
		wantMinor int
		wantPatch int
		wantPre   string
		wantErr   bool
	}{
		{"simple", "1.2.3", 1, 2, 3, "", false},
		{"with v prefix", "v1.2.3", 1, 2, 3, "", false},
		{"with prerelease", "1.0.0-rc", 1, 0, 0, "rc", false},
		{"with v and prerelease", "v2.1.0-rc", 2, 1, 0, "rc", false},
		{"zeros", "0.0.0", 0, 0, 0, "", false},
		{"large numbers", "1.10.0", 1, 10, 0, "", false},
		{"empty", "", 0, 0, 0, "", true},
		{"just v", "v", 0, 0, 0, "", true},
		{"two parts", "1.2", 0, 0, 0, "", true},
		{"four parts", "1.2.3.4", 0, 0, 0, "", true},
		{"non-numeric major", "x.2.3", 0, 0, 0, "", true},
		{"non-numeric minor", "1.x.3", 0, 0, 0, "", true},
		{"non-numeric patch", "1.2.x", 0, 0, 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if p.Major != tt.wantMajor || p.Minor != tt.wantMinor || p.Patch != tt.wantPatch || p.Prerelease != tt.wantPre {
				t.Fatalf("Parse(%q) = %+v, want {%d, %d, %d, %q}", tt.input, p, tt.wantMajor, tt.wantMinor, tt.wantPatch, tt.wantPre)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{"equal", "1.0.0", "1.0.0", 0},
		{"equal with v", "v1.0.0", "1.0.0", 0},
		{"major greater", "2.0.0", "1.0.0", 1},
		{"major less", "1.0.0", "2.0.0", -1},
		{"minor greater", "1.2.0", "1.1.0", 1},
		{"minor less", "1.1.0", "1.2.0", -1},
		{"patch greater", "1.0.2", "1.0.1", 1},
		{"patch less", "1.0.1", "1.0.2", -1},
		{"ten vs nine", "1.10.0", "1.9.0", 1},
		{"release beats prerelease", "1.0.0", "1.0.0-rc", 1},
		{"prerelease less than release", "1.0.0-rc", "1.0.0", -1},
		{"same prerelease", "1.0.0-rc", "1.0.0-rc", 0},
		{"prerelease ordering", "1.0.0-alpha", "1.0.0-beta", -1},
		{"prerelease ordering reverse", "1.0.0-beta", "1.0.0-alpha", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Compare(tt.a, tt.b)
			if err != nil {
				t.Fatalf("Compare(%q, %q) error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCompareErrors(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
	}{
		{"invalid a", "bad", "1.0.0"},
		{"invalid b", "1.0.0", "bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compare(tt.a, tt.b)
			if err == nil {
				t.Fatalf("expected error for Compare(%q, %q)", tt.a, tt.b)
			}
		})
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		current   string
		want      bool
	}{
		{"newer major", "2.0.0", "1.0.0", true},
		{"same", "1.0.0", "1.0.0", false},
		{"older", "1.0.0", "2.0.0", false},
		{"ten vs nine", "v1.10.0", "v1.9.0", true},
		{"rc not newer than release", "1.0.0-rc", "1.0.0", false},
		{"release newer than rc", "1.0.0", "1.0.0-rc", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsNewer(tt.candidate, tt.current)
			if err != nil {
				t.Fatalf("IsNewer(%q, %q) error: %v", tt.candidate, tt.current, err)
			}
			if got != tt.want {
				t.Fatalf("IsNewer(%q, %q) = %v, want %v", tt.candidate, tt.current, got, tt.want)
			}
		})
	}
}

func TestIsNewerError(t *testing.T) {
	_, err := IsNewer("bad", "1.0.0")
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestCurrent(t *testing.T) {
	// Verify the current version is parseable
	_, err := Parse(Current)
	if err != nil {
		t.Fatalf("Current version %q is not valid semver: %v", Current, err)
	}
}
