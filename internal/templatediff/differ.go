package templatediff

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

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

		// Skip feature files - we only care about template infrastructure
		if isFeatureFile(relPath) {
			continue
		}

		if !localMap[relPath] {
			// File added in remote
			content, err := os.ReadFile(remotePath) // #nosec G304 -- path is from validated template
			if err != nil {
				return nil, fmt.Errorf("failed to read remote file %s: %w", relPath, err)
			}
			diff.Added = append(diff.Added, FileDiff{
				Path:          relPath,
				Status:        FileStatusAdded,
				RemoteContent: content,
			})
		} else {
			// File exists in both, check if modified
			fileDiff, err := CompareFiles(localPath, remotePath, relPath)
			if err != nil {
				return nil, fmt.Errorf("failed to compare %s: %w", relPath, err)
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
		if !remoteMap[relPath] && !isFeatureFile(relPath) {
			localPath := filepath.Join(localDir, relPath)
			content, err := os.ReadFile(localPath) // #nosec G304 -- path is from validated local directory
			if err != nil {
				return nil, fmt.Errorf("failed to read local file %s: %w", relPath, err)
			}
			diff.Removed = append(diff.Removed, FileDiff{
				Path:         relPath,
				Status:       FileStatusRemoved,
				LocalContent: content,
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
	// #nosec G304 -- paths are from validated directories
	localContent, err := os.ReadFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local file: %w", err)
	}

	// #nosec G304 -- paths are from validated directories
	remoteContent, err := os.ReadFile(remotePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote file: %w", err)
	}

	// Quick byte comparison
	if string(localContent) == string(remoteContent) {
		return &FileDiff{
			Path:          relPath,
			Status:        FileStatusUnchanged,
			LocalContent:  localContent,
			RemoteContent: remoteContent,
		}, nil
	}

	// Generate unified diff
	diff, err := GenerateUnifiedDiff(string(localContent), string(remoteContent), relPath)
	if err != nil {
		return nil, fmt.Errorf("failed to generate diff: %w", err)
	}

	return &FileDiff{
		Path:          relPath,
		Status:        FileStatusModified,
		UnifiedDiff:   diff,
		LocalContent:  localContent,
		RemoteContent: remoteContent,
	}, nil
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
	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip .git directory
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		if !d.IsDir() {
			relPath, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, relPath)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
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

// sortFileDiffs sorts a slice of FileDiff by path
func sortFileDiffs(diffs []FileDiff) {
	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Path < diffs[j].Path
	})
}
