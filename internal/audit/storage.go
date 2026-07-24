package audit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// auditDirectory is an identity-bound handle to the real parent directory of
// an audit log. All log and append-lock leaf operations are relative to root.
type auditDirectory struct {
	root      *os.Root
	directory *os.File
	path      string
	identity  fs.FileInfo
}

func (d *auditDirectory) close() {
	if d == nil {
		return
	}
	if d.directory != nil {
		_ = d.directory.Close()
	}
	if d.root != nil {
		_ = d.root.Close()
	}
}

type auditScope struct {
	path     string
	base     string
	lockBase string
	parent   *auditDirectory
}

func (s *auditScope) close() {
	if s != nil {
		s.parent.close()
	}
}

func openAuditScope(path string, createParent bool) (*auditScope, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve audit path: %w", err)
	}
	absPath = filepath.Clean(absPath)
	base := filepath.Base(absPath)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return nil, fmt.Errorf("invalid audit log path %q", path)
	}
	parent, err := openAuditDirectory(filepath.Dir(absPath), createParent)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, nil
	}
	return &auditScope{
		path:     absPath,
		base:     base,
		lockBase: base + appendLockSuffix,
		parent:   parent,
	}, nil
}

func auditPathAnchor(path string) (string, string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	absPath = filepath.Clean(absPath)
	volume := filepath.VolumeName(absPath)
	anchor := volume + string(filepath.Separator)
	if volume == "" {
		anchor = string(filepath.Separator)
	}
	relative, err := filepath.Rel(anchor, absPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		if err != nil {
			return "", "", err
		}
		return "", "", fmt.Errorf("audit directory %q escapes its volume root", path)
	}
	return anchor, relative, nil
}

// openAuditDirectory rejects every symlink/reparse component, binds each
// opened directory back to the lstat identity, and retains the final handle.
func openAuditDirectory(path string, create bool) (*auditDirectory, error) {
	anchor, relative, err := auditPathAnchor(path)
	if err != nil {
		return nil, err
	}
	anchorBefore, err := os.Lstat(anchor)
	if err != nil {
		return nil, err
	}
	if anchorBefore.Mode()&os.ModeSymlink != 0 || !anchorBefore.IsDir() {
		return nil, fmt.Errorf("unsafe audit volume root %s", anchor)
	}
	current, err := os.OpenRoot(anchor)
	if err != nil {
		return nil, err
	}
	anchorOpened, err := current.Stat(".")
	anchorAfter, lstatErr := os.Lstat(anchor)
	if err != nil || lstatErr != nil || anchorAfter.Mode()&os.ModeSymlink != 0 || !anchorAfter.IsDir() || !os.SameFile(anchorBefore, anchorOpened) || !os.SameFile(anchorOpened, anchorAfter) {
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		if lstatErr != nil {
			return nil, lstatErr
		}
		return nil, fmt.Errorf("audit volume root changed while opening %s", anchor)
	}

	ownedCurrent := true
	if relative != "." {
		components := strings.Split(relative, string(filepath.Separator))
		for componentIndex, component := range components {
			if component == "" || component == "." || component == ".." {
				_ = current.Close()
				return nil, fmt.Errorf("unsafe audit directory component %q", component)
			}
			entry, statErr := current.Lstat(component)
			if errors.Is(statErr, fs.ErrNotExist) {
				if !create {
					_ = current.Close()
					return nil, nil
				}
				if mkdirErr := current.Mkdir(component, 0o750); mkdirErr != nil && !errors.Is(mkdirErr, fs.ErrExist) {
					_ = current.Close()
					return nil, fmt.Errorf("create audit directory component %s: %w", component, mkdirErr)
				}
				entry, statErr = current.Lstat(component)
			}
			if statErr != nil {
				_ = current.Close()
				return nil, statErr
			}
			if entry.Mode()&os.ModeSymlink != 0 && componentIndex == 0 {
				// macOS exposes root-owned compatibility aliases such as /var ->
				// /private/var and /tmp -> /private/tmp. Only this volume-root
				// component is trusted; links at every workspace-controlled depth
				// remain a hard failure.
				target, linkErr := current.Readlink(component)
				_ = current.Close()
				if linkErr != nil {
					return nil, linkErr
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(anchor, target)
				}
				resolved := target
				if componentIndex+1 < len(components) {
					resolved = filepath.Join(append([]string{target}, components[componentIndex+1:]...)...)
				}
				return openAuditDirectory(resolved, create)
			}
			if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
				_ = current.Close()
				return nil, fmt.Errorf("unsafe audit directory component %s", component)
			}
			child, openErr := current.OpenRoot(component)
			if openErr != nil {
				_ = current.Close()
				return nil, fmt.Errorf("open audit directory component %s: %w", component, openErr)
			}
			opened, childStatErr := child.Stat(".")
			after, afterErr := current.Lstat(component)
			if childStatErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(entry, opened) || !os.SameFile(opened, after) {
				_ = child.Close()
				_ = current.Close()
				if childStatErr != nil {
					return nil, childStatErr
				}
				if afterErr != nil {
					return nil, afterErr
				}
				return nil, fmt.Errorf("audit directory component changed while opening %s", component)
			}
			if ownedCurrent {
				_ = current.Close()
			}
			current = child
			ownedCurrent = true
		}
	}

	identity, err := current.Stat(".")
	if err != nil {
		_ = current.Close()
		return nil, err
	}
	directory, err := current.Open(".")
	if err != nil {
		_ = current.Close()
		return nil, fmt.Errorf("open audit directory handle: %w", err)
	}
	directoryIdentity, err := directory.Stat()
	if err != nil || !directoryIdentity.IsDir() || !os.SameFile(identity, directoryIdentity) {
		_ = directory.Close()
		_ = current.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("audit directory identity changed while binding %s", path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		_ = directory.Close()
		_ = current.Close()
		return nil, err
	}
	return &auditDirectory{root: current, directory: directory, path: filepath.Clean(absPath), identity: identity}, nil
}

func (s *auditScope) verifyParent() error {
	if s == nil || s.parent == nil {
		return errors.New("audit directory handle is unavailable")
	}
	current, err := openAuditDirectory(s.parent.path, false)
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("audit directory disappeared during operation")
	}
	defer current.close()
	if !os.SameFile(s.parent.identity, current.identity) {
		return errors.New("audit directory changed during operation")
	}
	return nil
}

type scopedAuditFile struct {
	file     *os.File
	name     string
	identity fs.FileInfo
}

func (f *scopedAuditFile) close() error {
	if f == nil || f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

func (s *auditScope) openRegular(name string, flags int, create bool) (*scopedAuditFile, error) {
	for range 3 {
		entry, err := s.parent.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				return nil, nil
			}
			file, createErr := s.parent.root.OpenFile(name, flags|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(createErr, fs.ErrExist) {
				continue
			}
			if createErr != nil {
				return nil, createErr
			}
			if chmodErr := file.Chmod(0o600); chmodErr != nil {
				_ = file.Close()
				return nil, chmodErr
			}
			entry, err = s.parent.root.Lstat(name)
			if err != nil {
				_ = file.Close()
				return nil, err
			}
			opened, statErr := file.Stat()
			if statErr != nil || entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
				_ = file.Close()
				if statErr != nil {
					return nil, statErr
				}
				return nil, fmt.Errorf("audit file changed while creating %s", name)
			}
			if err := requireSingleAuditLink(file); err != nil {
				_ = file.Close()
				return nil, fmt.Errorf("unsafe audit file %s: %w", name, err)
			}
			return &scopedAuditFile{file: file, name: name, identity: opened}, nil
		}
		if err != nil {
			return nil, err
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe audit file %s", name)
		}
		file, err := s.parent.root.OpenFile(name, flags, 0)
		if err != nil {
			return nil, err
		}
		opened, statErr := file.Stat()
		current, currentErr := s.parent.root.Lstat(name)
		if statErr != nil || currentErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) || !os.SameFile(opened, current) {
			_ = file.Close()
			if statErr != nil {
				return nil, statErr
			}
			if currentErr != nil {
				return nil, currentErr
			}
			return nil, fmt.Errorf("audit file changed while opening %s", name)
		}
		if err := requireSingleAuditLink(file); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("unsafe audit file %s: %w", name, err)
		}
		return &scopedAuditFile{file: file, name: name, identity: opened}, nil
	}
	return nil, fmt.Errorf("audit file %s changed repeatedly while opening", name)
}

func (s *auditScope) verifyOpenFile(opened *scopedAuditFile) error {
	if opened == nil || opened.file == nil {
		return errors.New("audit file handle is unavailable")
	}
	fileInfo, err := opened.file.Stat()
	if err != nil {
		return err
	}
	current, err := s.parent.root.Lstat(opened.name)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !fileInfo.Mode().IsRegular() || !os.SameFile(opened.identity, fileInfo) || !os.SameFile(fileInfo, current) {
		return fmt.Errorf("audit file %s changed during operation", opened.name)
	}
	if err := requireSingleAuditLink(opened.file); err != nil {
		return fmt.Errorf("unsafe audit file %s: %w", opened.name, err)
	}
	return s.verifyParent()
}
