package cmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/templatediff"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestInitCommandCreatesWorkspace(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("failed to remove workspace: %v", err)
	}
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	target := filepath.Join(fix.Root, initDirName)

	called := false
	original := fetchTemplate
	fetchTemplate = func(workdir, dest string) error {
		called = true
		if workdir != fix.Root {
			t.Fatalf("unexpected workdir: %s", workdir)
		}
		if dest != initDirName {
			t.Fatalf("unexpected dest: %s", dest)
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, "README.md"), []byte("scaffold\n"), 0o644)
	}
	t.Cleanup(func() { fetchTemplate = original })

	cmd := newInitCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !called {
		t.Fatalf("expected pinned template fetch to be called")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected directory created: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("VirtualBoard project initialised")) {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

func TestApplyFileDiffRejectsConcurrentChanges(t *testing.T) {
	tests := []struct {
		name       string
		local      *string
		remote     *string
		concurrent string
	}{
		{name: "added path created", remote: stringPtr("release\n"), concurrent: "user\n"},
		{name: "modified path changed", local: stringPtr("old\n"), remote: stringPtr("release\n"), concurrent: "user\n"},
		{name: "removed path changed", local: stringPtr("old\n"), concurrent: "user\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localRoot := t.TempDir()
			remoteRoot := t.TempDir()
			path := "managed.txt"
			if tt.local != nil {
				if err := os.WriteFile(filepath.Join(localRoot, path), []byte(*tt.local), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tt.remote != nil {
				if err := os.WriteFile(filepath.Join(remoteRoot, path), []byte(*tt.remote), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			diff, err := templatediff.CompareDirectories(localRoot, remoteRoot)
			if err != nil {
				t.Fatal(err)
			}
			fd := diff.GetFileDiff(path)
			if fd == nil {
				t.Fatal("comparison produced no file diff")
			}
			if err := os.WriteFile(filepath.Join(localRoot, path), []byte(tt.concurrent), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := applyFileDiff(localRoot, fd); err == nil || !strings.Contains(err.Error(), "conflict") {
				t.Fatalf("concurrent change was not rejected: %v", err)
			}
			content, err := os.ReadFile(filepath.Join(localRoot, path))
			if err != nil || string(content) != tt.concurrent {
				t.Fatalf("concurrent content was lost: %q, %v", content, err)
			}
		})
	}
}

func TestApplyFileDiffPreservesEditRacingAfterAtomicSourceMove(t *testing.T) {
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()
	path := "managed.txt"
	if err := os.WriteFile(filepath.Join(localRoot, path), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteRoot, path), []byte("release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := templatediff.CompareDirectories(localRoot, remoteRoot)
	if err != nil || len(diff.Modified) != 1 {
		t.Fatalf("comparison = %#v, %v", diff, err)
	}
	originalHook := afterTemplateSourceMoved
	afterTemplateSourceMoved = func(held string) {
		if err := os.WriteFile(held, []byte("concurrent edit\n"), 0o644); err != nil {
			t.Errorf("inject concurrent edit: %v", err)
		}
	}
	t.Cleanup(func() { afterTemplateSourceMoved = originalHook })
	if err := applyFileDiff(localRoot, &diff.Modified[0]); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("racing edit was not reported: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(localRoot, path))
	if err != nil || string(content) != "concurrent edit\n" {
		t.Fatalf("racing edit was lost: %q, %v", content, err)
	}
}

func TestApplyFileDiffRepairsModeOnlyDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()
	localPath := filepath.Join(localRoot, "managed.txt")
	if err := os.WriteFile(localPath, []byte("same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remoteRoot, "managed.txt"), []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := templatediff.CompareDirectories(localRoot, remoteRoot)
	if err != nil || len(diff.Modified) != 1 {
		t.Fatalf("mode-only comparison = %#v, %v", diff, err)
	}
	if err := applyFileDiff(localRoot, &diff.Modified[0]); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(localPath)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode drift was not repaired: %v, %v", info, err)
	}
}

func TestRemovalRequiresPriorManifestDigestAndMode(t *testing.T) {
	localRoot := t.TempDir()
	remoteRoot := t.TempDir()
	path := "retired.txt"
	content := []byte("user customized\n")
	if err := os.WriteFile(filepath.Join(localRoot, path), content, 0o644); err != nil {
		t.Fatal(err)
	}
	makeDiff := func() *templatediff.TemplateDiff {
		diff, err := templatediff.CompareDirectories(localRoot, remoteRoot)
		if err != nil {
			t.Fatal(err)
		}
		return diff
	}
	releasedDigest := sha256.Sum256([]byte("released\n"))
	previous := &templateManifest{Files: map[string]templateFileState{
		path: {SHA256: hex.EncodeToString(releasedDigest[:]), Mode: 0o644},
	}}
	if got := filterRemovalsToPreviouslyManaged(makeDiff(), previous); len(got.Removed) != 0 {
		t.Fatalf("customized retired file was authorized for removal: %#v", got.Removed)
	}
	currentDigest := sha256.Sum256(content)
	previous.Files[path] = templateFileState{SHA256: hex.EncodeToString(currentDigest[:]), Mode: 0o644}
	if got := filterRemovalsToPreviouslyManaged(makeDiff(), previous); len(got.Removed) != 1 {
		t.Fatalf("checksum-matched retired file was not authorized: %#v", got.Removed)
	}
}

func TestSaveTemplateProvenanceRejectsModeDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	root := t.TempDir()
	path := filepath.Join(root, "managed.txt")
	if err := os.WriteFile(path, []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := buildTemplateManifest(root, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveTemplateProvenance(root, "1.0.0", manifest); err == nil || !strings.Contains(err.Error(), "bytes or mode") {
		t.Fatalf("mode-drifted tree advanced provenance: %v", err)
	}
	for _, state := range []string{templateVersionFile, templateManifestFile} {
		if _, err := os.Stat(filepath.Join(root, state)); !os.IsNotExist(err) {
			t.Fatalf("failed provenance save wrote %s: %v", state, err)
		}
	}
}

func stringPtr(value string) *string { return &value }

func TestInitCommandDryRunVerifiesWithoutCreatingWorkspace(t *testing.T) {
	fix := testutil.NewFixture(t)
	target := filepath.Join(fix.Root, initDirName)
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	opts := fix.Options(t, true, false, true)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	called := false
	originalFetch := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		called = true
		return t.TempDir(), nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = originalFetch })

	cmd := newInitCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("dry-run did not verify the pinned template")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dry-run created workspace: %v", err)
	}
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			DryRun  bool `json:"dry_run"`
			Written bool `json:"written"`
		} `json:"data"`
	}
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("parse dry-run JSON: %v\n%s", err, output.String())
	}
	if !payload.Success || !payload.Data.DryRun || payload.Data.Written {
		t.Fatalf("unexpected dry-run payload: %+v", payload)
	}
}

func TestFetchTemplateToTempDirUsesAbsentDestination(t *testing.T) {
	originalFetch := fetchTemplate
	fetchTemplate = func(_, destination string) error {
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			return fmt.Errorf("temporary destination already exists: %v", err)
		}
		if err := os.Mkdir(destination, 0o750); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "marker"), []byte("verified\n"), 0o644)
	}
	t.Cleanup(func() { fetchTemplate = originalFetch })

	destination, err := fetchTemplateToTempDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(destination) })
	content, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil || string(content) != "verified\n" {
		t.Fatalf("temporary fetch marker = %q, %v", content, err)
	}
}

func TestInitCommandWarnsWhenExists(t *testing.T) {
	fix := testutil.NewFixture(t)
	target := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	called := false
	original := fetchTemplate
	fetchTemplate = func(workdir, dest string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { fetchTemplate = original })

	cmd := newInitCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when workspace exists")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Fatalf("expected validation exit code, got %v", err)
	}
	if called {
		t.Fatalf("template fetch should not be called")
	}
	expected := "VirtualBoard workspace already initialised at .virtualboard. Use --force to refresh framework files safely or --update to review changes. We recommend managing this directory with git."
	if err.Error() != expected {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestInitCommandForceRecreates(t *testing.T) {
	fix := testutil.NewFixture(t)
	target := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	file := filepath.Join(target, "stale.txt")
	if err := os.WriteFile(file, []byte("stale"), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	original := fetchTemplate
	fetchTemplate = func(workdir, dest string) error {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("force must not delete the existing workspace before a complete replacement is ready: %v", err)
		}
		return nil
	}
	t.Cleanup(func() { fetchTemplate = original })

	cmd := newInitCommand()
	cmd.Flags().Set("force", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	projectRoot := filepath.Dir(opts.RootDir)
	if _, err := os.Stat(filepath.Join(projectRoot, initDirName)); err != nil {
		t.Fatalf("expected directory recreated: %v", err)
	}
}

// Helper function to mock template version fetching in tests
func mockTemplateVersion(t *testing.T) {
	t.Helper()
	originalVersion := fetchTemplateVersionVar
	fetchTemplateVersionVar = func() (string, error) {
		return "0.0.1", nil
	}
	t.Cleanup(func() { fetchTemplateVersionVar = originalVersion })
}

func TestInitCommandUpdate_NoWorkspace(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("failed to remove workspace: %v", err)
	}

	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	cmd.Flags().Set("yes", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when updating non-existent workspace")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Fatalf("expected validation exit code, got %v", err)
	}
}

func TestInitCommandUpdate_NoChanges(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Create a test file
	testFile := filepath.Join(workspace, "README.md")
	testContent := "test content\n"
	if err := os.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock fetchTemplateToTempDir to return a temp directory with same content
	original := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		tempDir := t.TempDir()
		// Create same structure as local
		if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte(testContent), 0o644); err != nil {
			return "", err
		}
		return tempDir, nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = original })

	mockTemplateVersion(t)

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify file is unchanged
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != testContent {
		t.Errorf("file content should be unchanged if template matches, got: %s", string(content))
	}
}

func TestInitCommandUpdate_WithChanges(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Create existing file
	testFile := filepath.Join(workspace, "existing.txt")
	if err := os.WriteFile(testFile, []byte("old content\n"), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true // Automatic mode
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	mockTemplateVersion(t)

	// Mock fetchTemplateToTempDir
	original := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		tempDir := t.TempDir()
		// Modified file
		if err := os.WriteFile(filepath.Join(tempDir, "existing.txt"), []byte("new content\n"), 0o600); err != nil {
			return "", err
		}
		// New file
		if err := os.WriteFile(filepath.Join(tempDir, "new.txt"), []byte("added file\n"), 0o600); err != nil {
			return "", err
		}
		return tempDir, nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = original })

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	cmd.Flags().Set("yes", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify files were updated
	modifiedContent, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read modified file: %v", err)
	}
	if string(modifiedContent) != "new content\n" {
		t.Errorf("expected file to be updated, got: %s", string(modifiedContent))
	}

	// Verify new file was added
	newFile := filepath.Join(workspace, "new.txt")
	newContent, err := os.ReadFile(newFile)
	if err != nil {
		t.Fatalf("failed to read new file: %v", err)
	}
	if string(newContent) != "added file\n" {
		t.Errorf("expected new file content, got: %s", string(newContent))
	}
}

func TestInitCommandUpdate_WithFileFilter(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Create two files
	file1 := filepath.Join(workspace, "file1.txt")
	file2 := filepath.Join(workspace, "file2.txt")
	if err := os.WriteFile(file1, []byte("old1\n"), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if err := os.WriteFile(file2, []byte("old2\n"), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	mockTemplateVersion(t)

	// Mock fetchTemplateToTempDir
	original := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		tempDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tempDir, "file1.txt"), []byte("new1\n"), 0o600); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(tempDir, "file2.txt"), []byte("new2\n"), 0o600); err != nil {
			return "", err
		}
		return tempDir, nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = original })

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	cmd.Flags().Set("files", "file1.txt")
	cmd.Flags().Set("yes", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify only file1 was updated
	content1, _ := os.ReadFile(file1)
	if string(content1) != "new1\n" {
		t.Errorf("expected file1 to be updated, got: %s", string(content1))
	}

	content2, _ := os.ReadFile(file2)
	if string(content2) != "old2\n" {
		t.Errorf("expected file2 to remain unchanged, got: %s", string(content2))
	}
}

func TestInitCommandUpdateAddsDotfilesAndIntegrationSourcesWithoutTouchingFeatures(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	featurePath := filepath.Join(workspace, "features", "in-progress", "FTR-0042-user-work.md")
	if err := os.WriteFile(featurePath, []byte("user feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := fix.Options(t, true, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })
	mockTemplateVersion(t)

	original := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		remote := t.TempDir()
		for rel, content := range map[string]string{
			".vb-version":                                "v0.10.0\n",
			"docs/.cursor/rules/virtualboard.mdc":        "cursor\n",
			"docs/.opencode/skill/virtualboard/SKILL.md": "opencode\n",
			"features/in-progress/FTR-9999-history.md":   "template history\n",
		} {
			path := filepath.Join(remote, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return "", err
			}
		}
		return remote, nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = original })

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	cmd.Flags().Set("yes", "true")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("vb init --update: %v", err)
	}

	for _, rel := range []string{
		".vb-version",
		"docs/.cursor/rules/virtualboard.mdc",
		"docs/.opencode/skill/virtualboard/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(rel))); err != nil {
			t.Errorf("update did not add %s: %v", rel, err)
		}
	}
	if content, err := os.ReadFile(featurePath); err != nil || string(content) != "user feature\n" {
		t.Fatalf("update changed user feature: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "features", "in-progress", "FTR-9999-history.md")); !os.IsNotExist(err) {
		t.Fatalf("update copied template feature history: %v", err)
	}
}

func TestInitCommandUpdate_DryRun(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	testFile := filepath.Join(workspace, "test.txt")
	oldContent := "old content\n"
	if err := os.WriteFile(testFile, []byte(oldContent), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := fix.Options(t, false, false, true) // dry-run = true
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	mockTemplateVersion(t)

	// Mock fetchTemplateToTempDir
	original := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		tempDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(tempDir, "test.txt"), []byte("new content\n"), 0o600); err != nil {
			return "", err
		}
		return tempDir, nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = original })

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify file was NOT modified
	content, _ := os.ReadFile(testFile)
	if string(content) != oldContent {
		t.Errorf("expected file to remain unchanged in dry-run, got: %s", string(content))
	}

	if !strings.Contains(buf.String(), "Dry run") {
		t.Errorf("expected dry run message, got: %s", buf.String())
	}
}

func TestInitUpdateJSONRequiresExplicitYesAndWritesNothing(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.Remove(filepath.Join(workspace, templateVersionFile)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(workspace, "README.md")
	if _, err := os.Stat(filepath.Join(workspace, templateVersionFile)); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly contains template provenance before update: %v", err)
	}
	if err := os.WriteFile(target, []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := fix.Options(t, true, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })
	mockTemplateVersion(t)
	originalFetch := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		remote := t.TempDir()
		return remote, os.WriteFile(filepath.Join(remote, "README.md"), []byte("remote\n"), 0o600)
	}
	t.Cleanup(func() { fetchTemplateToTempDir = originalFetch })

	cmd := newInitCommand()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.Flags().Set("update", "true")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("JSON update without consent = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "local\n" {
		t.Fatalf("JSON update without consent wrote target: %q, %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, templateVersionFile)); !os.IsNotExist(err) {
		t.Fatalf("JSON update without consent advanced provenance: %v", err)
	}
}

func TestInitUpdateFilteredNoopDoesNotAdvanceFullTemplateProvenance(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	file1 := filepath.Join(workspace, "file1.txt")
	file2 := filepath.Join(workspace, "file2.txt")
	if err := os.WriteFile(file1, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prior, err := buildTemplateManifest(workspace, "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := saveTemplateProvenance(workspace, "0.0.0", prior); err != nil {
		t.Fatal(err)
	}
	opts := fix.Options(t, true, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })
	mockTemplateVersion(t)
	originalFetch := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		remote := t.TempDir()
		if err := os.WriteFile(filepath.Join(remote, "file1.txt"), []byte("same\n"), 0o600); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(remote, "file2.txt"), []byte("new\n"), 0o600); err != nil {
			return "", err
		}
		return remote, nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = originalFetch })

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	cmd.Flags().Set("files", "file1.txt")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	active, err := readTemplateVersion(workspace)
	if err != nil || active != "0.0.0" {
		t.Fatalf("filtered no-op advanced full version: %q, %v", active, err)
	}
	manifest, err := readTemplateManifest(workspace)
	if err != nil || manifest.TemplateVersion != "0.0.0" {
		t.Fatalf("filtered no-op replaced prior inventory: %#v, %v", manifest, err)
	}
	if content, _ := os.ReadFile(file2); string(content) != "old\n" {
		t.Fatalf("filtered no-op changed unselected file: %q", content)
	}
}

func TestInitUpdateNeverDeletesUnmanagedLocalFile(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	custom := filepath.Join(workspace, "custom-local.txt")
	if err := os.WriteFile(custom, []byte("user-owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := fix.Options(t, true, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })
	mockTemplateVersion(t)
	originalFetch := fetchTemplateToTempDir
	fetchTemplateToTempDir = func() (string, error) {
		remote := t.TempDir()
		if err := os.WriteFile(filepath.Join(remote, "README.md"), []byte("managed\n"), 0o600); err != nil {
			return "", err
		}
		return remote, nil
	}
	t.Cleanup(func() { fetchTemplateToTempDir = originalFetch })

	cmd := newInitCommand()
	cmd.Flags().Set("update", "true")
	cmd.Flags().Set("yes", "true")
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(custom); err != nil || string(content) != "user-owned\n" {
		t.Fatalf("unmanaged file was removed or changed: %q, %v", content, err)
	}
}

func TestInitUpdateRemovesOnlyChecksumMatchedRetiredV07File(t *testing.T) {
	for _, modified := range []bool{false, true} {
		t.Run(map[bool]string{false: "released-bytes", true: "user-modified"}[modified], func(t *testing.T) {
			fix := testutil.NewFixture(t)
			workspace := filepath.Join(fix.Root, initDirName)
			legacyPath := filepath.Join(workspace, ".claude-plugin", "plugin.json")
			if err := os.MkdirAll(filepath.Dir(legacyPath), 0o750); err != nil {
				t.Fatal(err)
			}
			content := []byte("released v0.7 plugin\n")
			if modified {
				content = []byte("user customization\n")
			}
			if err := os.WriteFile(legacyPath, content, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := saveTemplateVersion(workspace, "0.7.0"); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte("released v0.7 plugin\n"))
			originalLegacy := legacyRetiredFiles
			legacyRetiredFiles = map[string]templateFileState{
				".claude-plugin/plugin.json": {SHA256: hex.EncodeToString(digest[:]), Mode: 0o644},
			}
			t.Cleanup(func() { legacyRetiredFiles = originalLegacy })
			opts := fix.Options(t, true, false, false)
			config.SetCurrent(opts)
			t.Cleanup(func() { config.SetCurrent(nil) })
			originalFetch := fetchTemplateToTempDir
			fetchTemplateToTempDir = func() (string, error) {
				remote := t.TempDir()
				return remote, os.WriteFile(filepath.Join(remote, "README.md"), []byte("v0.8\n"), 0o644)
			}
			t.Cleanup(func() { fetchTemplateToTempDir = originalFetch })
			cmd := newInitCommand()
			cmd.Flags().Set("update", "true")
			cmd.Flags().Set("yes", "true")
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(legacyPath)
			if modified && err != nil {
				t.Fatalf("user-modified retired path was removed: %v", err)
			}
			if !modified && !os.IsNotExist(err) {
				t.Fatalf("checksum-matched retired path was not removed: %v", err)
			}
			if !modified {
				matches, globErr := filepath.Glob(filepath.Join(workspace, ".state", "template-retired", "change-*", ".claude-plugin", "plugin.json"))
				if globErr != nil || len(matches) != 1 {
					t.Fatalf("retired file recovery inventory = %v, %v", matches, globErr)
				}
				backupContent, backupErr := os.ReadFile(matches[0])
				if backupErr != nil || !bytes.Equal(backupContent, content) {
					t.Fatalf("retired file recovery copy = %q, %v", backupContent, backupErr)
				}
			}
		})
	}
}

func TestInitUpdateRejectsSymlinkedLocalProvenanceWithoutLeakingTarget(t *testing.T) {
	for _, stateFile := range []string{templateVersionFile, templateManifestFile} {
		t.Run(stateFile, func(t *testing.T) {
			fix := testutil.NewFixture(t)
			workspace := filepath.Join(fix.Root, initDirName)
			secret := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(secret, []byte("super-secret-value"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(secret, filepath.Join(workspace, stateFile)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			opts := fix.Options(t, true, false, true)
			config.SetCurrent(opts)
			t.Cleanup(func() { config.SetCurrent(nil) })
			cmd := newInitCommand()
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.Flags().Set("update", "true")
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			if err := cmd.Execute(); err == nil {
				t.Fatal("symlinked template provenance was accepted")
			}
			if strings.Contains(output.String(), "super-secret-value") {
				t.Fatal("symlink target content leaked in update output")
			}
		})
	}
}

func TestInitUpdateRejectsSymlinkedWorkspaceRoot(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, true, false, true)
	workspace := filepath.Join(fix.Root, initDirName)
	external := filepath.Join(t.TempDir(), "external-workspace")
	if err := os.Rename(workspace, external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, workspace); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })
	cmd := newInitCommand()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.Flags().Set("update", "true")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlinked template workspace root error = %v", err)
	}
}

func TestSaveAndReadTemplateVersion(t *testing.T) {
	tempDir := t.TempDir()
	version := "v1.2.3"

	// Test save
	if err := saveTemplateVersion(tempDir, version); err != nil {
		t.Fatalf("saveTemplateVersion() error = %v", err)
	}

	// Test read
	read, err := readTemplateVersion(tempDir)
	if err != nil {
		t.Fatalf("readTemplateVersion() error = %v", err)
	}

	if read != version {
		t.Errorf("readTemplateVersion() = %v, want %v", read, version)
	}
}

func TestReadTemplateVersion_NotExists(t *testing.T) {
	tempDir := t.TempDir()

	version, err := readTemplateVersion(tempDir)
	if err != nil {
		t.Fatalf("readTemplateVersion() error = %v, want nil for non-existent file", err)
	}

	if version != "" {
		t.Errorf("readTemplateVersion() = %v, want empty string", version)
	}
}

func TestFilterDiff(t *testing.T) {
	// Mock diff with multiple files
	diff := &templatediff.TemplateDiff{
		Added: []templatediff.FileDiff{
			{Path: "new1.txt"},
			{Path: "new2.txt"},
		},
		Modified: []templatediff.FileDiff{
			{Path: "mod1.txt"},
			{Path: "mod2.txt"},
		},
		Removed: []templatediff.FileDiff{
			{Path: "del1.txt"},
		},
	}

	// Filter to only include specific files
	filtered := filterDiff(diff, []string{"new1.txt", "mod2.txt"})

	if len(filtered.Added) != 1 || filtered.Added[0].Path != "new1.txt" {
		t.Errorf("expected 1 added file (new1.txt), got %d", len(filtered.Added))
	}

	if len(filtered.Modified) != 1 || filtered.Modified[0].Path != "mod2.txt" {
		t.Errorf("expected 1 modified file (mod2.txt), got %d", len(filtered.Modified))
	}

	if len(filtered.Removed) != 0 {
		t.Errorf("expected 0 removed files, got %d", len(filtered.Removed))
	}
}

func TestCollectFilePaths(t *testing.T) {
	diff := &templatediff.TemplateDiff{
		Added: []templatediff.FileDiff{
			{Path: "added.txt"},
		},
		Modified: []templatediff.FileDiff{
			{Path: "modified.txt"},
		},
		Removed: []templatediff.FileDiff{
			{Path: "removed.txt"},
		},
	}

	paths := collectFilePaths(diff)

	if len(paths) != 3 {
		t.Errorf("expected 3 paths, got %d", len(paths))
	}

	expected := map[string]bool{
		"added.txt":    true,
		"modified.txt": true,
		"removed.txt":  true,
	}

	for _, path := range paths {
		if !expected[path] {
			t.Errorf("unexpected path: %s", path)
		}
	}
}

func TestApplyFileDiff(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		fileDiff    *templatediff.FileDiff
		setupFile   bool
		wantExists  bool
		wantContent string
	}{
		{
			name: "add new file",
			fileDiff: &templatediff.FileDiff{
				Path:          "new.txt",
				Status:        templatediff.FileStatusAdded,
				RemoteContent: []byte("new content\n"),
				RemoteMode:    0o644,
			},
			setupFile:   false,
			wantExists:  true,
			wantContent: "new content\n",
		},
		{
			name: "modify existing file",
			fileDiff: &templatediff.FileDiff{
				Path:          "modified.txt",
				Status:        templatediff.FileStatusModified,
				LocalContent:  []byte("old content\n"),
				RemoteContent: []byte("updated content\n"),
				LocalMode:     0o644,
				RemoteMode:    0o644,
			},
			setupFile:   true,
			wantExists:  true,
			wantContent: "updated content\n",
		},
		{
			name: "remove file",
			fileDiff: &templatediff.FileDiff{
				Path:         "removed.txt",
				Status:       templatediff.FileStatusRemoved,
				LocalContent: []byte("old content\n"),
				LocalMode:    0o644,
			},
			setupFile:  true,
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(tempDir, tt.fileDiff.Path)

			// Setup: create file if needed
			if tt.setupFile {
				if err := os.WriteFile(filePath, []byte("old content\n"), 0o644); err != nil {
					t.Fatalf("setup failed: %v", err)
				}
			}

			// Apply the diff
			if err := applyFileDiff(tempDir, tt.fileDiff); err != nil {
				t.Fatalf("applyFileDiff() error = %v", err)
			}

			// Verify result
			_, err := os.Stat(filePath)
			exists := err == nil

			if exists != tt.wantExists {
				t.Errorf("file exists = %v, want %v", exists, tt.wantExists)
			}

			if tt.wantExists && tt.wantContent != "" {
				content, err := os.ReadFile(filePath)
				if err != nil {
					t.Fatalf("failed to read file: %v", err)
				}
				if string(content) != tt.wantContent {
					t.Errorf("file content = %q, want %q", string(content), tt.wantContent)
				}
			}
		})
	}
}

func TestApplyFileDiffPreservesFrameworkExecutableModes(t *testing.T) {
	tempDir := t.TempDir()
	diff := &templatediff.FileDiff{
		Path:          "scripts/check.sh",
		Status:        templatediff.FileStatusAdded,
		RemoteContent: []byte("#!/bin/sh\n"),
		RemoteMode:    0o755,
	}
	if err := applyFileDiff(tempDir, diff); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(tempDir, "scripts", "check.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !templateExecutableModeIsValid(info.Mode()) {
		t.Fatalf("framework script is not executable: %o", info.Mode().Perm())
	}
}

func TestShouldSkipTemplateFile(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantSkip bool
	}{
		{name: "preserve dotfile in root", path: ".pre-commit-config.yaml", wantSkip: false},
		{name: "skip unlisted top-level directory", path: "subdir/.gitignore", wantSkip: true},
		{name: "preserve dotdirectory", path: ".github/workflows/test.yml", wantSkip: false},
		{name: "preserve docs root file", path: "docs/README.md", wantSkip: false},
		{name: "preserve docs integration", path: "docs/.cursor/rules/virtualboard.mdc", wantSkip: false},
		{name: "skip git metadata", path: ".git/objects/abc", wantSkip: true},
		{name: "skip runtime state", path: ".state/audit.jsonl", wantSkip: true},
		{name: "skip template feature history", path: "features/in-progress/FTR-0001-example.md", wantSkip: true},
		{name: "skip generated feature index", path: "features/INDEX.md", wantSkip: true},
		{
			name:     "allow normal file",
			path:     "README.md",
			wantSkip: false,
		},
		{
			name:     "skip unlisted root file",
			path:     "schema.json",
			wantSkip: true,
		},
		{
			name:     "allow feature directory placeholder",
			path:     "features/backlog/.gitkeep",
			wantSkip: false,
		},
		{
			name:     "allow templates folder",
			path:     "templates/feature.md",
			wantSkip: false,
		},
		{name: "skip generated template-version dotfile", path: ".template-version", wantSkip: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipTemplateFile(tt.path)
			if got != tt.wantSkip {
				t.Errorf("shouldSkipTemplateFile(%q) = %v, want %v", tt.path, got, tt.wantSkip)
			}
		})
	}
}

func makePinnedTemplateArchive(t *testing.T, version string, omit map[string]bool) []byte {
	t.Helper()
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"version.txt":                                       {version + "\n", 0o644},
		".vb-version":                                       {"v0.10.0\n", 0o644},
		"virtualboard.json":                                 {string(contract.CanonicalJSON()), 0o644},
		"bin/vb-root":                                       {"#!/bin/sh\n", 0o755},
		"scripts/install-vb-cli.sh":                         {"#!/bin/sh\n", 0o755},
		"templates/feature.md":                              {pinnedTestFeatureTemplate, 0o644},
		"schemas/frontmatter.schema.json":                   {pinnedTestFeatureSchema, 0o644},
		"schemas/system-spec.schema.json":                   {pinnedTestSystemSpecSchema, 0o644},
		"docs/.cursor/rules/virtualboard.mdc":               {"cursor-rule\n", 0o644},
		"docs/.opencode/skill/virtualboard/SKILL.md":        {"opencode-skill\n", 0o644},
		"features/backlog/.gitkeep":                         {"", 0o644},
		"features/in-progress/.gitkeep":                     {"", 0o644},
		"features/blocked/.gitkeep":                         {"", 0o644},
		"features/review/.gitkeep":                          {"", 0o644},
		"features/done/.gitkeep":                            {"", 0o644},
		"features/INDEX.md":                                 {"template repository history\n", 0o644},
		"features/in-progress/FTR-0001-template-history.md": {"must not be scaffolded\n", 0o644},
		"README.md":                                         {"pinned scaffold\n", 0o644},
		".gitignore":                                        {".state/\n", 0o644},
		"developer-scratch.txt":                             {"must not be scaffolded\n", 0o644},
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	root := "template-base-" + templateRelease
	for rel, file := range files {
		if omit[rel] {
			continue
		}
		header := &zip.FileHeader{Name: root + "/" + rel, Method: zip.Deflate}
		header.SetMode(file.mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("create archive entry %s: %v", rel, err)
		}
		if _, err := entry.Write([]byte(file.content)); err != nil {
			t.Fatalf("write archive entry %s: %v", rel, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close template archive: %v", err)
	}
	return buffer.Bytes()
}

const pinnedTestFeatureTemplate = `---
id: TEMPLATE
title: Template Feature
status: backlog
owner: unassigned
implementation_owner: unassigned
priority: P2
complexity: M
created: 2026-01-01
updated: 2026-01-01
status_changed: 2026-01-01
labels: []
dependencies: []
risk_notes: ""
---

## Summary
`

const pinnedTestFeatureSchema = `{
  "$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":false,
  "required":["id","title","status","owner","implementation_owner","priority","complexity","created","updated","status_changed","labels","dependencies","risk_notes"],
  "properties":{
    "id":{"type":"string"},"title":{"type":"string"},"status":{"type":"string"},"owner":{"type":"string"},
    "implementation_owner":{"type":"string"},"priority":{"type":"string"},"complexity":{"type":"string"},
    "created":{"type":"string"},"updated":{"type":"string"},"status_changed":{"type":"string"},
    "labels":{"type":"array"},"dependencies":{"type":"array"},"epic":{"type":"string"},"risk_notes":{"type":"string"}
  }
}
`

const pinnedTestSystemSpecSchema = `{
  "$schema":"http://json-schema.org/draft-07/schema#","type":"object","additionalProperties":false,
  "required":["spec_type","title","status","last_updated","applicability"],
  "properties":{
    "spec_type":{"type":"string"},"title":{"type":"string"},"status":{"type":"string"},
    "last_updated":{"type":"string"},"applicability":{"type":"array"},"owner":{"type":"string"},"related_initiatives":{"type":"array"}
  }
}
`

func mockPinnedTemplateDownload(t *testing.T, archive []byte) {
	t.Helper()
	original := templateHTTPGet
	originalChecksum := templateArchiveSHA256
	digest := sha256.Sum256(archive)
	templateArchiveSHA256 = hex.EncodeToString(digest[:])
	templateHTTPGet = func(url string) (*http.Response, error) {
		if url != templateZipURL {
			t.Fatalf("download URL = %q, want exact pinned URL %q", url, templateZipURL)
		}
		if strings.Contains(url, "/heads/main") {
			t.Fatalf("moving main branch must never be downloaded: %s", url)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(archive)),
		}, nil
	}
	t.Cleanup(func() {
		templateHTTPGet = original
		templateArchiveSHA256 = originalChecksum
	})
}

func TestFetchTemplateUsesPinnedCompleteScaffold(t *testing.T) {
	projectRoot := t.TempDir()
	rootIgnore := filepath.Join(projectRoot, ".gitignore")
	if err := os.WriteFile(rootIgnore, []byte("application-ignore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mockPinnedTemplateDownload(t, makePinnedTemplateArchive(t, templateVersion, nil))

	if err := fetchTemplate(projectRoot, initDirName); err != nil {
		t.Fatalf("fetchTemplate() error = %v", err)
	}
	workspace := filepath.Join(projectRoot, initDirName)
	for _, rel := range []string{
		".vb-version",
		"docs/.cursor/rules/virtualboard.mdc",
		"docs/.opencode/skill/virtualboard/SKILL.md",
		"features/backlog/.gitkeep",
	} {
		if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected scaffold file %s: %v", rel, err)
		}
	}
	for _, rel := range []string{"features/in-progress/FTR-0001-template-history.md", "developer-scratch.txt"} {
		if _, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Errorf("template repository history %s must not be scaffolded", rel)
		}
	}
	indexContent, err := os.ReadFile(filepath.Join(workspace, "features", "INDEX.md"))
	if err != nil {
		t.Fatalf("clean feature index missing: %v", err)
	}
	if strings.Contains(string(indexContent), "FTR-0001") || !strings.Contains(string(indexContent), "**Total**: 0 features") {
		t.Fatalf("feature index was not reset to an empty board: %s", indexContent)
	}
	for _, rel := range []string{"bin/vb-root", "scripts/install-vb-cli.sh"} {
		info, err := os.Stat(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("stat executable %s: %v", rel, err)
		}
		if !templateExecutableModeIsValid(info.Mode()) {
			t.Errorf("%s lost executable mode: %o", rel, info.Mode().Perm())
		}
	}
	rootContent, err := os.ReadFile(rootIgnore)
	if err != nil {
		t.Fatal(err)
	}
	if string(rootContent) != "application-ignore\n" {
		t.Fatalf("application root ignore file was changed: %q", rootContent)
	}
}

func TestFetchTemplateRejectsVersionMismatchWithoutPartialWorkspace(t *testing.T) {
	projectRoot := t.TempDir()
	mockPinnedTemplateDownload(t, makePinnedTemplateArchive(t, "9.9.9", nil))

	err := fetchTemplate(projectRoot, initDirName)
	if err == nil || !strings.Contains(err.Error(), "version mismatch") {
		t.Fatalf("expected version mismatch, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(projectRoot, initDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("partial workspace was left behind: %v", statErr)
	}
}

func TestFetchTemplateRejectsChecksumMismatchWithoutPartialWorkspace(t *testing.T) {
	projectRoot := t.TempDir()
	archive := makePinnedTemplateArchive(t, templateVersion, nil)
	mockPinnedTemplateDownload(t, archive)
	templateArchiveSHA256 = strings.Repeat("0", sha256.Size*2)

	err := fetchTemplate(projectRoot, initDirName)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(projectRoot, initDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("checksum failure left a workspace behind: %v", statErr)
	}
}

func TestFetchTemplateRequiresReleaseDigest(t *testing.T) {
	projectRoot := t.TempDir()
	archive := makePinnedTemplateArchive(t, templateVersion, nil)
	mockPinnedTemplateDownload(t, archive)
	templateArchiveSHA256 = ""

	err := fetchTemplate(projectRoot, initDirName)
	if err == nil || !strings.Contains(err.Error(), "no compiled SHA-256 digest") {
		t.Fatalf("expected missing release digest failure, got %v", err)
	}
}

func TestFetchTemplateRejectsIncompleteArchiveWithoutPartialWorkspace(t *testing.T) {
	projectRoot := t.TempDir()
	mockPinnedTemplateDownload(t, makePinnedTemplateArchive(t, templateVersion, map[string]bool{
		"docs/.opencode/skill/virtualboard/SKILL.md": true,
	}))

	err := fetchTemplate(projectRoot, initDirName)
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("expected incomplete archive error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(projectRoot, initDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("partial workspace was left behind: %v", statErr)
	}
}

func TestFetchTemplateRefreshPreservesUserWorkspaceData(t *testing.T) {
	projectRoot := t.TempDir()
	workspace := filepath.Join(projectRoot, initDirName)
	preserved := map[string]string{
		"audit.jsonl":                                "legacy audit chain\n",
		"archive/2025/FTR-0007-archived.md":          "archived feature\n",
		"features/in-progress/FTR-0042-user-work.md": "feature\n",
		"features/INDEX.md":                          "user index\n",
		"specs/tech-stack.md":                        "spec\n",
		"reports/evidence.md":                        "report\n",
		".state/audit.jsonl":                         "audit\n",
	}
	for rel, content := range preserved {
		path := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stale := filepath.Join(workspace, "stale-framework-file.txt")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	mockPinnedTemplateDownload(t, makePinnedTemplateArchive(t, templateVersion, nil))

	if err := fetchTemplate(projectRoot, initDirName); err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	for rel, expected := range preserved {
		content, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil || string(content) != expected {
			t.Errorf("preserved path %s = %q, %v; want %q", rel, content, err, expected)
		}
	}
	if content, err := os.ReadFile(stale); err != nil || string(content) != "stale" {
		t.Fatalf("unmanaged local file was removed by force refresh: %q, %v", content, err)
	}
	readme, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil || string(readme) != "pinned scaffold\n" {
		t.Fatalf("framework was not refreshed: %q, %v", readme, err)
	}
}

func TestTemplateURLIsImmutableRelease(t *testing.T) {
	wantSuffix := "/releases/download/" + templateRelease + "/" + templateArchiveName
	if !strings.HasSuffix(templateZipURL, wantSuffix) {
		t.Fatalf("template URL is not the stable %s release asset: %s", templateRelease, templateZipURL)
	}
	if strings.Contains(templateZipURL, "/archive/") || strings.Contains(templateZipURL, "/heads/") || strings.Contains(templateZipURL, "/main") {
		t.Fatalf("template URL uses a moving branch: %s", templateZipURL)
	}
}

func TestFetchTemplateHTTPFailureLeavesNoWorkspace(t *testing.T) {
	projectRoot := t.TempDir()
	original := templateHTTPGet
	templateHTTPGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("missing")),
		}, nil
	}
	t.Cleanup(func() { templateHTTPGet = original })

	err := fetchTemplate(projectRoot, initDirName)
	if err == nil || !strings.Contains(err.Error(), "pinned template") {
		t.Fatalf("expected pinned-template HTTP failure, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(projectRoot, initDirName)); !os.IsNotExist(statErr) {
		t.Fatalf("HTTP failure left a workspace behind: %v", statErr)
	}
}

func makeArchiveWithEntries(t *testing.T, entries map[string]os.FileMode) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, mode := range entries {
		header := &zip.FileHeader{Name: name}
		header.SetMode(mode)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("content")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func archiveReader(t *testing.T, archiveBytes []byte) *zip.Reader {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestExtractTemplateArchiveRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name    string
		entries map[string]os.FileMode
	}{
		{
			name: "path traversal",
			entries: map[string]os.FileMode{
				"template-base/../../escape": 0o644,
			},
		},
		{
			name: "backslash traversal",
			entries: map[string]os.FileMode{
				"template-base/..\\escape": 0o644,
			},
		},
		{
			name: "multiple archive roots",
			entries: map[string]os.FileMode{
				"template-one/a": 0o644,
				"template-two/b": 0o644,
			},
		},
		{
			name: "symbolic link",
			entries: map[string]os.FileMode{
				"template-base/link": os.ModeSymlink | 0o777,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "stage")
			if err := os.MkdirAll(target, 0o755); err != nil {
				t.Fatal(err)
			}
			err := extractTemplateArchive(archiveReader(t, makeArchiveWithEntries(t, tt.entries)), target)
			if err == nil {
				t.Fatal("expected unsafe archive entry to be rejected")
			}
		})
	}
}

func TestExtractTemplateArchiveRejectsEntryCountAndDepthExplosion(t *testing.T) {
	deep := "template-base/" + strings.Repeat("d/", maxTemplateArchiveDepth) + "file"
	if err := extractTemplateArchive(archiveReader(t, makeArchiveWithEntries(t, map[string]os.FileMode{deep: 0o644})), t.TempDir()); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("over-deep archive entry was accepted: %v", err)
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for i := 0; i <= maxTemplateArchiveEntries; i++ {
		entry, err := writer.Create(fmt.Sprintf("template-base/files/%05d", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractTemplateArchive(archiveReader(t, buffer.Bytes()), t.TempDir()); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("entry-count explosion was accepted: %v", err)
	}
}
