package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/audit"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/util"
)

var ErrActiveLock = errors.New("lock already active")

// Info represents lock metadata stored on disk.
type Info struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	StartedAt  time.Time `json:"started_at"`
	TTLMinutes int       `json:"ttl_minutes"`
}

// ExpiresAt returns the timestamp when the lock expires.
func (i Info) ExpiresAt() time.Time {
	if i.TTLMinutes <= 0 {
		return i.StartedAt
	}
	return i.StartedAt.Add(time.Duration(i.TTLMinutes) * time.Minute)
}

// Expired indicates whether the lock TTL has elapsed.
func (i Info) Expired() bool {
	if i.TTLMinutes <= 0 {
		return false
	}
	return time.Now().UTC().After(i.ExpiresAt())
}

// Manager orchestrates lock operations.
type Manager struct {
	opts     *config.Options
	log      *logrus.Entry
	auditLog *audit.Logger
}

// NewManager constructs a lock manager.
func NewManager(opts *config.Options) *Manager {
	auditPath := filepath.Join(opts.RootDir, "audit.jsonl")
	auditLog, _ := audit.NewLogger(auditPath) // best-effort
	return &Manager{
		opts:     opts,
		log:      opts.Logger().WithField("component", "lock"),
		auditLog: auditLog,
	}
}

// auditEvent records a lock operation. Best-effort only.
func (m *Manager) auditEvent(action, id, details string) {
	if m.auditLog != nil {
		_ = m.auditLog.Log(action, lockUser(), id, details)
	}
}

// lockUser returns the OS username or "unknown".
func lockUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}

// Path returns the on-disk path to the lock file.
func (m *Manager) Path(id string) string {
	return filepath.Join(m.opts.RootDir, "locks", fmt.Sprintf("%s.lock", id))
}

// Load retrieves lock information if present.
func (m *Manager) Load(id string) (*Info, error) {
	path := m.Path(id)
	// #nosec G304 -- lock file path is constructed via controlled configuration
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to parse lock file: %w", err)
	}
	return &info, nil
}

// Acquire creates or refreshes a lock.
// For non-force acquisitions, uses O_CREATE|O_EXCL to prevent TOCTOU races.
func (m *Manager) Acquire(id, owner string, ttl int, force bool) (*Info, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	if owner == "" {
		owner = "unassigned"
	}

	info := &Info{
		ID:         id,
		Owner:      owner,
		StartedAt:  time.Now().UTC(),
		TTLMinutes: ttl,
	}

	if m.opts.DryRun {
		m.log.WithFields(logrus.Fields{
			"action": "lock",
			"id":     id,
			"dryRun": true,
		}).Info("Skipping lock write in dry-run mode")
		return info, nil
	}

	payload, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}

	path := m.Path(id)

	if force {
		// Force path: overwrite unconditionally
		if err := util.WriteFileAtomic(path, payload, 0o600); err != nil {
			return nil, err
		}
		m.log.WithFields(logrus.Fields{
			"action": "lock",
			"id":     id,
			"owner":  owner,
			"ttl":    ttl,
			"force":  true,
		}).Info("Lock force-acquired")
		m.auditEvent("lock-force", id, fmt.Sprintf("owner=%s ttl=%d", owner, ttl))
		return info, nil
	}

	// Non-force: try atomic exclusive create
	if err := createExclusive(path, payload); err == nil {
		m.log.WithFields(logrus.Fields{
			"action": "lock",
			"id":     id,
			"owner":  owner,
			"ttl":    ttl,
		}).Info("Lock created")
		m.auditEvent("lock", id, fmt.Sprintf("owner=%s ttl=%d", owner, ttl))
		return info, nil
	}

	// File exists — load and check if expired
	existing, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	if existing != nil && !existing.Expired() {
		return nil, fmt.Errorf("%w: active owner %s", ErrActiveLock, existing.Owner)
	}

	// Expired or corrupt: remove and retry exclusive create
	_ = os.Remove(path)
	if err := createExclusive(path, payload); err != nil {
		return nil, fmt.Errorf("%w: concurrent lock acquisition", ErrActiveLock)
	}

	m.log.WithFields(logrus.Fields{
		"action": "lock",
		"id":     id,
		"owner":  owner,
		"ttl":    ttl,
	}).Info("Lock acquired (expired lock replaced)")
	m.auditEvent("lock-expired-replace", id, fmt.Sprintf("owner=%s ttl=%d", owner, ttl))
	return info, nil
}

// createExclusive atomically creates a lock file using O_CREATE|O_EXCL.
// Returns an error if the file already exists, preventing TOCTOU races.
func createExclusive(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create lock directory: %w", err)
	}

	// #nosec G304 -- lock file path is constructed via controlled configuration
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
}

// Release removes a lock file.
func (m *Manager) Release(id string) error {
	path := m.Path(id)
	if m.opts.DryRun {
		m.log.WithFields(logrus.Fields{
			"action": "unlock",
			"id":     id,
			"dryRun": true,
		}).Info("Skipping lock removal in dry-run mode")
		return nil
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	m.log.WithFields(logrus.Fields{
		"action": "unlock",
		"id":     id,
	}).Info("Lock released")
	m.auditEvent("unlock", id, "")
	return nil
}
