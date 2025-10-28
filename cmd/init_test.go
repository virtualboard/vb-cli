package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
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
		return os.MkdirAll(target, 0o755)
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
		t.Fatalf("expected git clone to be called")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected directory created: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("VirtualBoard project initialised")) {
		t.Fatalf("unexpected output: %s", buf.String())
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
		t.Fatalf("git clone should not be called")
	}
	expected := "VirtualBoard workspace already initialised at .virtualboard. Use --force to re-create it or --update to update it. We recommend managing this directory with git."
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
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("expected original directory removed")
		}
		return os.MkdirAll(filepath.Join(workdir, dest), 0o755)
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
	if err := os.WriteFile(testFile, []byte(testContent), 0o600); err != nil {
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
		if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte(testContent), 0o600); err != nil {
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
				RemoteContent: []byte("updated content\n"),
			},
			setupFile:   true,
			wantExists:  true,
			wantContent: "updated content\n",
		},
		{
			name: "remove file",
			fileDiff: &templatediff.FileDiff{
				Path:   "removed.txt",
				Status: templatediff.FileStatusRemoved,
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
				if err := os.WriteFile(filePath, []byte("old content\n"), 0o600); err != nil {
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
