package util

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadRegularFileWithin reads one bounded regular file beneath rootDir without
// permitting a symlink swap to redirect the open outside that root. It also
// verifies the file and root identities before and after the read.
func ReadRegularFileWithin(rootDir, path string, maxBytes int64) ([]byte, os.FileInfo, error) {
	if maxBytes < 0 {
		return nil, nil, errors.New("secure-read byte limit must be non-negative")
	}
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve secure-read root: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve secure-read path: %w", err)
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, nil, fmt.Errorf("secure-read path %s is outside root %s", path, rootDir)
	}

	beforeRoot, err := os.Lstat(rootAbs)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect secure-read root: %w", err)
	}
	if !beforeRoot.IsDir() || beforeRoot.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("secure-read root is not a real directory: %s", rootDir)
	}
	root, err := os.OpenRoot(rootAbs)
	if err != nil {
		return nil, nil, fmt.Errorf("open secure-read root: %w", err)
	}
	defer func() { _ = root.Close() }()
	openedRoot, err := root.Stat(".")
	if err != nil || !openedRoot.IsDir() || !os.SameFile(beforeRoot, openedRoot) {
		return nil, nil, fmt.Errorf("secure-read root changed while opening: %s", rootDir)
	}
	currentRoot, err := os.Lstat(rootAbs)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(beforeRoot, currentRoot) {
		return nil, nil, fmt.Errorf("secure-read root changed while opening: %s", rootDir)
	}

	before, err := root.Lstat(relative)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("secure-read path is not a regular file: %s", path)
	}
	if before.Size() < 0 || before.Size() > maxBytes {
		return nil, nil, fmt.Errorf("secure-read file exceeds %d bytes: %s", maxBytes, path)
	}
	file, err := openRootReadOnlyNoFollow(root, relative)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, nil, fmt.Errorf("secure-read file changed while opening: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, nil, fmt.Errorf("secure-read file exceeds %d bytes: %s", maxBytes, path)
	}
	afterOpen, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	afterPath, err := root.Lstat(relative)
	if err != nil {
		return nil, nil, fmt.Errorf("reinspect secure-read file %s: %w", path, err)
	}
	if !afterPath.Mode().IsRegular() || !os.SameFile(before, afterPath) ||
		!os.SameFile(opened, afterOpen) || before.Size() != opened.Size() ||
		opened.Size() != afterOpen.Size() || afterOpen.Size() != afterPath.Size() ||
		before.Size() != int64(len(data)) || before.Mode() != opened.Mode() ||
		opened.Mode() != afterOpen.Mode() || afterOpen.Mode() != afterPath.Mode() ||
		!before.ModTime().Equal(opened.ModTime()) || !opened.ModTime().Equal(afterOpen.ModTime()) ||
		!afterOpen.ModTime().Equal(afterPath.ModTime()) {
		return nil, nil, fmt.Errorf("secure-read file changed while reading: %s", path)
	}
	return data, afterOpen, nil
}
