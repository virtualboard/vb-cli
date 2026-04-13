package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry represents a single audit log entry with hash chain integrity.
type Entry struct {
	Timestamp string `json:"timestamp"`
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	FeatureID string `json:"feature_id,omitempty"`
	Details   string `json:"details,omitempty"`
	PrevHash  string `json:"prev_hash"`
	EntryHash string `json:"entry_hash"`
}

// Logger writes append-only JSONL audit entries with hash chain integrity.
type Logger struct {
	path     string
	prevHash string
	mu       sync.Mutex
}

// NewLogger creates an audit logger that appends to the given path.
// It reads the last entry's hash from the existing file to resume the chain.
func NewLogger(auditPath string) (*Logger, error) {
	prev, err := lastHash(auditPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read audit chain: %w", err)
	}
	return &Logger{
		path:     auditPath,
		prevHash: prev,
	}, nil
}

// Log writes a new audit entry. Thread-safe.
func (l *Logger) Log(action, actor, featureID, details string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Action:    action,
		Actor:     actor,
		FeatureID: featureID,
		Details:   details,
		PrevHash:  l.prevHash,
	}
	entry.EntryHash = computeHash(entry)

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal audit entry: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create audit directory: %w", err)
	}

	// #nosec G304 -- audit path is constructed from controlled RootDir configuration
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open audit log: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("failed to write audit entry: %w", err)
	}

	l.prevHash = entry.EntryHash
	return nil
}

// computeHash computes SHA-256 of the entry content + prev hash for chain integrity.
func computeHash(e Entry) string {
	input := e.Timestamp + e.Action + e.Actor + e.FeatureID + e.Details + e.PrevHash
	h := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", h)
}

// lastHash reads the last line of the audit file and extracts its entry_hash.
// Returns empty string for missing or empty files.
func lastHash(path string) (string, error) {
	// #nosec G304 -- audit path is constructed from controlled RootDir configuration
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lastLine = line
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	if lastLine == "" {
		return "", nil
	}

	var entry Entry
	if err := json.Unmarshal([]byte(lastLine), &entry); err != nil {
		return "", nil // corrupt last line; start fresh chain
	}
	return entry.EntryHash, nil
}
