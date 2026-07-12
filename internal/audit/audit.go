package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	legacyHashVersion  = "v1"
	currentHashVersion = "v2"
	appendLockSuffix   = ".append.lock"

	defaultLockTimeout = 5 * time.Second
	defaultLockPoll    = 10 * time.Millisecond
)

// Entry represents a single audit log entry with hash chain integrity.
type Entry struct {
	HashVersion string `json:"hash_version,omitempty"`
	Timestamp   string `json:"timestamp"`
	Action      string `json:"action"`
	Actor       string `json:"actor"`
	FeatureID   string `json:"feature_id,omitempty"`
	Details     string `json:"details,omitempty"`
	PrevHash    string `json:"prev_hash"`
	EntryHash   string `json:"entry_hash"`
}

// Logger writes append-only JSONL audit entries with hash chain integrity.
// The in-process mutex avoids needless local lock contention; the adjacent
// lock directory serialises appenders across independent vb processes.
type Logger struct {
	path        string
	prevHash    string
	mu          sync.Mutex
	lockTimeout time.Duration
	lockPoll    time.Duration
	testHooks   *loggerTestHooks
}

type loggerTestHooks struct {
	afterAppendLock func()
	afterLogOpen    func()
	beforePersist   func()
	afterPersist    func()
}

// NewLogger creates a logger and validates the existing tail while holding the
// same portable process lock used by Log. Log re-reads the tail under that lock
// before every append, so two logger instances cannot fork the chain.
func NewLogger(auditPath string) (*Logger, error) {
	l := &Logger{
		path:        auditPath,
		lockTimeout: defaultLockTimeout,
		lockPoll:    defaultLockPoll,
	}
	scope, err := openAuditScope(auditPath, true)
	if err != nil {
		return nil, fmt.Errorf("open audit storage: %w", err)
	}
	if scope == nil {
		return nil, errors.New("audit storage is unavailable")
	}
	defer scope.close()
	lock, err := l.acquireAppendLockInScope(scope)
	if err != nil {
		return nil, err
	}
	prev, readErr := lastHashInScope(scope)
	releaseErr := lock.release()
	if err := errors.Join(readErr, releaseErr); err != nil {
		return nil, fmt.Errorf("validate audit chain tail: %w", err)
	}
	l.prevHash = prev
	return l, nil
}

// Log writes a new entry. It deliberately remains best-effort for feature
// mutations: callers decide whether an audit failure should fail their own
// operation. This method never attempts to roll back another subsystem.
func (l *Logger) Log(action, actor, featureID, details string) (retErr error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	scope, err := openAuditScope(l.path, true)
	if err != nil {
		return fmt.Errorf("open audit storage: %w", err)
	}
	if scope == nil {
		return errors.New("audit storage is unavailable")
	}
	defer scope.close()

	lock, err := l.acquireAppendLockInScope(scope)
	if err != nil {
		return err
	}
	defer func() {
		if err := lock.release(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release audit append lock: %w", err))
		}
	}()
	if l.testHooks != nil && l.testHooks.afterAppendLock != nil {
		l.testHooks.afterAppendLock()
	}
	if err := lock.verify(); err != nil {
		return fmt.Errorf("verify held audit append lock: %w", err)
	}

	logFile, err := scope.openRegular(scope.base, os.O_RDWR|os.O_APPEND, true)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer func() {
		if err := logFile.close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close audit log: %w", err))
		}
	}()
	if l.testHooks != nil && l.testHooks.afterLogOpen != nil {
		l.testHooks.afterLogOpen()
	}
	if err := lock.verify(); err != nil {
		return fmt.Errorf("verify held audit append lock: %w", err)
	}
	if err := scope.verifyOpenFile(logFile); err != nil {
		return fmt.Errorf("verify opened audit log: %w", err)
	}
	prevHash, err := lastHashFile(logFile.file)
	if err != nil {
		return fmt.Errorf("validate audit chain tail before append: %w", err)
	}
	entry := Entry{
		HashVersion: currentHashVersion,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Action:      action,
		Actor:       actor,
		FeatureID:   featureID,
		Details:     details,
		PrevHash:    prevHash,
	}
	entry.EntryHash, err = hashEntry(entry)
	if err != nil {
		return fmt.Errorf("hash audit entry: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	if len(data)+1 > maxLineBytes {
		return fmt.Errorf("audit entry exceeds %d-byte line limit", maxLineBytes)
	}
	data = append(data, '\n')

	if l.testHooks != nil && l.testHooks.beforePersist != nil {
		l.testHooks.beforePersist()
	}
	if err := lock.verify(); err != nil {
		return fmt.Errorf("verify held audit append lock before persistence: %w", err)
	}
	if err := scope.verifyOpenFile(logFile); err != nil {
		return fmt.Errorf("verify audit log before persistence: %w", err)
	}
	written, writeErr := logFile.file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	syncErr := logFile.file.Sync()
	if err := errors.Join(writeErr, syncErr); err != nil {
		return fmt.Errorf("persist audit entry: %w", err)
	}
	if l.testHooks != nil && l.testHooks.afterPersist != nil {
		l.testHooks.afterPersist()
	}
	if err := scope.verifyOpenFile(logFile); err != nil {
		return fmt.Errorf("audit entry persisted but log path changed: %w", err)
	}
	if err := lock.verify(); err != nil {
		return fmt.Errorf("audit entry persisted but append lock changed: %w", err)
	}

	l.prevHash = entry.EntryHash
	return nil
}

// computeHash retains the historical helper signature for package callers and
// tests. Entries without hash_version use the legacy v1 concatenation scheme.
func computeHash(e Entry) string {
	hash, _ := hashEntry(e)
	return hash
}

func hashEntry(e Entry) (string, error) {
	switch normalizedHashVersion(e.HashVersion) {
	case legacyHashVersion:
		return hashEntryV1(e), nil
	case currentHashVersion:
		return hashEntryV2(e), nil
	default:
		return "", fmt.Errorf("unsupported hash version %q", e.HashVersion)
	}
}

// hashEntryV1 is intentionally preserved so existing logs remain verifiable.
// Its unframed concatenation is ambiguous and must not be used for new writes.
func hashEntryV1(e Entry) string {
	input := e.Timestamp + e.Action + e.Actor + e.FeatureID + e.Details + e.PrevHash
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

// hashEntryV2 uses domain-separated, length-prefixed fields. Field boundaries
// are therefore unambiguous even when adjacent values can be repartitioned.
func hashEntryV2(e Entry) string {
	h := sha256.New()
	writeHashFrame(h, "virtualboard-audit-hash", currentHashVersion)
	writeHashFrame(h, "timestamp", e.Timestamp)
	writeHashFrame(h, "action", e.Action)
	writeHashFrame(h, "actor", e.Actor)
	writeHashFrame(h, "feature_id", e.FeatureID)
	writeHashFrame(h, "details", e.Details)
	writeHashFrame(h, "prev_hash", e.PrevHash)
	return hex.EncodeToString(h.Sum(nil))
}

func writeHashFrame(w io.Writer, name, value string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(name)))
	_, _ = w.Write(size[:]) // hash.Hash writes never return an error.
	_, _ = io.WriteString(w, name)
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = w.Write(size[:])
	_, _ = io.WriteString(w, value)
}

func normalizedHashVersion(version string) string {
	version = strings.ToLower(strings.TrimSpace(version))
	if version == "" {
		return legacyHashVersion
	}
	return version
}

func validEncodedHash(value string, allowEmpty bool) bool {
	if value == "" {
		return allowEmpty
	}
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// lastHash validates and returns the final complete entry hash. A non-empty
// file must end in a newline; malformed, oversized, or hash-invalid tails are
// treated as corruption rather than silently starting a new chain.
func lastHash(path string) (hash string, retErr error) {
	scope, err := openAuditScope(path, false)
	if err != nil {
		return "", err
	}
	if scope == nil {
		return "", nil
	}
	defer scope.close()
	return lastHashInScope(scope)
}

func lastHashInScope(scope *auditScope) (hash string, retErr error) {
	opened, err := scope.openRegular(scope.base, os.O_RDONLY, false)
	if err != nil {
		return "", err
	}
	if opened == nil {
		return "", nil
	}
	defer func() {
		if err := opened.close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close audit log: %w", err))
		}
	}()
	if err := scope.verifyOpenFile(opened); err != nil {
		return "", err
	}
	hash, err = lastHashFile(opened.file)
	if err != nil {
		return "", err
	}
	if err := scope.verifyOpenFile(opened); err != nil {
		return "", err
	}
	return hash, nil
}

func lastHashFile(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("audit log is not a regular file")
	}
	if info.Size() == 0 {
		return "", nil
	}
	windowSize := info.Size()
	if windowSize > int64(maxLineBytes+1) {
		windowSize = int64(maxLineBytes + 1)
	}
	window := make([]byte, int(windowSize))
	offset := info.Size() - windowSize
	if _, err := f.ReadAt(window, offset); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if window[len(window)-1] != '\n' {
		return "", fmt.Errorf("audit log has a truncated final line")
	}

	// Work backward from the required terminator, skipping compatibility blank
	// lines. The bounded window ensures a malicious tail cannot force an
	// unbounded read while the append lock is held.
	remaining := window[:len(window)-1]
	var lastLine []byte
	for {
		separator := bytes.LastIndexByte(remaining, '\n')
		candidate := remaining
		if separator >= 0 {
			candidate = remaining[separator+1:]
		}
		if len(bytes.TrimSpace(candidate)) > 0 {
			if separator < 0 && offset > 0 {
				return "", fmt.Errorf("final audit entry exceeds %d-byte line limit", maxLineBytes)
			}
			lastLine = candidate
			break
		}
		if separator < 0 {
			break
		}
		remaining = remaining[:separator]
	}
	if len(lastLine) == 0 {
		if offset > 0 {
			return "", fmt.Errorf("audit log tail exceeds %d-byte line limit", maxLineBytes)
		}
		return "", nil
	}

	entry, err := decodeEntry(lastLine)
	if err != nil {
		return "", fmt.Errorf("parse final audit entry: %w", err)
	}
	if !validEncodedHash(entry.PrevHash, true) {
		return "", fmt.Errorf("final audit entry has invalid prev_hash")
	}
	if !validEncodedHash(entry.EntryHash, false) {
		return "", fmt.Errorf("final audit entry has invalid entry_hash")
	}
	expected, err := hashEntry(entry)
	if err != nil {
		return "", err
	}
	if entry.EntryHash != expected {
		return "", fmt.Errorf("final audit entry hash mismatch")
	}
	return entry.EntryHash, nil
}

type appendLock struct {
	scope     *auditScope
	opened    *scopedAuditFile
	ownsScope bool
}

// acquireAppendLock retains the historical test/helper shape while ensuring
// the directory handle remains live until the returned lock is released.
func (l *Logger) acquireAppendLock() (*appendLock, error) {
	scope, err := openAuditScope(l.path, true)
	if err != nil {
		return nil, err
	}
	if scope == nil {
		return nil, errors.New("audit storage is unavailable")
	}
	lock, err := l.acquireAppendLockInScope(scope)
	if err != nil {
		scope.close()
		return nil, err
	}
	lock.ownsScope = true
	return lock, nil
}

func (l *Logger) acquireAppendLockInScope(scope *auditScope) (*appendLock, error) {
	opened, err := scope.openRegular(scope.lockBase, os.O_RDWR, true)
	if err != nil {
		return nil, fmt.Errorf("open audit append lock: %w", err)
	}
	deadline := time.Now().Add(l.lockTimeout)
	for {
		locked, lockErr := tryLockAuditAppend(opened.file)
		if lockErr != nil {
			_ = opened.close()
			return nil, fmt.Errorf("acquire audit append lock: %w", lockErr)
		}
		if locked {
			lock := &appendLock{scope: scope, opened: opened}
			if err := lock.verify(); err != nil {
				_ = unlockAuditAppend(opened.file)
				_ = opened.close()
				return nil, fmt.Errorf("verify acquired audit append lock: %w", err)
			}
			return lock, nil
		}
		if !time.Now().Before(deadline) {
			_ = opened.close()
			return nil, fmt.Errorf("timed out after %s waiting for audit append lock", l.lockTimeout)
		}
		time.Sleep(l.lockPoll)
	}
}

func (l *appendLock) verify() error {
	if l == nil || l.scope == nil || l.opened == nil || l.opened.file == nil {
		return errors.New("audit append lock handle is unavailable")
	}
	return l.scope.verifyOpenFile(l.opened)
}

func (l *appendLock) release() error {
	if l == nil || l.opened == nil || l.opened.file == nil {
		return nil
	}
	verifyErr := l.verify()
	unlockErr := unlockAuditAppend(l.opened.file)
	closeErr := l.opened.close()
	if l.ownsScope {
		l.scope.close()
	}
	return errors.Join(verifyErr, unlockErr, closeErr)
}
