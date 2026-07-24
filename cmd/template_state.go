package cmd

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
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/virtualboard/vb-cli/internal/templatediff"
	"github.com/virtualboard/vb-cli/internal/util"
)

const templateManifestFile = ".template-manifest.json"
const templateManifestVersion = 1
const maxTemplateManifestBytes = 10 << 20

var legacyRetiredFiles = map[string]templateFileState{
	".claude-plugin/plugin.json": {
		SHA256: "23d8add1787acd0846d70ff827765b7e7d71e0a3191d3e4d9e9dc19de44ebf18",
		Mode:   0o644,
	},
}

type templateManifest struct {
	ManifestVersion int                          `json:"manifest_version"`
	TemplateVersion string                       `json:"template_version"`
	Files           map[string]templateFileState `json:"files"`
}

type templateFileState struct {
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

func buildTemplateManifest(root, version string) (*templateManifest, error) {
	manifest := &templateManifest{
		ManifestVersion: templateManifestVersion,
		TemplateVersion: version,
		Files:           map[string]templateFileState{},
	}
	templateRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open template root: %w", err)
	}
	defer func() { _ = templateRoot.Close() }()

	entryCount := 0
	var totalBytes int64
	err = fs.WalkDir(templateRoot.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." {
			return nil
		}
		entryCount++
		if entryCount > maxTemplateArchiveEntries {
			return fmt.Errorf("template tree exceeds %d entries", maxTemplateArchiveEntries)
		}
		normalized := filepath.ToSlash(path)
		if entry.IsDir() {
			if isTemplateStateProtectedDirectory(normalized) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isTemplateManagedPath(normalized) {
			return nil
		}
		data, mode, err := readSecureRegularRootFileState(templateRoot, filepath.FromSlash(path), maxTemplateFileBytes)
		if err != nil {
			return fmt.Errorf("template manifest refuses unsafe path %s: %w", normalized, err)
		}
		totalBytes += int64(len(data))
		if totalBytes > maxTemplateExtractedBytes {
			return fmt.Errorf("template tree exceeds %d bytes", maxTemplateExtractedBytes)
		}
		digest := sha256.Sum256(data)
		manifest.Files[normalized] = templateFileState{
			SHA256: hex.EncodeToString(digest[:]),
			Mode:   uint32(scaffoldFileMode(normalized, mode).Perm()),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(manifest.Files) == 0 {
		return nil, errors.New("template manifest contains no managed files")
	}
	return manifest, nil
}

func writeTemplateManifest(root string, manifest *templateManifest) error {
	if manifest == nil || manifest.ManifestVersion != templateManifestVersion || strings.TrimSpace(manifest.TemplateVersion) == "" || len(manifest.Files) == 0 {
		return errors.New("invalid template manifest")
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return util.WriteFileAtomic(filepath.Join(root, templateManifestFile), payload, 0o600)
}

func readTemplateManifest(root string) (*templateManifest, error) {
	data, err := readSecureRegularFile(filepath.Join(root, templateManifestFile), maxTemplateManifestBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var manifest templateManifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("parse template manifest: %w", err)
	}
	if manifest.ManifestVersion != templateManifestVersion || strings.TrimSpace(manifest.TemplateVersion) == "" || len(manifest.Files) == 0 {
		return nil, errors.New("template manifest is incomplete")
	}
	for path, state := range manifest.Files {
		if !isTemplateManagedPath(path) || filepath.ToSlash(filepath.Clean(path)) != path {
			return nil, fmt.Errorf("template manifest contains unsafe path %q", path)
		}
		digest, err := hex.DecodeString(state.SHA256)
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("template manifest contains invalid digest for %s", path)
		}
		if state.Mode != 0o644 && state.Mode != 0o755 {
			return nil, fmt.Errorf("template manifest contains invalid mode for %s", path)
		}
	}
	return &manifest, nil
}

func readSecureRegularFile(path string, maxBytes int64) ([]byte, error) {
	data, _, err := readSecureRegularFileState(path, maxBytes)
	return data, err
}

func readSecureRegularFileState(path string, maxBytes int64) ([]byte, fs.FileMode, error) {
	parent := filepath.Dir(path)
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, 0, fmt.Errorf("open parent directory for %s: %w", path, err)
	}
	defer func() { _ = root.Close() }()
	return readSecureRegularRootFileState(root, filepath.Base(path), maxBytes)
}

func readSecureRegularRootFileState(root *os.Root, path string, maxBytes int64) ([]byte, fs.FileMode, error) {
	if root == nil {
		return nil, 0, errors.New("secure file root is required")
	}
	if maxBytes < 0 {
		return nil, 0, errors.New("secure file size limit must be non-negative")
	}
	before, err := root.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !before.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("refusing non-regular file %s", path)
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, 0, fmt.Errorf("file changed during secure read: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(data)) > maxBytes {
		return nil, 0, fmt.Errorf("file exceeds %d bytes: %s", maxBytes, path)
	}
	return data, after.Mode().Perm(), nil
}

func ensureTemplateManifest(root, version string) (*templateManifest, error) {
	manifest, err := readTemplateManifest(root)
	if err != nil {
		return nil, err
	}
	if manifest == nil {
		return buildTemplateManifest(root, version)
	}
	if manifest.TemplateVersion != version {
		return nil, fmt.Errorf("template manifest version %q does not match template %q", manifest.TemplateVersion, version)
	}
	actual, err := buildTemplateManifest(root, version)
	if err != nil {
		return nil, err
	}
	if len(actual.Files) != len(manifest.Files) {
		return nil, fmt.Errorf("template manifest inventory mismatch: recorded %d files, found %d", len(manifest.Files), len(actual.Files))
	}
	for path, expected := range manifest.Files {
		if got, ok := actual.Files[path]; !ok || got != expected {
			return nil, fmt.Errorf("template manifest verification failed for %s", path)
		}
	}
	return manifest, nil
}

func saveTemplateProvenance(root, version string, manifest *templateManifest) error {
	if manifest == nil {
		return errors.New("template manifest is required")
	}
	copyManifest := cloneTemplateManifest(manifest)
	copyManifest.TemplateVersion = version
	if err := verifyTemplateTreeMatchesManifest(root, copyManifest); err != nil {
		return fmt.Errorf("refusing to advance template provenance: %w", err)
	}
	if err := writeTemplateManifest(root, copyManifest); err != nil {
		return fmt.Errorf("save template file manifest: %w", err)
	}
	if err := saveTemplateVersion(root, version); err != nil {
		return fmt.Errorf("save template version: %w", err)
	}
	return nil
}

func cloneTemplateManifest(manifest *templateManifest) *templateManifest {
	clone := &templateManifest{
		ManifestVersion: manifest.ManifestVersion,
		TemplateVersion: manifest.TemplateVersion,
		Files:           make(map[string]templateFileState, len(manifest.Files)),
	}
	for path, state := range manifest.Files {
		clone.Files[path] = state
	}
	return clone
}

func filterRemovalsToPreviouslyManaged(diff *templatediff.TemplateDiff, previous *templateManifest) *templatediff.TemplateDiff {
	if previous == nil {
		diff.Removed = nil
		return diff
	}
	managed := make([]templatediff.FileDiff, 0, len(diff.Removed))
	for _, file := range diff.Removed {
		expected, ok := previous.Files[filepath.ToSlash(file.Path)]
		if ok && templateFileStateMatches(file.Path, file.LocalContent, file.LocalMode, expected) {
			managed = append(managed, file)
		}
	}
	diff.Removed = managed
	return diff
}

func templateFileStateMatches(path string, content []byte, mode fs.FileMode, expected templateFileState) bool {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]) == expected.SHA256 && uint32(installedTemplateFileMode(path, mode).Perm()) == expected.Mode
}

func installedTemplateFileMode(path string, mode fs.FileMode) fs.FileMode {
	if runtime.GOOS == "windows" {
		return scaffoldFileMode(path, mode)
	}
	return mode.Perm()
}

func verifyTemplateTreeMatchesManifest(root string, expected *templateManifest) error {
	if expected == nil {
		return errors.New("template manifest is required")
	}
	for path, state := range expected.Files {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		content, mode, err := readSecureRegularFileState(fullPath, maxTemplateFileBytes)
		if err != nil {
			return fmt.Errorf("verify installed template file %s: %w", path, err)
		}
		if !templateFileStateMatches(path, content, mode, state) {
			return fmt.Errorf("installed template file bytes or mode do not match authenticated manifest at %s", path)
		}
	}
	return nil
}

// legacyRemovalManifest contains the checksum-pinned subset of v0.7 framework
// files retired in v0.8. It is used only when an old workspace predates local
// manifests, and a path is admitted only if its current bytes still match the
// released v0.7 artifact. User-modified or unknown files remain untouched.
func legacyRemovalManifest(root, version string) (*templateManifest, error) {
	if strings.TrimPrefix(strings.TrimSpace(version), "v") != "0.7.0" {
		return nil, nil
	}
	manifest := &templateManifest{ManifestVersion: templateManifestVersion, TemplateVersion: "0.7.0", Files: map[string]templateFileState{}}
	for path, expected := range legacyRetiredFiles {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		data, mode, err := readSecureRegularFileState(fullPath, maxTemplateFileBytes)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("inspect legacy managed file %s: %w", path, err)
		}
		if templateFileStateMatches(path, data, mode, expected) {
			manifest.Files[path] = expected
		}
	}
	if len(manifest.Files) == 0 {
		return nil, nil
	}
	return manifest, nil
}

func isTemplateStateProtectedDirectory(path string) bool {
	normalized := filepath.ToSlash(path)
	for _, protected := range []string{".git", ".state", "archive", "locks", "reports", "specs"} {
		if normalized == protected || strings.HasPrefix(normalized, protected+"/") {
			return true
		}
	}
	return false
}

func isTemplateManagedPath(path string) bool {
	normalized := filepath.ToSlash(path)
	if normalized == "" || normalized == "." || normalized == "audit.jsonl" || normalized == templateVersionFile || normalized == templateManifestFile || normalized == "features/INDEX.md" {
		return false
	}
	if isTemplateStateProtectedDirectory(normalized) {
		return false
	}
	parts := strings.Split(normalized, "/")
	if len(parts) >= 3 && parts[0] == "features" && strings.HasSuffix(parts[len(parts)-1], ".md") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(normalized))
	return clean == normalized && clean != ".." && !strings.HasPrefix(clean, "../") && !filepath.IsAbs(normalized)
}

func sortedManifestPaths(manifest *templateManifest) []string {
	paths := make([]string, 0, len(manifest.Files))
	for path := range manifest.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// directory replacement journal ------------------------------------------------

const directoryReplaceJournalVersion = 2
const directoryReplaceLeaseVersion = 1

type directoryReplaceJournal struct {
	Version            int       `json:"version"`
	Target             string    `json:"target"`
	Stage              string    `json:"stage"`
	Backup             string    `json:"backup"`
	ExpectedTreeSHA256 string    `json:"expected_tree_sha256,omitempty"`
	Nonce              string    `json:"nonce"`
	PID                int       `json:"pid"`
	Host               string    `json:"host"`
	StartedAt          time.Time `json:"started_at"`
}

type directoryReplaceLeaseRecord struct {
	Version   int       `json:"version"`
	Nonce     string    `json:"nonce"`
	PID       int       `json:"pid"`
	Host      string    `json:"host"`
	StartedAt time.Time `json:"started_at"`
}

type directoryReplaceLease struct {
	path   string
	record directoryReplaceLeaseRecord
	file   *os.File
}

var renameTemplateDirectory = os.Rename

var renameDirectoryExclusive = renameDirectoryExclusiveOS

func directoryReplaceJournalPath(target string) string {
	return filepath.Join(filepath.Dir(target), ".vb-replace-"+filepath.Base(target)+".json")
}

func directoryReplaceLeasePath(target string) string {
	return filepath.Join(filepath.Dir(target), ".vb-replace-"+filepath.Base(target)+".lock")
}

// renameReplacementDirectoryExclusive atomically moves one real sibling
// directory to an absent sibling name. Unlike os.Rename, it never replaces a
// destination that another process created between inspection and activation.
// The parent identity is held open and verified around the platform no-replace
// primitive so compliant paths cannot be redirected through a replaced parent.
func renameReplacementDirectoryExclusive(source, destination string) error {
	source = filepath.Clean(source)
	destination = filepath.Clean(destination)
	parent := filepath.Dir(source)
	if filepath.Dir(destination) != parent || filepath.Base(source) == "." || filepath.Base(destination) == "." {
		return errors.New("exclusive directory rename requires distinct sibling paths")
	}
	if source == destination {
		return errors.New("exclusive directory rename source and destination are identical")
	}
	before, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect directory replacement parent: %w", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("directory replacement parent is not a real directory: %s", parent)
	}
	parentFile, err := os.Open(parent) // #nosec G304 -- parent is derived from validated sibling paths.
	if err != nil {
		return fmt.Errorf("open directory replacement parent: %w", err)
	}
	defer parentFile.Close()
	opened, err := parentFile.Stat()
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		return fmt.Errorf("directory replacement parent changed while opening: %s", parent)
	}
	current, err := os.Lstat(parent)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(before, current) {
		return fmt.Errorf("directory replacement parent changed while opening: %s", parent)
	}
	if err := renameDirectoryExclusive(parentFile, filepath.Base(source), filepath.Base(destination)); err != nil {
		return err
	}
	return nil
}

func recoverDirectoryReplacement(target string) error {
	lease, previousNonce, err := acquireDirectoryReplaceLease(target)
	if err != nil {
		return err
	}
	if err := recoverDirectoryReplacementWithLease(target, lease, previousNonce); err != nil {
		return errors.Join(err, lease.release())
	}
	return lease.release()
}

func recoverDirectoryReplacementWithLease(target string, lease *directoryReplaceLease, previousNonce string) error {
	journalPath := directoryReplaceJournalPath(target)
	data, err := readSecureRegularFile(journalPath, 4096)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var journal directoryReplaceJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return fmt.Errorf("parse directory replacement journal: %w", err)
	}
	if journal.Version != directoryReplaceJournalVersion || journal.Target != filepath.Base(target) || !validDirectoryReplaceNonce(journal.Nonce) || journal.PID <= 0 || strings.TrimSpace(journal.Host) == "" || !safeReplacementStageName(journal.Stage) || !safeReplacementName(journal.Backup, ".vb-template-backup-") || !validOptionalSHA256(journal.ExpectedTreeSHA256) {
		return errors.New("directory replacement journal is invalid; manual reconciliation required")
	}
	if previousNonce == "" || journal.Nonce != previousNonce {
		return errors.New("directory replacement journal has no matching prior-owner lease record; manual reconciliation required")
	}
	parent := filepath.Dir(target)
	stage := filepath.Join(parent, journal.Stage)
	backup := filepath.Join(parent, journal.Backup)
	targetExists, err := replacementDirectoryExists(target)
	if err != nil {
		return err
	}
	stageExists, err := replacementDirectoryExists(stage)
	if err != nil {
		return err
	}
	backupExists, err := replacementDirectoryExists(backup)
	if err != nil {
		return err
	}

	switch {
	case targetExists && stageExists && !backupExists:
		// The first rename never happened. Roll back the unused stage.
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
	case !targetExists && stageExists && backupExists:
		// The live tree was backed up. An integration replacement records the
		// exact tree that was approved; never activate its stage if the atomic
		// capture proves that a newer tree was moved instead.
		if journal.ExpectedTreeSHA256 != "" {
			digest, digestErr := integrationDestinationTreeDigest(backup)
			if digestErr != nil {
				return fmt.Errorf("verify captured integration tree during recovery: %w", digestErr)
			}
			if digest != journal.ExpectedTreeSHA256 {
				if err := renameReplacementDirectoryExclusive(backup, target); err != nil {
					return fmt.Errorf("captured integration tree differs from the approved state and could not be restored without replacement; recovery data retained: %w", err)
				}
				if err := os.RemoveAll(stage); err != nil {
					return fmt.Errorf("restored changed integration tree but failed to remove stale stage: %w", err)
				}
				return removeDirectoryReplaceJournal(journalPath, journal.Nonce)
			}
		}
		if err := renameReplacementDirectoryExclusive(stage, target); err != nil {
			return fmt.Errorf("recover template activation: %w", err)
		}
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("recover template backup cleanup: %w", err)
		}
	case targetExists && !stageExists && backupExists:
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("recover template backup cleanup: %w", err)
		}
	case !targetExists && !stageExists && backupExists:
		if err := renameReplacementDirectoryExclusive(backup, target); err != nil {
			return fmt.Errorf("restore interrupted template replacement: %w", err)
		}
	case targetExists && !stageExists && !backupExists:
		// Only a stale journal remains.
	default:
		return errors.New("directory replacement state is ambiguous; manual reconciliation required")
	}
	return removeDirectoryReplaceJournal(journalPath, journal.Nonce)
}

func acquireDirectoryReplaceLease(target string) (*directoryReplaceLease, string, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, "", fmt.Errorf("identify directory replacement host: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return nil, "", errors.New("identify directory replacement host: hostname is empty")
	}
	leasePath := directoryReplaceLeasePath(target)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0o750); err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(leasePath, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- deterministic sibling coordination path
	if err != nil {
		return nil, "", fmt.Errorf("open directory replacement lease: %w", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, "", err
	}
	current, err := os.Lstat(leasePath)
	if err != nil || !opened.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		_ = file.Close()
		return nil, "", errors.New("directory replacement lease path is unsafe")
	}
	locked, err := tryLockDirectoryReplaceLease(file)
	if err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("lock directory replacement lease: %w", err)
	}
	if !locked {
		_ = file.Close()
		return nil, "", errors.New("another template replacement is still in progress")
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = unlockDirectoryReplaceLease(file)
			_ = file.Close()
		}
	}()

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return nil, "", errors.New("directory replacement lease is unreadable or oversized")
	}
	var previousNonce string
	var nonce string
	journalExists := false
	if _, err := os.Lstat(directoryReplaceJournalPath(target)); err == nil {
		journalExists = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, "", err
	}
	if len(bytes.TrimSpace(data)) > 0 {
		stale, err := parseDirectoryReplaceLease(data)
		if err != nil {
			return nil, "", err
		}
		if journalExists {
			previousNonce = stale.Nonce
			nonce = stale.Nonce
		}
	}
	if nonce == "" {
		nonce, err = randomDirectoryReplaceNonce()
		if err != nil {
			return nil, "", err
		}
	}
	record := directoryReplaceLeaseRecord{Version: directoryReplaceLeaseVersion, Nonce: nonce, PID: os.Getpid(), Host: host, StartedAt: time.Now().UTC()}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, "", err
	}
	payload = append(payload, '\n')
	if err := file.Truncate(0); err != nil {
		return nil, "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	if _, err := file.Write(payload); err != nil {
		return nil, "", err
	}
	if err := file.Sync(); err != nil {
		return nil, "", err
	}
	closeOnError = false
	return &directoryReplaceLease{path: leasePath, record: record, file: file}, previousNonce, nil
}

// verifyOwnership confirms the lease path still names the retained locked
// inode and that its content still matches the record this holder wrote.
// Content is read back through the retained handle rather than a fresh open:
// Windows enforces the acquired byte-range lock across handles, so a second
// handle reading the same locked range fails closed even for this same
// process, unlike POSIX advisory locks.
func (lease *directoryReplaceLease) verifyOwnership() error {
	ownershipChanged := errors.New("directory replacement lease ownership changed; refusing to release it")
	pathInfo, err := os.Lstat(lease.path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return ownershipChanged
	}
	heldInfo, err := lease.file.Stat()
	if err != nil || !os.SameFile(pathInfo, heldInfo) {
		return ownershipChanged
	}
	if _, err := lease.file.Seek(0, io.SeekStart); err != nil {
		return ownershipChanged
	}
	data, err := io.ReadAll(io.LimitReader(lease.file, 4097))
	if err != nil || len(data) > 4096 {
		return ownershipChanged
	}
	current, err := parseDirectoryReplaceLease(data)
	if err != nil || current != lease.record {
		return ownershipChanged
	}
	return nil
}

func (lease *directoryReplaceLease) release() error {
	if lease == nil || lease.file == nil {
		return nil
	}
	if err := lease.verifyOwnership(); err != nil {
		return err
	}
	// A retained journal needs the exact nonce in this record so the next
	// lease-holder can prove it is recovering this operation. Remove the lease
	// name only after its journal is gone; otherwise keep the regular record for
	// deterministic recovery. If the platform cannot unlink an open file, leave
	// it for safe reuse.
	journalPath := strings.TrimSuffix(lease.path, ".lock") + ".json"
	if _, journalErr := os.Lstat(journalPath); errors.Is(journalErr, fs.ErrNotExist) {
		_ = os.Remove(lease.path)
	}
	unlockErr := unlockDirectoryReplaceLease(lease.file)
	closeErr := lease.file.Close()
	lease.file = nil
	return errors.Join(unlockErr, closeErr)
}

func writeDirectoryReplaceLease(path string, record directoryReplaceLeaseRecord) error {
	if record.Version != directoryReplaceLeaseVersion || !validDirectoryReplaceNonce(record.Nonce) || record.PID <= 0 || strings.TrimSpace(record.Host) == "" {
		return errors.New("invalid directory replacement lease")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- deterministic sibling path
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	remove = false
	return nil
}

func parseDirectoryReplaceLease(data []byte) (directoryReplaceLeaseRecord, error) {
	var record directoryReplaceLeaseRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return directoryReplaceLeaseRecord{}, fmt.Errorf("parse directory replacement lease: %w", err)
	}
	if record.Version != directoryReplaceLeaseVersion || !validDirectoryReplaceNonce(record.Nonce) || record.PID <= 0 || strings.TrimSpace(record.Host) == "" {
		return directoryReplaceLeaseRecord{}, errors.New("directory replacement lease is invalid; manual reconciliation required")
	}
	return record, nil
}

func randomDirectoryReplaceNonce() (string, error) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate directory replacement nonce: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func validDirectoryReplaceNonce(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 24
}

func removeDirectoryReplaceJournal(path, nonce string) error {
	data, err := readSecureRegularFile(path, 4096)
	if err != nil {
		return err
	}
	var current directoryReplaceJournal
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.Nonce != nonce {
		return errors.New("directory replacement journal ownership changed; refusing to remove it")
	}
	return os.Remove(path)
}

func writeDirectoryReplaceJournal(path string, journal directoryReplaceJournal) error {
	if journal.Version != directoryReplaceJournalVersion || journal.Target == "" || filepath.Base(journal.Target) != journal.Target || !safeReplacementStageName(journal.Stage) || !safeReplacementName(journal.Backup, ".vb-template-backup-") || !validDirectoryReplaceNonce(journal.Nonce) || journal.PID <= 0 || strings.TrimSpace(journal.Host) == "" || !validOptionalSHA256(journal.ExpectedTreeSHA256) {
		return errors.New("invalid directory replacement journal")
	}
	payload, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- deterministic sibling path
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	completed = true
	return nil
}

func validOptionalSHA256(value string) bool {
	if value == "" {
		return true
	}
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func safeReplacementName(name, prefix string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, prefix) && len(name) > len(prefix)
}

func safeReplacementStageName(name string) bool {
	return safeReplacementName(name, ".vb-template-stage-") ||
		safeReplacementName(name, ".vb-integration-stage-")
}

func replacementDirectoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, fmt.Errorf("replacement path is not a real directory: %s", path)
		}
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}
