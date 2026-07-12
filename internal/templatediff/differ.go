package templatediff

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/virtualboard/vb-cli/internal/util"
)

const maxComparisonFiles = 10000
const maxComparisonEntries = 20000
const maxComparisonDepth = 64
const maxComparisonFileBytes int64 = 20 << 20
const maxComparisonRetainedBytes int64 = 100 << 20
const maxUnifiedDiffInputBytes = 4 << 20
const maxUnifiedDiffLines = 200000

var comparisonEntryLimit = maxComparisonEntries

// CompareDirectories compares two template directories and returns the differences
func CompareDirectories(localDir, remoteDir string) (*TemplateDiff, error) {
	diff := &TemplateDiff{
		Added:     []FileDiff{},
		Modified:  []FileDiff{},
		Removed:   []FileDiff{},
		Unchanged: []FileDiff{},
	}

	// Build a map of all files in both directories
	localFiles, err := collectFiles(localDir)
	if err != nil {
		return nil, fmt.Errorf("failed to collect local files: %w", err)
	}

	remoteFiles, err := collectFiles(remoteDir)
	if err != nil {
		return nil, fmt.Errorf("failed to collect remote files: %w", err)
	}
	if len(localFiles) > maxComparisonFiles || len(remoteFiles) > maxComparisonFiles {
		return nil, fmt.Errorf("template comparison exceeds %d files", maxComparisonFiles)
	}
	var retainedBytes int64
	retain := func(contents ...[]byte) error {
		for _, content := range contents {
			retainedBytes += int64(len(content))
			if retainedBytes > maxComparisonRetainedBytes {
				return fmt.Errorf("template comparison exceeds %d retained bytes", maxComparisonRetainedBytes)
			}
		}
		return nil
	}

	// Create maps for quick lookup
	localMap := make(map[string]bool)
	for _, f := range localFiles {
		localMap[f] = true
	}

	remoteMap := make(map[string]bool)
	for _, f := range remoteFiles {
		remoteMap[f] = true
	}

	// Check for added and modified files
	for _, relPath := range remoteFiles {
		remotePath := filepath.Join(remoteDir, relPath)
		localPath := filepath.Join(localDir, relPath)

		// Skip feature files and other excluded files
		if isFeatureFile(relPath) || shouldSkipFile(relPath) {
			continue
		}

		if !localMap[relPath] {
			// File added in remote
			content, mode, err := readRegularFileStateWithin(remoteDir, remotePath)
			if err != nil {
				return nil, fmt.Errorf("failed to read remote file %s: %w", relPath, err)
			}
			if err := retain(content); err != nil {
				return nil, err
			}
			diff.Added = append(diff.Added, FileDiff{
				Path:          relPath,
				Status:        FileStatusAdded,
				RemoteContent: content,
				RemoteMode:    normalizedTemplateMode(mode),
			})
		} else {
			// File exists in both, check if modified
			fileDiff, err := compareFilesWithin(localDir, remoteDir, localPath, remotePath, relPath)
			if err != nil {
				return nil, fmt.Errorf("failed to compare %s: %w", relPath, err)
			}
			if err := retain(fileDiff.LocalContent, fileDiff.RemoteContent, []byte(fileDiff.UnifiedDiff)); err != nil {
				return nil, err
			}

			if fileDiff.Status == FileStatusModified {
				diff.Modified = append(diff.Modified, *fileDiff)
			} else {
				diff.Unchanged = append(diff.Unchanged, *fileDiff)
			}
		}
	}

	// Check for removed files
	for _, relPath := range localFiles {
		if !remoteMap[relPath] && !isFeatureFile(relPath) && !shouldSkipFile(relPath) {
			localPath := filepath.Join(localDir, relPath)
			content, mode, err := readRegularFileStateWithin(localDir, localPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read local file %s: %w", relPath, err)
			}
			if err := retain(content); err != nil {
				return nil, err
			}
			diff.Removed = append(diff.Removed, FileDiff{
				Path:         relPath,
				Status:       FileStatusRemoved,
				LocalContent: content,
				LocalMode:    installedTemplateMode(relPath, mode),
			})
		}
	}

	// Sort all slices for deterministic output
	sortFileDiffs(diff.Added)
	sortFileDiffs(diff.Modified)
	sortFileDiffs(diff.Removed)
	sortFileDiffs(diff.Unchanged)

	return diff, nil
}

// CompareFiles compares two files and generates a unified diff if they differ
func CompareFiles(localPath, remotePath, relPath string) (*FileDiff, error) {
	return compareFilesWithin(filepath.Dir(localPath), filepath.Dir(remotePath), localPath, remotePath, relPath)
}

func compareFilesWithin(localRoot, remoteRoot, localPath, remotePath, relPath string) (*FileDiff, error) {
	localContent, localMode, err := readRegularFileStateWithin(localRoot, localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local file: %w", err)
	}

	remoteContent, remoteMode, err := readRegularFileStateWithin(remoteRoot, remotePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote file: %w", err)
	}

	remoteMode = normalizedTemplateMode(remoteMode)
	localMode = installedTemplateMode(relPath, localMode)

	// Quick byte and effective-mode comparison. Local permissions are intentionally
	// not normalized: mode-only drift in an installed tree must be repaired.
	if bytes.Equal(localContent, remoteContent) && localMode.Perm() == remoteMode.Perm() {
		return &FileDiff{
			Path:          relPath,
			Status:        FileStatusUnchanged,
			LocalContent:  localContent,
			RemoteContent: remoteContent,
			LocalMode:     localMode,
			RemoteMode:    remoteMode,
		}, nil
	}

	var diff string
	if !bytes.Equal(localContent, remoteContent) {
		if unifiedDiffIsBounded(localContent, remoteContent) {
			// Generate a unified diff only when its line index and output remain
			// predictably bounded. The exact bytes are still retained for CAS.
			diff, err = GenerateUnifiedDiff(string(localContent), string(remoteContent), relPath)
			if err != nil {
				return nil, fmt.Errorf("failed to generate diff: %w", err)
			}
		} else {
			diff = fmt.Sprintf("content differs; unified diff omitted (limit %d input bytes and %d lines per side)\n", maxUnifiedDiffInputBytes, maxUnifiedDiffLines)
		}
	}
	if localMode.Perm() != remoteMode.Perm() {
		modeDiff := fmt.Sprintf("mode %04o -> %04o\n", localMode.Perm(), remoteMode.Perm())
		diff = modeDiff + diff
	}

	return &FileDiff{
		Path:          relPath,
		Status:        FileStatusModified,
		UnifiedDiff:   diff,
		LocalContent:  localContent,
		RemoteContent: remoteContent,
		LocalMode:     localMode,
		RemoteMode:    remoteMode,
	}, nil
}

func unifiedDiffIsBounded(localContent, remoteContent []byte) bool {
	if len(localContent)+len(remoteContent) > maxUnifiedDiffInputBytes {
		return false
	}
	return bytes.Count(localContent, []byte("\n")) <= maxUnifiedDiffLines &&
		bytes.Count(remoteContent, []byte("\n")) <= maxUnifiedDiffLines
}

func normalizedTemplateMode(mode fs.FileMode) fs.FileMode {
	if mode.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func installedTemplateMode(relPath string, mode fs.FileMode) fs.FileMode {
	if runtime.GOOS != "windows" {
		return mode.Perm()
	}
	normalized := filepath.ToSlash(relPath)
	if strings.HasPrefix(normalized, "bin/") ||
		(strings.HasPrefix(normalized, "scripts/") && strings.HasSuffix(normalized, ".sh")) {
		return 0o755
	}
	return 0o644
}

// GenerateUnifiedDiff creates a unified diff string between two contents
func GenerateUnifiedDiff(localContent, remoteContent, filename string) (string, error) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(localContent),
		B:        difflib.SplitLines(remoteContent),
		FromFile: fmt.Sprintf("a/%s", filename),
		ToFile:   fmt.Sprintf("b/%s", filename),
		Context:  3,
	}
	return difflib.GetUnifiedDiffString(diff)
}

// collectFiles walks a directory and returns all file paths relative to the root
func collectFiles(root string) ([]string, error) {
	beforeRoot, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !beforeRoot.IsDir() || beforeRoot.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("template comparison root is not a real directory: %s", root)
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rootHandle.Close() }()
	openedRoot, err := rootHandle.Stat(".")
	if err != nil || !openedRoot.IsDir() || !os.SameFile(beforeRoot, openedRoot) {
		return nil, fmt.Errorf("template comparison root changed while opening: %s", root)
	}
	currentRoot, err := os.Lstat(root)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(beforeRoot, currentRoot) {
		return nil, fmt.Errorf("template comparison root changed while opening: %s", root)
	}

	type pendingDirectory struct {
		path  string
		depth int
	}
	pending := []pendingDirectory{{path: ".", depth: 0}}
	files := make([]string, 0)
	entryCount := 0
	for len(pending) > 0 {
		last := len(pending) - 1
		current := pending[last]
		pending = pending[:last]
		before, err := rootHandle.Lstat(current.path)
		if err != nil {
			return nil, err
		}
		if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing non-directory path in template comparison: %s", current.path)
		}
		directoryRoot, err := rootHandle.OpenRoot(current.path)
		if err != nil {
			return nil, err
		}
		opened, err := directoryRoot.Stat(".")
		if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
			_ = directoryRoot.Close()
			return nil, fmt.Errorf("template comparison directory changed while opening: %s", current.path)
		}
		directory, err := directoryRoot.Open(".")
		if err != nil {
			_ = directoryRoot.Close()
			return nil, err
		}
		for {
			entries, readErr := directory.ReadDir(128)
			for _, entry := range entries {
				entryCount++
				if entryCount > comparisonEntryLimit {
					_ = directory.Close()
					_ = directoryRoot.Close()
					return nil, fmt.Errorf("template comparison exceeds %d entries", comparisonEntryLimit)
				}
				relPath := entry.Name()
				if current.path != "." {
					relPath = filepath.Join(current.path, entry.Name())
				}
				if entry.Type()&os.ModeSymlink != 0 {
					_ = directory.Close()
					_ = directoryRoot.Close()
					return nil, fmt.Errorf("refusing symbolic link in template comparison: %s", relPath)
				}
				info, infoErr := directoryRoot.Lstat(entry.Name())
				if infoErr != nil {
					_ = directory.Close()
					_ = directoryRoot.Close()
					return nil, infoErr
				}
				if info.IsDir() {
					if isProtectedDirectory(relPath) {
						continue
					}
					if current.depth >= maxComparisonDepth {
						_ = directory.Close()
						_ = directoryRoot.Close()
						return nil, fmt.Errorf("template comparison exceeds %d directory levels", maxComparisonDepth)
					}
					pending = append(pending, pendingDirectory{path: relPath, depth: current.depth + 1})
					continue
				}
				if !info.Mode().IsRegular() {
					_ = directory.Close()
					_ = directoryRoot.Close()
					return nil, fmt.Errorf("refusing non-regular path in template comparison: %s", relPath)
				}
				files = append(files, relPath)
				if len(files) > maxComparisonFiles {
					_ = directory.Close()
					_ = directoryRoot.Close()
					return nil, fmt.Errorf("template comparison exceeds %d files", maxComparisonFiles)
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = directory.Close()
				_ = directoryRoot.Close()
				return nil, readErr
			}
		}
		if err := directory.Close(); err != nil {
			_ = directoryRoot.Close()
			return nil, err
		}
		if err := directoryRoot.Close(); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func readRegularFile(path string) ([]byte, error) {
	content, _, err := readRegularFileState(path)
	return content, err
}

func readRegularFileState(path string) ([]byte, fs.FileMode, error) {
	return readRegularFileStateWithin(filepath.Dir(path), path)
}

func readRegularFileStateWithin(root, path string) ([]byte, fs.FileMode, error) {
	content, info, err := util.ReadRegularFileWithin(root, path, maxComparisonFileBytes)
	if err != nil {
		return nil, 0, err
	}
	return content, info.Mode().Perm(), nil
}

func isProtectedDirectory(relPath string) bool {
	normalized := filepath.ToSlash(relPath)
	for _, protected := range []string{".git", ".state", "archive", "locks", "reports", "specs"} {
		if normalized == protected || strings.HasPrefix(normalized, protected+"/") {
			return true
		}
	}
	return false
}

// isFeatureFile returns true if the path is a feature specification file
func isFeatureFile(relPath string) bool {
	// Feature files are under features/{status}/*.md (but not INDEX.md)
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	if len(parts) >= 3 && parts[0] == "features" {
		// Check if it's in a status directory (backlog, in-progress, etc.)
		statusDirs := []string{"backlog", "in-progress", "blocked", "review", "done"}
		for _, status := range statusDirs {
			if parts[1] == status && strings.HasSuffix(parts[len(parts)-1], ".md") {
				return true
			}
		}
	}
	return false
}

// shouldSkipFile returns true if the file should be skipped during template comparison
func shouldSkipFile(relPath string) bool {
	normalized := filepath.ToSlash(relPath)
	// Skip features/INDEX.md - user-specific index file
	if normalized == "features/INDEX.md" {
		return true
	}
	// Skip generated/local state that never comes from a release archive.
	if normalized == "audit.jsonl" || normalized == ".template-version" || normalized == ".template-manifest.json" {
		return true
	}
	return false
}

// sortFileDiffs sorts a slice of FileDiff by path
func sortFileDiffs(diffs []FileDiff) {
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Path < diffs[j].Path
	})
}
