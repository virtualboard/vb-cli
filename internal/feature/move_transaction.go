package feature

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/virtualboard/vb-cli/internal/util"
)

var (
	// ErrPendingFeatureMutation indicates that a journaled feature mutation is
	// still active or needs deterministic recovery before the board is safe.
	ErrPendingFeatureMutation = errors.New("pending feature mutation transaction")
	// ErrPendingMove is retained as a compatibility alias for callers that
	// handled the original move-only journal error.
	ErrPendingMove = ErrPendingFeatureMutation

	installMoveDestination = util.WriteFileExclusiveAtomicWithin
	removeMoveSource       func(string) error
)

const moveTransactionVersion = 1

type moveTransaction struct {
	Version        int    `json:"version"`
	ID             string `json:"id"`
	OldPath        string `json:"old_path"`
	NewPath        string `json:"new_path"`
	OriginalSHA256 string `json:"original_sha256"`
	PlannedSHA256  string `json:"planned_sha256"`
}

func (m *Manager) executeMoveTransaction(id, oldPath, newPath string, originalData, plannedData []byte, source *sourceSnapshot) error {
	if source == nil {
		return fmt.Errorf("%w: move source snapshot is unavailable", ErrStaleFeature)
	}
	return m.executeFeatureMutationTransaction(
		featureMutationMove,
		id,
		oldPath,
		newPath,
		originalData,
		plannedData,
		source.mode,
		0o644,
		source,
	)
}

// recoverPendingMoves restores a deterministic board view before discovery.
// A live transaction is never interfered with. An abandoned transaction is
// completed or rolled back only when every visible file matches its journaled
// digest; otherwise recovery fails closed for manual reconciliation.
func (m *Manager) recoverPendingMoves() error {
	names, err := m.discoverFeatureTransactionJournals()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return nil
	}
	return m.withLock(boardGraphOperationLockID, m.recoverPendingFeatureTransactions)
}

func (m *Manager) recoverLegacyMoveJournal(payload []byte, journalPath string) error {
	var transaction moveTransaction
	if err := json.Unmarshal(payload, &transaction); err != nil {
		return fmt.Errorf("%w: parse legacy move transaction: %v", ErrPendingMove, err)
	}
	oldPath, newPath, err := m.validateMoveTransaction(&transaction)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPendingMove, err)
	}
	oldState, err := m.readTransactionFile(oldPath)
	if err != nil {
		return fmt.Errorf("%w: inspect source: %v", ErrPendingMove, err)
	}
	newState, err := m.readTransactionFile(newPath)
	if err != nil {
		return fmt.Errorf("%w: inspect destination: %v", ErrPendingMove, err)
	}
	if oldState.exists && digestBytes(oldState.data) != transaction.OriginalSHA256 {
		return fmt.Errorf("%w: source bytes changed for %s; manual reconciliation required", ErrPendingMove, transaction.ID)
	}
	if newState.exists && digestBytes(newState.data) != transaction.PlannedSHA256 {
		return fmt.Errorf("%w: destination bytes changed for %s; manual reconciliation required", ErrPendingMove, transaction.ID)
	}

	switch {
	case oldState.exists && newState.exists:
		migrated, err := m.newFeatureMutationTransaction(
			featureMutationMove,
			transaction.ID,
			oldPath,
			newPath,
			oldState.data,
			newState.data,
			oldState.info.Mode(),
			newState.info.Mode(),
		)
		if err != nil {
			return err
		}
		migrated.MigratedLegacy = true
		migrated.LegacySHA256 = digestBytes(payload)
		migratedPayload, err := json.MarshalIndent(migrated, "", "  ")
		if err != nil {
			return err
		}
		newJournalPath := m.transactionPath(transaction.ID)
		if err := util.WriteFileExclusiveAtomicWithin(m.opts.RootDir, newJournalPath, migratedPayload, 0o600); err != nil {
			return fmt.Errorf("%w: migrate legacy move journal: %v", ErrPendingMove, err)
		}
		return m.recoverFeatureMutationTransaction(migrated, newJournalPath, migratedPayload)
	case oldState.exists && !newState.exists:
		// Destination installation never completed; retain the byte-identical
		// source and roll back the journal.
	case !oldState.exists && newState.exists:
		// Logical move completed; only the stale journal remains.
	default:
		return fmt.Errorf("%w: both source and destination are missing for %s", ErrPendingMove, transaction.ID)
	}
	if err := m.removeJournalIfExact(journalPath, payload); err != nil {
		return fmt.Errorf("%w: remove recovered journal: %v", ErrPendingMove, err)
	}
	return nil
}

func (m *Manager) validateMoveTransaction(transaction *moveTransaction) (string, string, error) {
	if transaction.Version != moveTransactionVersion {
		return "", "", fmt.Errorf("unsupported move transaction version %d", transaction.Version)
	}
	if err := m.lifecycle.ValidateFeatureID(transaction.ID); err != nil {
		return "", "", err
	}
	oldPath, err := resolveTransactionPath(m.opts.RootDir, transaction.OldPath)
	if err != nil {
		return "", "", fmt.Errorf("invalid old_path: %w", err)
	}
	newPath, err := resolveTransactionPath(m.opts.RootDir, transaction.NewPath)
	if err != nil {
		return "", "", fmt.Errorf("invalid new_path: %w", err)
	}
	if filepath.Clean(oldPath) == filepath.Clean(newPath) {
		return "", "", errors.New("source and destination are identical")
	}
	oldBase := filepath.Base(oldPath)
	if oldBase != filepath.Base(newPath) {
		return "", "", errors.New("source and destination basenames differ")
	}
	if err := m.lifecycle.ValidateFilename(oldBase); err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(oldBase, transaction.ID+"-") {
		return "", "", fmt.Errorf("basename %q does not match transaction id %q", oldBase, transaction.ID)
	}
	if !m.isStatusDirectory(filepath.Dir(oldPath)) || !m.isStatusDirectory(filepath.Dir(newPath)) {
		return "", "", errors.New("transaction paths must be direct children of configured status directories")
	}
	if _, err := hex.DecodeString(transaction.OriginalSHA256); err != nil || len(transaction.OriginalSHA256) != sha256.Size*2 {
		return "", "", errors.New("invalid original_sha256")
	}
	if _, err := hex.DecodeString(transaction.PlannedSHA256); err != nil || len(transaction.PlannedSHA256) != sha256.Size*2 {
		return "", "", errors.New("invalid planned_sha256")
	}
	return oldPath, newPath, nil
}

func (m *Manager) isStatusDirectory(path string) bool {
	for _, status := range m.lifecycle.Statuses() {
		directory, _ := m.lifecycle.DirectoryForStatus(status)
		if filepath.Clean(path) == filepath.Clean(directory) {
			return true
		}
	}
	return false
}

func resolveTransactionPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("path must be relative")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	abs := filepath.Join(root, clean)
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(abs))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	return abs, nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
