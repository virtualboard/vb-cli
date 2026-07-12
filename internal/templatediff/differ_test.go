package templatediff

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompareDirectoriesDetectsModeOnlyDrift(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}
	local := t.TempDir()
	remote := t.TempDir()
	localPath := filepath.Join(local, "tool.txt")
	remotePath := filepath.Join(remote, "tool.txt")
	writeFile(t, localPath, "same\n")
	writeFile(t, remotePath, "same\n")
	if err := os.Chmod(localPath, 0o600); err != nil {
		t.Fatal(err)
	}

	diff, err := CompareDirectories(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Modified) != 1 || diff.Modified[0].LocalMode.Perm() != 0o600 || diff.Modified[0].RemoteMode.Perm() != 0o644 {
		t.Fatalf("mode-only drift was not represented: %#v", diff.Modified)
	}
	if !strings.Contains(diff.Modified[0].UnifiedDiff, "mode 0600 -> 0644") {
		t.Fatalf("mode-only diff is not visible: %q", diff.Modified[0].UnifiedDiff)
	}
}

func TestCompareDirectoriesRejectsOversizedLocalFile(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	path := filepath.Join(local, "unmanaged-large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxComparisonFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := CompareDirectories(local, remote); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized local file was accepted: %v", err)
	}
}

func TestCollectFilesStreamsAndBoundsDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	original := comparisonEntryLimit
	comparisonEntryLimit = 3
	t.Cleanup(func() { comparisonEntryLimit = original })
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(root, fmt.Sprintf("file-%d.txt", i)), "x")
	}
	if _, err := collectFiles(root); err == nil || !strings.Contains(err.Error(), "exceeds 3 entries") {
		t.Fatalf("entry-bounded collection error = %v", err)
	}
}

func TestCollectFilesRejectsSymlinkRoot(t *testing.T) {
	external := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "linked")
	if err := os.Symlink(external, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := collectFiles(root); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlink-root collection error = %v", err)
	}
}

func TestCompareFilesOmitsResourceIntensiveUnifiedDiff(t *testing.T) {
	root := t.TempDir()
	local := filepath.Join(root, "local.txt")
	remote := filepath.Join(root, "remote.txt")
	if err := os.WriteFile(local, bytes.Repeat([]byte("a"), maxUnifiedDiffInputBytes/2+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(remote, bytes.Repeat([]byte("b"), maxUnifiedDiffInputBytes/2+1), 0o600); err != nil {
		t.Fatal(err)
	}
	diff, err := CompareFiles(local, remote, "large.txt")
	if err != nil {
		t.Fatal(err)
	}
	if diff.Status != FileStatusModified || !strings.Contains(diff.UnifiedDiff, "unified diff omitted") || len(diff.UnifiedDiff) > 512 {
		t.Fatalf("resource-bounded unified diff = status %s, %q", diff.Status, diff.UnifiedDiff)
	}
}

func TestCompareDirectories(t *testing.T) {
	// Create temp directories for testing
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	// Setup test files
	// Unchanged file
	writeFile(t, filepath.Join(localDir, "unchanged.txt"), "same content\n")
	writeFile(t, filepath.Join(remoteDir, "unchanged.txt"), "same content\n")

	// Modified file
	writeFile(t, filepath.Join(localDir, "modified.txt"), "old content\n")
	writeFile(t, filepath.Join(remoteDir, "modified.txt"), "new content\n")

	// Added file (only in remote)
	writeFile(t, filepath.Join(remoteDir, "added.txt"), "new file\n")

	// Removed file (only in local)
	writeFile(t, filepath.Join(localDir, "removed.txt"), "old file\n")

	// Feature files (should be ignored)
	featureDir := filepath.Join(localDir, "features", "backlog")
	if err := os.MkdirAll(featureDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(featureDir, "FEAT-001-test.md"), "feature content\n")

	featureDirRemote := filepath.Join(remoteDir, "features", "backlog")
	if err := os.MkdirAll(featureDirRemote, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(featureDirRemote, "FEAT-002-other.md"), "different feature\n")

	// Run comparison
	diff, err := CompareDirectories(localDir, remoteDir)
	if err != nil {
		t.Fatalf("CompareDirectories() error = %v", err)
	}

	// Verify results
	if len(diff.Added) != 1 {
		t.Errorf("Expected 1 added file, got %d", len(diff.Added))
	} else if diff.Added[0].Path != "added.txt" {
		t.Errorf("Expected added.txt, got %s", diff.Added[0].Path)
	}

	if len(diff.Modified) != 1 {
		t.Errorf("Expected 1 modified file, got %d", len(diff.Modified))
	} else if diff.Modified[0].Path != "modified.txt" {
		t.Errorf("Expected modified.txt, got %s", diff.Modified[0].Path)
	}

	if len(diff.Removed) != 1 {
		t.Errorf("Expected 1 removed file, got %d", len(diff.Removed))
	} else if diff.Removed[0].Path != "removed.txt" {
		t.Errorf("Expected removed.txt, got %s", diff.Removed[0].Path)
	}

	if len(diff.Unchanged) != 1 {
		t.Errorf("Expected 1 unchanged file, got %d", len(diff.Unchanged))
	} else if diff.Unchanged[0].Path != "unchanged.txt" {
		t.Errorf("Expected unchanged.txt, got %s", diff.Unchanged[0].Path)
	}

	// Verify feature files were ignored
	for _, fd := range diff.Added {
		if isFeatureFile(fd.Path) {
			t.Errorf("Feature file %s should have been ignored", fd.Path)
		}
	}
	for _, fd := range diff.Removed {
		if isFeatureFile(fd.Path) {
			t.Errorf("Feature file %s should have been ignored", fd.Path)
		}
	}
}

func TestCompareFiles(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name        string
		local       string
		remote      string
		wantStatus  FileStatus
		wantHasDiff bool
	}{
		{
			name:        "identical files",
			local:       "same content\n",
			remote:      "same content\n",
			wantStatus:  FileStatusUnchanged,
			wantHasDiff: false,
		},
		{
			name:        "different files",
			local:       "old content\n",
			remote:      "new content\n",
			wantStatus:  FileStatusModified,
			wantHasDiff: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localPath := filepath.Join(tempDir, "local.txt")
			remotePath := filepath.Join(tempDir, "remote.txt")

			writeFile(t, localPath, tt.local)
			writeFile(t, remotePath, tt.remote)

			diff, err := CompareFiles(localPath, remotePath, "test.txt")
			if err != nil {
				t.Fatalf("CompareFiles() error = %v", err)
			}

			if diff.Status != tt.wantStatus {
				t.Errorf("CompareFiles() status = %v, want %v", diff.Status, tt.wantStatus)
			}

			if tt.wantHasDiff && diff.UnifiedDiff == "" {
				t.Error("Expected non-empty unified diff")
			}
			if !tt.wantHasDiff && diff.UnifiedDiff != "" {
				t.Error("Expected empty unified diff")
			}
		})
	}
}

func TestGenerateUnifiedDiff(t *testing.T) {
	tests := []struct {
		name    string
		local   string
		remote  string
		wantErr bool
	}{
		{
			name:    "simple diff",
			local:   "line 1\nline 2\nline 3\n",
			remote:  "line 1\nline 2 modified\nline 3\n",
			wantErr: false,
		},
		{
			name:    "identical content",
			local:   "same\n",
			remote:  "same\n",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, err := GenerateUnifiedDiff(tt.local, tt.remote, "test.txt")
			if (err != nil) != tt.wantErr {
				t.Errorf("GenerateUnifiedDiff() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && tt.local != tt.remote && diff == "" {
				t.Error("Expected non-empty diff for different content")
			}
		})
	}
}

func TestCollectFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create test file structure
	files := []string{
		"file1.txt",
		"dir1/file2.txt",
		"dir1/dir2/file3.txt",
	}

	for _, f := range files {
		fullPath := filepath.Join(tempDir, f)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, fullPath, "content\n")
	}

	// Create .git directory (should be skipped)
	gitDir := filepath.Join(tempDir, ".git", "objects")
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gitDir, "shouldnotappear.txt"), "git content\n")

	collected, err := collectFiles(tempDir)
	if err != nil {
		t.Fatalf("collectFiles() error = %v", err)
	}

	if len(collected) != len(files) {
		t.Errorf("collectFiles() returned %d files, want %d", len(collected), len(files))
	}

	// Verify all expected files are present
	fileMap := make(map[string]bool)
	for _, f := range collected {
		fileMap[f] = true
	}

	for _, expected := range files {
		if !fileMap[expected] {
			t.Errorf("Expected file %s not found in collected files", expected)
		}
	}

	// Verify .git files are not present
	for _, f := range collected {
		if filepath.HasPrefix(f, ".git") {
			t.Errorf("Collected file %s should have been skipped (.git directory)", f)
		}
	}
}

func TestCompareDirectoriesRejectsSourceSymlinksWithoutReadingTargets(t *testing.T) {
	for _, side := range []string{"local", "remote"} {
		t.Run(side, func(t *testing.T) {
			local := t.TempDir()
			remote := t.TempDir()
			secret := filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(secret, []byte("do-not-read"), 0o600); err != nil {
				t.Fatal(err)
			}
			root := local
			if side == "remote" {
				root = remote
			}
			if err := os.Symlink(secret, filepath.Join(root, "README.md")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if _, err := CompareDirectories(local, remote); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Fatalf("%s source symlink was accepted: %v", side, err)
			}
		})
	}
}

func TestIsFeatureFile(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "feature in backlog",
			path: "features/backlog/FEAT-001-test.md",
			want: true,
		},
		{
			name: "feature in in-progress",
			path: "features/in-progress/FEAT-002-test.md",
			want: true,
		},
		{
			name: "feature in done",
			path: "features/done/FEAT-003-test.md",
			want: true,
		},
		{
			name: "feature in review",
			path: "features/review/FEAT-004-test.md",
			want: true,
		},
		{
			name: "feature in blocked",
			path: "features/blocked/FEAT-005-test.md",
			want: true,
		},
		{
			name: "INDEX.md is not a feature file",
			path: "features/INDEX.md",
			want: false,
		},
		{
			name: "README in features dir",
			path: "features/README.md",
			want: false,
		},
		{
			name: "regular file",
			path: "README.md",
			want: false,
		},
		{
			name: "file in templates",
			path: "templates/feature.md",
			want: false,
		},
		{
			name: "nested feature-like path",
			path: "docs/features/backlog/guide.md",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFeatureFile(tt.path); got != tt.want {
				t.Errorf("isFeatureFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestShouldSkipFile(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "features/INDEX.md should be skipped",
			path: "features/INDEX.md",
			want: true,
		},
		{
			name: "features/INDEX.md with filepath.Join should be skipped",
			path: filepath.Join("features", "INDEX.md"),
			want: true,
		},
		{
			name: "regular file should not be skipped",
			path: "README.md",
			want: false,
		},
		{
			name: "feature file should not be skipped by this function",
			path: "features/backlog/FEAT-001-test.md",
			want: false,
		},
		{
			name: "templates file should not be skipped",
			path: "templates/feature.md",
			want: false,
		},
		{
			name: "audit.jsonl should be skipped",
			path: "audit.jsonl",
			want: true,
		},
		{
			name: "audit.jsonl in subdirectory should not be skipped",
			path: "features/audit.jsonl",
			want: false,
		},
		{
			name: ".template-version is local state",
			path: ".template-version",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipFile(tt.path); got != tt.want {
				t.Errorf("shouldSkipFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestProtectedTemplateUpdateDirectories(t *testing.T) {
	for _, path := range []string{
		".git",
		".state/audit.jsonl",
		"archive/2025",
		"locks",
		"reports/testing",
		"specs",
	} {
		if !isProtectedDirectory(path) {
			t.Errorf("isProtectedDirectory(%q) = false", path)
		}
	}
	for _, path := range []string{".github", "docs/.cursor", "templates/reports", "schemas"} {
		if isProtectedDirectory(path) {
			t.Errorf("framework directory %q must be updateable", path)
		}
	}
}

func TestSortFileDiffs(t *testing.T) {
	diffs := []FileDiff{
		{Path: "z.txt"},
		{Path: "a.txt"},
		{Path: "m.txt"},
	}

	sortFileDiffs(diffs)

	expected := []string{"a.txt", "m.txt", "z.txt"}
	for i, want := range expected {
		if diffs[i].Path != want {
			t.Errorf("After sorting, diffs[%d].Path = %v, want %v", i, diffs[i].Path, want)
		}
	}
}

func TestCompareDirectories_InvalidPaths(t *testing.T) {
	tests := []struct {
		name      string
		localDir  string
		remoteDir string
		wantErr   bool
	}{
		{
			name:      "nonexistent local directory",
			localDir:  "/nonexistent/path",
			remoteDir: t.TempDir(),
			wantErr:   true,
		},
		{
			name:      "nonexistent remote directory",
			localDir:  t.TempDir(),
			remoteDir: "/nonexistent/path",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CompareDirectories(tt.localDir, tt.remoteDir)
			if (err != nil) != tt.wantErr {
				t.Errorf("CompareDirectories() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCompareFiles_InvalidPaths(t *testing.T) {
	tests := []struct {
		name       string
		localPath  string
		remotePath string
		wantErr    bool
	}{
		{
			name:       "nonexistent local file",
			localPath:  "/nonexistent/file.txt",
			remotePath: filepath.Join(t.TempDir(), "exists.txt"),
			wantErr:    true,
		},
		{
			name:       "nonexistent remote file",
			localPath:  filepath.Join(t.TempDir(), "exists.txt"),
			remotePath: "/nonexistent/file.txt",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create the existing file for the test
			if tt.wantErr && tt.localPath != "/nonexistent/file.txt" {
				writeFile(t, tt.localPath, "content")
			}

			_, err := CompareFiles(tt.localPath, tt.remotePath, "test.txt")
			if (err != nil) != tt.wantErr {
				t.Errorf("CompareFiles() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Helper function to write test files
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("Failed to write test file %s: %v", path, err)
	}
}

func TestCollectFiles_IncludesFrameworkDotfilesAndDocs(t *testing.T) {
	testDir := t.TempDir()

	// Create regular files that should be collected
	writeFile(t, filepath.Join(testDir, "README.md"), "readme")
	writeFile(t, filepath.Join(testDir, "schema.json"), "schema")

	// Framework dotfiles are part of the released scaffold.
	writeFile(t, filepath.Join(testDir, ".gitignore"), "gitignore")
	writeFile(t, filepath.Join(testDir, ".pre-commit-config.yaml"), "pre-commit")

	// Framework dotdirectories are also included (except .git and .state).
	dotDir := filepath.Join(testDir, ".github", "workflows")
	if err := os.MkdirAll(dotDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dotDir, "test.yml"), "workflow")

	// Documentation includes editor integration sources and must be updated.
	docsDir := filepath.Join(testDir, "docs")
	if err := os.MkdirAll(docsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(docsDir, "guide.md"), "guide")

	// Nested docs are included.
	nestedDocsDir := filepath.Join(testDir, "docs", "api")
	if err := os.MkdirAll(nestedDocsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nestedDocsDir, "reference.md"), "reference")

	// Create reports folder that should be skipped
	reportsDir := filepath.Join(testDir, "reports")
	if err := os.MkdirAll(reportsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(reportsDir, "report.md"), "report")

	stateDir := filepath.Join(testDir, ".state")
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stateDir, "audit.jsonl"), "local state")

	specsDir := filepath.Join(testDir, "specs")
	if err := os.MkdirAll(specsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(specsDir, "tech-stack.md"), "project spec")

	// Collect files
	files, err := collectFiles(testDir)
	if err != nil {
		t.Fatalf("collectFiles() error = %v", err)
	}

	expectedFiles := map[string]bool{
		"README.md":               true,
		"schema.json":             true,
		".gitignore":              true,
		".pre-commit-config.yaml": true,
		filepath.Join(".github", "workflows", "test.yml"): true,
		filepath.Join("docs", "guide.md"):                 true,
		filepath.Join("docs", "api", "reference.md"):      true,
	}

	if len(files) != len(expectedFiles) {
		t.Errorf("Expected %d files, got %d: %v", len(expectedFiles), len(files), files)
	}

	for _, file := range files {
		if !expectedFiles[file] {
			t.Errorf("Unexpected file collected: %s", file)
		}
	}

	for _, file := range files {
		if strings.HasPrefix(filepath.ToSlash(file), "reports/") ||
			strings.HasPrefix(filepath.ToSlash(file), ".state/") ||
			strings.HasPrefix(filepath.ToSlash(file), "specs/") {
			t.Errorf("workspace-authored file should have been skipped: %s", file)
		}
	}
}
