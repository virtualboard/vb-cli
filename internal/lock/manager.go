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
	opts *config.Options
	log  *logrus.Entry
}

// NewManager constructs a lock manager.
func NewManager(opts *config.Options) *Manager {
	return &Manager{
		opts: opts,
		log:  opts.Logger().WithField("component", "lock"),
	}
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
func (m *Manager) Acquire(id, owner string, ttl int, force bool) (*Info, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	if owner == "" {
		owner = "unassigned"
	}

	existing, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	if existing != nil && !existing.Expired() && !force {
		return nil, fmt.Errorf("%w: active owner %s", ErrActiveLock, existing.Owner)
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
	if err := util.WriteFileAtomic(path, payload, 0o644); err != nil {
		return nil, err
	}

	m.log.WithFields(logrus.Fields{
		"action": "lock",
		"id":     id,
		"owner":  owner,
		"ttl":    ttl,
	}).Info("Lock created/updated")

	return info, nil
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
	return nil
}
