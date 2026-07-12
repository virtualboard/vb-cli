package feature

import (
	"bytes"
	"crypto/sha256"
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

	"github.com/virtualboard/vb-cli/internal/audit"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/lock"
	"github.com/virtualboard/vb-cli/internal/util"
)

var idPattern = regexp.MustCompile(`FTR-(\d{4})`)

const maxFeatureFileBytes int64 = 8 << 20

const boardGraphOperationLockID = "op-board-graph"

type sourceSnapshot struct {
	root   string
	path   string
	info   os.FileInfo
	digest [sha256.Size]byte
	mode   fs.FileMode
}

// GuardedFeatureSnapshot captures one feature while its canonical per-feature
// operation guard is held. Data and Mode describe the exact regular file that
// was parsed into Feature.
type GuardedFeatureSnapshot struct {
	Feature *Feature
	Data    []byte
	Mode    fs.FileMode
}

// DeletePlan binds interactive deletion approval to the exact authorized
// feature path, inode, bytes, and mode observed before prompting.
type DeletePlan struct {
	Feature *Feature
	data    []byte
	source  *sourceSnapshot
}

// MutationBatch is a set of per-feature operation guards acquired in stable ID
// order. Values of this type are valid only for the duration of the callback
// passed to WithFeatureMutationBatch.
type MutationBatch struct {
	manager *Manager
	ids     map[string]struct{}
	active  bool
}

// Manager encapsulates feature file operations.
type Manager struct {
	opts               *config.Options
	log                *logrus.Entry
	lockMgr            *lock.Manager
	auditLog           *audit.Logger
	lifecycle          *contract.Lifecycle
	contractErr        error
	batchGuardAcquired func(string)
}

// NewManager constructs a manager with shared configuration.
func NewManager(opts *config.Options) *Manager {
	lifecycle, contractErr := contract.Load(opts.RootDir)
	var auditLog *audit.Logger
	if lifecycle != nil {
		auditLog, _ = audit.NewLogger(lifecycle.AuditLogPath()) // best-effort; nil on error
	}
	return &Manager{
		opts:        opts,
		log:         opts.Logger().WithField("component", "feature"),
		lockMgr:     lock.NewManager(opts),
		auditLog:    auditLog,
		lifecycle:   lifecycle,
		contractErr: contractErr,
	}
}

// auditEvent records a mutating operation to the audit log. Best-effort only.
func (m *Manager) auditEvent(action, featureID, details string) {
	if m.opts.DryRun {
		return
	}
	if m.auditLog != nil {
		_ = m.auditLog.Log(action, m.opts.EffectiveActor(), featureID, details)
	}
}

// withLock holds the cross-process operation guard until fn returns. The OS
// releases the advisory guard if the process exits, so no TTL can expire while
// a long-running mutation is still live.
func (m *Manager) withLock(lockID string, fn func() error) error {
	if m.lockMgr == nil || m.opts.DryRun {
		return fn()
	}
	if err := m.lockMgr.WithOperationGuard(lockID, fn); err != nil {
		// Callback errors (including typed CLI errors) must retain their concrete
		// type. The lock manager already adds context to acquisition failures.
		return err
	}
	return nil
}

// WithBoardGraphSnapshot holds the canonical board-graph guard while fn reads
// feature state and publishes any derived artifact. Feature mutations acquire
// the same guard before their per-feature guards, so a snapshot cannot be
// superseded by a spec commit before its caller finishes.
func (m *Manager) WithBoardGraphSnapshot(fn func() error) error {
	if fn == nil {
		return errors.New("board graph snapshot callback is required")
	}
	if _, err := m.Lifecycle(); err != nil {
		return err
	}
	return m.withLock(boardGraphOperationLockID, func() error {
		if err := m.recoverPendingFeatureTransactions(); err != nil {
			return err
		}
		return fn()
	})
}

func (m *Manager) withBoardGraphMutation(fn func() error) error {
	return m.WithBoardGraphSnapshot(fn)
}

func featureMutationLockID(id string) string {
	// Use the same guard namespace as user-facing feature locks. This makes the
	// authorization check and feature write linearizable with lock acquire,
	// release, expiry replacement, and force replacement.
	return id
}

// withFeatureMutation serializes every mutation of one immutable feature ID.
// Recovery runs before the guard is acquired so a transaction never attempts
// to recover its own journal while holding the same per-feature lock.
func (m *Manager) withFeatureMutation(id string, fn func() error) error {
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return err
	}
	if err := lifecycle.ValidateFeatureID(id); err != nil {
		return fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
	}
	err = m.withLock(featureMutationLockID(id), fn)
	if errors.Is(err, lock.ErrActiveLock) || errors.Is(err, lock.ErrLockChanged) || errors.Is(err, lock.ErrLockStorageConflict) {
		return fmt.Errorf("%w: %v", ErrLockConflict, err)
	}
	return err
}

// WithFeatureMutationBatch acquires the same cross-process guards used by
// ordinary feature mutations and user-facing locks. IDs are deduplicated and
// acquired lexicographically so overlapping bulk operations cannot deadlock.
// All guards remain held through fn, including any rollback it performs.
func (m *Manager) WithFeatureMutationBatch(ids []string, fn func(*MutationBatch) error) error {
	return m.withBoardGraphMutation(func() error {
		return m.withFeatureMutationBatchLocked(ids, fn)
	})
}

func (m *Manager) withFeatureMutationBatchLocked(ids []string, fn func(*MutationBatch) error) error {
	if fn == nil {
		return errors.New("feature mutation batch callback is required")
	}
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return err
	}
	unique := make(map[string]struct{}, len(ids))
	ordered := make([]string, 0, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if err := lifecycle.ValidateFeatureID(id); err != nil {
			return fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
		}
		if _, exists := unique[id]; exists {
			continue
		}
		unique[id] = struct{}{}
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	batch := &MutationBatch{manager: m, ids: unique}
	var acquire func(int) error
	acquire = func(index int) error {
		if index == len(ordered) {
			batch.active = true
			defer func() { batch.active = false }()
			return fn(batch)
		}
		return m.withLock(featureMutationLockID(ordered[index]), func() error {
			if m.batchGuardAcquired != nil {
				m.batchGuardAcquired(ordered[index])
			}
			return acquire(index + 1)
		})
	}
	if err := acquire(0); err != nil {
		if errors.Is(err, lock.ErrActiveLock) || errors.Is(err, lock.ErrLockChanged) || errors.Is(err, lock.ErrLockStorageConflict) {
			return fmt.Errorf("%w: %v", ErrLockConflict, err)
		}
		return err
	}
	return nil
}

func (b *MutationBatch) requireID(id string) error {
	if b == nil || b.manager == nil || !b.active {
		return errors.New("feature mutation batch is not active")
	}
	if _, ok := b.ids[id]; !ok {
		return fmt.Errorf("feature %s is not guarded by this mutation batch", id)
	}
	return nil
}

// Load reads one guarded feature and returns the exact bytes and mode captured
// by the manager's regular-file identity checks.
func (b *MutationBatch) Load(id string) (*GuardedFeatureSnapshot, error) {
	if err := b.requireID(id); err != nil {
		return nil, err
	}
	feat, err := b.manager.loadByID(id)
	if err != nil {
		return nil, err
	}
	data, err := verifyFeatureSource(feat)
	if err != nil {
		return nil, err
	}
	return &GuardedFeatureSnapshot{
		Feature: feat,
		Data:    append([]byte(nil), data...),
		Mode:    feat.source.mode.Perm(),
	}, nil
}

// Authorize performs ownership and active user-lock checks while the matching
// per-feature operation guard remains held.
func (b *MutationBatch) Authorize(feat *Feature, allowOwnerOverride bool) error {
	if feat == nil {
		return errors.New("cannot authorize a nil feature")
	}
	id := strings.TrimSpace(feat.FrontMatter.ID)
	if err := b.requireID(id); err != nil {
		return err
	}
	return b.manager.authorizeMutation(feat, allowOwnerOverride)
}

// CompareAndSwap replaces one guarded feature only while its canonical path,
// regular-file identity, mode, and bytes still match expected. Replacement is
// parsed and checked against the immutable feature identity before staging.
func (b *MutationBatch) CompareAndSwap(id, path string, expected, replacement []byte, mode fs.FileMode) error {
	if err := b.requireID(id); err != nil {
		return err
	}
	lifecycle, err := b.manager.Lifecycle()
	if err != nil {
		return err
	}
	candidate, err := Parse(path, replacement)
	if err != nil {
		return fmt.Errorf("parse guarded replacement for %s: %w", id, err)
	}
	if err := validateFeatureIdentity(lifecycle, path, candidate, id); err != nil {
		return err
	}
	currentPath, err := b.manager.findByID(id)
	if err != nil {
		return fmt.Errorf("%w: locate current feature %s: %v", ErrStaleFeature, id, err)
	}
	if filepath.Clean(currentPath) != filepath.Clean(path) {
		return fmt.Errorf("%w: feature %s moved from %s to %s", ErrStaleFeature, id, path, currentPath)
	}
	currentData, currentInfo, err := readRegularFeatureFileWithInfo(b.manager.opts.RootDir, currentPath)
	if err != nil {
		return fmt.Errorf("%w: securely read current feature %s: %v", ErrStaleFeature, id, err)
	}
	if !bytes.Equal(currentData, expected) || currentInfo.Mode().Perm() != mode.Perm() {
		return fmt.Errorf("%w: source bytes or mode changed for %s", ErrStaleFeature, id)
	}
	expectedFeature, err := Parse(path, expected)
	if err != nil {
		return fmt.Errorf("parse guarded expected feature for %s: %w", id, err)
	}
	if featureGraphRelevantChange(expectedFeature, candidate) {
		if err := b.manager.validateFeatureGraphCandidate(candidate); err != nil {
			return err
		}
	}
	currentSource := snapshotForSource(b.manager.opts.RootDir, currentPath, currentData, currentInfo)
	if b.manager.opts.DryRun {
		return nil
	}
	if err := b.manager.executeFeatureMutationTransaction(
		featureMutationReplace,
		id,
		path,
		path,
		expected,
		replacement,
		currentSource.mode,
		mode.Perm(),
		currentSource,
	); err != nil {
		return err
	}
	persisted, info, err := readRegularFeatureFileWithInfo(b.manager.opts.RootDir, path)
	if err != nil {
		return fmt.Errorf("verify guarded replacement for %s: %w", id, err)
	}
	if !bytes.Equal(persisted, replacement) || info.Mode().Perm() != mode.Perm() {
		return fmt.Errorf("%w: persisted bytes or mode changed for %s", ErrStaleFeature, id)
	}
	return nil
}

// FeaturesDir returns the path to the features directory.
func (m *Manager) FeaturesDir() string {
	if m.lifecycle != nil {
		return m.lifecycle.FeaturesDir()
	}
	return filepath.Join(m.opts.RootDir, "features")
}

// TemplatePath returns the feature template path.
func (m *Manager) TemplatePath() string {
	if m.lifecycle != nil {
		return filepath.Join(m.lifecycle.TemplatesDir(), "feature.md")
	}
	return filepath.Join(m.opts.RootDir, "templates", "feature.md")
}

// SchemaPath returns the JSON schema path for validation.
func (m *Manager) SchemaPath() string {
	if m.lifecycle != nil {
		return filepath.Join(m.lifecycle.SchemasDir(), "frontmatter.schema.json")
	}
	return filepath.Join(m.opts.RootDir, "schemas", "frontmatter.schema.json")
}

// IndexPath returns the canonical configured feature index path.
func (m *Manager) IndexPath() string {
	if m.lifecycle != nil {
		return m.lifecycle.IndexPath()
	}
	return filepath.Join(m.opts.RootDir, "features", "INDEX.md")
}

// LocksDir returns the directory for lock files.
func (m *Manager) LocksDir() string {
	if m.lifecycle != nil {
		return m.lifecycle.LocksDir()
	}
	return filepath.Join(m.opts.RootDir, "locks")
}

// SlugForTitle returns a contract-compliant, bounded feature slug.
func (m *Manager) SlugForTitle(title string) string {
	slug := util.Slugify(title)
	limit := 6
	if m.lifecycle != nil && m.lifecycle.SlugMaxWords() > 0 {
		limit = m.lifecycle.SlugMaxWords()
	}
	parts := strings.Split(slug, "-")
	if len(parts) > limit {
		parts = parts[:limit]
	}
	return strings.Join(parts, "-")
}

// Lifecycle returns the workspace lifecycle contract or its load error.
func (m *Manager) Lifecycle() (*contract.Lifecycle, error) {
	if m.contractErr != nil {
		return nil, m.contractErr
	}
	return m.lifecycle, nil
}

// DryRun reports whether the manager is configured to avoid writes.
func (m *Manager) DryRun() bool { return m.opts.DryRun }

func (m *Manager) requireActor() (string, error) {
	actor, err := m.opts.RequireActor()
	if err != nil {
		return "", err
	}
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return "", err
	}
	if err := lifecycle.ValidateOwner(actor, false); err != nil {
		return "", fmt.Errorf("%w: %v", config.ErrActorInvalid, err)
	}
	return actor, nil
}

// AuthorizeMutation enforces frontmatter ownership and active feature locks.
func (m *Manager) AuthorizeMutation(feat *Feature) error {
	return m.authorizeMutation(feat, false)
}

// AuthorizeMigration optionally permits an administrative owner override while
// always enforcing explicit actor identity and active feature locks.
func (m *Manager) AuthorizeMigration(feat *Feature, allowOwnerOverride bool) error {
	return m.authorizeMutation(feat, allowOwnerOverride)
}

func (m *Manager) authorizeMutation(feat *Feature, allowOwnerOverride bool) error {
	actor, err := m.requireActor()
	if err != nil {
		return err
	}
	owner := strings.TrimSpace(feat.FrontMatter.Owner)
	if !allowOwnerOverride && owner != "" && !strings.EqualFold(owner, "unassigned") && owner != actor {
		return fmt.Errorf("%w: %s is owned by %s (actor %s)", ErrOwnershipConflict, feat.FrontMatter.ID, owner, actor)
	}
	if err := m.lockMgr.CheckMutation(feat.FrontMatter.ID, actor); err != nil {
		if errors.Is(err, lock.ErrLockOwner) || errors.Is(err, lock.ErrLockStorageConflict) {
			return fmt.Errorf("%w: %v", ErrLockConflict, err)
		}
		return err
	}
	return nil
}

// RecordAudit appends a best-effort canonical audit event.
func (m *Manager) RecordAudit(action, featureID, details string) {
	m.auditEvent(action, featureID, details)
}

// NextID calculates the next available feature ID (e.g., FTR-0005).
func (m *Manager) NextID() (string, error) {
	if _, err := m.Lifecycle(); err != nil {
		return "", err
	}
	if err := m.recoverPendingMoves(); err != nil {
		return "", err
	}
	files, err := m.discoverFeatureFiles()
	if err != nil {
		return "", err
	}
	maxID := 0
	for _, file := range files {
		name := filepath.Base(file.path)
		if err := m.lifecycle.ValidateFilename(name); err != nil {
			return "", fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
		}
		matches := idPattern.FindStringSubmatch(name)
		if len(matches) == 2 {
			idNum, convErr := strconv.Atoi(matches[1])
			if convErr == nil && idNum > maxID {
				maxID = idNum
			}
		}
	}

	return fmt.Sprintf("FTR-%04d", maxID+1), nil
}

// LoadByID returns the feature with matching ID.
func (m *Manager) LoadByID(id string) (*Feature, error) {
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return nil, err
	}
	if err := lifecycle.ValidateFeatureID(id); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
	}
	if err := m.recoverPendingMoves(); err != nil {
		return nil, err
	}
	return m.loadByID(id)
}

// loadByID reads one feature without initiating transaction recovery. Callers
// holding a per-feature mutation guard use it after recovery has completed.
func (m *Manager) loadByID(id string) (*Feature, error) {
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return nil, err
	}
	if err := lifecycle.ValidateFeatureID(id); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
	}
	path, err := m.findByID(id)
	if err != nil {
		return nil, err
	}
	data, info, err := readRegularFeatureFileWithInfo(m.opts.RootDir, path)
	if err != nil {
		return nil, fmt.Errorf("failed to read feature %s: %w", id, err)
	}
	feat, err := Parse(path, data)
	if err != nil {
		return nil, err
	}
	if err := validateFeatureIdentity(lifecycle, path, feat, id); err != nil {
		return nil, err
	}
	feat.source = snapshotForSource(m.opts.RootDir, path, data, info)
	return feat, nil
}

// findByID locates the file path for a given feature ID.
func (m *Manager) findByID(id string) (string, error) {
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return "", err
	}
	if err := lifecycle.ValidateFeatureID(id); err != nil {
		return "", fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
	}
	files, err := m.discoverFeatureFiles()
	if err != nil {
		return "", err
	}
	var matches []string
	prefix := strings.ToUpper(id) + "-"
	for _, file := range files {
		if strings.HasPrefix(strings.ToUpper(filepath.Base(file.path)), prefix) {
			matches = append(matches, file.path)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("%w %s: %s", ErrDuplicateID, id, strings.Join(matches, ", "))
	}
	return matches[0], nil
}

// Save persists the feature to disk.
func (m *Manager) Save(feat *Feature) error {
	if feat == nil {
		return errors.New("cannot save a nil feature")
	}
	return m.withBoardGraphMutation(func() error {
		return m.saveFeature(feat)
	})
}

func (m *Manager) saveFeature(feat *Feature) error {
	id := strings.TrimSpace(feat.FrontMatter.ID)
	return m.withFeatureMutation(id, func() error {
		var current *Feature
		if feat.source == nil {
			if _, err := m.requireActor(); err != nil {
				return err
			}
		} else {
			var err error
			current, err = m.loadByID(id)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return fmt.Errorf("%w: source for %s no longer exists", ErrStaleFeature, id)
				}
				return err
			}
			if err := m.AuthorizeMutation(current); err != nil {
				return err
			}
		}
		if current == nil || featureGraphRelevantChange(current, feat) {
			if err := m.validateFeatureGraphCandidate(feat); err != nil {
				return err
			}
		}
		return m.saveFeatureLocked(feat)
	})
}

func (m *Manager) saveFeatureLocked(feat *Feature) error {
	lifecycle, err := m.Lifecycle()
	if err != nil {
		return err
	}
	id := strings.TrimSpace(feat.FrontMatter.ID)
	if err := validateFeatureIdentity(lifecycle, feat.Path, feat, id); err != nil {
		return err
	}
	expectedDirectory, ok := lifecycle.DirectoryForStatus(strings.TrimSpace(feat.FrontMatter.Status))
	if !ok || filepath.Clean(filepath.Dir(feat.Path)) != filepath.Clean(expectedDirectory) {
		return fmt.Errorf("%w: feature path %s is not a direct child of status %s", ErrIdentityMismatch, feat.Path, feat.FrontMatter.Status)
	}
	data, err := feat.Encode()
	if err != nil {
		return err
	}
	if feat.source != nil {
		if _, err := verifyFeatureSource(feat); err != nil {
			return err
		}
	}
	if m.opts.DryRun {
		m.log.WithFields(logrus.Fields{
			"action": "save",
			"path":   feat.Path,
			"dryRun": true,
		}).Info("Skipping write in dry-run mode")
		return nil
	}
	if feat.source == nil {
		if err := util.WriteFileExclusiveAtomicWithin(m.opts.RootDir, feat.Path, data, 0o644); err != nil {
			return fmt.Errorf("create feature without replacing an existing path: %w", err)
		}
	} else {
		expected, err := verifyFeatureSource(feat)
		if err != nil {
			return err
		}
		if err := m.executeFeatureMutationTransaction(
			featureMutationReplace,
			id,
			feat.Path,
			feat.Path,
			expected,
			data,
			feat.source.mode,
			0o644,
			feat.source,
		); err != nil {
			return err
		}
	}
	return refreshFeatureSource(m.opts.RootDir, feat, data)
}

func verifyFeatureSource(feat *Feature) ([]byte, error) {
	if feat == nil || feat.source == nil {
		return nil, fmt.Errorf("%w: source snapshot is unavailable", ErrStaleFeature)
	}
	if filepath.Clean(feat.Path) != feat.source.path {
		return nil, fmt.Errorf("%w: source path changed from %s to %s", ErrStaleFeature, feat.source.path, feat.Path)
	}
	return verifySourceSnapshot(feat.Path, feat.source)
}

func verifySourceSnapshot(path string, source *sourceSnapshot) ([]byte, error) {
	if source == nil || strings.TrimSpace(source.root) == "" || filepath.Clean(path) != source.path {
		return nil, fmt.Errorf("%w: source snapshot does not match %s", ErrStaleFeature, path)
	}
	data, info, err := readRegularFeatureFileWithInfo(source.root, path)
	if err != nil {
		return nil, fmt.Errorf("%w: read current source %s: %v", ErrStaleFeature, path, err)
	}
	digest := sha256.Sum256(data)
	if !os.SameFile(source.info, info) || source.digest != digest || source.mode != info.Mode() {
		return nil, fmt.Errorf("%w: source identity, content, or mode changed for %s", ErrStaleFeature, path)
	}
	return data, nil
}

func refreshFeatureSource(root string, feat *Feature, expected []byte) error {
	data, info, err := readRegularFeatureFileWithInfo(root, feat.Path)
	if err != nil {
		return fmt.Errorf("refresh feature source snapshot: %w", err)
	}
	if sha256.Sum256(data) != sha256.Sum256(expected) {
		return fmt.Errorf("%w: persisted bytes changed for %s", ErrStaleFeature, feat.Path)
	}
	feat.source = snapshotForSource(root, feat.Path, data, info)
	return nil
}

// MutateFeature loads, authorizes, transforms, and persists one feature while
// holding the canonical per-feature operation guard. The final write uses the
// identity and digest captured by the guarded load.
func (m *Manager) MutateFeature(id string, mutate func(*Feature) error) (*Feature, error) {
	if mutate == nil {
		return nil, errors.New("feature mutation callback is required")
	}
	var feat *Feature
	err := m.withBoardGraphMutation(func() error {
		return m.withFeatureMutation(id, func() error {
			var err error
			feat, err = m.loadByID(id)
			if err != nil {
				return err
			}
			if err := m.AuthorizeMutation(feat); err != nil {
				return err
			}
			originalGraph := cloneFeatureGraphFields(feat)
			if err := mutate(feat); err != nil {
				return err
			}
			if featureGraphRelevantChange(originalGraph, feat) {
				if err := m.validateFeatureGraphCandidate(feat); err != nil {
					return err
				}
			}
			return m.saveFeatureLocked(feat)
		})
	})
	if err != nil {
		return nil, err
	}
	return feat, nil
}

// CreateFeature creates a new feature using the template.
// Uses an operational lock to prevent ID-allocation races from concurrent calls.
func (m *Manager) CreateFeature(title string, labels []string) (*Feature, error) {
	return m.CreateFeatureValidated(title, labels, nil)
}

// CreateFeatureValidated builds and validates a candidate while holding the ID
// allocation lock, then persists it only if validate accepts the complete
// candidate. This prevents schema-invalid files and ID races from being
// committed between validation and creation.
func (m *Manager) CreateFeatureValidated(title string, labels []string, validate func(*Feature) error) (*Feature, error) {
	if _, err := m.requireActor(); err != nil {
		return nil, err
	}
	var feat *Feature
	err := m.withBoardGraphMutation(func() error {
		return m.withLock("op-create-feature", func() error {
			templateData, readErr := os.ReadFile(m.TemplatePath())
			if readErr != nil {
				return fmt.Errorf("failed to read template: %w", readErr)
			}

			nextID, idErr := m.NextID()
			if idErr != nil {
				return fmt.Errorf("failed to compute next feature ID: %w", idErr)
			}

			slug := m.SlugForTitle(title)
			filename := fmt.Sprintf("%s-%s.md", nextID, slug)
			lifecycle, contractErr := m.Lifecycle()
			if contractErr != nil {
				return contractErr
			}
			initialStatus := lifecycle.InitialStatus()
			initialDir, ok := lifecycle.DirectoryForStatus(initialStatus)
			if !ok {
				return fmt.Errorf("initial status %q has no directory", initialStatus)
			}
			path := filepath.Join(initialDir, filename)

			today := time.Now().Format("2006-01-02")

			parsed, parseErr := Parse(path, templateData)
			if parseErr != nil {
				return parseErr
			}

			parsed.Path = path
			parsed.FrontMatter.ID = nextID
			parsed.FrontMatter.Title = title
			parsed.FrontMatter.Status = initialStatus
			parsed.FrontMatter.Owner = "unassigned"
			parsed.FrontMatter.ImplementationOwner = "unassigned"
			parsed.FrontMatter.Created = today
			parsed.FrontMatter.Updated = today
			parsed.FrontMatter.StatusChanged = today
			parsed.FrontMatter.Labels = normalizeList(labels)
			parsed.Body = strings.ReplaceAll(parsed.Body, "<Feature Title>", title)
			if validate != nil {
				if validationErr := validate(parsed); validationErr != nil {
					return validationErr
				}
			}
			if graphErr := m.validateFeatureGraphCandidate(parsed); graphErr != nil {
				return graphErr
			}

			if saveErr := m.saveFeature(parsed); saveErr != nil {
				return saveErr
			}

			m.log.WithFields(logrus.Fields{
				"action": "new",
				"id":     nextID,
				"path":   path,
				"labels": parsed.LabelsAsYAML(),
			}).Info("Feature created")

			feat = parsed
			return nil
		})
	})
	if err == nil && feat != nil {
		m.auditEvent("create", feat.FrontMatter.ID, fmt.Sprintf("title=%s", feat.FrontMatter.Title))
	}
	return feat, err
}

// UpdateFeature persists changes to an existing feature.
func (m *Manager) UpdateFeature(feat *Feature) error {
	if feat == nil {
		return errors.New("cannot update a nil feature")
	}
	feat.UpdateTimestamp()
	return m.Save(feat)
}

// MoveFeature updates status and moves file accordingly.
// Uses the same per-feature operational guard as update, template, and delete.
func (m *Manager) MoveFeature(id, newStatus, owner string) (*Feature, string, error) {
	var feat *Feature
	var summary string
	err := m.withBoardGraphMutation(func() error {
		return m.withFeatureMutation(id, func() error {
			var loadErr error
			feat, loadErr = m.loadByID(id)
			if loadErr != nil {
				return loadErr
			}
			if authErr := m.AuthorizeMutation(feat); authErr != nil {
				return authErr
			}
			lifecycle, contractErr := m.Lifecycle()
			if contractErr != nil {
				return contractErr
			}

			currentStatus := strings.ToLower(feat.FrontMatter.Status)
			if currentStatus == strings.ToLower(strings.TrimSpace(newStatus)) {
				summary = fmt.Sprintf("Feature %s is already %s", feat.FrontMatter.ID, currentStatus)
				return nil
			}
			if transErr := lifecycle.ValidateTransition(currentStatus, newStatus); transErr != nil {
				return fmt.Errorf("%w: %v", ErrInvalidTransition, transErr)
			}

			if depErr := m.verifyDependenciesForMove(feat, newStatus); depErr != nil {
				return depErr
			}

			newStatus = strings.ToLower(strings.TrimSpace(newStatus))
			if newStatus == "review" || newStatus == "done" {
				if bodyErrors := ValidateBodyForStatus(feat.Body, newStatus); len(bodyErrors) > 0 {
					return fmt.Errorf("%w: %s", ErrStatusReadiness, strings.Join(bodyErrors, "; "))
				}
			}
			actor, actorErr := m.requireActor()
			if actorErr != nil {
				return actorErr
			}
			currentOwner := strings.TrimSpace(feat.FrontMatter.Owner)
			requestedOwner := strings.TrimSpace(owner)
			if newStatus == "in-progress" && currentStatus == "review" {
				preservedOwner := strings.TrimSpace(feat.FrontMatter.ImplementationOwner)
				if preservedOwner == "" || strings.EqualFold(preservedOwner, "unassigned") {
					return fmt.Errorf("%w: review to in-progress requires preserved implementation_owner metadata; run lifecycle migration", ErrOwnershipConflict)
				}
				if err := lifecycle.ValidateOwner(preservedOwner, false); err != nil {
					return fmt.Errorf("%w: invalid preserved implementation_owner: %v", ErrOwnershipConflict, err)
				}
				if requestedOwner != "" && requestedOwner != preservedOwner {
					return fmt.Errorf("%w: review handback owner %s must match preserved implementation owner %s", ErrOwnershipConflict, requestedOwner, preservedOwner)
				}
				requestedOwner = preservedOwner
			} else if requestedOwner == "" {
				requestedOwner = currentOwner
			}
			if lifecycle.RequiresAssignedOwner(newStatus) && (requestedOwner == "" || strings.EqualFold(requestedOwner, "unassigned")) {
				requestedOwner = actor
			}
			if lifecycle.RequiresAssignedOwner(newStatus) && (requestedOwner == "" || strings.EqualFold(requestedOwner, "unassigned")) {
				return fmt.Errorf("%w: status %s requires an assigned owner", ErrOwnershipConflict, newStatus)
			}
			if requestedOwner != "" {
				if err := lifecycle.ValidateOwner(requestedOwner, !lifecycle.RequiresAssignedOwner(newStatus)); err != nil {
					return fmt.Errorf("%w: %v", ErrOwnershipConflict, err)
				}
			}
			if currentStatus == lifecycle.InitialStatus() && lifecycle.RequiresAssignedOwner(newStatus) && requestedOwner != actor {
				return fmt.Errorf("%w: initial claim owner %s must match actor %s", ErrOwnershipConflict, requestedOwner, actor)
			}
			implementationOwner := strings.TrimSpace(feat.FrontMatter.ImplementationOwner)
			if lifecycle.RequiresAssignedOwner(newStatus) {
				switch {
				case newStatus == "in-progress" && currentStatus == lifecycle.InitialStatus():
					implementationOwner = requestedOwner
				case implementationOwner != "" && !strings.EqualFold(implementationOwner, "unassigned"):
					if err := lifecycle.ValidateOwner(implementationOwner, false); err != nil {
						return fmt.Errorf("%w: invalid implementation_owner: %v", ErrOwnershipConflict, err)
					}
				case currentStatus == "in-progress" && currentOwner != "" && !strings.EqualFold(currentOwner, "unassigned"):
					if err := lifecycle.ValidateOwner(currentOwner, false); err != nil {
						return fmt.Errorf("%w: invalid current implementation owner: %v", ErrOwnershipConflict, err)
					}
					implementationOwner = currentOwner
				case currentStatus == lifecycle.InitialStatus():
					implementationOwner = requestedOwner
				default:
					return fmt.Errorf("%w: status %s requires an explicit implementation owner migration", ErrOwnershipConflict, currentStatus)
				}
			}
			if (newStatus == "review" || newStatus == "done") &&
				implementationOwner != "" && !strings.EqualFold(implementationOwner, "unassigned") &&
				strings.EqualFold(requestedOwner, implementationOwner) {
				return fmt.Errorf("%w: %s owner %s must be distinct from implementation_owner", ErrOwnershipConflict, newStatus, requestedOwner)
			}
			feat.FrontMatter.Status = newStatus
			if requestedOwner != "" {
				feat.FrontMatter.Owner = requestedOwner
			} else if feat.FrontMatter.Owner == "" {
				feat.FrontMatter.Owner = "unassigned"
			}
			if implementationOwner != "" {
				feat.FrontMatter.ImplementationOwner = implementationOwner
			}
			feat.UpdateTimestamp()
			feat.FrontMatter.StatusChanged = time.Now().Format("2006-01-02")
			if graphErr := m.validateFeatureGraphCandidate(feat); graphErr != nil {
				return graphErr
			}

			newDir, ok := lifecycle.DirectoryForStatus(newStatus)
			if !ok {
				return fmt.Errorf("unknown status directory for %s", newStatus)
			}

			oldPath := feat.Path
			needsMove := !strings.EqualFold(newStatus, currentStatus) || filepath.Dir(feat.Path) != newDir

			if needsMove {
				if !m.opts.DryRun {
					if mkErr := os.MkdirAll(newDir, 0o750); mkErr != nil {
						return fmt.Errorf("failed to create status directory: %w", mkErr)
					}
				}
				newPath := filepath.Join(newDir, filepath.Base(feat.Path))
				if m.opts.DryRun {
					if _, sourceErr := verifyFeatureSource(feat); sourceErr != nil {
						return sourceErr
					}
					feat.Path = newPath
				} else {
					if _, statErr := os.Lstat(newPath); statErr == nil {
						return fmt.Errorf("destination feature already exists: %s", newPath)
					} else if !errors.Is(statErr, os.ErrNotExist) {
						return fmt.Errorf("failed to inspect destination feature: %w", statErr)
					}
					originalData, sourceErr := verifyFeatureSource(feat)
					if sourceErr != nil {
						return sourceErr
					}
					plannedData, encodeErr := feat.Encode()
					if encodeErr != nil {
						return fmt.Errorf("failed to encode feature move: %w", encodeErr)
					}
					if moveErr := m.executeMoveTransaction(feat.FrontMatter.ID, oldPath, newPath, originalData, plannedData, feat.source); moveErr != nil {
						return fmt.Errorf("failed to move feature: %w", moveErr)
					}
					feat.Path = newPath
					if snapshotErr := refreshFeatureSource(m.opts.RootDir, feat, plannedData); snapshotErr != nil {
						return snapshotErr
					}
				}
			} else {
				if saveErr := m.saveFeatureLocked(feat); saveErr != nil {
					return saveErr
				}
			}

			summary = fmt.Sprintf("Moved %s to %s", feat.FrontMatter.ID, newStatus)
			m.log.WithFields(logrus.Fields{
				"action":   "move",
				"id":       feat.FrontMatter.ID,
				"from":     currentStatus,
				"to":       newStatus,
				"owner":    feat.FrontMatter.Owner,
				"new_path": feat.Path,
			}).Info("Feature moved")

			return nil
		})
	})
	if err != nil {
		return nil, "", err
	}
	m.auditEvent("move", feat.FrontMatter.ID, fmt.Sprintf("status=%s", feat.FrontMatter.Status))
	return feat, summary, nil
}

// RenameToMatchTitle is retained for API compatibility. Feature basenames are
// immutable after creation, so title edits never rename a feature file.
func (m *Manager) RenameToMatchTitle(feat *Feature) (bool, error) {
	return false, nil
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
	plan, err := m.PrepareDelete(id)
	if err != nil {
		return "", err
	}
	return m.DeleteFeaturePlanned(plan)
}

// PrepareDelete authorizes and policy-checks a deletion under the board graph
// and feature guards, returning an approval token bound to exact source state.
func (m *Manager) PrepareDelete(id string) (*DeletePlan, error) {
	var plan *DeletePlan
	err := m.WithBoardGraphSnapshot(func() error {
		return m.withFeatureMutation(id, func() error {
			feat, err := m.loadByID(id)
			if err != nil {
				return err
			}
			if err := m.AuthorizeMutation(feat); err != nil {
				return err
			}
			if err := m.validateDeletePolicy(feat); err != nil {
				return err
			}
			data, err := verifyFeatureSource(feat)
			if err != nil {
				return err
			}
			sourceCopy := *feat.source
			plan = &DeletePlan{Feature: feat, data: append([]byte(nil), data...), source: &sourceCopy}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

// DeleteFeaturePlanned deletes only if the exact feature authorized by
// PrepareDelete is still present and the board policy still permits removal.
func (m *Manager) DeleteFeaturePlanned(plan *DeletePlan) (string, error) {
	if plan == nil || plan.Feature == nil || plan.source == nil {
		return "", errors.New("valid delete plan is required")
	}
	id := strings.TrimSpace(plan.Feature.FrontMatter.ID)
	var path string
	err := m.withBoardGraphMutation(func() error {
		return m.withFeatureMutation(id, func() error {
			feat, err := m.loadByID(id)
			if err != nil {
				return err
			}
			if err := m.AuthorizeMutation(feat); err != nil {
				return err
			}
			if err := m.validateDeletePolicy(feat); err != nil {
				return err
			}
			path = feat.Path
			if filepath.Clean(path) != plan.source.path || !os.SameFile(feat.source.info, plan.source.info) || feat.source.mode != plan.source.mode || feat.source.digest != plan.source.digest {
				return fmt.Errorf("%w: approved delete source changed", ErrStaleFeature)
			}
			expected, err := verifyFeatureSource(feat)
			if err != nil {
				return err
			}
			if !bytes.Equal(expected, plan.data) {
				return fmt.Errorf("%w: approved delete bytes changed", ErrStaleFeature)
			}
			if m.opts.DryRun {
				m.log.WithFields(logrus.Fields{
					"action": "delete",
					"path":   path,
					"dryRun": true,
				}).Info("Skipping delete in dry-run mode")
				return nil
			}
			if err := m.executeFeatureMutationTransaction(
				featureMutationDelete,
				id,
				path,
				"",
				expected,
				nil,
				feat.source.mode,
				0,
				feat.source,
			); err != nil {
				return fmt.Errorf("failed to delete feature: %w", err)
			}
			m.log.WithFields(logrus.Fields{
				"action": "delete",
				"path":   path,
			}).Info("Feature deleted")
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	m.auditEvent("delete", id, fmt.Sprintf("path=%s", path))
	return path, nil
}

// List returns all features metadata.
func (m *Manager) List() ([]*Feature, error) {
	if _, err := m.Lifecycle(); err != nil {
		return nil, err
	}
	if err := m.recoverPendingMoves(); err != nil {
		return nil, err
	}
	files, err := m.discoverFeatureFiles()
	if err != nil {
		return nil, err
	}
	var features []*Feature
	var invalidFiles []InvalidFile
	var actualAggregate int64
	for _, file := range files {
		path := file.path
		data, info, readErr := readRegularFeatureFileWithInfo(m.opts.RootDir, path)
		if readErr != nil {
			return nil, readErr
		}
		actualAggregate += int64(len(data))
		if actualAggregate > maxFeatureAggregateBytes {
			return nil, fmt.Errorf("aggregate feature byte limit exceeded during read (%d)", maxFeatureAggregateBytes)
		}
		feat, parseErr := Parse(path, data)
		if parseErr != nil {
			// Collect parse errors instead of failing immediately
			invalidFiles = append(invalidFiles, InvalidFile{
				Path:   path,
				Reason: parseErr.Error(),
			})
			continue
		}
		if identityErr := validateFeatureIdentity(m.lifecycle, path, feat, ""); identityErr != nil {
			invalidFiles = append(invalidFiles, InvalidFile{Path: path, Reason: identityErr.Error()})
			continue
		}
		feat.source = snapshotForSource(m.opts.RootDir, path, data, info)
		features = append(features, feat)
	}

	// If we found invalid files, return them as an error
	if len(invalidFiles) > 0 {
		return nil, &InvalidFileError{Files: invalidFiles}
	}

	sort.Slice(features, func(i, j int) bool {
		return features[i].FrontMatter.ID < features[j].FrontMatter.ID
	})
	return features, nil
}

func validateFeatureIdentity(lifecycle *contract.Lifecycle, path string, feat *Feature, requestedID string) error {
	name := filepath.Base(path)
	if err := lifecycle.ValidateFilename(name); err != nil {
		return fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
	}
	frontmatterID := strings.TrimSpace(feat.FrontMatter.ID)
	if err := lifecycle.ValidateFeatureID(frontmatterID); err != nil {
		return fmt.Errorf("%w: invalid frontmatter id: %v", ErrIdentityMismatch, err)
	}
	if !strings.HasPrefix(name, frontmatterID+"-") {
		return fmt.Errorf("%w: filename %q does not match frontmatter id %q", ErrIdentityMismatch, name, frontmatterID)
	}
	if requestedID != "" && frontmatterID != requestedID {
		return fmt.Errorf("%w: requested id %q does not match frontmatter id %q", ErrIdentityMismatch, requestedID, frontmatterID)
	}
	return nil
}

func validateRegularFeatureEntry(path string, entry fs.DirEntry) error {
	if entry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("feature path is a symbolic link: %s", path)
	}
	info, err := entry.Info()
	if err != nil {
		return fmt.Errorf("inspect feature path %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("feature path is not a regular file: %s", path)
	}
	return nil
}

func readRegularFeatureFileWithInfo(rootPath, path string) ([]byte, os.FileInfo, error) {
	data, info, err := util.ReadRegularFileWithin(rootPath, path, maxFeatureFileBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read feature file securely: %w", err)
	}
	return data, info, nil
}

func snapshotForSource(root, path string, data []byte, info os.FileInfo) *sourceSnapshot {
	return &sourceSnapshot{
		root:   filepath.Clean(root),
		path:   filepath.Clean(path),
		info:   info,
		digest: sha256.Sum256(data),
		mode:   info.Mode(),
	}
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
