package spec

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/util"
)

var (
	// ErrNotFound indicates a spec file could not be located.
	ErrNotFound = errors.New("spec not found")
)

const (
	maxSpecFileBytes  int64 = 8 << 20
	maxSpecTotalBytes int64 = 64 << 20
	maxSpecEntries          = 4096
)

// InvalidFile represents a spec file that failed to parse.
type InvalidFile struct {
	Path   string
	Reason string
}

// InvalidFileError aggregates multiple parse failures.
type InvalidFileError struct {
	Files []InvalidFile
}

func (e *InvalidFileError) Error() string {
	var parts []string
	for _, f := range e.Files {
		parts = append(parts, fmt.Sprintf("%s: %s", f.Path, f.Reason))
	}
	return fmt.Sprintf("failed to parse %d spec file(s): %s", len(e.Files), strings.Join(parts, "; "))
}

// Manager encapsulates spec file operations.
type Manager struct {
	opts        *config.Options
	log         *logrus.Entry
	lifecycle   *contract.Lifecycle
	contractErr error
}

// NewManager constructs a manager with shared configuration.
func NewManager(opts *config.Options) *Manager {
	lifecycle, contractErr := contract.Load(opts.RootDir)
	return &Manager{
		opts:        opts,
		log:         opts.Logger().WithField("component", "spec"),
		lifecycle:   lifecycle,
		contractErr: contractErr,
	}
}

// Lifecycle returns the workspace contract or its load error.
func (m *Manager) Lifecycle() (*contract.Lifecycle, error) {
	if m.contractErr != nil {
		return nil, m.contractErr
	}
	return m.lifecycle, nil
}

// SpecsDir returns the path to the specs directory.
func (m *Manager) SpecsDir() string {
	if m.lifecycle != nil {
		return m.lifecycle.SpecsDir()
	}
	return filepath.Join(m.opts.RootDir, "specs")
}

// SchemaPath returns the JSON schema path for validation.
func (m *Manager) SchemaPath() string {
	if m.lifecycle != nil {
		return filepath.Join(m.lifecycle.SchemasDir(), "system-spec.schema.json")
	}
	return filepath.Join(m.opts.RootDir, "schemas", "system-spec.schema.json")
}

// LoadByName returns the spec with the given filename.
func (m *Manager) LoadByName(name string) (*Spec, error) {
	if _, err := m.Lifecycle(); err != nil {
		return nil, err
	}
	specsDir := m.SpecsDir()
	if _, statErr := os.Stat(specsDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, statErr
	}

	// Support both with and without .md extension
	if !strings.HasSuffix(name, ".md") {
		name = name + ".md"
	}
	if filepath.Base(name) != name {
		return nil, fmt.Errorf("invalid spec name %q", name)
	}

	path := filepath.Join(specsDir, name)
	data, err := readRegularSpecFile(m.SpecsDir(), path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, fmt.Errorf("failed to read spec %s: %w", name, err)
	}

	spec, err := Parse(path, data)
	if err != nil {
		return nil, err
	}

	return spec, nil
}

// Save persists the spec to disk.
func (m *Manager) Save(spec *Spec) error {
	data, err := spec.Encode()
	if err != nil {
		return err
	}
	if m.opts.DryRun {
		m.log.WithFields(logrus.Fields{
			"action": "save",
			"path":   spec.Path,
			"dryRun": true,
		}).Info("Skipping write in dry-run mode")
		return nil
	}
	return util.WriteFileAtomic(spec.Path, data, 0o644)
}

// List returns all specs.
func (m *Manager) List() ([]*Spec, error) {
	if _, err := m.Lifecycle(); err != nil {
		return nil, err
	}
	specsDir := m.SpecsDir()
	beforeRoot, statErr := os.Lstat(specsDir)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("required spec inventory root is missing: %s", specsDir)
		}
		return nil, statErr
	}
	if !beforeRoot.IsDir() || beforeRoot.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("spec inventory root is not a real directory: %s", specsDir)
	}
	root, err := os.OpenRoot(specsDir)
	if err != nil {
		return nil, fmt.Errorf("open spec inventory root: %w", err)
	}
	defer func() { _ = root.Close() }()
	openedRoot, err := root.Stat(".")
	if err != nil || !openedRoot.IsDir() || !os.SameFile(beforeRoot, openedRoot) {
		return nil, fmt.Errorf("spec inventory root changed while opening: %s", specsDir)
	}
	currentRoot, err := os.Lstat(specsDir)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(beforeRoot, currentRoot) {
		return nil, fmt.Errorf("spec inventory root changed while opening: %s", specsDir)
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open spec inventory directory: %w", err)
	}
	defer directory.Close()

	var specs []*Spec
	var invalidFiles []InvalidFile
	entryCount := 0
	var totalBytes int64
	for {
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			entryCount++
			if entryCount > maxSpecEntries {
				return nil, fmt.Errorf("spec inventory exceeds %d entries", maxSpecEntries)
			}
			name := entry.Name()
			path := filepath.Join(specsDir, name)
			if entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("spec path is a symbolic link: %s", path)
			}
			if entry.IsDir() {
				continue
			}
			if filepath.Ext(name) != ".md" {
				continue
			}
			if entryErr := validateRegularSpecEntry(path, entry); entryErr != nil {
				return nil, entryErr
			}
			if strings.EqualFold(name, "index.md") || strings.EqualFold(name, "readme.md") {
				continue
			}
			data, fileErr := readRegularSpecFile(specsDir, path)
			if fileErr != nil {
				return nil, fileErr
			}
			totalBytes += int64(len(data))
			if totalBytes > maxSpecTotalBytes {
				return nil, fmt.Errorf("spec inventory exceeds %d bytes", maxSpecTotalBytes)
			}
			spec, parseErr := Parse(path, data)
			if parseErr != nil {
				invalidFiles = append(invalidFiles, InvalidFile{Path: path, Reason: parseErr.Error()})
				continue
			}
			specs = append(specs, spec)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("stream spec inventory: %w", readErr)
		}
	}

	if len(invalidFiles) > 0 {
		return nil, &InvalidFileError{Files: invalidFiles}
	}

	// Sort by filename for deterministic output
	sort.Slice(specs, func(i, j int) bool {
		return filepath.Base(specs[i].Path) < filepath.Base(specs[j].Path)
	})

	return specs, nil
}

func validateRegularSpecEntry(path string, entry fs.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("spec path is a symbolic link: %s", path)
	}
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect spec path %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("spec path is not a regular file: %s", path)
	}
	return nil
}

func readRegularSpecFile(root, path string) ([]byte, error) {
	data, _, err := util.ReadRegularFileWithin(root, path, maxSpecFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read spec file securely: %w", err)
	}
	return data, nil
}
