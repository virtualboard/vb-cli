package spec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/util"
)

var (
	// ErrNotFound indicates a spec file could not be located.
	ErrNotFound = errors.New("spec not found")
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
	opts *config.Options
	log  *logrus.Entry
}

// NewManager constructs a manager with shared configuration.
func NewManager(opts *config.Options) *Manager {
	return &Manager{
		opts: opts,
		log:  opts.Logger().WithField("component", "spec"),
	}
}

// SpecsDir returns the path to the specs directory.
func (m *Manager) SpecsDir() string {
	return filepath.Join(m.opts.RootDir, "specs")
}

// SchemaPath returns the JSON schema path for validation.
func (m *Manager) SchemaPath() string {
	return filepath.Join(m.opts.RootDir, "schemas", "system-spec.schema.json")
}

// LoadByName returns the spec with the given filename.
func (m *Manager) LoadByName(name string) (*Spec, error) {
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

	path := filepath.Join(specsDir, name)
	// #nosec G304 -- spec paths are derived from repository structure
	data, err := os.ReadFile(path)
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
	specsDir := m.SpecsDir()
	if _, statErr := os.Stat(specsDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return []*Spec{}, nil
		}
		return nil, statErr
	}

	var specs []*Spec
	var invalidFiles []InvalidFile

	err := filepath.WalkDir(specsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Only walk the specs directory, not subdirectories
			if path != specsDir {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		name := filepath.Base(path)
		// Skip README and index files
		if strings.EqualFold(name, "index.md") || strings.EqualFold(name, "readme.md") {
			return nil
		}

		// #nosec G304 -- spec paths are derived from repository structure during discovery
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		spec, parseErr := Parse(path, data)
		if parseErr != nil {
			invalidFiles = append(invalidFiles, InvalidFile{
				Path:   path,
				Reason: parseErr.Error(),
			})
			return nil
		}

		specs = append(specs, spec)
		return nil
	})

	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
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
