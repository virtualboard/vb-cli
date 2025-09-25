package util

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// atomicFile encapsulates the subset of *os.File behaviour needed by WriteFileAtomic.
type atomicFile interface {
	Write([]byte) (int, error)
	Close() error
	Name() string
}

// atomicFS abstracts file-system operations for atomic writes.
type atomicFS interface {
	MkdirAll(string, fs.FileMode) error
	CreateTemp(string, string) (atomicFile, error)
	Chmod(string, fs.FileMode) error
	Rename(string, string) error
	Remove(string) error
}

// osAtomicFS implements atomicFS using the standard library os package.
type osAtomicFS struct{}

func (osAtomicFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (osAtomicFS) CreateTemp(dir, pattern string) (atomicFile, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &wrappedFile{File: file}, nil
}
func (osAtomicFS) Chmod(name string, perm fs.FileMode) error { return os.Chmod(name, perm) }
func (osAtomicFS) Rename(oldpath, newpath string) error      { return os.Rename(oldpath, newpath) }
func (osAtomicFS) Remove(name string) error                  { return os.Remove(name) }

// wrappedFile adapts *os.File to the atomicFile interface.
type wrappedFile struct{ *os.File }

func (f *wrappedFile) Write(b []byte) (int, error) { return f.File.Write(b) }
func (f *wrappedFile) Close() error                { return f.File.Close() }
func (f *wrappedFile) Name() string                { return f.File.Name() }

var defaultAtomicFS atomicFS = osAtomicFS{}

// WriteFileAtomic writes data to the path using a temporary file then renames it for atomicity.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	return writeFileAtomic(defaultAtomicFS, path, data, perm)
}

func writeFileAtomic(fs atomicFS, path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := fs.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmp, err := fs.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		// #nosec G104 -- cleanup best-effort during write failure
		tmp.Close()
		// #nosec G104 -- cleanup best-effort during write failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		// #nosec G104 -- cleanup best-effort on close failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := fs.Chmod(tmpName, perm); err != nil {
		// #nosec G104 -- cleanup best-effort on chmod failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}

	if err := fs.Rename(tmpName, path); err != nil {
		// #nosec G104 -- cleanup best-effort on rename failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
