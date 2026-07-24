package util

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// atomicFile encapsulates the subset of *os.File behaviour needed by WriteFileAtomic.
type atomicFile interface {
	Write([]byte) (int, error)
	Chmod(fs.FileMode) error
	Sync() error
	Stat() (fs.FileInfo, error)
	Close() error
	Name() string
}

// atomicFS abstracts file-system operations for atomic writes.
type atomicFS interface {
	MkdirAll(string, fs.FileMode) error
	CreateTemp(string, string) (atomicFile, error)
	Rename(string, string) error
	Remove(string) error
	SyncDir(string) error
	Lstat(string) (fs.FileInfo, error)
	SameFile(fs.FileInfo, fs.FileInfo) bool
}

// osAtomicFS implements atomicFS using the standard library os package.
type osAtomicFS struct{}

const atomicStageDirectoryPrefix = ".atomic-write-"

func (osAtomicFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (osAtomicFS) CreateTemp(dir, pattern string) (atomicFile, error) {
	stageDir, err := os.MkdirTemp(dir, atomicStageDirectoryPrefix+"*")
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(stageDir, pattern)
	if err != nil {
		_ = os.Remove(stageDir)
		return nil, err
	}
	return &wrappedFile{File: file}, nil
}
func (osAtomicFS) Rename(oldpath, newpath string) error {
	if err := os.Rename(oldpath, newpath); err != nil {
		return err
	}
	removeAtomicStageDirectory(oldpath)
	return nil
}
func (osAtomicFS) Remove(name string) error {
	err := os.Remove(name)
	removeAtomicStageDirectory(name)
	return err
}
func (osAtomicFS) SyncDir(path string) error               { return syncDirectoryPath(path) }
func (osAtomicFS) Lstat(path string) (fs.FileInfo, error)  { return os.Lstat(path) }
func (osAtomicFS) SameFile(first, second fs.FileInfo) bool { return os.SameFile(first, second) }

func removeAtomicStageDirectory(stagedPath string) {
	directory := filepath.Dir(stagedPath)
	if strings.HasPrefix(filepath.Base(directory), atomicStageDirectoryPrefix) {
		_ = os.Remove(directory)
	}
}

// wrappedFile adapts *os.File to the atomicFile interface.
type wrappedFile struct{ *os.File }

func (f *wrappedFile) Write(b []byte) (int, error)  { return f.File.Write(b) }
func (f *wrappedFile) Chmod(mode fs.FileMode) error { return f.File.Chmod(mode) }
func (f *wrappedFile) Sync() error                  { return f.File.Sync() }
func (f *wrappedFile) Stat() (fs.FileInfo, error)   { return f.File.Stat() }
func (f *wrappedFile) Close() error                 { return f.File.Close() }
func (f *wrappedFile) Name() string                 { return f.File.Name() }

var defaultAtomicFS atomicFS = osAtomicFS{}

// Test-only scheduling hooks used to prove that source substitution is caught
// before publication and that a newer destination is never removed on an
// ambiguous post-publication observation.
var (
	beforeExclusiveAtomicPublish func(stagePath, destinationPath string)
	afterExclusiveAtomicPublish  func(stagePath, destinationPath string)
)

// WriteFileAtomic writes data to the path using a temporary file then renames it for atomicity.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	return writeFileAtomic(defaultAtomicFS, path, data, perm)
}

// WriteFileExclusiveAtomic publishes a fully staged file only when path is
// absent. The hard-link publication is atomic and never replaces a file that
// appeared concurrently.
func WriteFileExclusiveAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	return WriteFileExclusiveAtomicWithin(dir, path, data, perm)
}

// WriteFileExclusiveAtomicWithin publishes a fully staged file beneath rootDir
// only when path is absent. Every operation after opening rootDir is scoped by
// os.Root, so a concurrently replaced path component cannot redirect the write
// outside the authorized tree.
func WriteFileExclusiveAtomicWithin(rootDir, path string, data []byte, perm fs.FileMode) error {
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return fmt.Errorf("failed to resolve exclusive-write root: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve exclusive-write path: %w", err)
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("exclusive-write path %s is outside root %s", path, rootDir)
	}

	beforeRoot, err := os.Lstat(rootAbs)
	if err != nil {
		return fmt.Errorf("failed to inspect exclusive-write root: %w", err)
	}
	if !beforeRoot.IsDir() || beforeRoot.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("exclusive-write root is not a real directory: %s", rootDir)
	}
	root, err := os.OpenRoot(rootAbs)
	if err != nil {
		return fmt.Errorf("failed to open exclusive-write root: %w", err)
	}
	defer func() { _ = root.Close() }()
	openedRoot, err := root.Stat(".")
	if err != nil || !openedRoot.IsDir() || !os.SameFile(beforeRoot, openedRoot) {
		return fmt.Errorf("exclusive-write root changed while opening: %s", rootDir)
	}
	currentRoot, err := os.Lstat(rootAbs)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(beforeRoot, currentRoot) {
		return fmt.Errorf("exclusive-write root changed while opening: %s", rootDir)
	}
	relativeDir := filepath.Dir(relative)
	if relativeDir != "." {
		if err := root.MkdirAll(relativeDir, 0o750); err != nil {
			return fmt.Errorf("failed to create exclusive-write directory: %w", err)
		}
	}

	stage, err := createPrivateExclusiveStage(root, rootAbs, relativeDir)
	if err != nil {
		return err
	}
	cleanupPending := true
	defer func() {
		if cleanupPending {
			_ = stage.cleanup()
		}
	}()
	if written, err := stage.file.Write(data); err != nil {
		return fmt.Errorf("failed to write exclusive staging file: %w", err)
	} else if written != len(data) {
		return fmt.Errorf("failed to write exclusive staging file: %w", io.ErrShortWrite)
	}
	if err := stage.file.Chmod(perm); err != nil {
		return fmt.Errorf("failed to chmod exclusive staging file: %w", err)
	}
	if err := stage.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync exclusive staging file: %w", err)
	}
	identity, err := stage.file.Stat()
	if err != nil {
		return fmt.Errorf("failed to inspect exclusive staging file: %w", err)
	}
	stage.fileIdentity = identity
	if err := stage.verify(data, perm); err != nil {
		return err
	}
	if beforeExclusiveAtomicPublish != nil {
		beforeExclusiveAtomicPublish(stage.absolutePath(), pathAbs)
	}
	if err := stage.verify(data, perm); err != nil {
		return fmt.Errorf("exclusive staging source changed before publication: %w", err)
	}
	if err := root.Link(stage.relativePath(), relative); err != nil {
		return fmt.Errorf("failed to publish exclusive file: %w", err)
	}
	if afterExclusiveAtomicPublish != nil {
		afterExclusiveAtomicPublish(stage.absolutePath(), pathAbs)
	}
	published, err := root.Lstat(relative)
	if err != nil || !published.Mode().IsRegular() || !os.SameFile(stage.fileIdentity, published) {
		return fmt.Errorf("published exclusive path does not reference the retained staged inode; destination preserved for reconciliation")
	}
	if err := stage.verify(data, perm); err != nil {
		return fmt.Errorf("published exclusive file changed during verification; destination preserved for reconciliation: %w", err)
	}
	if err := stage.cleanup(); err != nil {
		return fmt.Errorf("exclusive file published but private stage cleanup failed: %w", err)
	}
	cleanupPending = false
	directory, err := root.Open(relativeDir)
	if err != nil {
		return fmt.Errorf("failed to open exclusive-write directory for sync: %w", err)
	}
	syncErr := syncDirectoryFile(directory)
	closeErr := directory.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("failed to sync exclusive-write directory: %w", err)
	}
	return nil
}

const exclusiveStageDirectoryPrefix = ".exclusive-write-"

type privateExclusiveStage struct {
	workspace      *os.Root
	root           *os.Root
	workspacePath  string
	directory      string
	directoryInfo  fs.FileInfo
	file           *os.File
	fileName       string
	fileIdentity   fs.FileInfo
	cleanupStarted bool
}

func createPrivateExclusiveStage(workspace *os.Root, workspacePath, relativeDir string) (*privateExclusiveStage, error) {
	const attempts = 100
	for range attempts {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return nil, fmt.Errorf("failed to generate exclusive stage directory: %w", err)
		}
		directory := exclusiveStageDirectoryPrefix + hex.EncodeToString(random)
		if relativeDir != "." {
			directory = filepath.Join(relativeDir, directory)
		}
		if err := workspace.Mkdir(directory, 0o700); errors.Is(err, fs.ErrExist) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to create private exclusive stage: %w", err)
		}
		directoryInfo, err := workspace.Lstat(directory)
		if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
			_ = workspace.Remove(directory)
			return nil, errors.New("private exclusive stage directory is unsafe")
		}
		stageRoot, err := workspace.OpenRoot(directory)
		if err != nil {
			_ = workspace.Remove(directory)
			return nil, fmt.Errorf("failed to bind private exclusive stage: %w", err)
		}
		openedDirectory, err := stageRoot.Stat(".")
		if err != nil || !openedDirectory.IsDir() || !os.SameFile(directoryInfo, openedDirectory) {
			_ = stageRoot.Close()
			_ = workspace.Remove(directory)
			return nil, errors.New("private exclusive stage directory changed while opening")
		}
		file, fileName, err := createExclusiveRootTemp(stageRoot, ".")
		if err != nil {
			_ = stageRoot.Close()
			_ = workspace.Remove(directory)
			return nil, err
		}
		fileIdentity, err := file.Stat()
		if err != nil || !fileIdentity.Mode().IsRegular() {
			_ = file.Close()
			_ = stageRoot.Remove(fileName)
			_ = stageRoot.Close()
			_ = workspace.Remove(directory)
			return nil, errors.New("private exclusive staging file is unsafe")
		}
		return &privateExclusiveStage{
			workspace: workspace, root: stageRoot, workspacePath: workspacePath,
			directory: directory, directoryInfo: directoryInfo,
			file: file, fileName: fileName, fileIdentity: fileIdentity,
		}, nil
	}
	return nil, errors.New("failed to allocate a private exclusive stage directory")
}

func (s *privateExclusiveStage) relativePath() string {
	return filepath.Join(s.directory, s.fileName)
}

func (s *privateExclusiveStage) absolutePath() string {
	return filepath.Join(s.workspacePath, s.relativePath())
}

func (s *privateExclusiveStage) verify(expected []byte, mode fs.FileMode) error {
	if s == nil || s.workspace == nil || s.root == nil || s.file == nil || s.fileIdentity == nil {
		return errors.New("private exclusive stage is incomplete")
	}
	directoryAtPath, err := s.workspace.Lstat(s.directory)
	if err != nil || !directoryAtPath.IsDir() || !os.SameFile(s.directoryInfo, directoryAtPath) {
		return errors.New("private exclusive stage directory changed")
	}
	openedDirectory, err := s.root.Stat(".")
	if err != nil || !openedDirectory.IsDir() || !os.SameFile(s.directoryInfo, openedDirectory) {
		return errors.New("bound private exclusive stage directory changed")
	}
	openedFile, err := s.file.Stat()
	if err != nil || !openedFile.Mode().IsRegular() || !os.SameFile(s.fileIdentity, openedFile) || !PermMatchesRequested(openedFile.Mode(), mode) {
		return errors.New("retained exclusive staging file changed")
	}
	namedFile, err := s.root.Lstat(s.fileName)
	if err != nil || !namedFile.Mode().IsRegular() || !os.SameFile(s.fileIdentity, namedFile) {
		return errors.New("exclusive staging file path changed")
	}
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind exclusive staging file: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(s.file, int64(len(expected))+1))
	if err != nil {
		return fmt.Errorf("read exclusive staging file: %w", err)
	}
	if len(data) != len(expected) || !bytes.Equal(data, expected) {
		return errors.New("exclusive staging bytes changed")
	}
	return nil
}

// PermMatchesRequested reports whether actual retains the permission intent
// of requested. Windows exposes ACL-backed writable regular files as 0666
// regardless of the requested Unix permission bits, so only the writable
// attribute is meaningful there.
func PermMatchesRequested(actual, requested fs.FileMode) bool {
	if runtime.GOOS == "windows" {
		return (actual.Perm()&0o200 != 0) == (requested.Perm()&0o200 != 0)
	}
	return actual.Perm() == requested.Perm()
}

func (s *privateExclusiveStage) cleanup() error {
	if s == nil || s.cleanupStarted {
		return nil
	}
	s.cleanupStarted = true
	var cleanupErrors []error
	if s.file != nil {
		cleanupErrors = append(cleanupErrors, s.file.Close())
		s.file = nil
	}
	if s.root != nil {
		if current, err := s.root.Lstat(s.fileName); err == nil && s.fileIdentity != nil && os.SameFile(s.fileIdentity, current) {
			cleanupErrors = append(cleanupErrors, s.root.Remove(s.fileName))
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
		cleanupErrors = append(cleanupErrors, s.root.Close())
		s.root = nil
	}
	if s.workspace != nil {
		if current, err := s.workspace.Lstat(s.directory); err == nil && os.SameFile(s.directoryInfo, current) {
			cleanupErrors = append(cleanupErrors, s.workspace.Remove(s.directory))
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func createExclusiveRootTemp(root *os.Root, dir string) (*os.File, string, error) {
	const attempts = 100
	for range attempts {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return nil, "", fmt.Errorf("failed to generate exclusive staging name: %w", err)
		}
		name := ".tmp-exclusive-" + hex.EncodeToString(random)
		if dir != "." {
			name = filepath.Join(dir, name)
		}
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to create exclusive staging file: %w", err)
		}
		return file, name, nil
	}
	return nil, "", errors.New("failed to allocate an exclusive staging file")
}

func writeFileAtomic(fs atomicFS, path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := fs.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	tmp, err := fs.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if written, err := tmp.Write(data); err != nil {
		// #nosec G104 -- cleanup best-effort during write failure
		tmp.Close()
		// #nosec G104 -- cleanup best-effort during write failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	} else if written != len(data) {
		_ = tmp.Close()
		_ = fs.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", io.ErrShortWrite)
	}

	if err := tmp.Chmod(perm); err != nil {
		// #nosec G104 -- cleanup best-effort on close failure
		tmp.Close()
		// #nosec G104 -- cleanup best-effort on chmod failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = fs.Remove(tmpName)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	stagedInfo, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		_ = fs.Remove(tmpName)
		return fmt.Errorf("failed to inspect staged file: %w", err)
	}
	if !stagedInfo.Mode().IsRegular() {
		_ = tmp.Close()
		_ = fs.Remove(tmpName)
		return errors.New("staged file is not regular")
	}
	namedInfo, err := fs.Lstat(tmpName)
	if err != nil || !namedInfo.Mode().IsRegular() || !fs.SameFile(stagedInfo, namedInfo) {
		_ = tmp.Close()
		_ = fs.Remove(tmpName)
		return errors.New("staged file path changed before close")
	}

	if err := tmp.Close(); err != nil {
		// #nosec G104 -- cleanup best-effort on close failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	namedInfo, err = fs.Lstat(tmpName)
	if err != nil || !namedInfo.Mode().IsRegular() || !fs.SameFile(stagedInfo, namedInfo) {
		_ = fs.Remove(tmpName)
		return errors.New("staged file path changed before activation")
	}

	if err := fs.Rename(tmpName, path); err != nil {
		// #nosec G104 -- cleanup best-effort on rename failure
		fs.Remove(tmpName)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	if err := fs.SyncDir(dir); err != nil {
		return fmt.Errorf("file activated but failed to sync parent directory: %w", err)
	}

	return nil
}
