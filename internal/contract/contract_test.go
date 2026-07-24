package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultAndConfiguredLifecycle(t *testing.T) {
	root := t.TempDir()
	writeLegacyMarker(t, root, ".template-version", "0.7.0")
	legacy, err := Load(root)
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	if err := legacy.ValidateTransition("backlog", "in-progress"); err != nil {
		t.Fatalf("default transition: %v", err)
	}
	if !legacy.RequiresAssignedOwner("done") || legacy.RequiresAssignedOwner("backlog") {
		t.Fatal("unexpected default ownership policy")
	}

	writeContract(t, root, validContractJSON())
	configured, err := Load(root)
	if err != nil {
		t.Fatalf("load configured: %v", err)
	}
	if configured.FeaturesDir() != filepath.Join(root, "features") {
		t.Fatalf("features path: %s", configured.FeaturesDir())
	}
	if configured.SpecsDir() != filepath.Join(root, "specs") {
		t.Fatalf("specs path: %s", configured.SpecsDir())
	}
	if configured.TemplatesDir() != filepath.Join(root, "templates") {
		t.Fatalf("templates path: %s", configured.TemplatesDir())
	}
	if configured.SchemasDir() != filepath.Join(root, "schemas") {
		t.Fatalf("schemas path: %s", configured.SchemasDir())
	}
	if configured.IndexPath() != filepath.Join(root, "features", "INDEX.md") {
		t.Fatalf("index path: %s", configured.IndexPath())
	}
	if configured.LocksDir() != filepath.Join(root, ".state", "locks") {
		t.Fatalf("locks path: %s", configured.LocksDir())
	}
	if configured.AuditLogPath() != filepath.Join(root, "audit.jsonl") {
		t.Fatalf("audit path: %s", configured.AuditLogPath())
	}
	if configured.InitialStatus() != "backlog" {
		t.Fatalf("initial status: %s", configured.InitialStatus())
	}
	if configured.SlugMaxWords() != 6 {
		t.Fatalf("slug max words: %d", configured.SlugMaxWords())
	}
	if err := configured.ValidateFilename("FTR-0001-four-word-slug.md"); err != nil {
		t.Fatalf("valid filename: %v", err)
	}
	if err := configured.ValidateFilename("FTR-0001-five-word-slug-is-compatible.md"); err != nil {
		t.Fatal("legacy long filename compatibility was not enforced")
	}
	if err := configured.ValidateOwner("agent-1", false); err != nil {
		t.Fatalf("valid owner: %v", err)
	}
	if err := configured.ValidateOwner("unassigned", false); err == nil {
		t.Fatal("unassigned actor was accepted")
	}
	if err := configured.ValidateFeatureID("FTR-0001"); err != nil {
		t.Fatalf("valid feature ID: %v", err)
	}
	if err := configured.ValidateFeatureID("../../escape"); err == nil {
		t.Fatal("invalid feature ID accepted")
	}
	if dir, ok := configured.DirectoryForStatus("BACKLOG"); !ok || dir != filepath.Join(root, "features", "backlog") {
		t.Fatalf("status directory: %s %v", dir, ok)
	}
	if _, ok := configured.DirectoryForStatus("missing"); ok {
		t.Fatal("unknown status unexpectedly resolved")
	}
	if err := configured.ValidateTransition("backlog", "in-progress"); err != nil {
		t.Fatalf("configured transition: %v", err)
	}
	if err := configured.ValidateTransition("in-progress", "backlog"); err == nil {
		t.Fatal("unexpected reverse transition")
	}
	if err := configured.ValidateTransition("done", "backlog"); err == nil || !strings.Contains(err.Error(), "cannot transition") {
		t.Fatalf("terminal transition error: %v", err)
	}
	if err := configured.ValidateStatus("missing"); err == nil {
		t.Fatal("unknown status accepted")
	}
}

func TestLoadAcceptsSemanticallyEquivalentJSONFormattingAndKeyOrder(t *testing.T) {
	var value map[string]any
	if err := json.Unmarshal(CanonicalJSON(), &value); err != nil {
		t.Fatal(err)
	}
	reordered, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(reordered, CanonicalJSON()) {
		t.Fatal("test fixture did not change JSON representation")
	}
	root := t.TempDir()
	writeContract(t, root, string(reordered))
	if _, err := Load(root); err != nil {
		t.Fatalf("semantic canonical contract was rejected: %v", err)
	}
}

func TestLoadRequiresContractUnlessLegacyVersionIsExplicit(t *testing.T) {
	tests := []struct {
		name        string
		marker      string
		value       string
		wantLegacy  bool
		wantRequire bool
	}{
		{name: "no marker", wantRequire: true},
		{name: "modern initialized workspace", marker: ".template-version", value: "0.8.0", wantRequire: true},
		{name: "modern template checkout", marker: "version.txt", value: "0.8.0", wantRequire: true},
		{name: "legacy initialized workspace", marker: ".template-version", value: "0.7.0", wantLegacy: true},
		{name: "legacy template checkout", marker: "version.txt", value: "0.7.0", wantLegacy: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.marker != "" {
				writeLegacyMarker(t, root, tt.marker, tt.value)
			}
			lifecycle, err := Load(root)
			if tt.wantRequire {
				if !errors.Is(err, ErrContractRequired) {
					t.Fatalf("missing modern contract error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("load explicit legacy workspace: %v", err)
			}
			if !tt.wantLegacy || lifecycle.LocksDir() != filepath.Join(root, "locks") {
				t.Fatalf("unexpected legacy lifecycle: %+v", lifecycle)
			}
		})
	}
}

func TestLoadRejectsUnsafeOrConflictingLegacyMarkers(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "outside-version")
		if err := os.WriteFile(target, []byte("0.7.0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, ".template-version")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := Load(root); err == nil || errors.Is(err, ErrContractRequired) {
			t.Fatalf("unsafe legacy marker was not rejected explicitly: %v", err)
		}
	})

	t.Run("conflicting versions", func(t *testing.T) {
		root := t.TempDir()
		writeLegacyMarker(t, root, ".template-version", "0.7.0")
		writeLegacyMarker(t, root, "version.txt", "0.8.0")
		if _, err := Load(root); !errors.Is(err, ErrContractRequired) {
			t.Fatalf("modern marker did not disable legacy fallback: %v", err)
		}
	})
}

func TestLoadRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"invalid-json", `{`},
		{"unknown-top-level-field", strings.Replace(validContractJSON(), `{`, `{"unknown":true,`, 1)},
		{"missing-required-top-level-field", strings.Replace(validContractJSON(), `"contractVersion":1,`, ``, 1)},
		{"duplicate-field", strings.Replace(validContractJSON(), `{`, `{"contractVersion":1,`, 1)},
		{"authorization-policy-tamper", strings.Replace(validContractJSON(), `"install":{"confirmation":"explicit-required"}`, `"install":{"confirmation":"covered-by-task-scope"}`, 1)},
		{"unknown-authorization-effect", strings.Replace(validContractJSON(), `"effects":{`, `"effects":{"unknown":{"confirmation":"not-required"},`, 1)},
		{"empty-path", strings.Replace(validContractJSON(), `"features":"features"`, `"features":""`, 1)},
		{"absolute-path", strings.Replace(validContractJSON(), `"locks":".state/locks"`, `"locks":"/tmp/locks"`, 1)},
		{"escaping-path", strings.Replace(validContractJSON(), `"auditLog":"audit.jsonl"`, `"auditLog":"../audit"`, 1)},
		{"escaping-specs-path", strings.Replace(validContractJSON(), `"specs":"specs"`, `"specs":"../blueprints"`, 1)},
		{"escaping-templates-path", strings.Replace(validContractJSON(), `"templates":"templates"`, `"templates":"../scaffold"`, 1)},
		{"escaping-schemas-path", strings.Replace(validContractJSON(), `"schemas":"schemas"`, `"schemas":"../contracts"`, 1)},
		{"noncanonical-features-path", strings.Replace(validContractJSON(), `"features":"features"`, `"features":"work/items"`, 1)},
		{"empty-statuses", strings.Replace(validContractJSON(), `"statuses":["backlog","in-progress","blocked","review","done"]`, `"statuses":[]`, 1)},
		{"invalid-status", strings.Replace(validContractJSON(), `"backlog"`, `"bad/status"`, 1)},
		{"parent-status", strings.Replace(validContractJSON(), `"backlog"`, `".."`, 1)},
		{"dot-status", strings.Replace(validContractJSON(), `"backlog"`, `"."`, 1)},
		{"hidden-status", strings.Replace(validContractJSON(), `"backlog"`, `".hidden"`, 1)},
		{"duplicate-status", strings.Replace(validContractJSON(), `"backlog","in-progress"`, `"backlog","backlog"`, 1)},
		{"unknown-source", strings.Replace(validContractJSON(), `"backlog":["in-progress"]`, `"missing":["in-progress"]`, 1)},
		{"unknown-target", strings.Replace(validContractJSON(), `["in-progress"]`, `["missing"]`, 1)},
		{"missing-owner", strings.Replace(validContractJSON(), `,"done":"assigned-required"`, `,"other":"assigned-required"`, 1)},
		{"invalid-owner", strings.Replace(validContractJSON(), `"in-progress":"assigned-required"`, `"in-progress":"optional"`, 1)},
		{"invalid-slug-limit", strings.Replace(validContractJSON(), `"slugMaxWords":6`, `"slugMaxWords":-1`, 1)},
		{"unsupported-filename-pattern", strings.Replace(validContractJSON(), `"filenamePattern":"^FTR-[0-9]{4}-[a-z0-9]+(?:-[a-z0-9]+)*\\.md$"`, `"filenamePattern":"^["`, 1)},
		{"invalid-owner-pattern", strings.Replace(validContractJSON(), `"ownerPattern":"^(?:unassigned|[A-Za-z0-9][A-Za-z0-9._-]*)$"`, `"ownerPattern":"["`, 1)},
		{"invalid-id-pattern", strings.Replace(validContractJSON(), `"idPattern":"^FTR-[0-9]{4}$"`, `"idPattern":"["`, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeContract(t, root, tt.content)
			if _, err := Load(root); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, fileName), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("read error not returned")
	}
}

func TestLoadRejectsUnsafeContractFileShapes(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "canonical-contract.json")
		if err := os.WriteFile(target, CanonicalJSON(), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, fileName)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("contract symlink was not rejected explicitly: %v", err)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, fileName), bytes.Repeat([]byte{'x'}, int(maxContractBytes+1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "size") {
			t.Fatalf("oversize contract was not rejected explicitly: %v", err)
		}
	})
}

func TestLoadRejectsWorkspacePathSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeContract(t, root, validContractJSON())
	if err := os.Symlink(outside, filepath.Join(root, "features")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("workspace feature path symlink escape accepted")
	}
}

func writeLegacyMarker(t *testing.T, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsStatusDirectorySymlinkEscape(t *testing.T) {
	for _, status := range canonicalStatuses {
		t.Run(status, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			writeContract(t, root, validContractJSON())
			featuresRoot := filepath.Join(root, "features")
			if err := os.MkdirAll(featuresRoot, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(featuresRoot, status)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("status directory symlink escape accepted: %v", err)
			}
		})
	}
}

func writeContract(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, fileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func validContractJSON() string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, CanonicalJSON()); err != nil {
		panic(err)
	}
	return compact.String()
}
