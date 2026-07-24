package lock

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxLockFileBytes = 64 << 10

// lockStorage is an identity-bound handle to one lock directory. All leaf
// operations are relative to root, rather than reconstructed absolute paths.
// directory is retained for the platform's handle-relative exclusive rename.
type lockStorage struct {
	root      *os.Root
	directory *os.File
	relative  string
	absolute  string
	identity  fs.FileInfo
}

func (s *lockStorage) close() {
	if s == nil {
		return
	}
	if s.directory != nil {
		_ = s.directory.Close()
	}
	if s.root != nil {
		_ = s.root.Close()
	}
}

type lockStorageSet struct {
	workspace         *os.Root
	workspacePath     string
	workspaceIdentity fs.FileInfo
	canonicalRelative string
	legacyRelative    string
	canonical         *lockStorage
	legacy            *lockStorage
	sameStorage       bool
}

func (set *lockStorageSet) close() {
	if set == nil {
		return
	}
	if set.legacy != nil && set.legacy != set.canonical {
		set.legacy.close()
	}
	set.canonical.close()
	if set.workspace != nil {
		_ = set.workspace.Close()
	}
}

func (m *Manager) canonicalRelativeDir() (string, error) {
	directory := filepath.Join(m.opts.RootDir, "locks")
	if m.lifecycle != nil {
		directory = m.lifecycle.LocksDir()
	}
	relative, err := filepath.Rel(m.opts.RootDir, directory)
	if err != nil {
		return "", fmt.Errorf("resolve lock directory: %w", err)
	}
	relative = filepath.Clean(relative)
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe lock directory %q", directory)
	}
	return relative, nil
}

func (m *Manager) openStorageSet(createCanonical bool) (*lockStorageSet, error) {
	workspace, workspaceIdentity, err := openBoundWorkspace(m.opts.RootDir)
	if err != nil {
		return nil, fmt.Errorf("open lock workspace: %w", err)
	}
	set := &lockStorageSet{
		workspace:         workspace,
		workspacePath:     m.opts.RootDir,
		workspaceIdentity: workspaceIdentity,
		legacyRelative:    "locks",
	}
	failed := true
	defer func() {
		if failed {
			set.close()
		}
	}()

	set.canonicalRelative, err = m.canonicalRelativeDir()
	if err != nil {
		return nil, err
	}
	set.sameStorage = filepath.Clean(set.canonicalRelative) == filepath.Clean(set.legacyRelative)
	set.canonical, err = openBoundStorage(workspace, m.opts.RootDir, set.canonicalRelative, createCanonical)
	if err != nil {
		return nil, fmt.Errorf("open canonical lock storage: %w", err)
	}
	if set.sameStorage {
		set.legacy = set.canonical
	} else {
		set.legacy, err = openBoundStorage(workspace, m.opts.RootDir, set.legacyRelative, false)
		if err != nil {
			return nil, fmt.Errorf("open legacy lock storage: %w", err)
		}
	}
	failed = false
	return set, nil
}

func openBoundWorkspace(path string) (*os.Root, fs.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, nil, fmt.Errorf("unsafe workspace root %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	opened, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, nil, err
	}
	after, err := os.Lstat(path)
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		_ = root.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("workspace root changed while opening %s", path)
	}
	return root, opened, nil
}

// openBoundStorage walks one component at a time. A component is lstat'd,
// opened without accepting a link as the terminal component, and matched back
// to its directory entry before the next component is considered. The final
// os.Root and directory file descriptor remain open for the entire operation.
func openBoundStorage(workspace *os.Root, workspacePath, relative string, create bool) (*lockStorage, error) {
	clean := filepath.Clean(relative)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("unsafe relative lock directory %q", relative)
	}
	components := strings.Split(clean, string(filepath.Separator))
	current := workspace
	ownedCurrent := false
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			if ownedCurrent {
				_ = current.Close()
			}
			return nil, fmt.Errorf("unsafe lock directory component %q", component)
		}
		entry, err := current.Lstat(component)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				if ownedCurrent {
					_ = current.Close()
				}
				return nil, nil
			}
			if err := current.Mkdir(component, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
				if ownedCurrent {
					_ = current.Close()
				}
				return nil, fmt.Errorf("create lock directory component %s: %w", component, err)
			}
			entry, err = current.Lstat(component)
		}
		if err != nil {
			if ownedCurrent {
				_ = current.Close()
			}
			return nil, err
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
			if ownedCurrent {
				_ = current.Close()
			}
			return nil, fmt.Errorf("unsafe lock directory component %s", component)
		}
		child, err := current.OpenRoot(component)
		if err != nil {
			if ownedCurrent {
				_ = current.Close()
			}
			return nil, fmt.Errorf("open lock directory component %s: %w", component, err)
		}
		opened, statErr := child.Stat(".")
		after, lstatErr := current.Lstat(component)
		if statErr != nil || lstatErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(entry, opened) || !os.SameFile(opened, after) {
			_ = child.Close()
			if ownedCurrent {
				_ = current.Close()
			}
			if statErr != nil {
				return nil, statErr
			}
			if lstatErr != nil {
				return nil, lstatErr
			}
			return nil, fmt.Errorf("lock directory component changed while opening %s", component)
		}
		if ownedCurrent {
			_ = current.Close()
		}
		current = child
		ownedCurrent = true
	}

	identity, err := current.Stat(".")
	if err != nil {
		_ = current.Close()
		return nil, err
	}
	directory, err := current.Open(".")
	if err != nil {
		_ = current.Close()
		return nil, fmt.Errorf("open lock directory handle: %w", err)
	}
	directoryIdentity, err := directory.Stat()
	if err != nil || !directoryIdentity.IsDir() || !os.SameFile(identity, directoryIdentity) {
		_ = directory.Close()
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("lock directory identity changed while binding %s", relative)
	}
	return &lockStorage{
		root:      current,
		directory: directory,
		relative:  clean,
		absolute:  filepath.Join(workspacePath, clean),
		identity:  identity,
	}, nil
}

func (set *lockStorageSet) ensureLegacy() error {
	if set.sameStorage || set.legacy != nil {
		return nil
	}
	legacy, err := openBoundStorage(set.workspace, set.workspacePath, set.legacyRelative, false)
	if err != nil {
		return fmt.Errorf("open legacy lock storage: %w", err)
	}
	set.legacy = legacy
	return nil
}

func (set *lockStorageSet) verifyWorkspace() error {
	current, err := os.Lstat(set.workspacePath)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(set.workspaceIdentity, current) {
		return fmt.Errorf("workspace root changed during lock operation")
	}
	return nil
}

func (set *lockStorageSet) verifyStorage(storage *lockStorage) error {
	if storage == nil {
		return nil
	}
	current, err := openBoundStorage(set.workspace, set.workspacePath, storage.relative, false)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("lock storage %s disappeared during operation", storage.relative)
	}
	defer current.close()
	if !os.SameFile(storage.identity, current.identity) {
		return fmt.Errorf("lock storage %s changed during operation", storage.relative)
	}
	return nil
}

func (set *lockStorageSet) verifyPaths() error {
	if err := set.verifyWorkspace(); err != nil {
		return err
	}
	if err := set.verifyStorage(set.canonical); err != nil {
		return err
	}
	if set.legacy != nil && set.legacy != set.canonical {
		if err := set.verifyStorage(set.legacy); err != nil {
			return err
		}
	} else if !set.sameStorage {
		appeared, err := openBoundStorage(set.workspace, set.workspacePath, set.legacyRelative, false)
		if err != nil {
			return err
		}
		if appeared != nil {
			appeared.close()
			return fmt.Errorf("legacy lock storage appeared during operation")
		}
	}
	return nil
}

func (set *lockStorageSet) storageFor(record *lockRecord) (*lockStorage, error) {
	if record == nil {
		return nil, fmt.Errorf("%w: lock record disappeared", ErrLockChanged)
	}
	storage := set.canonical
	if record.legacy {
		storage = set.legacy
	}
	if storage == nil || record.storageIdentity == nil || !os.SameFile(record.storageIdentity, storage.identity) {
		return nil, fmt.Errorf("%w: lock storage identity changed", ErrLockChanged)
	}
	return storage, nil
}

func lockFileName(id string) string {
	return id + ".lock"
}

func (s *lockStorage) load(id string, legacy bool) (*lockRecord, error) {
	return s.loadNamed(id, lockFileName(id), legacy)
}

func (s *lockStorage) loadNamed(id, name string, legacy bool) (_ *lockRecord, err error) {
	before, err := s.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe lock file %s", filepath.Join(s.absolute, name))
	}
	file, err := s.root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("lock file changed while opening %s", filepath.Join(s.absolute, name))
	}
	if err := requireSingleLink(file); err != nil {
		return nil, fmt.Errorf("unsafe lock file %s: %w", filepath.Join(s.absolute, name), err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLockFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLockFileBytes {
		return nil, fmt.Errorf("lock file exceeds %d bytes", maxLockFileBytes)
	}
	openedAfter, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, openedAfter) || !os.SameFile(openedAfter, current) {
		return nil, fmt.Errorf("lock file changed while reading %s", filepath.Join(s.absolute, name))
	}
	if err := requireSingleLink(file); err != nil {
		return nil, fmt.Errorf("unsafe lock file %s: %w", filepath.Join(s.absolute, name), err)
	}
	info, err := parseLockData(data, id)
	if err != nil {
		return nil, err
	}
	return &lockRecord{
		path:            filepath.Join(s.absolute, lockFileName(id)),
		name:            name,
		legacy:          legacy,
		data:            data,
		info:            info,
		storageIdentity: s.identity,
		fileIdentity:    openedAfter,
	}, nil
}

type stagedLock struct {
	name     string
	data     []byte
	identity fs.FileInfo
}

func (s *lockStorage) prepare(data []byte) (_ *stagedLock, err error) {
	var file *os.File
	var name string
	for range 16 {
		token, tokenErr := newLockToken()
		if tokenErr != nil {
			return nil, tokenErr
		}
		name = ".lock-publish-" + token
		file, err = s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create lock staging file: %w", err)
		}
	}
	if file == nil {
		return nil, errors.New("unable to allocate unique lock staging file")
	}
	cleanup := true
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		if cleanup {
			_ = s.root.Remove(name)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("chmod lock staging file: %w", err)
	}
	if written, err := file.Write(data); err != nil {
		return nil, fmt.Errorf("write lock staging file: %w", err)
	} else if written != len(data) {
		return nil, fmt.Errorf("write lock staging file: %w", io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("sync lock staging file: %w", err)
	}
	identity, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !identity.Mode().IsRegular() {
		return nil, errors.New("lock staging file is not regular")
	}
	if err := requireSingleLink(file); err != nil {
		return nil, fmt.Errorf("unsafe lock staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close lock staging file: %w", err)
	}
	file = nil
	current, err := s.root.Lstat(name)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(identity, current) {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("lock staging file changed after writing")
	}
	cleanup = false
	return &stagedLock{name: name, data: append([]byte(nil), data...), identity: identity}, nil
}

func (s *lockStorage) removeStaged(stage *stagedLock) error {
	if stage == nil {
		return nil
	}
	_, err := s.root.Lstat(stage.name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.verifyStaged(stage); err != nil {
		return err
	}
	return s.root.Remove(stage.name)
}

func (s *lockStorage) verifyStaged(stage *stagedLock) (_ error) {
	entry, err := s.root.Lstat(stage.name)
	if err != nil {
		return err
	}
	if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() || !os.SameFile(stage.identity, entry) {
		return fmt.Errorf("%w: lock staging file changed", ErrLockChanged)
	}
	file, err := s.root.OpenFile(stage.name, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close() // #nosec G104 -- verification errors take precedence.
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(stage.identity, opened) {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: lock staging file identity changed", ErrLockChanged)
	}
	if err := requireSingleLink(file); err != nil {
		return fmt.Errorf("unsafe lock staging file: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLockFileBytes+1))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, stage.data) {
		return fmt.Errorf("%w: lock staging file contents changed", ErrLockChanged)
	}
	current, err := s.root.Lstat(stage.name)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: lock staging file changed while verifying", ErrLockChanged)
	}
	return requireSingleLink(file)
}

func sameRecordIdentity(expected, current *lockRecord) bool {
	if expected == nil || current == nil || expected.storageIdentity == nil || current.storageIdentity == nil || expected.fileIdentity == nil || current.fileIdentity == nil {
		return false
	}
	return os.SameFile(expected.storageIdentity, current.storageIdentity) &&
		os.SameFile(expected.fileIdentity, current.fileIdentity) &&
		bytes.Equal(expected.data, current.data)
}

type detachedLock struct {
	storage  *lockStorage
	original string
	hold     string
	record   *lockRecord
}

func (s *lockStorage) detachExact(record *lockRecord) (*detachedLock, error) {
	current, err := s.load(record.info.ID, record.legacy)
	if err != nil {
		return nil, err
	}
	if !sameRecord(record, current) {
		return nil, fmt.Errorf("%w: %s was replaced before quarantine", ErrLockChanged, record.info.ID)
	}
	var hold string
	for range 16 {
		token, tokenErr := newLockToken()
		if tokenErr != nil {
			return nil, tokenErr
		}
		hold = ".lock-hold-" + token
		if _, statErr := s.root.Lstat(hold); errors.Is(statErr, fs.ErrNotExist) {
			break
		} else if statErr != nil {
			return nil, statErr
		}
		hold = ""
	}
	if hold == "" {
		return nil, errors.New("unable to allocate unique lock quarantine name")
	}
	if err := renameStoragePathExclusive(s.directory, record.name, hold); err != nil {
		return nil, fmt.Errorf("quarantine lock file: %w", err)
	}
	detached := &detachedLock{storage: s, original: record.name, hold: hold, record: record}
	held, err := s.loadNamed(record.info.ID, hold, record.legacy)
	if err != nil {
		restoreErr := detached.restore()
		return nil, errors.Join(fmt.Errorf("inspect quarantined lock: %w", err), restoreErr)
	}
	if !sameRecordIdentity(record, held) {
		// The source changed between the pre-rename observation and the atomic
		// quarantine. Restore that exact unexpected file (not the stale expected
		// identity) when the original name is still free.
		unexpected := &detachedLock{storage: s, original: record.name, hold: hold, record: held}
		return nil, errors.Join(
			fmt.Errorf("%w: %s changed while quarantining", ErrLockChanged, record.info.ID),
			unexpected.restore(),
		)
	}
	return detached, nil
}

func (d *detachedLock) restore() error {
	if d == nil {
		return nil
	}
	if _, err := d.storage.root.Lstat(d.hold); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	held, err := d.storage.loadNamed(d.record.info.ID, d.hold, d.record.legacy)
	if err != nil {
		return err
	}
	if !sameRecordIdentity(d.record, held) {
		return fmt.Errorf("%w: quarantined lock %s changed before restore", ErrLockChanged, d.record.info.ID)
	}
	if err := renameStoragePathExclusive(d.storage.directory, d.hold, d.original); err != nil {
		return fmt.Errorf("restore quarantined lock %s: %w (preserved as %s)", d.record.info.ID, err, d.hold)
	}
	return nil
}

func (d *detachedLock) discard() error {
	held, err := d.storage.loadNamed(d.record.info.ID, d.hold, d.record.legacy)
	if err != nil {
		return err
	}
	if !sameRecordIdentity(d.record, held) {
		return fmt.Errorf("%w: quarantined lock %s changed before removal", ErrLockChanged, d.record.info.ID)
	}
	if err := d.storage.root.Remove(d.hold); err != nil {
		return err
	}
	return nil
}

func restoreDetached(detached []*detachedLock) error {
	var result error
	for index := len(detached) - 1; index >= 0; index-- {
		result = errors.Join(result, detached[index].restore())
	}
	return result
}

func discardDetached(detached []*detachedLock) error {
	var result error
	for _, item := range detached {
		result = errors.Join(result, item.discard())
	}
	return result
}
