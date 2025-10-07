package feature

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/util"
)

var idPattern = regexp.MustCompile(`FTR-(\d{4})`)

// Manager encapsulates feature file operations.
type Manager struct {
	opts *config.Options
	log  *logrus.Entry
}

// NewManager constructs a manager with shared configuration.
func NewManager(opts *config.Options) *Manager {
	return &Manager{
		opts: opts,
		log:  opts.Logger().WithField("component", "feature"),
	}
}

// FeaturesDir returns the path to the features directory.
func (m *Manager) FeaturesDir() string {
	return filepath.Join(m.opts.RootDir, "features")
}

// TemplatePath returns the spec template path.
func (m *Manager) TemplatePath() string {
	return filepath.Join(m.opts.RootDir, "templates", "spec.md")
}

// SchemaPath returns the JSON schema path for validation.
func (m *Manager) SchemaPath() string {
	return filepath.Join(m.opts.RootDir, "schemas", "frontmatter.schema.json")
}

// LocksDir returns the directory for lock files.
func (m *Manager) LocksDir() string {
	return filepath.Join(m.opts.RootDir, "locks")
}

// NextID calculates the next available feature ID (e.g., FTR-0005).
func (m *Manager) NextID() (string, error) {
	featuresDir := m.FeaturesDir()
	if _, statErr := os.Stat(featuresDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "FTR-0001", nil
		}
		return "", statErr
	}
	maxID := 0
	err := filepath.WalkDir(featuresDir, func(path string, d fs.DirEntry, err error) error {

		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := filepath.Base(path)
		matches := idPattern.FindStringSubmatch(name)
		if len(matches) == 2 {
			idNum, convErr := strconv.Atoi(matches[1])
			if convErr == nil && idNum > maxID {
				maxID = idNum
			}
		}
		return nil
	})

	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	return fmt.Sprintf("FTR-%04d", maxID+1), nil
}

// LoadByID returns the feature with matching ID.
func (m *Manager) LoadByID(id string) (*Feature, error) {
	path, err := m.findByID(id)
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- feature paths are derived from repository structure during discovery
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read feature %s: %w", id, err)
	}
	feat, err := Parse(path, data)
	if err != nil {
		return nil, err
	}
	return feat, nil
}

// findByID locates the file path for a given feature ID.
func (m *Manager) findByID(id string) (string, error) {
	featuresDir := m.FeaturesDir()
	if _, statErr := os.Stat(featuresDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return "", statErr
	}
	var found string
	prefix := strings.ToUpper(id) + "-"
	err := filepath.WalkDir(featuresDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		if strings.HasPrefix(strings.ToUpper(filepath.Base(path)), prefix) {
			found = path
			return fs.SkipAll
		}
		return nil
	})

	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	if found == "" {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return found, nil
}

// Save persists the feature to disk.
func (m *Manager) Save(feat *Feature) error {
	data, err := feat.Encode()
	if err != nil {
		return err
	}
	if m.opts.DryRun {
		m.log.WithFields(logrus.Fields{
			"action": "save",
			"path":   feat.Path,
			"dryRun": true,
		}).Info("Skipping write in dry-run mode")
		return nil
	}
	return util.WriteFileAtomic(feat.Path, data, 0o644)
}

// CreateFeature creates a new feature using the template.
func (m *Manager) CreateFeature(title string, labels []string) (*Feature, error) {
	templateData, err := os.ReadFile(m.TemplatePath())
	if err != nil {
		return nil, fmt.Errorf("failed to read template: %w", err)
	}

	nextID, err := m.NextID()
	if err != nil {
		return nil, fmt.Errorf("failed to compute next feature ID: %w", err)
	}

	slug := util.Slugify(title)
	filename := fmt.Sprintf("%s-%s.md", nextID, slug)
	path := filepath.Join(m.FeaturesDir(), "backlog", filename)

	today := time.Now().Format("2006-01-02")

	feat, err := Parse(path, templateData)
	if err != nil {
		return nil, err
	}

	feat.Path = path
	feat.FrontMatter.ID = nextID
	feat.FrontMatter.Title = title
	feat.FrontMatter.Status = "backlog"
	feat.FrontMatter.Owner = "unassigned"
	feat.FrontMatter.Created = today
	feat.FrontMatter.Updated = today
	feat.FrontMatter.Labels = normalizeList(labels)
	feat.Body = strings.ReplaceAll(feat.Body, "<Feature Title>", title)

	if err := m.Save(feat); err != nil {
		return nil, err
	}

	m.log.WithFields(logrus.Fields{
		"action": "new",
		"id":     nextID,
		"path":   path,
		"labels": feat.LabelsAsYAML(),
	}).Info("Feature created")

	return feat, nil
}

// UpdateFeature persists changes to an existing feature.
func (m *Manager) UpdateFeature(feat *Feature) error {
	feat.UpdateTimestamp()
	return m.Save(feat)
}

// MoveFeature updates status and moves file accordingly.
func (m *Manager) MoveFeature(id, newStatus, owner string) (*Feature, string, error) {
	feat, err := m.LoadByID(id)
	if err != nil {
		return nil, "", err
	}

	currentStatus := strings.ToLower(feat.FrontMatter.Status)
	if err := ValidateTransition(currentStatus, newStatus); err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}

	if err := m.verifyDependenciesForMove(feat, newStatus); err != nil {
		return nil, "", err
	}

	newStatus = strings.ToLower(newStatus)
	feat.FrontMatter.Status = newStatus
	if owner != "" {
		feat.FrontMatter.Owner = owner
	} else if feat.FrontMatter.Owner == "" {
		feat.FrontMatter.Owner = "unassigned"
	}
	feat.UpdateTimestamp()

	newDir := filepath.Join(m.opts.RootDir, DirectoryForStatus(newStatus))
	if newDir == "" {
		return nil, "", fmt.Errorf("unknown status directory for %s", newStatus)
	}

	oldPath := feat.Path
	needsMove := !strings.EqualFold(newStatus, currentStatus) || filepath.Dir(feat.Path) != newDir

	if needsMove {
		if !m.opts.DryRun {
			if err := os.MkdirAll(newDir, 0o750); err != nil {
				return nil, "", fmt.Errorf("failed to create status directory: %w", err)
			}
		}
		newPath := filepath.Join(newDir, filepath.Base(feat.Path))
		feat.Path = newPath

		// Write the updated content to the new location first (atomically)
		if err := m.Save(feat); err != nil {
			return nil, "", fmt.Errorf("failed to write feature to new location: %w", err)
		}

		// Only remove the old file after the new one is successfully written
		if oldPath != newPath && !m.opts.DryRun {
			if err := os.Remove(oldPath); err != nil {
				// Log but don't fail - the new file is already written
				m.log.WithField("path", oldPath).Warn("Failed to remove old feature file")
			}
		}
	} else {
		// Status/owner changed but no directory move needed
		if err := m.Save(feat); err != nil {
			return nil, "", err
		}
	}

	summary := fmt.Sprintf("Moved %s to %s", feat.FrontMatter.ID, newStatus)
	m.log.WithFields(logrus.Fields{
		"action":   "move",
		"id":       feat.FrontMatter.ID,
		"from":     currentStatus,
		"to":       newStatus,
		"owner":    feat.FrontMatter.Owner,
		"new_path": feat.Path,
	}).Info("Feature moved")

	return feat, summary, nil
}

func (m *Manager) verifyDependenciesForMove(feat *Feature, target string) error {
	if strings.ToLower(target) != "in-progress" {
		return nil
	}
	for _, dep := range feat.FrontMatter.Dependencies {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		depFeature, err := m.LoadByID(dep)
		if err != nil {
			return fmt.Errorf("%w: dependency %s missing: %v", ErrDependencyBlocked, dep, err)
		}
		if strings.ToLower(depFeature.FrontMatter.Status) != "done" {
			return fmt.Errorf("%w: dependency %s is not done (status: %s)", ErrDependencyBlocked, dep, depFeature.FrontMatter.Status)
		}
	}
	return nil
}

// DeleteFeature removes the feature file from disk.
func (m *Manager) DeleteFeature(id string) (string, error) {
	path, err := m.findByID(id)
	if err != nil {
		return "", err
	}
	if m.opts.DryRun {
		m.log.WithFields(logrus.Fields{
			"action": "delete",
			"path":   path,
			"dryRun": true,
		}).Info("Skipping delete in dry-run mode")
		return path, nil
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("failed to delete feature: %w", err)
	}
	m.log.WithFields(logrus.Fields{
		"action": "delete",
		"path":   path,
	}).Info("Feature deleted")
	return path, nil
}

// List returns all features metadata.
func (m *Manager) List() ([]*Feature, error) {
	featuresDir := m.FeaturesDir()
	if _, statErr := os.Stat(featuresDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return []*Feature{}, nil
		}
		return nil, statErr
	}
	var features []*Feature
	err := filepath.WalkDir(featuresDir, func(path string, d fs.DirEntry, err error) error {

		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		name := filepath.Base(path)
		if strings.EqualFold(name, "index.md") || strings.EqualFold(name, "readme.md") {
			return nil
		}
		// #nosec G304 -- feature paths are derived from repository structure during discovery
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		feat, parseErr := Parse(path, data)
		if parseErr != nil {
			return parseErr
		}
		features = append(features, feat)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sort.Slice(features, func(i, j int) bool {
		return features[i].FrontMatter.ID < features[j].FrontMatter.ID
	})
	return features, nil
}

func normalizeList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
