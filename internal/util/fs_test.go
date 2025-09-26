package util

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeFile struct {
	name     string
	writeErr error
	closeErr error
	closed   bool
}

func (f *fakeFile) Write(b []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return len(b), nil
}

func (f *fakeFile) Close() error {
	f.closed = true
	return f.closeErr
}

func (f *fakeFile) Name() string { return f.name }

type fakeFS struct {
	root      string
	mkdirErr  error
	createErr error
	chmodErr  error
	renameErr error
	removeErr error
	file      *fakeFile
	removed   []string
}

func (f *fakeFS) MkdirAll(path string, perm os.FileMode) error {
	if f.mkdirErr != nil {
		return f.mkdirErr
	}
	return os.MkdirAll(path, perm)
}

func (f *fakeFS) CreateTemp(dir, pattern string) (atomicFile, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.file == nil {
		f.file = &fakeFile{name: filepath.Join(dir, pattern+"tmp")}
	}
	return f.file, nil
}

func (f *fakeFS) Chmod(name string, perm os.FileMode) error {
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return nil
}

func (f *fakeFS) Rename(oldpath, newpath string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	return os.WriteFile(newpath, []byte(""), 0o644)
}

func (f *fakeFS) Remove(name string) error {
	f.removed = append(f.removed, name)
	return f.removeErr
}

func TestWriteFileAtomicSuccess(t *testing.T) {
	temp := t.TempDir()
	path := filepath.Join(temp, "dir", "file.txt")
	if err := WriteFileAtomic(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "hello" {
		t.Fatalf("unexpected content: %q", string(content))
	}
}

func TestWriteFileAtomicFailures(t *testing.T) {
	temp := t.TempDir()
	base := filepath.Join(temp, "dir", "file.txt")

	testCases := []struct {
		name     string
		fs       *fakeFS
		errorMsg string
	}{
		{
			name:     "mkdir",
			fs:       &fakeFS{mkdirErr: errors.New("mkdir fail")},
			errorMsg: "failed to create directory",
		},
		{
			name:     "create",
			fs:       &fakeFS{createErr: errors.New("create fail")},
			errorMsg: "failed to create temp file",
		},
		{
			name:     "write",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp"), writeErr: errors.New("write fail")}},
			errorMsg: "failed to write temp file",
		},
		{
			name:     "close",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp"), closeErr: errors.New("close fail")}},
			errorMsg: "failed to close temp file",
		},
		{
			name:     "chmod",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp")}, chmodErr: errors.New("chmod fail")},
			errorMsg: "failed to chmod temp file",
		},
		{
			name:     "rename",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp")}, renameErr: errors.New("rename fail")},
			errorMsg: "failed to rename temp file",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			original := defaultAtomicFS
			defer func() { defaultAtomicFS = original }()
			defaultAtomicFS = tc.fs
			err := WriteFileAtomic(base, []byte("data"), 0o644)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), tc.errorMsg) {
				t.Fatalf("expected error containing %q, got %v", tc.errorMsg, err)
			}
		})
	}
}
