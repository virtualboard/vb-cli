package lock

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/virtualboard/vb-cli/internal/audit"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
)

var ErrActiveLock = errors.New("lock already active")
var lockIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var lockTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var lockOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// MaxTTLMinutes bounds persisted and newly requested leases to seven days. A
// finite bound prevents corrupt or attacker-authored records from becoming
// effectively permanent coordination state.
const MaxTTLMinutes = 7 * 24 * 60

// ErrLockStorageConflict indicates that active canonical and legacy lock files
// coexist. Refusing the operation prevents an upgrade from bypassing either
// lock owner.
var ErrLockStorageConflict = errors.New("conflicting canonical and legacy locks")

// ErrLockOwner indicates that an actor attempted to mutate or release another
// actor's active feature lock.
var ErrLockOwner = errors.New("active lock is owned by another actor")

// ErrLockTokenRequired indicates that a token-bearing acquisition must be
// released with its exact acquisition token. Owner identity alone is not a
// capability: a stale process owned by the same actor must not be able to
// remove a newer acquisition.
var ErrLockTokenRequired = errors.New("exact lock acquisition token is required")

// ErrLockChanged indicates that a lock was replaced after the caller observed
// it. The replacement is left intact rather than deleting a newer acquisition.
var ErrLockChanged = errors.New("lock changed during operation")

// Info represents lock metadata stored on disk.
type Info struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	StartedAt  time.Time `json:"started_at"`
	TTLMinutes int       `json:"ttl_minutes"`
	Token      string    `json:"token,omitempty"`
}

type lockRecord struct {
	path            string
	name            string
	legacy          bool
	data            []byte
	info            *Info
	storageIdentity fs.FileInfo
	fileIdentity    fs.FileInfo
}

type managerTestHooks struct {
	afterGuard       func(operation, id string)
	afterSnapshot    func(operation, id string)
	beforeRemove     func(operation, id string)
	beforeAtomicSwap func(operation, id string)
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
	opts        *config.Options
	log         *logrus.Entry
	auditLog    *audit.Logger
	lifecycle   *contract.Lifecycle
	contractErr error
	testHooks   *managerTestHooks
}

// NewManager constructs a lock manager.
func NewManager(opts *config.Options) *Manager {
	lifecycle, contractErr := contract.Load(opts.RootDir)
	var auditLog *audit.Logger
	if lifecycle != nil {
		auditLog, _ = audit.NewLogger(lifecycle.AuditLogPath()) // best-effort
	}
	return &Manager{
		opts:        opts,
		log:         opts.Logger().WithField("component", "lock"),
		auditLog:    auditLog,
		lifecycle:   lifecycle,
		contractErr: contractErr,
	}
}

// auditEvent records a lock operation. Best-effort only.
func (m *Manager) auditEvent(action, id, details string) {
	if m.opts.DryRun {
		return
	}
	if m.auditLog != nil {
		_ = m.auditLog.Log(action, m.opts.EffectiveActor(), id, details)
	}
}

// Path returns the on-disk path to the lock file.
func (m *Manager) Path(id string) string {
	if !lockIDPattern.MatchString(id) {
		id = "invalid-lock-id"
	}
	if m.lifecycle != nil {
		return filepath.Join(m.lifecycle.LocksDir(), fmt.Sprintf("%s.lock", id))
	}
	return filepath.Join(m.opts.RootDir, "locks", fmt.Sprintf("%s.lock", id))
}

func validateLockID(id string) error {
	if !lockIDPattern.MatchString(id) {
		return fmt.Errorf("invalid lock ID %q", id)
	}
	return nil
}

func newLockToken() (string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate lock token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

// ValidateToken validates the opaque capability returned by Acquire.
func ValidateToken(token string) error {
	if !lockTokenPattern.MatchString(token) {
		return fmt.Errorf("invalid expected lock token")
	}
	return nil
}

func (m *Manager) guardPath(id string) string {
	return filepath.Join(filepath.Dir(m.Path(id)), "."+id+".guard")
}

func openGuardFile(storage *lockStorage, id string) (*os.File, fs.FileInfo, error) {
	name := "." + id + ".guard"
	path := filepath.Join(storage.absolute, name)
	for range 3 {
		entry, err := storage.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			guard, createErr := storage.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if errors.Is(createErr, fs.ErrExist) {
				continue
			}
			if createErr != nil {
				return nil, nil, fmt.Errorf("create lock guard: %w", createErr)
			}
			entry, err = storage.root.Lstat(name)
			if err != nil {
				_ = guard.Close()
				return nil, nil, fmt.Errorf("inspect created lock guard: %w", err)
			}
			opened, statErr := guard.Stat()
			if statErr != nil || entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) {
				_ = guard.Close()
				if statErr != nil {
					return nil, nil, statErr
				}
				return nil, nil, fmt.Errorf("lock guard changed while creating %s", path)
			}
			if err := requireSingleLink(guard); err != nil {
				_ = guard.Close()
				return nil, nil, fmt.Errorf("unsafe lock guard %s: %w", path, err)
			}
			return guard, opened, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("inspect lock guard: %w", err)
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("unsafe lock guard %s", path)
		}
		guard, err := storage.root.OpenFile(name, os.O_RDWR, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("open lock guard: %w", err)
		}
		opened, statErr := guard.Stat()
		current, lstatErr := storage.root.Lstat(name)
		if statErr != nil || lstatErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(entry, opened) || !os.SameFile(opened, current) {
			_ = guard.Close()
			if statErr != nil {
				return nil, nil, statErr
			}
			if lstatErr != nil {
				return nil, nil, lstatErr
			}
			return nil, nil, fmt.Errorf("lock guard changed while opening %s", path)
		}
		if err := requireSingleLink(guard); err != nil {
			_ = guard.Close()
			return nil, nil, fmt.Errorf("unsafe lock guard %s: %w", path, err)
		}
		return guard, opened, nil
	}
	return nil, nil, fmt.Errorf("lock guard %s changed repeatedly while opening", path)
}

func verifyGuardFile(storage *lockStorage, id string, guard *os.File, identity fs.FileInfo) error {
	name := "." + id + ".guard"
	current, err := storage.root.Lstat(name)
	if err != nil {
		return fmt.Errorf("reinspect lock guard: %w", err)
	}
	opened, err := guard.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened lock guard: %w", err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(identity, opened) || !os.SameFile(opened, current) {
		return fmt.Errorf("lock guard changed during operation %s", filepath.Join(storage.absolute, name))
	}
	if err := requireSingleLink(guard); err != nil {
		return fmt.Errorf("unsafe lock guard %s: %w", filepath.Join(storage.absolute, name), err)
	}
	return nil
}

func (m *Manager) withGuard(id, operation string, fn func(*lockStorageSet) error) (err error) {
	if err := validateLockID(id); err != nil {
		return err
	}
	storages, err := m.openStorageSet(true)
	if err != nil {
		return err
	}
	defer storages.close()
	if storages.canonical == nil {
		return errors.New("canonical lock storage is unavailable")
	}
	guard, guardIdentity, err := openGuardFile(storages.canonical, id)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := guard.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close lock guard: %w", closeErr)
		}
	}()
	if err := lockGuardFile(guard); err != nil {
		return fmt.Errorf("acquire lock guard: %w", err)
	}
	defer func() {
		if unlockErr := unlockGuardFile(guard); err == nil && unlockErr != nil {
			err = fmt.Errorf("release lock guard: %w", unlockErr)
		}
	}()
	if err := verifyGuardFile(storages.canonical, id, guard, guardIdentity); err != nil {
		return err
	}
	if m.testHooks != nil && m.testHooks.afterGuard != nil {
		m.testHooks.afterGuard(operation, id)
	}
	if err := storages.verifyPaths(); err != nil {
		return fmt.Errorf("verify lock storage after acquiring guard: %w", err)
	}
	if err := verifyGuardFile(storages.canonical, id, guard, guardIdentity); err != nil {
		return err
	}
	if err := fn(storages); err != nil {
		return err
	}
	if err := storages.verifyPaths(); err != nil {
		return fmt.Errorf("verify lock storage before releasing guard: %w", err)
	}
	return verifyGuardFile(storages.canonical, id, guard, guardIdentity)
}

// WithOperationGuard serializes an internal critical section for id across CLI
// processes. Unlike user-facing TTL locks, the guard remains live until fn
// returns (or the process exits), so long-running operations cannot expire.
func (m *Manager) WithOperationGuard(id string, fn func() error) error {
	if fn == nil {
		return errors.New("operation guard callback is required")
	}
	if m.contractErr != nil {
		return m.contractErr
	}
	return m.withGuard(id, "operation", func(_ *lockStorageSet) error { return fn() })
}

// CheckMutation rejects a mutation when another actor owns an active lock.
func (m *Manager) CheckMutation(id, actor string) error {
	info, err := m.Load(id)
	if err != nil {
		return err
	}
	if info == nil || info.Expired() || info.Owner == actor {
		return nil
	}
	return fmt.Errorf("%w: %s is owned by %s", ErrLockOwner, id, info.Owner)
}

// Load retrieves lock information if present.
func (m *Manager) Load(id string) (*Info, error) {
	if err := validateLockID(id); err != nil {
		return nil, err
	}
	if m.contractErr != nil {
		return nil, m.contractErr
	}
	storages, err := m.openStorageSet(false)
	if err != nil {
		return nil, err
	}
	defer storages.close()
	records, err := m.loadRecords(storages, id)
	if err != nil {
		return nil, err
	}
	selected, err := selectRecord(records, id)
	if err != nil {
		return nil, err
	}
	if selected == nil {
		return nil, nil
	}
	return selected.info, nil
}

func parseLockData(data []byte, expectedID string) (*Info, error) {
	var info Info
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to parse lock file: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("failed to parse lock file: trailing JSON value")
		}
		return nil, fmt.Errorf("failed to parse lock file: trailing data: %w", err)
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawFields); err != nil {
		return nil, fmt.Errorf("failed to parse lock file: %w", err)
	}
	if rawToken, present := rawFields["token"]; present && strings.TrimSpace(string(rawToken)) == "null" {
		return nil, errors.New("failed to parse lock file: acquisition token must be a string")
	}
	if info.ID != expectedID {
		return nil, fmt.Errorf("failed to parse lock file: lock ID %q does not match expected ID %q", info.ID, expectedID)
	}
	owner := strings.TrimSpace(info.Owner)
	if owner != info.Owner || !lockOwnerPattern.MatchString(owner) || strings.EqualFold(owner, "unassigned") || strings.EqualFold(owner, "unknown") {
		return nil, fmt.Errorf("failed to parse lock file: invalid lock owner %q", info.Owner)
	}
	if info.StartedAt.IsZero() {
		return nil, errors.New("failed to parse lock file: started_at must be a valid UTC timestamp")
	}
	_, offset := info.StartedAt.Zone()
	if offset != 0 {
		return nil, errors.New("failed to parse lock file: started_at must use UTC")
	}
	if info.TTLMinutes <= 0 || info.TTLMinutes > MaxTTLMinutes {
		return nil, fmt.Errorf("failed to parse lock file: ttl_minutes must be between 1 and %d", MaxTTLMinutes)
	}
	if info.Token != "" && !lockTokenPattern.MatchString(info.Token) {
		return nil, fmt.Errorf("failed to parse lock file: invalid acquisition token")
	}
	return &info, nil
}

func (m *Manager) loadRecords(storages *lockStorageSet, id string) ([]*lockRecord, error) {
	if err := storages.ensureLegacy(); err != nil {
		return nil, err
	}
	if err := storages.verifyPaths(); err != nil {
		return nil, fmt.Errorf("verify lock storage: %w", err)
	}
	records := make([]*lockRecord, 0, 2)
	if storages.canonical != nil {
		canonical, err := storages.canonical.load(id, false)
		if err != nil {
			return nil, fmt.Errorf("read canonical lock: %w", err)
		}
		if canonical != nil {
			records = append(records, canonical)
		}
	}
	if storages.sameStorage {
		return records, nil
	}
	if storages.legacy != nil {
		legacy, err := storages.legacy.load(id, true)
		if err != nil {
			return nil, fmt.Errorf("read legacy lock: %w", err)
		}
		if legacy != nil {
			records = append(records, legacy)
		}
	}
	if err := storages.verifyPaths(); err != nil {
		return nil, fmt.Errorf("verify lock storage after read: %w", err)
	}
	return records, nil
}

func selectRecord(records []*lockRecord, id string) (*lockRecord, error) {
	var canonical, legacy *lockRecord
	for _, record := range records {
		if record.legacy {
			legacy = record
		} else {
			canonical = record
		}
	}
	if canonical != nil && legacy != nil && !canonical.info.Expired() && !legacy.info.Expired() {
		return nil, fmt.Errorf("%w for %s (%s and %s)", ErrLockStorageConflict, id, canonical.path, legacy.path)
	}
	if canonical != nil && !canonical.info.Expired() {
		return canonical, nil
	}
	if legacy != nil && !legacy.info.Expired() {
		return legacy, nil
	}
	if canonical != nil {
		return canonical, nil
	}
	return legacy, nil
}

func sameRecord(expected, current *lockRecord) bool {
	if expected == nil || current == nil || expected.legacy != current.legacy || expected.path != current.path || expected.name != current.name {
		return false
	}
	if expected.info.Token != "" || current.info.Token != "" {
		if expected.info.Token == "" || current.info.Token == "" || expected.info.Token != current.info.Token {
			return false
		}
	}
	// File and directory identities make a byte-identical inode or path swap a
	// conflict. Exact bytes additionally protect tokenless legacy records.
	return sameRecordIdentity(expected, current)
}

// Acquire creates or refreshes a lock. Each acquisition receives a random
// token, and all inspection/replacement work is serialized by the per-ID guard.
func (m *Manager) Acquire(id, owner string, ttl int, force bool) (*Info, error) {
	if err := validateLockID(id); err != nil {
		return nil, err
	}
	if m.contractErr != nil {
		return nil, m.contractErr
	}
	if ttl <= 0 || ttl > MaxTTLMinutes {
		return nil, fmt.Errorf("ttl must be between 1 and %d minutes", MaxTTLMinutes)
	}
	if strings.TrimSpace(owner) != owner || !lockOwnerPattern.MatchString(owner) || strings.EqualFold(owner, "unassigned") || strings.EqualFold(owner, "unknown") {
		return nil, fmt.Errorf("invalid lock owner %q", owner)
	}
	token, err := newLockToken()
	if err != nil {
		return nil, err
	}

	info := &Info{
		ID:         id,
		Owner:      owner,
		StartedAt:  time.Now().UTC(),
		TTLMinutes: ttl,
		Token:      token,
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

	replacedExpired := false
	operation := "acquire"
	if force {
		operation = "force-acquire"
	}
	err = m.withGuard(id, operation, func(storages *lockStorageSet) error {
		if m.contractErr != nil {
			return m.contractErr
		}
		records, err := m.loadRecords(storages, id)
		if err != nil {
			return err
		}
		selected, selectionErr := selectRecord(records, id)
		if selectionErr != nil && (!force || !errors.Is(selectionErr, ErrLockStorageConflict)) {
			return selectionErr
		}

		if force {
			if m.testHooks != nil && m.testHooks.beforeAtomicSwap != nil {
				m.testHooks.beforeAtomicSwap(operation, id)
			}
			return m.replaceRecordSet(storages, id, payload, records)
		}

		if selected != nil && !selected.info.Expired() {
			return fmt.Errorf("%w: active owner %s", ErrActiveLock, selected.info.Owner)
		}
		if len(records) > 0 {
			if m.testHooks != nil && m.testHooks.beforeRemove != nil {
				m.testHooks.beforeRemove("expired-replace", id)
			}
		}
		if err := m.replaceRecordSet(storages, id, payload, records); err != nil {
			if len(records) == 0 && errors.Is(err, ErrLockChanged) {
				return fmt.Errorf("%w: concurrent lock acquisition: %v", ErrActiveLock, err)
			}
			return err
		}
		replacedExpired = len(records) > 0
		return nil
	})
	if err != nil {
		return nil, err
	}

	if force {
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

	m.log.WithFields(logrus.Fields{
		"action": "lock",
		"id":     id,
		"owner":  owner,
		"ttl":    ttl,
	}).Info("Lock created")
	action := "lock"
	if replacedExpired {
		action = "lock-expired-replace"
	}
	m.auditEvent(action, id, fmt.Sprintf("owner=%s ttl=%d", owner, ttl))
	return info, nil
}

func sameRecordSet(expected, current []*lockRecord) bool {
	if len(expected) != len(current) {
		return false
	}
	for _, want := range expected {
		matched := false
		for _, got := range current {
			if sameRecord(want, got) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func (m *Manager) replaceRecordSet(storages *lockStorageSet, id string, payload []byte, expected []*lockRecord) (err error) {
	if storages.canonical == nil {
		return errors.New("canonical lock storage is unavailable")
	}
	stage, err := storages.canonical.prepare(payload)
	if err != nil {
		return err
	}
	stageLive := true
	defer func() {
		if stageLive {
			err = errors.Join(err, storages.canonical.removeStaged(stage))
		}
	}()

	current, err := m.loadRecords(storages, id)
	if err != nil {
		return err
	}
	if !sameRecordSet(expected, current) {
		return fmt.Errorf("%w: %s was replaced before lock publication", ErrLockChanged, id)
	}

	detached := make([]*detachedLock, 0, len(current))
	for _, record := range current {
		storage, storageErr := storages.storageFor(record)
		if storageErr != nil {
			return errors.Join(storageErr, restoreDetached(detached))
		}
		item, detachErr := storage.detachExact(record)
		if detachErr != nil {
			return errors.Join(detachErr, restoreDetached(detached))
		}
		detached = append(detached, item)
	}
	if err := storages.verifyPaths(); err != nil {
		return errors.Join(fmt.Errorf("lock storage changed before publication: %w", err), restoreDetached(detached))
	}
	if err := storages.canonical.verifyStaged(stage); err != nil {
		return errors.Join(err, restoreDetached(detached))
	}
	if err := renameStoragePathExclusive(storages.canonical.directory, stage.name, lockFileName(id)); err != nil {
		return errors.Join(fmt.Errorf("publish replacement lock: %w", err), restoreDetached(detached))
	}
	stageLive = false
	published, err := storages.canonical.load(id, false)
	if err != nil {
		return err
	}
	if published == nil || !os.SameFile(stage.identity, published.fileIdentity) || !bytesEqual(stage.data, published.data) {
		return fmt.Errorf("%w: published lock identity changed", ErrLockChanged)
	}
	if err := discardDetached(detached); err != nil {
		return err
	}
	final, err := m.loadRecords(storages, id)
	if err != nil {
		return err
	}
	if len(final) != 1 || final[0].legacy || !sameRecord(published, final[0]) {
		return fmt.Errorf("%w: lock storage changed after publication", ErrLockChanged)
	}
	return nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (m *Manager) removeExactRecords(storages *lockStorageSet, operation, id string, expected []*lockRecord, requireExactSet bool) error {
	if m.testHooks != nil && m.testHooks.beforeRemove != nil {
		m.testHooks.beforeRemove(operation, id)
	}
	current, err := m.loadRecords(storages, id)
	if err != nil {
		return err
	}
	if requireExactSet && !sameRecordSet(expected, current) {
		return fmt.Errorf("%w: %s was replaced before removal", ErrLockChanged, id)
	}
	for _, want := range expected {
		var got *lockRecord
		for _, candidate := range current {
			if candidate.path == want.path && candidate.legacy == want.legacy {
				got = candidate
				break
			}
		}
		if !sameRecord(want, got) {
			return fmt.Errorf("%w: %s was replaced before removal", ErrLockChanged, id)
		}
	}
	detached := make([]*detachedLock, 0, len(expected))
	for _, wanted := range expected {
		var record *lockRecord
		for _, candidate := range current {
			if sameRecord(wanted, candidate) {
				record = candidate
				break
			}
		}
		storage, storageErr := storages.storageFor(record)
		if storageErr != nil {
			return errors.Join(storageErr, restoreDetached(detached))
		}
		item, detachErr := storage.detachExact(record)
		if detachErr != nil {
			return errors.Join(detachErr, restoreDetached(detached))
		}
		detached = append(detached, item)
	}
	if err := discardDetached(detached); err != nil {
		return err
	}
	return nil
}

// Release removes only the acquisition identified by expectedToken. A stale
// caller can therefore never release a newer lock with the same ID or owner.
func (m *Manager) Release(id, expectedToken string) error {
	if err := validateLockID(id); err != nil {
		return err
	}
	if m.contractErr != nil {
		return m.contractErr
	}
	if err := ValidateToken(expectedToken); err != nil {
		return err
	}
	if m.opts.DryRun {
		storages, err := m.openStorageSet(false)
		if err != nil {
			return err
		}
		defer storages.close()
		records, err := m.loadRecords(storages, id)
		if err != nil {
			return err
		}
		if len(recordsWithToken(records, expectedToken)) != 1 {
			return fmt.Errorf("%w: acquisition token for %s is no longer current", ErrLockChanged, id)
		}
		m.log.WithFields(logrus.Fields{
			"action": "unlock",
			"id":     id,
			"dryRun": true,
		}).Info("Skipping lock removal in dry-run mode")
		return nil
	}
	err := m.withGuard(id, "release", func(storages *lockStorageSet) error {
		if m.contractErr != nil {
			return m.contractErr
		}
		records, err := m.loadRecords(storages, id)
		if err != nil {
			return err
		}
		targets := recordsWithToken(records, expectedToken)
		if len(targets) != 1 {
			return fmt.Errorf("%w: acquisition token for %s is no longer current", ErrLockChanged, id)
		}
		return m.removeExactRecords(storages, "release", id, targets, false)
	})
	if err != nil {
		return err
	}
	m.log.WithFields(logrus.Fields{
		"action": "unlock",
		"id":     id,
	}).Info("Lock released")
	m.auditEvent("unlock", id, "")
	return nil
}

func recordsWithToken(records []*lockRecord, expectedToken string) []*lockRecord {
	targets := make([]*lockRecord, 0, 1)
	for _, record := range records {
		if record.info.Token == expectedToken {
			targets = append(targets, record)
		}
	}
	return targets
}

// ReleaseOwned releases a lock only for its owner unless force is explicit.
func (m *Manager) ReleaseOwned(id, actor string, force bool) error {
	if err := validateLockID(id); err != nil {
		return err
	}
	if m.contractErr != nil {
		return m.contractErr
	}
	if strings.TrimSpace(actor) != actor || !lockOwnerPattern.MatchString(actor) || strings.EqualFold(actor, "unassigned") || strings.EqualFold(actor, "unknown") {
		return fmt.Errorf("invalid lock actor %q", actor)
	}
	// Capture the acquisition this invocation intends to release before waiting
	// on the guard. If another process replaces it first, the guarded CAS below
	// refuses to delete that newer acquisition, even when the owner is unchanged.
	snapshotStorages, err := m.openStorageSet(false)
	if err != nil {
		return err
	}
	records, err := m.loadRecords(snapshotStorages, id)
	snapshotStorages.close()
	if err != nil {
		return err
	}
	selected, selectionErr := selectRecord(records, id)
	if selectionErr != nil && (!force || !errors.Is(selectionErr, ErrLockStorageConflict)) {
		return selectionErr
	}
	if selected != nil && !selected.info.Expired() && !force && selected.info.Owner != actor {
		return fmt.Errorf("%w: %s is owned by %s", ErrLockOwner, id, selected.info.Owner)
	}
	if selected != nil && selected.info.Token != "" && !force {
		return fmt.Errorf("%w for %s", ErrLockTokenRequired, id)
	}
	if m.testHooks != nil && m.testHooks.afterSnapshot != nil {
		m.testHooks.afterSnapshot("release-owned", id)
	}
	if m.opts.DryRun {
		return nil
	}
	err = m.withGuard(id, "release-owned", func(storages *lockStorageSet) error {
		if len(records) == 0 {
			return nil
		}
		return m.removeExactRecords(storages, "release-owned", id, records, true)
	})
	if err != nil {
		return err
	}
	m.log.WithFields(logrus.Fields{
		"action": "unlock",
		"id":     id,
	}).Info("Lock released")
	m.auditEvent("unlock", id, "")
	return nil
}
