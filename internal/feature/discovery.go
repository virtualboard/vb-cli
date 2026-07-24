package feature

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxFeatureDirectoryEntries = 10_000
	maxFeatureAggregateBytes   = 64 << 20
	featureReadDirBatchSize    = 128
)

type discoveredFeatureFile struct {
	path string
	size int64
}

// discoverFeatureFiles enumerates only direct children of the fixed lifecycle
// directories. It opens every directory through the workspace root, reads in
// bounded batches, rejects linked/reparse roots and nested entries, and caps
// both directory fan-out and aggregate feature bytes before callers allocate
// or parse the board.
func (m *Manager) discoverFeatureFiles() ([]discoveredFeatureFile, error) {
	return m.discoverFeatureFilesWithLimits(maxFeatureDirectoryEntries, maxFeatureAggregateBytes)
}

func (m *Manager) discoverFeatureFilesWithLimits(entryLimit int, aggregateLimit int64) ([]discoveredFeatureFile, error) {
	if entryLimit <= 0 || aggregateLimit <= 0 {
		return nil, errors.New("feature discovery limits must be positive")
	}
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return nil, err
	}
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return nil, fmt.Errorf("open feature discovery root: %w", err)
	}
	defer func() { _ = root.Close() }()

	featuresRelative, err := relativeTransactionPath(m.opts.RootDir, m.FeaturesDir())
	if err != nil {
		return nil, err
	}
	featuresInfo, err := root.Lstat(featuresRelative)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("configured features root is missing: %s", m.FeaturesDir())
	}
	if err != nil {
		return nil, fmt.Errorf("inspect features directory: %w", err)
	}
	if featurePathIsLinked(featuresInfo) || !featuresInfo.IsDir() {
		return nil, fmt.Errorf("features root is a symbolic link/reparse point or not a directory: %s", m.FeaturesDir())
	}

	files := make([]discoveredFeatureFile, 0)
	entryCount := 0
	var aggregateBytes int64
	for _, status := range lifecycle.Statuses() {
		statusPath, ok := lifecycle.DirectoryForStatus(status)
		if !ok {
			return nil, fmt.Errorf("status %s has no canonical directory", status)
		}
		statusRelative, err := relativeTransactionPath(m.opts.RootDir, statusPath)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(filepath.Dir(statusRelative)) != filepath.Clean(featuresRelative) {
			return nil, fmt.Errorf("status directory escapes the direct features root: %s", statusPath)
		}
		before, err := root.Lstat(statusRelative)
		if err != nil {
			return nil, fmt.Errorf("inspect status directory %s: %w", status, err)
		}
		if featurePathIsLinked(before) || !before.IsDir() {
			return nil, fmt.Errorf("status directory is a symbolic link/reparse point or not a directory: %s", statusPath)
		}
		directory, err := root.Open(statusRelative)
		if err != nil {
			return nil, fmt.Errorf("open status directory %s: %w", status, err)
		}
		opened, err := directory.Stat()
		if err != nil || featurePathIsLinked(opened) || !opened.IsDir() || !os.SameFile(before, opened) {
			_ = directory.Close()
			return nil, fmt.Errorf("status directory changed while opening: %s", statusPath)
		}

		for {
			entries, readErr := directory.ReadDir(featureReadDirBatchSize)
			for _, entry := range entries {
				entryCount++
				if entryCount > entryLimit {
					_ = directory.Close()
					return nil, fmt.Errorf("feature directory entry limit exceeded (%d)", entryLimit)
				}
				entryRelative := filepath.Join(statusRelative, entry.Name())
				info, infoErr := root.Lstat(entryRelative)
				if infoErr != nil {
					_ = directory.Close()
					return nil, fmt.Errorf("inspect feature directory entry %s: %w", entry.Name(), infoErr)
				}
				if featurePathIsLinked(info) {
					_ = directory.Close()
					return nil, fmt.Errorf("feature path is a symbolic link or reparse point: %s", filepath.Join(statusPath, entry.Name()))
				}
				if info.IsDir() {
					_ = directory.Close()
					return nil, fmt.Errorf("feature path is not a regular file (nested directories are forbidden): %s", filepath.Join(statusPath, entry.Name()))
				}
				if !info.Mode().IsRegular() {
					_ = directory.Close()
					return nil, fmt.Errorf("non-regular entry is forbidden in a status directory: %s", filepath.Join(statusPath, entry.Name()))
				}
				if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
					continue
				}
				if info.Size() < 0 || info.Size() > maxFeatureFileBytes {
					_ = directory.Close()
					return nil, fmt.Errorf("feature file size exceeds %d bytes: %s", maxFeatureFileBytes, filepath.Join(statusPath, entry.Name()))
				}
				aggregateBytes += info.Size()
				if aggregateBytes > aggregateLimit {
					_ = directory.Close()
					return nil, fmt.Errorf("aggregate feature byte limit exceeded (%d)", aggregateLimit)
				}
				files = append(files, discoveredFeatureFile{path: filepath.Join(statusPath, entry.Name()), size: info.Size()})
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = directory.Close()
				return nil, fmt.Errorf("read status directory %s: %w", status, readErr)
			}
		}
		if err := directory.Close(); err != nil {
			return nil, fmt.Errorf("close status directory %s: %w", status, err)
		}
		current, err := root.Lstat(statusRelative)
		if err != nil || featurePathIsLinked(current) || !current.IsDir() || !os.SameFile(opened, current) {
			return nil, fmt.Errorf("status directory changed during enumeration: %s", statusPath)
		}
	}
	currentFeatures, err := root.Lstat(featuresRelative)
	if err != nil || featurePathIsLinked(currentFeatures) || !currentFeatures.IsDir() || !os.SameFile(featuresInfo, currentFeatures) {
		return nil, fmt.Errorf("features root changed during enumeration: %s", m.FeaturesDir())
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}
