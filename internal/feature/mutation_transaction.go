package feature

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/virtualboard/vb-cli/internal/util"
)

const featureMutationTransactionVersion = 2

type featureMutationOperation string

const (
	featureMutationReplace featureMutationOperation = "replace"
	featureMutationDelete  featureMutationOperation = "delete"
	featureMutationMove    featureMutationOperation = "move"
)

var (
	featureMutationNoncePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

	// Tests use these hooks to make the former final check-to-rename/remove
	// windows deterministic. Production leaves them nil.
	afterFeatureSourceCaptured       func(featureMutationOperation, string, string, string)
	afterFeatureDestinationPublished func(featureMutationOperation, string, string)
	beforeCapturedFeatureRemoval     func(featureMutationOperation, string, string)
	interruptFeatureTransaction      func(string, featureMutationOperation, string) error
	afterFeaturePathQuarantined      func(string, string)
	beforeFeaturePathQuarantine      func(string)
)

type featureMutationTransaction struct {
	Version         int                      `json:"version"`
	Operation       featureMutationOperation `json:"operation"`
	ID              string                   `json:"id"`
	SourcePath      string                   `json:"source_path"`
	DestinationPath string                   `json:"destination_path,omitempty"`
	RecoveryDir     string                   `json:"recovery_dir"`
	ExpectedSHA256  string                   `json:"expected_sha256"`
	PlannedSHA256   string                   `json:"planned_sha256,omitempty"`
	ExpectedMode    uint32                   `json:"expected_mode"`
	PlannedMode     uint32                   `json:"planned_mode,omitempty"`
	MigratedLegacy  bool                     `json:"migrated_legacy,omitempty"`
	LegacySHA256    string                   `json:"legacy_journal_sha256,omitempty"`
}

type transactionFileState struct {
	data   []byte
	info   os.FileInfo
	exists bool
}

func (m *Manager) transactionPath(id string) string {
	return filepath.Join(m.LocksDir(), fmt.Sprintf(".mutation-%s.json", id))
}

func (m *Manager) legacyMoveTransactionPath(id string) string {
	return filepath.Join(m.LocksDir(), fmt.Sprintf(".move-%s.json", id))
}

func (transaction *featureMutationTransaction) heldPath(root string) string {
	return filepath.Join(root, transaction.RecoveryDir, "held")
}

func (transaction *featureMutationTransaction) stagePath(root string) string {
	return filepath.Join(root, transaction.RecoveryDir, "stage")
}

func newFeatureMutationNonce() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate feature transaction nonce: %w", err)
	}
	return hex.EncodeToString(random), nil
}

func (m *Manager) newFeatureMutationTransaction(operation featureMutationOperation, id, sourcePath, destinationPath string, expected, planned []byte, expectedMode, plannedMode fs.FileMode) (*featureMutationTransaction, error) {
	nonce, err := newFeatureMutationNonce()
	if err != nil {
		return nil, err
	}
	sourceRelative, err := relativeTransactionPath(m.opts.RootDir, sourcePath)
	if err != nil {
		return nil, fmt.Errorf("relativize feature transaction source: %w", err)
	}
	destinationRelative := ""
	if destinationPath != "" {
		destinationRelative, err = relativeTransactionPath(m.opts.RootDir, destinationPath)
		if err != nil {
			return nil, fmt.Errorf("relativize feature transaction destination: %w", err)
		}
	}
	locksRelative, err := relativeTransactionPath(m.opts.RootDir, m.LocksDir())
	if err != nil {
		return nil, fmt.Errorf("relativize feature transaction directory: %w", err)
	}
	transaction := &featureMutationTransaction{
		Version:         featureMutationTransactionVersion,
		Operation:       operation,
		ID:              id,
		SourcePath:      sourceRelative,
		DestinationPath: destinationRelative,
		RecoveryDir:     filepath.Join(locksRelative, fmt.Sprintf(".mutation-%s-%s", id, nonce)),
		ExpectedSHA256:  digestBytes(expected),
		ExpectedMode:    uint32(expectedMode.Perm()),
		PlannedMode:     uint32(plannedMode.Perm()),
	}
	if operation != featureMutationDelete {
		transaction.PlannedSHA256 = digestBytes(planned)
	}
	if _, _, err := m.validateFeatureMutationTransaction(transaction); err != nil {
		return nil, err
	}
	return transaction, nil
}

// executeFeatureMutationTransaction performs a true path compare-and-swap.
// The live source is moved out of the lifecycle tree before it is trusted; the
// captured inode, content, and mode are then verified against source. A path
// that appears after capture is never replaced or removed.
func (m *Manager) executeFeatureMutationTransaction(operation featureMutationOperation, id, sourcePath, destinationPath string, expected, planned []byte, expectedMode, plannedMode fs.FileMode, source *sourceSnapshot) error {
	if err := m.validateFeatureTransactionDirectory(); err != nil {
		return err
	}
	transaction, err := m.newFeatureMutationTransaction(operation, id, sourcePath, destinationPath, expected, planned, expectedMode, plannedMode)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(transaction, "", "  ")
	if err != nil {
		return fmt.Errorf("encode feature transaction: %w", err)
	}
	journalPath := m.transactionPath(id)
	if err := util.WriteFileExclusiveAtomicWithin(m.opts.RootDir, journalPath, payload, 0o600); err != nil {
		return fmt.Errorf("create feature transaction journal: %w", err)
	}

	cleanupBeforeCapture := func(cause error) error {
		cleanupErr := m.cleanupUncapturedFeatureTransaction(transaction, journalPath, payload)
		if cleanupErr != nil {
			return fmt.Errorf("%v; cleanup failed: %v (%w remains at %s)", cause, cleanupErr, ErrPendingMove, journalPath)
		}
		return cause
	}
	if err := m.createFeatureRecoveryDirectory(transaction); err != nil {
		return cleanupBeforeCapture(fmt.Errorf("create feature recovery directory: %w", err))
	}
	if operation != featureMutationDelete {
		stagePath := transaction.stagePath(m.opts.RootDir)
		stageWriter := util.WriteFileExclusiveAtomicWithin
		if operation == featureMutationMove {
			stageWriter = installMoveDestination
		}
		if err := stageWriter(m.opts.RootDir, stagePath, planned, plannedMode.Perm()); err != nil {
			return cleanupBeforeCapture(fmt.Errorf("stage feature replacement: %w", err))
		}
		stage, err := m.readTransactionFile(stagePath)
		if err != nil || !stage.exists || !transaction.plannedMatches(stage) {
			if err == nil {
				err = errors.New("staged replacement content or mode changed")
			}
			return cleanupBeforeCapture(fmt.Errorf("verify staged feature replacement: %w", err))
		}
	}

	heldPath := transaction.heldPath(m.opts.RootDir)
	if err := m.captureFeatureSource(sourcePath, heldPath); err != nil {
		return cleanupBeforeCapture(fmt.Errorf("capture feature source: %w", err))
	}
	if afterFeatureSourceCaptured != nil {
		afterFeatureSourceCaptured(operation, id, sourcePath, heldPath)
	}
	held, err := m.readTransactionFile(heldPath)
	if err != nil {
		return m.rollbackCapturedFeatureTransaction(transaction, journalPath, payload, fmt.Errorf("read captured feature source: %w", err))
	}
	if !held.exists || !transaction.expectedMatches(held) || source == nil || !os.SameFile(source.info, held.info) {
		return m.rollbackCapturedFeatureTransaction(transaction, journalPath, payload, fmt.Errorf("%w: captured source identity, content, or mode changed for %s", ErrStaleFeature, id))
	}
	if live, readErr := m.readTransactionFile(sourcePath); readErr != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("inspect source after capture: %w", readErr))
	} else if live.exists {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%w: source path was recreated after capture; concurrent content preserved", ErrStaleFeature))
	}
	if interruptFeatureTransaction != nil {
		if err := interruptFeatureTransaction("captured", operation, id); err != nil {
			return m.pendingFeatureTransaction(transaction, journalPath, err)
		}
	}

	if operation != featureMutationDelete {
		if err := m.publishTransactionStage(transaction); err != nil {
			return m.rollbackCapturedFeatureTransaction(transaction, journalPath, payload, fmt.Errorf("publish feature replacement: %w", err))
		}
		if afterFeatureDestinationPublished != nil {
			afterFeatureDestinationPublished(operation, id, destinationPath)
		}
	}
	if err := m.verifyCommittedFeatureTransaction(transaction); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	if interruptFeatureTransaction != nil {
		if err := interruptFeatureTransaction("published", operation, id); err != nil {
			return m.pendingFeatureTransaction(transaction, journalPath, err)
		}
	}
	if beforeCapturedFeatureRemoval != nil {
		beforeCapturedFeatureRemoval(operation, id, heldPath)
	}
	if err := m.verifyHeldExpected(transaction); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	if err := m.removeCapturedFeature(transaction, heldPath); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("remove captured feature source: %w", err))
	}
	if interruptFeatureTransaction != nil {
		if err := interruptFeatureTransaction("retired", operation, id); err != nil {
			return m.pendingFeatureTransaction(transaction, journalPath, err)
		}
	}
	if err := m.finishFeatureTransaction(transaction, journalPath, payload); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	return nil
}

func (m *Manager) validateFeatureTransactionDirectory() error {
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	locksRelative, err := relativeTransactionPath(m.opts.RootDir, m.LocksDir())
	if err != nil {
		return err
	}
	info, err := root.Lstat(locksRelative)
	if err != nil {
		return fmt.Errorf("inspect feature transaction directory: %w", err)
	}
	if featurePathIsLinked(info) || !info.IsDir() {
		return fmt.Errorf("feature transaction directory is a symbolic link/reparse point or not a directory: %s", m.LocksDir())
	}
	return nil
}

func (m *Manager) createFeatureRecoveryDirectory(transaction *featureMutationTransaction) error {
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Mkdir(transaction.RecoveryDir, 0o700); err != nil {
		return err
	}
	return nil
}

func (m *Manager) captureFeatureSource(sourcePath, heldPath string) error {
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	sourceRelative, err := relativeTransactionPath(m.opts.RootDir, sourcePath)
	if err != nil {
		return err
	}
	heldRelative, err := relativeTransactionPath(m.opts.RootDir, heldPath)
	if err != nil {
		return err
	}
	if _, err := root.Lstat(heldRelative); err == nil {
		return errors.New("feature recovery path already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return m.renameWithinFeatureRootExclusive(sourceRelative, heldRelative)
}

func (m *Manager) publishTransactionStage(transaction *featureMutationTransaction) error {
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	stageRelative := filepath.Join(transaction.RecoveryDir, "stage")
	if _, err := root.Lstat(transaction.DestinationPath); err == nil {
		return fmt.Errorf("%w: destination appeared during mutation", ErrStaleFeature)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := root.Link(stageRelative, transaction.DestinationPath); err != nil {
		return err
	}
	return nil
}

func (m *Manager) verifyCommittedFeatureTransaction(transaction *featureMutationTransaction) error {
	return m.verifyPublishedFeatureTransaction(transaction, true)
}

func (m *Manager) verifyPublishedFeatureTransaction(transaction *featureMutationTransaction, requireHeld bool) error {
	sourcePath := filepath.Join(m.opts.RootDir, transaction.SourcePath)
	source, err := m.readTransactionFile(sourcePath)
	if err != nil {
		return fmt.Errorf("inspect committed source: %w", err)
	}
	switch transaction.Operation {
	case featureMutationReplace:
		if !source.exists || !transaction.plannedMatches(source) {
			return fmt.Errorf("%w: replacement changed before commit; manual reconciliation required", ErrPendingMove)
		}
	case featureMutationDelete, featureMutationMove:
		if source.exists {
			return fmt.Errorf("%w: source reappeared before commit; manual reconciliation required", ErrPendingMove)
		}
	}
	if transaction.Operation == featureMutationMove {
		destination, err := m.readTransactionFile(filepath.Join(m.opts.RootDir, transaction.DestinationPath))
		if err != nil {
			return fmt.Errorf("inspect committed destination: %w", err)
		}
		if !destination.exists || !transaction.plannedMatches(destination) {
			return fmt.Errorf("%w: move destination changed before commit; manual reconciliation required", ErrPendingMove)
		}
	}
	stage, err := m.readTransactionFile(transaction.stagePath(m.opts.RootDir))
	if err != nil {
		return fmt.Errorf("inspect committed transaction stage: %w", err)
	}
	if stage.exists {
		if !transaction.plannedMatches(stage) {
			return fmt.Errorf("%w: transaction stage changed; manual reconciliation required", ErrPendingMove)
		}
		published := source
		if transaction.Operation == featureMutationMove {
			published, err = m.readTransactionFile(filepath.Join(m.opts.RootDir, transaction.DestinationPath))
			if err != nil {
				return err
			}
		}
		if !published.exists || !os.SameFile(stage.info, published.info) {
			return fmt.Errorf("%w: published destination identity changed; manual reconciliation required", ErrPendingMove)
		}
	}
	if requireHeld {
		return m.verifyHeldExpected(transaction)
	}
	return nil
}

func (m *Manager) verifyHeldExpected(transaction *featureMutationTransaction) error {
	held, err := m.readTransactionFile(transaction.heldPath(m.opts.RootDir))
	if err != nil {
		return fmt.Errorf("inspect captured feature source: %w", err)
	}
	if !held.exists || !transaction.expectedMatches(held) {
		return fmt.Errorf("%w: captured source changed; concurrent content preserved for manual reconciliation", ErrPendingMove)
	}
	return nil
}

func (m *Manager) removeCapturedFeature(transaction *featureMutationTransaction, heldPath string) error {
	if transaction.Operation == featureMutationMove && removeMoveSource != nil {
		return removeMoveSource(heldPath)
	}
	held, err := m.readTransactionFile(heldPath)
	if err != nil {
		return err
	}
	if !transaction.expectedMatches(held) {
		return errors.New("captured feature changed before quarantine; manual reconciliation required")
	}
	return m.quarantineAndRemoveExact(heldPath, held)
}

func (m *Manager) rollbackCapturedFeatureTransaction(transaction *featureMutationTransaction, journalPath string, payload []byte, cause error) error {
	heldPath := transaction.heldPath(m.opts.RootDir)
	held, err := m.readTransactionFile(heldPath)
	if err != nil || !held.exists {
		if err == nil {
			err = errors.New("captured source is missing")
		}
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; cannot restore captured source: %v", cause, err))
	}
	sourcePath := filepath.Join(m.opts.RootDir, transaction.SourcePath)
	source, err := m.readTransactionFile(sourcePath)
	if err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; inspect rollback destination: %v", cause, err))
	}
	if source.exists {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; source path contains concurrent content and captured bytes were retained", cause))
	}
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; open rollback root: %v", cause, err))
	}
	linkErr := root.Link(filepath.Join(transaction.RecoveryDir, "held"), transaction.SourcePath)
	_ = root.Close()
	if linkErr != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; restore captured source exclusively: %v", cause, linkErr))
	}
	restored, err := m.readTransactionFile(sourcePath)
	if err != nil || !restored.exists || !os.SameFile(held.info, restored.info) {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; restored source could not be verified", cause))
	}
	if err := m.removeRecoveryContents(transaction, true); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; rollback cleanup failed: %v", cause, err))
	}
	if err := m.removeJournalIfExact(journalPath, payload); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("%v; rollback journal cleanup failed: %v", cause, err))
	}
	return cause
}

func (m *Manager) cleanupUncapturedFeatureTransaction(transaction *featureMutationTransaction, journalPath string, payload []byte) error {
	held, err := m.readTransactionFile(transaction.heldPath(m.opts.RootDir))
	if err != nil {
		return err
	}
	if held.exists {
		return errors.New("captured source exists; refusing uncaptured cleanup")
	}
	if err := m.removeRecoveryContents(transaction, false); err != nil {
		return err
	}
	return m.removeJournalIfExact(journalPath, payload)
}

func (m *Manager) finishFeatureTransaction(transaction *featureMutationTransaction, journalPath string, payload []byte) error {
	if err := m.removeRecoveryContents(transaction, false); err != nil {
		return fmt.Errorf("clean committed feature transaction: %w", err)
	}
	if err := m.removeLegacyJournalForTransaction(transaction); err != nil {
		return fmt.Errorf("remove migrated legacy journal: %w", err)
	}
	if err := m.removeJournalIfExact(journalPath, payload); err != nil {
		return fmt.Errorf("remove committed feature journal: %w", err)
	}
	return nil
}

func (m *Manager) removeLegacyJournalForTransaction(transaction *featureMutationTransaction) error {
	if transaction == nil || !transaction.MigratedLegacy {
		return nil
	}
	path := m.legacyMoveTransactionPath(transaction.ID)
	state, err := m.readTransactionFile(path)
	if err != nil {
		return err
	}
	if !state.exists {
		return nil
	}
	if digestBytes(state.data) != transaction.LegacySHA256 {
		return errors.New("legacy move journal changed; manual reconciliation required")
	}
	return m.removeJournalIfExact(path, state.data)
}

func (m *Manager) removeRecoveryContents(transaction *featureMutationTransaction, removeHeld bool) error {
	stagePath := transaction.stagePath(m.opts.RootDir)
	stage, err := m.readTransactionFile(stagePath)
	if err != nil {
		return err
	}
	if stage.exists {
		if !transaction.plannedMatches(stage) {
			return errors.New("transaction stage changed before quarantine; manual reconciliation required")
		}
		if err := m.quarantineAndRemoveExact(stagePath, stage); err != nil {
			return err
		}
	}
	if removeHeld {
		heldPath := transaction.heldPath(m.opts.RootDir)
		held, err := m.readTransactionFile(heldPath)
		if err != nil {
			return err
		}
		if held.exists {
			source, err := m.readTransactionFile(filepath.Join(m.opts.RootDir, transaction.SourcePath))
			if err != nil {
				return err
			}
			if !source.exists || !os.SameFile(source.info, held.info) {
				return errors.New("captured rollback alias changed; manual reconciliation required")
			}
			if err := m.quarantineAndRemoveExact(heldPath, held); err != nil {
				return err
			}
		}
	}
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(transaction.RecoveryDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (m *Manager) removeJournalIfExact(path string, expected []byte) error {
	current, err := m.readTransactionFile(path)
	if err != nil {
		return err
	}
	if !current.exists {
		return nil
	}
	if !bytes.Equal(current.data, expected) {
		return errors.New("feature transaction journal changed; manual reconciliation required")
	}
	return m.quarantineAndRemoveExact(path, current)
}

// quarantineAndRemoveExact moves the exact observed file to a unique
// root-scoped quarantine path with no-replace semantics, verifies the captured
// inode, bytes, and mode after the move, and only then unlinks the quarantine.
// A path that appears at the original name after capture is retained.
func (m *Manager) quarantineAndRemoveExact(path string, expected transactionFileState) error {
	if !expected.exists || expected.info == nil {
		return errors.New("exact file state is required for quarantine")
	}
	nonce, err := newFeatureMutationNonce()
	if err != nil {
		return err
	}
	locksRelative, err := relativeTransactionPath(m.opts.RootDir, m.LocksDir())
	if err != nil {
		return err
	}
	quarantineDir := filepath.Join(locksRelative, ".quarantine-"+nonce)
	quarantineRelative := filepath.Join(quarantineDir, "captured")
	originalRelative, err := relativeTransactionPath(m.opts.RootDir, path)
	if err != nil {
		return err
	}
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	if err := root.Mkdir(quarantineDir, 0o700); err != nil {
		_ = root.Close()
		return fmt.Errorf("create feature quarantine: %w", err)
	}
	_ = root.Close()
	if beforeFeaturePathQuarantine != nil {
		beforeFeaturePathQuarantine(path)
	}
	if err := m.renameWithinFeatureRootExclusive(originalRelative, quarantineRelative); err != nil {
		cleanupRoot, openErr := m.openFeatureTransactionRoot()
		if openErr == nil {
			_ = cleanupRoot.Remove(quarantineDir)
			_ = cleanupRoot.Close()
		}
		return err
	}
	quarantinePath := filepath.Join(m.opts.RootDir, quarantineRelative)
	if afterFeaturePathQuarantined != nil {
		afterFeaturePathQuarantined(path, quarantinePath)
	}
	captured, captureErr := m.readTransactionFile(quarantinePath)
	if captureErr != nil || !captured.exists || !os.SameFile(expected.info, captured.info) || !bytes.Equal(expected.data, captured.data) || expected.info.Mode() != captured.info.Mode() {
		original, originalErr := m.readTransactionFile(path)
		if originalErr == nil && !original.exists {
			if restoreErr := m.renameWithinFeatureRootExclusive(quarantineRelative, originalRelative); restoreErr == nil {
				cleanupRoot, openErr := m.openFeatureTransactionRoot()
				if openErr == nil {
					_ = cleanupRoot.Remove(quarantineDir)
					_ = cleanupRoot.Close()
				}
			}
		}
		return fmt.Errorf("quarantined file changed; manual reconciliation required at %s", quarantinePath)
	}
	newer, err := m.readTransactionFile(path)
	if err != nil {
		return err
	}
	latest, err := m.readTransactionFile(quarantinePath)
	if err != nil || !latest.exists || !os.SameFile(captured.info, latest.info) || !bytes.Equal(captured.data, latest.data) || captured.info.Mode() != latest.info.Mode() {
		return fmt.Errorf("quarantined file changed before unlink; manual reconciliation required at %s", quarantinePath)
	}
	root, err = m.openFeatureTransactionRoot()
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.Remove(quarantineRelative); err != nil {
		return fmt.Errorf("unlink verified feature quarantine: %w", err)
	}
	newerAfterUnlink, inspectErr := m.readTransactionFile(path)
	if inspectErr != nil {
		return inspectErr
	}
	if err := root.Remove(quarantineDir); err != nil {
		return fmt.Errorf("remove feature quarantine directory: %w", err)
	}
	if newer.exists || newerAfterUnlink.exists {
		return fmt.Errorf("newer path appeared after quarantine and was preserved at %s", path)
	}
	return nil
}

func (m *Manager) pendingFeatureTransaction(transaction *featureMutationTransaction, journalPath string, cause error) error {
	return fmt.Errorf("%w: %w for %s remains at %s; preserved data is under %s", cause, ErrPendingMove, transaction.ID, journalPath, filepath.Join(m.opts.RootDir, transaction.RecoveryDir))
}

func (m *Manager) openFeatureTransactionRoot() (*os.Root, error) {
	rootPath := filepath.Clean(m.opts.RootDir)
	before, err := os.Lstat(rootPath)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("feature transaction root is not a real directory: %s", rootPath)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("feature transaction root changed while opening: %s", rootPath)
	}
	current, err := os.Lstat(rootPath)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(before, current) {
		_ = root.Close()
		return nil, fmt.Errorf("feature transaction root changed while opening: %s", rootPath)
	}
	return root, nil
}

func (m *Manager) readTransactionFile(path string) (transactionFileState, error) {
	data, info, err := readRegularFeatureFileWithInfo(m.opts.RootDir, path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return transactionFileState{}, nil
		}
		return transactionFileState{}, err
	}
	return transactionFileState{data: data, info: info, exists: true}, nil
}

func (transaction *featureMutationTransaction) expectedMatches(state transactionFileState) bool {
	return state.exists && digestBytes(state.data) == transaction.ExpectedSHA256 && uint32(state.info.Mode().Perm()) == transaction.ExpectedMode
}

func (transaction *featureMutationTransaction) plannedMatches(state transactionFileState) bool {
	return state.exists && digestBytes(state.data) == transaction.PlannedSHA256 && uint32(state.info.Mode().Perm()) == transaction.PlannedMode
}

func relativeTransactionPath(root, path string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes feature transaction root")
	}
	return filepath.Clean(relative), nil
}

func (m *Manager) validateFeatureMutationTransaction(transaction *featureMutationTransaction) (string, string, error) {
	if transaction == nil || transaction.Version != featureMutationTransactionVersion {
		return "", "", errors.New("unsupported feature transaction version")
	}
	if err := m.lifecycle.ValidateFeatureID(transaction.ID); err != nil {
		return "", "", err
	}
	if transaction.Operation != featureMutationReplace && transaction.Operation != featureMutationDelete && transaction.Operation != featureMutationMove {
		return "", "", errors.New("invalid feature transaction operation")
	}
	sourcePath, err := resolveTransactionPath(m.opts.RootDir, transaction.SourcePath)
	if err != nil {
		return "", "", fmt.Errorf("invalid source_path: %w", err)
	}
	if !m.isStatusDirectory(filepath.Dir(sourcePath)) || filepath.Base(sourcePath) == "." {
		return "", "", errors.New("feature transaction source must be a direct child of a status directory")
	}
	if err := m.lifecycle.ValidateFilename(filepath.Base(sourcePath)); err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(filepath.Base(sourcePath), transaction.ID+"-") {
		return "", "", errors.New("feature transaction source does not match its ID")
	}
	destinationPath := ""
	if transaction.Operation == featureMutationDelete {
		if transaction.DestinationPath != "" || transaction.PlannedSHA256 != "" || transaction.PlannedMode != 0 {
			return "", "", errors.New("delete transaction contains replacement metadata")
		}
	} else {
		destinationPath, err = resolveTransactionPath(m.opts.RootDir, transaction.DestinationPath)
		if err != nil {
			return "", "", fmt.Errorf("invalid destination_path: %w", err)
		}
		if !m.isStatusDirectory(filepath.Dir(destinationPath)) || filepath.Base(destinationPath) != filepath.Base(sourcePath) {
			return "", "", errors.New("feature transaction destination is not a matching lifecycle path")
		}
		if transaction.Operation == featureMutationReplace && filepath.Clean(destinationPath) != filepath.Clean(sourcePath) {
			return "", "", errors.New("replacement transaction changes feature path")
		}
		if transaction.Operation == featureMutationMove && filepath.Clean(destinationPath) == filepath.Clean(sourcePath) {
			return "", "", errors.New("move transaction retains feature path")
		}
		if !validTransactionDigest(transaction.PlannedSHA256) || transaction.PlannedMode > 0o777 {
			return "", "", errors.New("invalid planned feature content or mode")
		}
	}
	if !validTransactionDigest(transaction.ExpectedSHA256) || transaction.ExpectedMode > 0o777 {
		return "", "", errors.New("invalid expected feature content or mode")
	}
	if transaction.MigratedLegacy {
		if transaction.Operation != featureMutationMove || !validTransactionDigest(transaction.LegacySHA256) {
			return "", "", errors.New("invalid migrated legacy journal metadata")
		}
	} else if transaction.LegacySHA256 != "" {
		return "", "", errors.New("unexpected legacy journal metadata")
	}
	locksRelative, err := relativeTransactionPath(m.opts.RootDir, m.LocksDir())
	if err != nil {
		return "", "", err
	}
	recoveryParent := filepath.Dir(transaction.RecoveryDir)
	recoveryBase := filepath.Base(transaction.RecoveryDir)
	prefix := ".mutation-" + transaction.ID + "-"
	if filepath.Clean(recoveryParent) != filepath.Clean(locksRelative) || !strings.HasPrefix(recoveryBase, prefix) || !featureMutationNoncePattern.MatchString(strings.TrimPrefix(recoveryBase, prefix)) {
		return "", "", errors.New("invalid feature recovery directory")
	}
	return sourcePath, destinationPath, nil
}

func validTransactionDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

// recoverPendingFeatureTransactions runs under the same per-feature guards as
// normal mutation. It only completes or rolls back states whose visible bytes,
// modes, and path relationships are uniquely explained by the journal.
func (m *Manager) recoverPendingFeatureTransactions() error {
	if m.contractErr != nil {
		return m.contractErr
	}
	names, err := m.discoverFeatureTransactionJournals()
	if err != nil {
		return err
	}
	type pendingJournal struct {
		name           string
		path           string
		data           []byte
		id             string
		version        int
		skip           bool
		migratedLegacy bool
		legacySHA256   string
	}
	pending := make([]pendingJournal, 0, len(names))
	for _, name := range names {
		journalPath := filepath.Join(m.LocksDir(), name)
		data, _, err := util.ReadRegularFileWithin(m.opts.RootDir, journalPath, maxFeatureFileBytes)
		if err != nil {
			return fmt.Errorf("read feature transaction %s: %w", name, err)
		}
		id, version, err := parseFeatureTransactionHeader(data)
		if err != nil {
			return fmt.Errorf("%w: parse %s: %v", ErrPendingMove, name, err)
		}
		if err := m.lifecycle.ValidateFeatureID(id); err != nil {
			return fmt.Errorf("%w: invalid transaction ID in %s: %v", ErrPendingMove, name, err)
		}
		expectedName := fmt.Sprintf(".mutation-%s.json", id)
		if version == 1 {
			expectedName = fmt.Sprintf(".move-%s.json", id)
		}
		if name != expectedName {
			return fmt.Errorf("%w: journal name %q does not match transaction ID %q", ErrPendingMove, name, id)
		}
		journal := pendingJournal{name: name, path: journalPath, data: data, id: id, version: version}
		if version == 1 {
			var legacy moveTransaction
			if err := json.Unmarshal(data, &legacy); err != nil {
				return fmt.Errorf("%w: parse legacy %s: %v", ErrPendingMove, name, err)
			}
			if _, _, err := m.validateMoveTransaction(&legacy); err != nil {
				return fmt.Errorf("%w: validate legacy %s: %v", ErrPendingMove, name, err)
			}
		} else {
			var transaction featureMutationTransaction
			if err := json.Unmarshal(data, &transaction); err != nil {
				return fmt.Errorf("%w: parse %s: %v", ErrPendingMove, name, err)
			}
			if _, _, err := m.validateFeatureMutationTransaction(&transaction); err != nil {
				return fmt.Errorf("%w: validate %s: %v", ErrPendingMove, name, err)
			}
			journal.migratedLegacy = transaction.MigratedLegacy
			journal.legacySHA256 = transaction.LegacySHA256
		}
		pending = append(pending, journal)
	}
	groups := map[string][]int{}
	for index := range pending {
		groups[pending[index].id] = append(groups[pending[index].id], index)
	}
	for id, indexes := range groups {
		if len(indexes) == 1 {
			continue
		}
		if len(indexes) != 2 {
			return fmt.Errorf("%w: %d transaction journals exist for %s", ErrPendingMove, len(indexes), id)
		}
		first := &pending[indexes[0]]
		second := &pending[indexes[1]]
		legacy, migrated := first, second
		if legacy.version != 1 {
			legacy, migrated = second, first
		}
		if legacy.version != 1 || migrated.version != featureMutationTransactionVersion || !migrated.migratedLegacy || migrated.legacySHA256 != digestBytes(legacy.data) {
			return fmt.Errorf("%w: conflicting transaction journals for %s: %s and %s", ErrPendingMove, id, first.name, second.name)
		}
		legacy.skip = true
	}
	if m.opts.DryRun {
		for _, journal := range pending {
			if !journal.skip {
				return fmt.Errorf("%w: %s requires recovery outside dry-run mode", ErrPendingMove, journal.id)
			}
		}
	}
	for _, journal := range pending {
		if journal.skip {
			continue
		}
		recovered := false
		if err := m.lockMgr.WithOperationGuard(featureMutationLockID(journal.id), func() error {
			current, _, currentErr := util.ReadRegularFileWithin(m.opts.RootDir, journal.path, maxFeatureFileBytes)
			if errors.Is(currentErr, fs.ErrNotExist) {
				return nil
			}
			if currentErr != nil {
				return currentErr
			}
			if !bytes.Equal(journal.data, current) {
				return fmt.Errorf("%w: transaction journal %s changed while awaiting its guard", ErrPendingMove, journal.name)
			}
			recovered = true
			if journal.version == 1 {
				return m.recoverLegacyMoveJournal(current, journal.path)
			}
			var transaction featureMutationTransaction
			if err := json.Unmarshal(current, &transaction); err != nil {
				return err
			}
			return m.recoverFeatureMutationTransaction(&transaction, journal.path, current)
		}); err != nil {
			return fmt.Errorf("recover feature transaction under guard: %w", err)
		}
		if recovered {
			m.auditEvent("feature-recover", journal.id, "journal="+journal.name)
		}
	}
	return nil
}

func (m *Manager) discoverFeatureTransactionJournals() ([]string, error) {
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	locksRelative, err := relativeTransactionPath(m.opts.RootDir, m.LocksDir())
	if err != nil {
		return nil, err
	}
	before, err := root.Lstat(locksRelative)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if featurePathIsLinked(before) || !before.IsDir() {
		return nil, fmt.Errorf("%w: transaction directory is linked, reparsed, or not a directory", ErrPendingMove)
	}
	directory, err := root.Open(locksRelative)
	if err != nil {
		return nil, err
	}
	defer func() { _ = directory.Close() }()
	opened, err := directory.Stat()
	if err != nil || featurePathIsLinked(opened) || !opened.IsDir() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("%w: transaction directory changed while opening", ErrPendingMove)
	}
	names := make([]string, 0)
	entryCount := 0
	for {
		entries, readErr := directory.ReadDir(featureReadDirBatchSize)
		for _, entry := range entries {
			entryCount++
			if entryCount > maxFeatureDirectoryEntries {
				return nil, fmt.Errorf("%w: transaction directory entry limit exceeded", ErrPendingMove)
			}
			name := entry.Name()
			if !((strings.HasPrefix(name, ".mutation-") || strings.HasPrefix(name, ".move-")) && strings.HasSuffix(name, ".json")) {
				continue
			}
			info, err := root.Lstat(filepath.Join(locksRelative, name))
			if err != nil {
				return nil, err
			}
			if featurePathIsLinked(info) || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%w: unsafe transaction journal %s", ErrPendingMove, name)
			}
			names = append(names, name)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	current, err := root.Lstat(locksRelative)
	if err != nil || featurePathIsLinked(current) || !current.IsDir() || !os.SameFile(opened, current) {
		return nil, fmt.Errorf("%w: transaction directory changed during enumeration", ErrPendingMove)
	}
	sort.Strings(names)
	return names, nil
}

func parseFeatureTransactionHeader(data []byte) (string, int, error) {
	var header struct {
		Version int    `json:"version"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return "", 0, err
	}
	if header.Version != 1 && header.Version != featureMutationTransactionVersion {
		return "", 0, fmt.Errorf("unsupported transaction version %d", header.Version)
	}
	return header.ID, header.Version, nil
}

func (m *Manager) recoverFeatureMutationTransaction(transaction *featureMutationTransaction, journalPath string, payload []byte) error {
	sourcePath, destinationPath, err := m.validateFeatureMutationTransaction(transaction)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPendingMove, err)
	}
	source, err := m.readTransactionFile(sourcePath)
	if err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("inspect recovery source: %w", err))
	}
	held, err := m.readTransactionFile(transaction.heldPath(m.opts.RootDir))
	if err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("inspect recovery capture: %w", err))
	}
	destination := transactionFileState{}
	if transaction.Operation == featureMutationMove {
		destination, err = m.readTransactionFile(destinationPath)
		if err != nil {
			return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("inspect recovery destination: %w", err))
		}
	}

	if held.exists && !transaction.expectedMatches(held) {
		return m.pendingFeatureTransaction(transaction, journalPath, errors.New("captured source was edited; manual reconciliation required"))
	}
	if transaction.Operation == featureMutationMove && destination.exists && !transaction.plannedMatches(destination) {
		return m.pendingFeatureTransaction(transaction, journalPath, errors.New("move destination was edited; manual reconciliation required"))
	}
	if transaction.Operation == featureMutationReplace && source.exists && !transaction.expectedMatches(source) && !transaction.plannedMatches(source) {
		return m.pendingFeatureTransaction(transaction, journalPath, errors.New("replacement destination was edited; manual reconciliation required"))
	}

	switch transaction.Operation {
	case featureMutationReplace:
		switch {
		case held.exists && source.exists && os.SameFile(held.info, source.info):
			return m.cleanupRecoveredRollback(transaction, journalPath, payload)
		case held.exists && !source.exists:
			return m.restoreRecoveredCapture(transaction, journalPath, payload, held)
		case held.exists && source.exists && transaction.plannedMatches(source):
			return m.cleanupRecoveredCommit(transaction, journalPath, payload)
		case !held.exists && source.exists && transaction.plannedMatches(source):
			return m.cleanupRecoveredCommit(transaction, journalPath, payload)
		case !held.exists && source.exists && transaction.expectedMatches(source):
			return m.cleanupRecoveredRollback(transaction, journalPath, payload)
		default:
			return m.pendingFeatureTransaction(transaction, journalPath, errors.New("ambiguous replacement recovery state; manual reconciliation required"))
		}
	case featureMutationDelete:
		switch {
		case held.exists && source.exists && os.SameFile(held.info, source.info):
			return m.cleanupRecoveredRollback(transaction, journalPath, payload)
		case held.exists && !source.exists:
			if err := m.verifyHeldExpected(transaction); err != nil {
				return m.pendingFeatureTransaction(transaction, journalPath, err)
			}
			if err := m.removeCapturedFeature(transaction, transaction.heldPath(m.opts.RootDir)); err != nil {
				return m.pendingFeatureTransaction(transaction, journalPath, err)
			}
			return m.cleanupRecoveredCommit(transaction, journalPath, payload)
		case !held.exists && !source.exists:
			return m.cleanupRecoveredCommit(transaction, journalPath, payload)
		case !held.exists && source.exists:
			// No capture occurred. Preserve any direct edit and roll back only
			// transaction-owned staging and metadata.
			return m.cleanupRecoveredRollback(transaction, journalPath, payload)
		default:
			return m.pendingFeatureTransaction(transaction, journalPath, errors.New("ambiguous delete recovery state; manual reconciliation required"))
		}
	case featureMutationMove:
		switch {
		case held.exists && source.exists && os.SameFile(held.info, source.info) && !destination.exists:
			return m.cleanupRecoveredRollback(transaction, journalPath, payload)
		case held.exists && !source.exists && destination.exists:
			return m.cleanupRecoveredCommit(transaction, journalPath, payload)
		case held.exists && !source.exists && !destination.exists:
			return m.restoreRecoveredCapture(transaction, journalPath, payload, held)
		case !held.exists && !source.exists && destination.exists:
			return m.cleanupRecoveredCommit(transaction, journalPath, payload)
		case !held.exists && source.exists && !destination.exists && transaction.expectedMatches(source):
			return m.cleanupRecoveredRollback(transaction, journalPath, payload)
		case transaction.MigratedLegacy && !held.exists && source.exists && destination.exists && transaction.expectedMatches(source) && transaction.plannedMatches(destination):
			return m.completeMigratedLegacyMove(transaction, journalPath, payload, source)
		default:
			return m.pendingFeatureTransaction(transaction, journalPath, errors.New("ambiguous move recovery state; manual reconciliation required"))
		}
	default:
		return m.pendingFeatureTransaction(transaction, journalPath, errors.New("unsupported feature transaction operation"))
	}
}

func (m *Manager) restoreRecoveredCapture(transaction *featureMutationTransaction, journalPath string, payload []byte, held transactionFileState) error {
	sourcePath := filepath.Join(m.opts.RootDir, transaction.SourcePath)
	source, err := m.readTransactionFile(sourcePath)
	if err != nil || source.exists {
		return m.pendingFeatureTransaction(transaction, journalPath, errors.New("source cannot be restored without overwriting concurrent content"))
	}
	root, err := m.openFeatureTransactionRoot()
	if err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	err = root.Link(filepath.Join(transaction.RecoveryDir, "held"), transaction.SourcePath)
	_ = root.Close()
	if err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("restore captured source: %w", err))
	}
	restored, err := m.readTransactionFile(sourcePath)
	if err != nil || !restored.exists || !os.SameFile(held.info, restored.info) {
		return m.pendingFeatureTransaction(transaction, journalPath, errors.New("restored source identity could not be verified"))
	}
	return m.cleanupRecoveredRollback(transaction, journalPath, payload)
}

func (m *Manager) cleanupRecoveredRollback(transaction *featureMutationTransaction, journalPath string, payload []byte) error {
	if err := m.removeRecoveryContents(transaction, true); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("clean recovered rollback: %w", err))
	}
	if err := m.removeLegacyJournalForTransaction(transaction); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("clean migrated legacy rollback journal: %w", err))
	}
	if err := m.removeJournalIfExact(journalPath, payload); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, fmt.Errorf("clean recovered rollback journal: %w", err))
	}
	return nil
}

func (m *Manager) cleanupRecoveredCommit(transaction *featureMutationTransaction, journalPath string, payload []byte) error {
	held, err := m.readTransactionFile(transaction.heldPath(m.opts.RootDir))
	if err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	if transaction.Operation != featureMutationDelete {
		if err := m.verifyPublishedFeatureTransaction(transaction, held.exists); err != nil {
			return m.pendingFeatureTransaction(transaction, journalPath, err)
		}
	}
	if held.exists {
		if err := m.removeCapturedFeature(transaction, transaction.heldPath(m.opts.RootDir)); err != nil {
			return m.pendingFeatureTransaction(transaction, journalPath, err)
		}
	}
	if err := m.finishFeatureTransaction(transaction, journalPath, payload); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	return nil
}

func (m *Manager) completeMigratedLegacyMove(transaction *featureMutationTransaction, journalPath string, payload []byte, source transactionFileState) error {
	if err := m.createFeatureRecoveryDirectory(transaction); err != nil && !errors.Is(err, fs.ErrExist) {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	if err := m.captureFeatureSource(filepath.Join(m.opts.RootDir, transaction.SourcePath), transaction.heldPath(m.opts.RootDir)); err != nil {
		return m.pendingFeatureTransaction(transaction, journalPath, err)
	}
	held, err := m.readTransactionFile(transaction.heldPath(m.opts.RootDir))
	if err != nil || !held.exists || !transaction.expectedMatches(held) || !os.SameFile(source.info, held.info) {
		return m.pendingFeatureTransaction(transaction, journalPath, errors.New("legacy move source changed during capture"))
	}
	return m.cleanupRecoveredCommit(transaction, journalPath, payload)
}
