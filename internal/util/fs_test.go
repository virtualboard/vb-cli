package util

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeFile struct {
	name     string
	writeErr error
	closeErr error
	syncErr  error
	chmodErr error
	closed   bool
}

type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string     { return f.name }
func (fakeFileInfo) Size() int64        { return 1 }
func (fakeFileInfo) Mode() os.FileMode  { return 0o600 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() interface{}   { return nil }

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

func (f *fakeFile) Sync() error                { return f.syncErr }
func (f *fakeFile) Chmod(os.FileMode) error    { return f.chmodErr }
func (f *fakeFile) Stat() (os.FileInfo, error) { return fakeFileInfo{name: f.name}, nil }

func (f *fakeFile) Name() string { return f.name }

type fakeFS struct {
	root       string
	mkdirErr   error
	createErr  error
	renameErr  error
	removeErr  error
	syncDirErr error
	file       *fakeFile
	removed    []string
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

func (f *fakeFS) SyncDir(string) error { return f.syncDirErr }
func (f *fakeFS) Lstat(string) (os.FileInfo, error) {
	return fakeFileInfo{name: f.file.name}, nil
}
func (f *fakeFS) SameFile(os.FileInfo, os.FileInfo) bool { return true }

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
	matches, err := filepath.Glob(filepath.Join(temp, "dir", atomicStageDirectoryPrefix+"*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("atomic write left private staging directories: %v, %v", matches, err)
	}
}

func TestWriteFileExclusiveAtomicNeverReplacesExistingPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileExclusiveAtomic(path, []byte("replacement\n"), 0o644); err == nil {
		t.Fatal("exclusive write replaced an existing path")
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "existing\n" {
		t.Fatalf("exclusive write changed target: %q, %v", content, err)
	}
}

func TestWriteFileExclusiveAtomicPublishesCompleteFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "file.txt")
	if err := WriteFileExclusiveAtomic(path, []byte("complete\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "complete\n" {
		t.Fatalf("exclusive file = %q, %v", content, err)
	}
	stages, err := filepath.Glob(filepath.Join(root, "nested", exclusiveStageDirectoryPrefix+"*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("successful exclusive write left private stages: %v, %v", stages, err)
	}
}

func TestWriteFileExclusiveAtomicRejectsStagedSourceSubstitution(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "feature.md")
	var substitutedStage string
	originalHook := beforeExclusiveAtomicPublish
	beforeExclusiveAtomicPublish = func(stagePath, _ string) {
		substitutedStage = stagePath
		if err := os.Remove(stagePath); err != nil {
			t.Fatalf("remove retained stage path: %v", err)
		}
		if err := os.WriteFile(stagePath, []byte("attacker\n"), 0o600); err != nil {
			t.Fatalf("substitute retained stage path: %v", err)
		}
	}
	t.Cleanup(func() { beforeExclusiveAtomicPublish = originalHook })

	err := WriteFileExclusiveAtomicWithin(root, destination, []byte("expected\n"), 0o600)
	if err == nil || !strings.Contains(err.Error(), "staging source changed") {
		t.Fatalf("stage substitution error = %v", err)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("substituted stage was published: %v", err)
	}
	if data, err := os.ReadFile(substitutedStage); err != nil || string(data) != "attacker\n" {
		t.Fatalf("cleanup removed or changed an unrecognized staged path: %q, %v", data, err)
	}
}

func TestWriteFileExclusiveAtomicPreservesNewerDestinationOnAmbiguousPublication(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "feature.md")
	originalHook := afterExclusiveAtomicPublish
	afterExclusiveAtomicPublish = func(_, destinationPath string) {
		if err := os.Remove(destinationPath); err != nil {
			t.Fatalf("remove just-published destination: %v", err)
		}
		if err := os.WriteFile(destinationPath, []byte("newer\n"), 0o600); err != nil {
			t.Fatalf("publish newer destination: %v", err)
		}
	}
	t.Cleanup(func() { afterExclusiveAtomicPublish = originalHook })

	err := WriteFileExclusiveAtomicWithin(root, destination, []byte("expected\n"), 0o600)
	if err == nil || !strings.Contains(err.Error(), "destination preserved for reconciliation") {
		t.Fatalf("ambiguous publication error = %v", err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "newer\n" {
		t.Fatalf("ambiguous cleanup removed a newer destination: %q, %v", data, err)
	}
}

func TestWriteFileExclusiveAtomicWithinRejectsLexicalEscape(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "..", "escaped.txt")
	if err := WriteFileExclusiveAtomicWithin(root, destination, []byte("escape\n"), 0o600); err == nil {
		t.Fatal("root-scoped exclusive write accepted an escaping path")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("escaping destination was created: %v", err)
	}
}

func TestWriteFileExclusiveAtomicWithinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	destination := filepath.Join(link, "escaped.txt")
	if err := WriteFileExclusiveAtomicWithin(root, destination, []byte("escape\n"), 0o600); err == nil {
		t.Fatal("root-scoped exclusive write followed a symlink outside its root")
	}
	if _, err := os.Lstat(filepath.Join(external, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink target was written: %v", err)
	}
}

func TestWriteFileExclusiveAtomicWithinRejectsSymlinkRoot(t *testing.T) {
	external := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "linked-root")
	if err := os.Symlink(external, root); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	destination := filepath.Join(root, "escaped.txt")
	if err := WriteFileExclusiveAtomicWithin(root, destination, []byte("escape\n"), 0o600); err == nil {
		t.Fatal("root-scoped exclusive write accepted a symlink as its root")
	}
	if _, err := os.Lstat(filepath.Join(external, "escaped.txt")); !os.IsNotExist(err) {
		t.Fatalf("symlink-root target was written: %v", err)
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
			name:     "sync",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp"), syncErr: errors.New("sync fail")}},
			errorMsg: "failed to sync temp file",
		},
		{
			name:     "close",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp"), closeErr: errors.New("close fail")}},
			errorMsg: "failed to close temp file",
		},
		{
			name:     "sync directory",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp")}, syncDirErr: errors.New("dir sync fail")},
			errorMsg: "failed to sync parent directory",
		},
		{
			name:     "chmod",
			fs:       &fakeFS{file: &fakeFile{name: filepath.Join(temp, "tmp"), chmodErr: errors.New("chmod fail")}},
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
