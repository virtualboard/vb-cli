package cmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/templatediff"
	"github.com/virtualboard/vb-cli/internal/util"
	"github.com/virtualboard/vb-cli/internal/version"
	"github.com/xeipuuv/gojsonschema"
)

const initDirName = ".virtualboard"
const templateRelease = "v0.8.0"
const templateVersion = "0.8.0"
const templateArchiveName = "template-base-" + templateRelease + ".zip"
const templateZipURL = "https://github.com/virtualboard/template-base/releases/download/" + templateRelease + "/" + templateArchiveName
const maxTemplateArchiveBytes int64 = 50 * 1024 * 1024
const maxTemplateFileBytes int64 = 20 * 1024 * 1024
const maxTemplateExtractedBytes int64 = 100 * 1024 * 1024
const maxTemplateArchiveEntries = 10000
const maxTemplateArchiveDepth = 32
const templateVersionFile = ".template-version"

var afterTemplateSourceMoved func(string)

// templateArchiveSHA256 must be injected by the coordinated CLI release after
// the pinned template release asset exists, for example with:
// -ldflags "-X github.com/virtualboard/vb-cli/cmd.templateArchiveSHA256=<hex>"
// Development builds intentionally fail closed when it is unset.
var templateArchiveSHA256 string

type fetchTemplateFunc func(workdir, dest string) error
type fetchTemplateToDirFunc func() (string, error)
type fetchTemplateVersionFunc func() (string, error)

var templateHTTPGet = (&http.Client{Timeout: 30 * time.Second}).Get

var fetchTemplate fetchTemplateFunc = func(workdir, dest string) error {
	var archiveBytes []byte
	localArchive := strings.TrimSpace(os.Getenv("VB_TEMPLATE_ARCHIVE_FILE"))
	if localArchive != "" {
		var err error
		archiveBytes, err = readSecureRegularFile(localArchive, maxTemplateArchiveBytes)
		if err != nil {
			return fmt.Errorf("failed to read local pinned template archive: %w", err)
		}
	} else {
		resp, err := templateHTTPGet(templateZipURL)
		if err != nil {
			return fmt.Errorf("failed to download template archive: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status %d fetching pinned template %s", resp.StatusCode, templateRelease)
		}

		archiveBytes, err = io.ReadAll(io.LimitReader(resp.Body, maxTemplateArchiveBytes+1))
		if err != nil {
			return fmt.Errorf("failed to read template archive: %w", err)
		}
	}
	if int64(len(archiveBytes)) > maxTemplateArchiveBytes {
		return fmt.Errorf("template archive exceeds %d bytes", maxTemplateArchiveBytes)
	}
	if err := verifyTemplateArchiveChecksum(archiveBytes); err != nil {
		return err
	}

	archive, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return fmt.Errorf("failed to open template archive: %w", err)
	}

	targetRoot := dest
	if !filepath.IsAbs(targetRoot) {
		targetRoot = filepath.Join(workdir, dest)
	}
	targetRoot = filepath.Clean(targetRoot)
	if err := recoverDirectoryReplacement(targetRoot); err != nil {
		return fmt.Errorf("recover interrupted template replacement: %w", err)
	}
	parentDir := filepath.Dir(targetRoot)
	if err := os.MkdirAll(parentDir, 0o750); err != nil {
		return fmt.Errorf("failed to create template parent directory: %w", err)
	}

	stageDir, err := os.MkdirTemp(parentDir, ".vb-template-stage-*")
	if err != nil {
		return fmt.Errorf("failed to create template staging directory: %w", err)
	}
	stageLive := true
	defer func() {
		if stageLive {
			_ = os.RemoveAll(stageDir)
		}
	}()

	if err := extractTemplateArchive(archive, stageDir); err != nil {
		return err
	}
	if err := seedEmptyFeatureIndex(stageDir); err != nil {
		return err
	}
	if err := validateTemplateScaffold(stageDir); err != nil {
		return err
	}
	manifest, err := buildTemplateManifest(stageDir, templateVersion)
	if err != nil {
		return fmt.Errorf("build authenticated template manifest: %w", err)
	}
	if err := saveTemplateProvenance(stageDir, templateVersion, manifest); err != nil {
		return err
	}

	targetExists, err := pathExists(targetRoot)
	if err != nil {
		return fmt.Errorf("failed to inspect template target: %w", err)
	}
	if !targetExists {
		if err := os.Rename(stageDir, targetRoot); err != nil {
			return fmt.Errorf("failed to activate template scaffold: %w", err)
		}
		stageLive = false
		return nil
	}

	if err := refreshManagedTemplateFiles(targetRoot, stageDir); err != nil {
		return err
	}
	return nil
}

func refreshManagedTemplateFiles(targetRoot, stagedRoot string) error {
	currentVersion, err := readTemplateVersion(targetRoot)
	if err != nil {
		return fmt.Errorf("read current template version: %w", err)
	}
	previous, err := readTemplateManifest(targetRoot)
	if err != nil {
		return fmt.Errorf("read current template manifest: %w", err)
	}
	if previous == nil {
		previous, err = legacyRemovalManifest(targetRoot, currentVersion)
		if err != nil {
			return err
		}
	}
	diff, err := templatediff.CompareDirectories(targetRoot, stagedRoot)
	if err != nil {
		return fmt.Errorf("compare managed template files: %w", err)
	}
	diff = filterRemovalsToPreviouslyManaged(diff, previous)
	for _, group := range [][]templatediff.FileDiff{diff.Added, diff.Modified, diff.Removed} {
		for index := range group {
			if err := applyFileDiff(targetRoot, &group[index]); err != nil {
				return fmt.Errorf("refresh managed template file %s: %w", group[index].Path, err)
			}
		}
	}
	remaining, err := templatediff.CompareDirectories(targetRoot, stagedRoot)
	if err != nil {
		return fmt.Errorf("verify managed template refresh: %w", err)
	}
	remaining = filterRemovalsToPreviouslyManaged(remaining, previous)
	if remaining.HasChanges() {
		return fmt.Errorf("managed template refresh is incomplete: %d change(s) remain: %s", remaining.TotalChanges(), strings.Join(collectFilePaths(remaining), ", "))
	}
	manifest, err := ensureTemplateManifest(stagedRoot, templateVersion)
	if err != nil {
		return err
	}
	return saveTemplateProvenance(targetRoot, templateVersion, manifest)
}

func verifyTemplateArchiveChecksum(archiveBytes []byte) error {
	expectedHex := strings.TrimSpace(templateArchiveSHA256)
	if expectedHex == "" {
		return fmt.Errorf("pinned template %s has no compiled SHA-256 digest; release build is incomplete", templateRelease)
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil || len(expected) != sha256.Size {
		return fmt.Errorf("compiled template SHA-256 digest is invalid")
	}
	actual := sha256.Sum256(archiveBytes)
	if !bytes.Equal(actual[:], expected) {
		return fmt.Errorf("template archive checksum mismatch for %s: got %x", templateRelease, actual)
	}
	return nil
}

func extractTemplateArchive(archive *zip.Reader, targetRoot string) error {
	if len(archive.File) > maxTemplateArchiveEntries {
		return fmt.Errorf("template archive exceeds %d entries", maxTemplateArchiveEntries)
	}
	archiveRoot := ""
	var extractedBytes int64
	for _, file := range archive.File {
		name := path.Clean(file.Name)
		if name == "." || path.IsAbs(name) || strings.Contains(name, "\\") || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("archive entry escapes template root: %s", file.Name)
		}
		parts := strings.Split(name, "/")
		if len(parts) > maxTemplateArchiveDepth {
			return fmt.Errorf("template archive entry exceeds depth %d: %s", maxTemplateArchiveDepth, file.Name)
		}
		if archiveRoot == "" {
			archiveRoot = parts[0]
		}
		if parts[0] != archiveRoot {
			return fmt.Errorf("template archive has multiple roots: %q and %q", archiveRoot, parts[0])
		}
		if len(parts) < 2 {
			continue
		}
		relative := path.Join(parts[1:]...)
		if !file.FileInfo().IsDir() && file.Mode()&os.ModeType != 0 {
			return fmt.Errorf("template archive contains unsupported non-regular entry: %s", file.Name)
		}

		if shouldSkipTemplateFile(relative) {
			continue
		}

		clean := filepath.Join(targetRoot, filepath.FromSlash(relative))
		rel, err := filepath.Rel(targetRoot, clean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry escapes target directory: %s", file.Name)
		}

		if file.UncompressedSize64 > uint64(maxTemplateFileBytes) {
			return fmt.Errorf("archive entry too large: %s", file.Name)
		}
		remainingExtractedBytes := maxTemplateExtractedBytes - extractedBytes
		if file.UncompressedSize64 > uint64(remainingExtractedBytes) {
			return fmt.Errorf("template archive expands beyond %d bytes", maxTemplateExtractedBytes)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(clean, 0o750); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", clean, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(clean), 0o750); err != nil {
			return fmt.Errorf("failed to ensure parent directory: %w", err)
		}

		src, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open archive entry %s: %w", file.Name, err)
		}
		if err := func() error {
			defer src.Close()
			mode := archiveFileMode(file.Mode())
			// #nosec G304 -- clean is contained in a private, newly created staging root.
			out, err := os.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", clean, err)
			}
			written, err := io.Copy(out, io.LimitReader(src, maxTemplateFileBytes+1))
			if err != nil {
				_ = out.Close()
				return fmt.Errorf("failed to write file %s: %w", clean, err)
			}
			if written > maxTemplateFileBytes {
				_ = out.Close()
				return fmt.Errorf("archive entry expands beyond %d bytes: %s", maxTemplateFileBytes, file.Name)
			}
			extractedBytes += written
			if extractedBytes > maxTemplateExtractedBytes {
				_ = out.Close()
				return fmt.Errorf("template archive expands beyond %d bytes", maxTemplateExtractedBytes)
			}
			if written < 0 || uint64(written) != file.UncompressedSize64 {
				_ = out.Close()
				return fmt.Errorf("archive entry size mismatch for %s: wrote %d bytes, expected %d", clean, written, file.UncompressedSize64)
			}
			if err := out.Chmod(mode); err != nil {
				_ = out.Close()
				return fmt.Errorf("failed to set mode on file %s: %w", clean, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("failed to close file %s: %w", clean, err)
			}
			return nil
		}(); err != nil {
			return err
		}
	}
	if archiveRoot == "" {
		return errors.New("template archive is empty")
	}
	return nil
}

func archiveFileMode(mode fs.FileMode) fs.FileMode {
	if mode.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func validateTemplateScaffold(root string) error {
	versionBytes, err := os.ReadFile(filepath.Join(root, "version.txt")) // #nosec G304 -- root is an isolated staging directory
	if err != nil {
		return fmt.Errorf("pinned template is missing version.txt: %w", err)
	}
	if got := strings.TrimSpace(string(versionBytes)); got != templateVersion {
		return fmt.Errorf("pinned template version mismatch: archive contains %q, expected %q", got, templateVersion)
	}

	cliVersionBytes, err := os.ReadFile(filepath.Join(root, ".vb-version")) // #nosec G304 -- root is an isolated staging directory
	if err != nil {
		return fmt.Errorf("pinned template is missing .vb-version: %w", err)
	}
	if got := strings.TrimSpace(string(cliVersionBytes)); got != version.Current {
		return fmt.Errorf("pinned template requires CLI %q, release binary is %q", got, version.Current)
	}

	lifecycle, err := contract.Load(root)
	if err != nil {
		return fmt.Errorf("pinned template lifecycle contract is invalid: %w", err)
	}

	required := []struct {
		path       string
		executable bool
	}{
		{path: ".vb-version"},
		{path: "virtualboard.json"},
		{path: filepath.Join("bin", "vb-root"), executable: true},
		{path: filepath.Join("scripts", "install-vb-cli.sh"), executable: true},
		{path: filepath.Join("templates", "feature.md")},
		{path: filepath.Join("schemas", "frontmatter.schema.json")},
		{path: filepath.Join("schemas", "system-spec.schema.json")},
		{path: filepath.Join("docs", ".cursor", "rules", "virtualboard.mdc")},
		{path: filepath.Join("docs", ".opencode", "skill", "virtualboard", "SKILL.md")},
	}
	for _, requiredFile := range required {
		info, statErr := os.Stat(filepath.Join(root, requiredFile.path))
		if statErr != nil {
			return fmt.Errorf("pinned template is incomplete; required file %s is unavailable: %w", filepath.ToSlash(requiredFile.path), statErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("pinned template required path %s is not a regular file", filepath.ToSlash(requiredFile.path))
		}
		if requiredFile.executable && !templateExecutableModeIsValid(info.Mode()) {
			return fmt.Errorf("pinned template required path %s is not executable", filepath.ToSlash(requiredFile.path))
		}
	}
	for _, status := range lifecycle.Statuses() {
		directory, ok := lifecycle.DirectoryForStatus(status)
		if !ok {
			return fmt.Errorf("pinned template lifecycle status %s has no directory", status)
		}
		info, statErr := os.Stat(directory)
		if statErr != nil || !info.IsDir() {
			return fmt.Errorf("pinned template lifecycle directory for %s is unavailable", status)
		}
	}
	for schemaPath, requiredFields := range map[string][]string{
		filepath.Join(root, "schemas", "frontmatter.schema.json"): {
			"id", "title", "status", "owner", "implementation_owner", "priority", "complexity", "created", "updated", "status_changed", "labels", "dependencies", "risk_notes",
		},
		filepath.Join(root, "schemas", "system-spec.schema.json"): {"spec_type", "title", "status", "last_updated", "applicability"},
	} {
		if _, err := gojsonschema.NewSchema(gojsonschema.NewReferenceLoader("file://" + filepath.ToSlash(schemaPath))); err != nil {
			return fmt.Errorf("pinned template schema %s is invalid: %w", filepath.Base(schemaPath), err)
		}
		if err := validateScaffoldSchemaShape(schemaPath, requiredFields); err != nil {
			return fmt.Errorf("pinned template schema %s is incomplete: %w", filepath.Base(schemaPath), err)
		}
	}
	templateData, err := os.ReadFile(filepath.Join(root, "templates", "feature.md")) // #nosec G304 -- root is an isolated staging directory
	if err != nil {
		return err
	}
	if _, err := feature.Parse(filepath.Join(root, "templates", "feature.md"), templateData); err != nil {
		return fmt.Errorf("pinned feature template is invalid: %w", err)
	}
	indexData, err := os.ReadFile(filepath.Join(lifecycle.FeaturesDir(), "INDEX.md")) // #nosec G304 -- resolved by the validated lifecycle contract
	if err != nil || string(indexData) != emptyFeatureIndexContent {
		return fmt.Errorf("pinned template empty feature index is invalid")
	}
	return nil
}

func templateExecutableModeIsValid(mode fs.FileMode) bool {
	return runtime.GOOS == "windows" || mode.Perm()&0o111 != 0
}

func validateScaffoldSchemaShape(schemaPath string, mandatory []string) error {
	data, err := os.ReadFile(schemaPath) // #nosec G304 -- schema path is beneath the isolated staging root
	if err != nil {
		return err
	}
	var schema struct {
		Type                 string                     `json:"type"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		return err
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		return errors.New("must be a closed object schema")
	}
	required := make(map[string]bool, len(schema.Required))
	for _, field := range schema.Required {
		required[field] = true
	}
	for _, field := range mandatory {
		if !required[field] {
			return fmt.Errorf("required field %s is missing", field)
		}
		if _, ok := schema.Properties[field]; !ok {
			return fmt.Errorf("property %s is missing", field)
		}
	}
	return nil
}

const emptyFeatureIndexContent = `# Features Index

> Auto-generated from feature state - Do not edit manually

| ID | Title | Status | Owner | P | C | Labels | Updated | Status Changed | File |
|---|---|---|---|---|---|---|---|---|---|

## Summary


**Total**: 0 features
`

func seedEmptyFeatureIndex(root string) error {
	indexPath := filepath.Join(root, "features", "INDEX.md")
	if err := util.WriteFileAtomic(indexPath, []byte(emptyFeatureIndexContent), 0o644); err != nil {
		return fmt.Errorf("failed to seed empty feature index: %w", err)
	}
	return nil
}

var protectedWorkspacePaths = []string{
	".state",
	"audit.jsonl",
	"archive",
	"features",
	"specs",
	"reports",
	"locks",
}

func preserveWorkspaceData(existingRoot, stagedRoot string) error {
	for _, rel := range protectedWorkspacePaths {
		source := filepath.Join(existingRoot, rel)
		if _, err := os.Lstat(source); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := copyPathPreservingMode(source, filepath.Join(stagedRoot, rel)); err != nil {
			return fmt.Errorf("preserve %s: %w", rel, err)
		}
	}
	return nil
}

func copyPathPreservingMode(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to copy symbolic link %s", source)
	}
	if info.IsDir() {
		if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPathPreservingMode(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return os.Chmod(destination, info.Mode().Perm())
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to copy non-regular path %s", source)
	}
	content, err := os.ReadFile(source) // #nosec G304 -- source is beneath a validated workspace path
	if err != nil {
		return err
	}
	return util.WriteFileAtomic(destination, content, info.Mode().Perm())
}

func replaceDirectoryWithLease(targetRoot, stagedRoot string, lease *directoryReplaceLease) (bool, error) {
	for _, directory := range []string{targetRoot, stagedRoot} {
		exists, err := replacementDirectoryExists(directory)
		if err != nil {
			return true, err
		}
		if !exists {
			return true, fmt.Errorf("replacement directory does not exist: %s", directory)
		}
	}
	backupRoot, err := os.MkdirTemp(filepath.Dir(targetRoot), ".vb-template-backup-*")
	if err != nil {
		return true, fmt.Errorf("failed to reserve template backup path: %w", err)
	}
	if err := os.Remove(backupRoot); err != nil {
		return true, fmt.Errorf("failed to prepare template backup path: %w", err)
	}
	journalPath := directoryReplaceJournalPath(targetRoot)
	journal := directoryReplaceJournal{
		Version: directoryReplaceJournalVersion, Target: filepath.Base(targetRoot),
		Stage: filepath.Base(stagedRoot), Backup: filepath.Base(backupRoot),
		Nonce: lease.record.Nonce, PID: lease.record.PID, Host: lease.record.Host, StartedAt: time.Now().UTC(),
	}
	if err := writeDirectoryReplaceJournal(journalPath, journal); err != nil {
		return true, fmt.Errorf("create template replacement journal: %w", err)
	}
	if err := renameTemplateDirectory(targetRoot, backupRoot); err != nil {
		if cleanupErr := removeDirectoryReplaceJournal(journalPath, lease.record.Nonce); cleanupErr != nil {
			return false, fmt.Errorf("failed to back up existing workspace: %v; failed to clean replacement journal: %w", err, cleanupErr)
		}
		return true, fmt.Errorf("failed to back up existing workspace: %w", err)
	}
	if err := renameTemplateDirectory(stagedRoot, targetRoot); err != nil {
		if restoreErr := renameTemplateDirectory(backupRoot, targetRoot); restoreErr != nil {
			return false, fmt.Errorf("failed to activate template scaffold: %v; failed to restore workspace: %w", err, restoreErr)
		}
		if cleanupErr := removeDirectoryReplaceJournal(journalPath, lease.record.Nonce); cleanupErr != nil {
			return false, fmt.Errorf("failed to activate template scaffold: %v; failed to clean replacement journal: %w", err, cleanupErr)
		}
		return true, fmt.Errorf("failed to activate template scaffold: %w", err)
	}
	if err := os.RemoveAll(backupRoot); err != nil {
		return false, fmt.Errorf("template refreshed but failed to remove backup %s: %w", backupRoot, err)
	}
	if err := removeDirectoryReplaceJournal(journalPath, lease.record.Nonce); err != nil {
		return false, fmt.Errorf("template refreshed but failed to remove replacement journal: %w", err)
	}
	return true, nil
}

var fetchTemplateToTempDir fetchTemplateToDirFunc = func() (string, error) {
	tempDir, err := os.MkdirTemp("", "vb-template-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	// fetchTemplate activates into an absent destination. Remove the reserved
	// empty path first; otherwise it is mistaken for an installed workspace and
	// the refresh path requires provenance that cannot exist yet.
	if err := os.Remove(tempDir); err != nil {
		return "", fmt.Errorf("failed to prepare temporary template destination: %w", err)
	}

	if err := fetchTemplate("", tempDir); err != nil {
		// Clean up on failure
		_ = os.RemoveAll(tempDir)
		return "", err
	}

	return tempDir, nil
}

var fetchTemplateVersionVar fetchTemplateVersionFunc = func() (string, error) { return templateVersion, nil }

func saveTemplateVersion(targetPath, version string) error {
	versionPath := filepath.Join(targetPath, templateVersionFile)
	return util.WriteFileAtomic(versionPath, []byte(version+"\n"), 0o644)
}

func readTemplateVersion(targetPath string) (string, error) {
	versionPath := filepath.Join(targetPath, templateVersionFile)
	data, err := readSecureRegularFile(versionPath, 128)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New("template version file is empty")
	}
	if _, err := version.Parse(value); err != nil {
		return "", fmt.Errorf("invalid template version %q: %w", value, err)
	}
	return value, nil
}

func handleUpdate(cmd *cobra.Command, opts *config.Options, targetPath string, fileFilter []string, autoYes bool) error {
	// Check if workspace exists
	exists, err := pathExists(targetPath)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, err)
	}
	if !exists {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf(".virtualboard workspace does not exist. Run 'vb init' first"))
	}
	verifiedRoot, err := openVerifiedTemplateWorkspaceRoot(targetPath)
	if err != nil {
		return WrapCLIError(ExitCodeValidation, err)
	}
	if err := verifiedRoot.Close(); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("close template workspace root: %w", err))
	}

	// Get current version
	currentVersion, err := readTemplateVersion(targetPath)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("read current template version: %w", err))
	}
	previousManifest, err := readTemplateManifest(targetPath)
	if err != nil {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("read prior template manifest: %w", err))
	}
	if previousManifest == nil {
		previousManifest, err = legacyRemovalManifest(targetPath, currentVersion)
		if err != nil {
			return WrapCLIError(ExitCodeValidation, err)
		}
	}

	// Fetch the exact pinned template to a temporary directory.
	if !opts.JSONOutput {
		fmt.Fprintf(os.Stderr, "Fetching pinned template %s...\n", templateRelease)
	}

	tempDir, err := fetchTemplateToTempDir()
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to fetch template: %w", err))
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory

	// Get new version
	newVersion, err := fetchTemplateVersionVar()
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("resolve pinned template version: %w", err))
	}
	newManifest, err := ensureTemplateManifest(tempDir, newVersion)
	if err != nil {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("verify pinned template manifest: %w", err))
	}

	// Compare directories
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "Comparing with local .virtualboard/...")
	}

	fullDiff, err := templatediff.CompareDirectories(targetPath, tempDir)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to compare templates: %w", err))
	}
	fullDiff = filterRemovalsToPreviouslyManaged(fullDiff, previousManifest)
	diff := fullDiff

	// Filter files if specified
	if len(fileFilter) > 0 {
		diff = filterDiff(diff, fileFilter)
	}

	// Check if there are changes
	if !diff.HasChanges() {
		complete := !fullDiff.HasChanges()
		if complete && !opts.DryRun {
			if err := saveTemplateProvenance(targetPath, newVersion, newManifest); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
		}
		msg := "Template is already up to date"
		if !complete {
			msg = fmt.Sprintf("No selected files require changes; %d managed change(s) remain", fullDiff.TotalChanges())
		}
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"current_version": currentVersion,
			"latest_version":  newVersion,
			"changes":         0,
			"remaining":       fullDiff.TotalChanges(),
			"complete":        complete,
			"dry_run":         opts.DryRun,
		})
	}

	// Display summary
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "")
		displayUpdateSummary(diff, currentVersion, newVersion)
	}

	// In dry-run mode, just show what would change
	if opts.DryRun {
		return respond(cmd, opts, true, "Dry run complete - no changes applied", map[string]interface{}{
			"current_version": currentVersion,
			"latest_version":  newVersion,
			"added":           len(diff.Added),
			"modified":        len(diff.Modified),
			"removed":         len(diff.Removed),
			"files":           collectFilePaths(diff),
		})
	}
	if opts.JSONOutput && !autoYes {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("--yes is required to apply template changes with --json; use --dry-run to inspect first"))
	}

	// Ask user if they want to proceed (unless --yes flag is set or JSON mode)
	if !opts.JSONOutput && !autoYes {
		fmt.Fprintln(os.Stderr, "")
		choice, err := util.PromptUserForUpdate("Apply all changes?")
		if err != nil {
			return WrapCLIError(ExitCodeUnknown, fmt.Errorf("failed to read user input: %w", err))
		}

		switch choice {
		case util.UpdateChoiceApplyAll:
			autoYes = true // Apply all without further prompts
		case util.UpdateChoiceReviewFiles:
			// Continue to file-by-file review (autoYes stays false)
		case util.UpdateChoiceQuit:
			return respond(cmd, opts, true, "Update cancelled by user", map[string]interface{}{
				"cancelled": true,
			})
		}
	}

	// Interactive update process
	applied, err := applyUpdates(opts, targetPath, diff, autoYes)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to apply updates: %w", err))
	}

	remaining, err := templatediff.CompareDirectories(targetPath, tempDir)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("verify applied template update: %w", err))
	}
	remaining = filterRemovalsToPreviouslyManaged(remaining, previousManifest)
	complete := !remaining.HasChanges()
	activeVersion := currentVersion
	if complete {
		if err := saveTemplateProvenance(targetPath, newVersion, newManifest); err != nil {
			return WrapCLIError(ExitCodeFilesystem, err)
		}
		activeVersion = newVersion
	}

	msg := fmt.Sprintf("Template update applied %d change(s)", applied)
	if !complete {
		msg += fmt.Sprintf("; %d managed change(s) remain and the active template version was not advanced", remaining.TotalChanges())
	}
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"current_version": currentVersion,
		"target_version":  newVersion,
		"active_version":  activeVersion,
		"applied":         applied,
		"total_changes":   diff.TotalChanges(),
		"remaining":       remaining.TotalChanges(),
		"complete":        complete,
	})
}

func filterDiff(diff *templatediff.TemplateDiff, fileFilter []string) *templatediff.TemplateDiff {
	filterMap := make(map[string]bool)
	for _, f := range fileFilter {
		filterMap[f] = true
	}

	filtered := &templatediff.TemplateDiff{
		Added:     []templatediff.FileDiff{},
		Modified:  []templatediff.FileDiff{},
		Removed:   []templatediff.FileDiff{},
		Unchanged: []templatediff.FileDiff{},
	}

	for _, fd := range diff.Added {
		if filterMap[fd.Path] {
			filtered.Added = append(filtered.Added, fd)
		}
	}
	for _, fd := range diff.Modified {
		if filterMap[fd.Path] {
			filtered.Modified = append(filtered.Modified, fd)
		}
	}
	for _, fd := range diff.Removed {
		if filterMap[fd.Path] {
			filtered.Removed = append(filtered.Removed, fd)
		}
	}

	return filtered
}

func displayUpdateSummary(diff *templatediff.TemplateDiff, currentVersion, newVersion string) {
	if currentVersion != "" && newVersion != "" {
		fmt.Fprintf(os.Stderr, "Upgrading template: %s → %s\n\n", currentVersion, newVersion)
	}

	// Calculate line statistics
	addedLinesTotal := 0
	removedLinesTotal := 0
	addedLinesFromNew := 0
	addedLinesFromMod := 0
	removedLinesFromMod := 0
	removedLinesFromRem := 0

	for _, fd := range diff.Added {
		lines := countLines(fd.RemoteContent)
		addedLinesFromNew += lines
		addedLinesTotal += lines
	}

	for _, fd := range diff.Modified {
		add, rem := countDiffLines(fd.UnifiedDiff)
		addedLinesFromMod += add
		removedLinesFromMod += rem
		addedLinesTotal += add
		removedLinesTotal += rem
	}

	for _, fd := range diff.Removed {
		lines := countLines(fd.LocalContent)
		removedLinesFromRem += lines
		removedLinesTotal += lines
	}

	fmt.Fprintln(os.Stderr, "Changes detected:")
	if len(diff.Added) > 0 {
		fmt.Fprintf(os.Stderr, "  %d file(s) added (+%d lines)\n", len(diff.Added), addedLinesFromNew)
	}
	if len(diff.Modified) > 0 {
		fmt.Fprintf(os.Stderr, "  %d file(s) modified (+%d, -%d lines)\n", len(diff.Modified), addedLinesFromMod, removedLinesFromMod)
	}
	if len(diff.Removed) > 0 {
		fmt.Fprintf(os.Stderr, "  %d file(s) removed (-%d lines)\n", len(diff.Removed), removedLinesFromRem)
	}
	fmt.Fprintln(os.Stderr, "")

	// List new files with line counts
	if len(diff.Added) > 0 {
		fmt.Fprintln(os.Stderr, "New files:")
		for i, fd := range diff.Added {
			lines := countLines(fd.RemoteContent)
			fmt.Fprintf(os.Stderr, "  %d. %s (+%d lines)\n", i+1, fd.Path, lines)
		}
		fmt.Fprintln(os.Stderr, "")
	}

	// List modified files with line counts
	if len(diff.Modified) > 0 {
		fmt.Fprintln(os.Stderr, "Modified files:")
		for i, fd := range diff.Modified {
			add, rem := countDiffLines(fd.UnifiedDiff)
			fmt.Fprintf(os.Stderr, "  %d. %s (+%d, -%d lines)\n", i+1, fd.Path, add, rem)
		}
		fmt.Fprintln(os.Stderr, "")
	}

	// List removed files with line counts
	if len(diff.Removed) > 0 {
		fmt.Fprintln(os.Stderr, "Removed files:")
		for i, fd := range diff.Removed {
			lines := countLines(fd.LocalContent)
			fmt.Fprintf(os.Stderr, "  %d. %s (-%d lines)\n", i+1, fd.Path, lines)
		}
		fmt.Fprintln(os.Stderr, "")
	}
}

// countLines counts the number of lines in content
func countLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	return len(strings.Split(string(content), "\n"))
}

// countDiffLines counts added and removed lines in a unified diff
// Returns (added, removed) line counts
func countDiffLines(diff string) (int, int) {
	added := 0
	removed := 0

	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}

		// Count actual diff lines, not headers
		switch line[0] {
		case '+':
			if !strings.HasPrefix(line, "+++") {
				added++
			}
		case '-':
			if !strings.HasPrefix(line, "---") {
				removed++
			}
		}
	}

	return added, removed
}

func applyUpdates(opts *config.Options, targetPath string, diff *templatediff.TemplateDiff, autoYes bool) (int, error) {
	applied := 0
	applyAll := autoYes // If --yes flag is set, apply all changes automatically

	// Process new files first
	for _, fd := range diff.Added {
		if opts.JSONOutput && applyAll {
			// In JSON mode, apply all changes automatically
			if err := applyFileDiff(targetPath, &fd); err != nil {
				return applied, err
			}
			applied++
			continue
		}

		// Clear screen before showing each file
		util.ClearScreen()

		fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))
		lines := strings.Split(string(fd.RemoteContent), "\n")
		totalLines := len(lines)
		fmt.Fprintf(os.Stderr, "NEW FILE: %s (%d lines)\n", fd.Path, totalLines)
		fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))

		// Show first 20 lines as preview
		previewLines := 20
		if totalLines <= previewLines {
			fmt.Fprintln(os.Stderr, string(fd.RemoteContent))
		} else {
			preview := strings.Join(lines[:previewLines], "\n")
			fmt.Fprintln(os.Stderr, preview)
			fmt.Fprintf(os.Stderr, "\n... (%d more lines, press 'd' to view full details)\n", totalLines-previewLines)
		}
		fmt.Fprintln(os.Stderr, "")

		if !applyAll {
			// Use enhanced prompting for interactive mode
			fullPath := filepath.Join(targetPath, fd.Path)
			choice, err := util.PromptUserEnhanced("Add this file?", string(fd.RemoteContent), fullPath)
			if err != nil {
				return applied, fmt.Errorf("failed to read user input: %w", err)
			}

			switch choice {
			case util.PromptChoiceYes:
				// Apply this one
			case util.PromptChoiceNo:
				continue
			case util.PromptChoiceAll:
				applyAll = true
			case util.PromptChoiceQuit:
				return applied, nil
			case util.PromptChoiceEdit:
				// User edited the file, re-prompt for this file
				fmt.Fprintln(os.Stderr, "File created for editing. You can now apply the change.")
				// Create the file first so they can edit it
				if err := applyFileDiff(targetPath, &fd); err != nil {
					return applied, err
				}
				applied++
				continue
			default:
				fmt.Fprintln(os.Stderr, "Invalid choice, skipping...")
				continue
			}
		}

		if err := applyFileDiff(targetPath, &fd); err != nil {
			return applied, err
		}
		applied++
	}

	// Process modified files
	for _, fd := range diff.Modified {
		if opts.JSONOutput && applyAll {
			// In JSON mode, apply all changes automatically
			if err := applyFileDiff(targetPath, &fd); err != nil {
				return applied, err
			}
			applied++
			continue
		}

		// Clear screen before showing each file
		util.ClearScreen()

		fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))
		fmt.Fprintf(os.Stderr, "MODIFIED FILE: %s\n", fd.Path)
		fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))

		// Colorize diff output for better readability
		var colorizedDiff string
		if fd.UnifiedDiff == "" {
			fmt.Fprintln(os.Stderr, "Warning: No diff available for this file")
			colorizedDiff = ""
		} else {
			colorizedDiff = util.ColorizeDiff(fd.UnifiedDiff)

			// Show preview of diff if it's too long
			lines := strings.Split(colorizedDiff, "\n")
			totalLines := len(lines)
			previewLines := 30

			if totalLines <= previewLines {
				fmt.Fprintln(os.Stderr, colorizedDiff)
			} else {
				preview := strings.Join(lines[:previewLines], "\n")
				fmt.Fprintln(os.Stderr, preview)
				fmt.Fprintf(os.Stderr, "\n... (%d more lines, press 'd' to view full diff)\n", totalLines-previewLines)
			}
		}
		fmt.Fprintln(os.Stderr, "")

		if !applyAll {
			// Use enhanced prompting for interactive mode
			fullPath := filepath.Join(targetPath, fd.Path)
			choice, err := util.PromptUserEnhanced("Apply this change?", colorizedDiff, fullPath)
			if err != nil {
				return applied, fmt.Errorf("failed to read user input: %w", err)
			}

			switch choice {
			case util.PromptChoiceYes:
				// Apply this one
			case util.PromptChoiceNo:
				continue
			case util.PromptChoiceAll:
				applyAll = true
			case util.PromptChoiceQuit:
				return applied, nil
			case util.PromptChoiceEdit:
				// User manually edited the file, ask if they want to skip applying the remote version
				fmt.Fprintln(os.Stderr, "File edited. Skipping automatic application of remote changes.")
				continue
			default:
				fmt.Fprintln(os.Stderr, "Invalid choice, skipping...")
				continue
			}
		}

		if err := applyFileDiff(targetPath, &fd); err != nil {
			return applied, err
		}
		applied++
	}

	// Process removed files
	for _, fd := range diff.Removed {
		if opts.JSONOutput && applyAll {
			// In JSON mode, apply all changes automatically
			if err := applyFileDiff(targetPath, &fd); err != nil {
				return applied, err
			}
			applied++
			continue
		}

		// Clear screen before showing each file
		util.ClearScreen()

		fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))
		fmt.Fprintf(os.Stderr, "REMOVED FILE: %s\n", fd.Path)
		fmt.Fprintln(os.Stderr, strings.Repeat("=", 60))
		fmt.Fprintln(os.Stderr, "")

		if !applyAll {
			// Use enhanced prompting for interactive mode
			fullPath := filepath.Join(targetPath, fd.Path)
			choice, err := util.PromptUserEnhanced("Remove this file?", string(fd.LocalContent), fullPath)
			if err != nil {
				return applied, fmt.Errorf("failed to read user input: %w", err)
			}

			switch choice {
			case util.PromptChoiceYes:
				// Apply this one
			case util.PromptChoiceNo:
				continue
			case util.PromptChoiceAll:
				applyAll = true
			case util.PromptChoiceQuit:
				return applied, nil
			case util.PromptChoiceEdit:
				// For removed files, editing doesn't make sense. Just skip the removal.
				fmt.Fprintln(os.Stderr, "Skipping file removal. File will be kept.")
				continue
			default:
				fmt.Fprintln(os.Stderr, "Invalid choice, skipping...")
				continue
			}
		}

		if err := applyFileDiff(targetPath, &fd); err != nil {
			return applied, err
		}
		applied++
	}

	return applied, nil
}

func applyFileDiff(targetPath string, fd *templatediff.FileDiff) error {
	fullPath, _, err := resolveWorkspaceWritePath(targetPath, fd.Path)
	if err != nil {
		return err
	}

	switch fd.Status {
	case templatediff.FileStatusAdded:
		mode, err := validatedTemplateDiffMode(fd.RemoteMode)
		if err != nil {
			return fmt.Errorf("invalid target mode for %s: %w", fd.Path, err)
		}
		if err := util.WriteFileExclusiveAtomicWithin(targetPath, fullPath, fd.RemoteContent, mode); err != nil {
			if _, statErr := os.Lstat(fullPath); statErr == nil {
				return fmt.Errorf("template update conflict: %s was created after comparison", fd.Path)
			}
			return err
		}
		return nil

	case templatediff.FileStatusModified:
		mode, err := validatedTemplateDiffMode(fd.RemoteMode)
		if err != nil {
			return fmt.Errorf("invalid target mode for %s: %w", fd.Path, err)
		}
		backupDir, _, err := moveTemplateSourceForCAS(targetPath, fd, "template-replaced")
		if err != nil {
			return err
		}
		if err := util.WriteFileExclusiveAtomicWithin(targetPath, fullPath, fd.RemoteContent, mode); err != nil {
			return fmt.Errorf("template update conflict: could not publish %s without replacement; previous bytes retained at %s: %w", fd.Path, backupDir, err)
		}
		return nil

	case templatediff.FileStatusRemoved:
		backupDir, _, err := moveTemplateSourceForCAS(targetPath, fd, "template-retired")
		if err != nil {
			return err
		}
		if _, err := os.Lstat(fullPath); err == nil {
			return fmt.Errorf("template update conflict: %s was recreated during removal; previous bytes retained at %s", fd.Path, backupDir)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("verify removed template path %s: %w", fd.Path, err)
		}
		return nil

	default:
		return fmt.Errorf("unknown file status: %s", fd.Status)
	}
}

func moveTemplateSourceForCAS(targetPath string, fd *templatediff.FileDiff, category string) (string, string, error) {
	root, err := openVerifiedTemplateWorkspaceRoot(targetPath)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = root.Close() }()

	backupRootRelative := filepath.Join(".state", category)
	if err := root.MkdirAll(backupRootRelative, 0o750); err != nil {
		return "", "", fmt.Errorf("create template recovery root for %s: %w", fd.Path, err)
	}
	var backupDirRelative string
	for range 100 {
		nonce, nonceErr := randomDirectoryReplaceNonce()
		if nonceErr != nil {
			return "", "", nonceErr
		}
		candidate := filepath.Join(backupRootRelative, "change-"+nonce)
		if mkdirErr := root.Mkdir(candidate, 0o750); errors.Is(mkdirErr, fs.ErrExist) {
			continue
		} else if mkdirErr != nil {
			return "", "", fmt.Errorf("reserve template recovery directory for %s: %w", fd.Path, mkdirErr)
		}
		backupDirRelative = candidate
		break
	}
	if backupDirRelative == "" {
		return "", "", fmt.Errorf("reserve template recovery directory for %s: exhausted unique names", fd.Path)
	}
	sourceRelative := filepath.FromSlash(fd.Path)
	heldRelative := filepath.Join(backupDirRelative, sourceRelative)
	if err := root.MkdirAll(filepath.Dir(heldRelative), 0o750); err != nil {
		_ = root.RemoveAll(backupDirRelative)
		return "", "", fmt.Errorf("create template recovery path for %s: %w", fd.Path, err)
	}
	if err := root.Rename(sourceRelative, heldRelative); err != nil {
		_ = root.RemoveAll(backupDirRelative)
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", fmt.Errorf("template update conflict: %s was removed after comparison", fd.Path)
		}
		return "", "", fmt.Errorf("move %s into template recovery storage: %w", fd.Path, err)
	}
	backupDir := filepath.Join(targetPath, backupDirRelative)
	heldPath := filepath.Join(targetPath, heldRelative)
	if afterTemplateSourceMoved != nil {
		afterTemplateSourceMoved(heldPath)
	}
	content, mode, err := readSecureRegularRootFileState(root, heldRelative, maxTemplateFileBytes)
	if err == nil && bytes.Equal(content, fd.LocalContent) && installedTemplateFileMode(fd.Path, mode).Perm() == fd.LocalMode.Perm() {
		return backupDir, heldPath, nil
	}
	restoreErr := root.Link(heldRelative, sourceRelative)
	if restoreErr == nil {
		if removeErr := root.Remove(heldRelative); removeErr == nil {
			_ = root.RemoveAll(backupDirRelative)
		}
	}
	if err != nil {
		return "", "", fmt.Errorf("template update conflict: inspect moved source %s: %v; recovery copy retained at %s", fd.Path, err, heldPath)
	}
	if restoreErr != nil {
		return "", "", fmt.Errorf("template update conflict: %s changed after comparison and could not be restored without replacement; recovery copy retained at %s: %v", fd.Path, heldPath, restoreErr)
	}
	return "", "", fmt.Errorf("template update conflict: %s bytes or mode changed after comparison", fd.Path)
}

func openVerifiedTemplateWorkspaceRoot(path string) (*os.Root, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect template workspace root: %w", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("template workspace root is not a real directory: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open template workspace root: %w", err)
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("template workspace root changed while opening: %s", path)
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(before, current) {
		_ = root.Close()
		return nil, fmt.Errorf("template workspace root changed while opening: %s", path)
	}
	return root, nil
}

func verifyFileDiffPrecondition(fullPath string, fd *templatediff.FileDiff) error {
	if fd.Status == templatediff.FileStatusAdded {
		if _, err := os.Lstat(fullPath); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("inspect update target %s: %w", fd.Path, err)
		}
		return fmt.Errorf("template update conflict: %s was created after comparison", fd.Path)
	}
	if fd.LocalMode.Perm() == 0 {
		return fmt.Errorf("template update for %s is missing its source mode precondition", fd.Path)
	}
	content, mode, err := readSecureRegularFileState(fullPath, maxTemplateFileBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("template update conflict: %s was removed after comparison", fd.Path)
		}
		return fmt.Errorf("verify update precondition for %s: %w", fd.Path, err)
	}
	if !bytes.Equal(content, fd.LocalContent) || installedTemplateFileMode(fd.Path, mode).Perm() != fd.LocalMode.Perm() {
		return fmt.Errorf("template update conflict: %s bytes or mode changed after comparison", fd.Path)
	}
	return nil
}

func validatedTemplateDiffMode(mode fs.FileMode) (fs.FileMode, error) {
	switch mode.Perm() {
	case 0o644:
		return 0o644, nil
	case 0o755:
		return 0o755, nil
	default:
		return 0, fmt.Errorf("got %04o, want 0644 or 0755", mode.Perm())
	}
}

func scaffoldFileMode(relPath string, existing fs.FileMode) fs.FileMode {
	if existing.Perm()&0o111 != 0 {
		return 0o755
	}
	normalized := filepath.ToSlash(relPath)
	if strings.HasPrefix(normalized, "bin/") ||
		(strings.HasPrefix(normalized, "scripts/") && strings.HasSuffix(normalized, ".sh")) {
		return 0o755
	}
	return 0o644
}

func collectFilePaths(diff *templatediff.TemplateDiff) []string {
	var paths []string
	for _, fd := range diff.Added {
		paths = append(paths, fd.Path)
	}
	for _, fd := range diff.Modified {
		paths = append(paths, fd.Path)
	}
	for _, fd := range diff.Removed {
		paths = append(paths, fd.Path)
	}
	return paths
}

func newInitCommand() *cobra.Command {
	var force bool
	var update bool
	var files []string
	var yes bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise a VirtualBoard workspace in the current directory",
		Long: `Initialise a VirtualBoard workspace in the current directory.

By default, creates a new .virtualboard/ directory from the exact pinned template release.
Use --update to compare an existing workspace with that pinned template version.
Use --files to update only specific files when using --update.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			projectRoot := opts.RootDir
			if filepath.Base(projectRoot) == initDirName {
				projectRoot = filepath.Dir(projectRoot)
			}

			targetPath := filepath.Join(projectRoot, initDirName)

			// Handle --update flag
			if update {
				return handleUpdate(cmd, opts, targetPath, files, yes)
			}

			// Original init logic
			exists, err := pathExists(targetPath)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if exists && !force {
				detail := fmt.Sprintf("VirtualBoard workspace already initialised at %s. Use --force to refresh framework files safely or --update to review changes. We recommend managing this directory with git.", initDirName)
				if opts.JSONOutput {
					if respErr := respond(cmd, opts, false, detail, map[string]interface{}{
						"path":          initDirName,
						"force_hint":    true,
						"update_hint":   true,
						"recommend_git": true,
					}); respErr != nil {
						return respErr
					}
					return WrapCLIError(ExitCodeValidation, fmt.Errorf("virtualboard workspace already initialised"))
				}
				return WrapCLIError(ExitCodeValidation, errors.New(detail))
			}
			if opts.DryRun {
				verifiedTemplate, verifyErr := fetchTemplateToTempDir()
				if verifyErr != nil {
					return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to verify pinned template: %w", verifyErr))
				}
				defer os.RemoveAll(verifiedTemplate)
				version, versionErr := fetchTemplateVersionVar()
				if versionErr != nil {
					return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to resolve pinned template version: %w", versionErr))
				}
				action := "initialise"
				if exists {
					action = "refresh"
				}
				return respond(cmd, opts, true, fmt.Sprintf("Dry-run: would %s %s from verified template %s", action, initDirName, templateRelease), map[string]interface{}{
					"path":    initDirName,
					"source":  templateZipURL,
					"version": version,
					"dry_run": true,
					"written": false,
				})
			}

			if err := fetchTemplate(projectRoot, initDirName); err != nil {
				return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to prepare template: %w", err))
			}

			// Persist authenticated provenance. Real fetches already stage these
			// files before activation; this verification also fails closed for
			// alternate fetch implementations.
			version, err := fetchTemplateVersionVar()
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("resolve pinned template version: %w", err))
			}
			manifest, err := ensureTemplateManifest(targetPath, version)
			if err != nil {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("verify installed template manifest: %w", err))
			}
			if err := saveTemplateProvenance(targetPath, version, manifest); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			msg := fmt.Sprintf("VirtualBoard project initialised in %s. Review the files under %s.", initDirName, initDirName)
			return respond(cmd, opts, true, msg, map[string]interface{}{
				"path":    initDirName,
				"source":  templateZipURL,
				"version": version,
			})
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Refresh framework files while preserving feature, archive, spec, report, and runtime data")
	cmd.Flags().BoolVar(&update, "update", false, "Update existing workspace to the exact pinned template version")
	cmd.Flags().StringSliceVar(&files, "files", nil, "Specific files to update (only valid with --update)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Automatically apply all changes without prompting (only valid with --update)")
	cmd.SilenceUsage = true
	return cmd
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// shouldSkipTemplateFile excludes repository/runtime state and template feature
// history. Dotfiles, documentation, and integration sources are scaffold inputs.
var scaffoldTopLevel = map[string]struct{}{
	".claude-plugin": {}, ".github": {}, ".gitignore": {}, ".pre-commit-config.yaml": {},
	".pymarkdown.json": {}, ".typos.toml": {}, ".vb-version": {},
	"AGENTS.md": {}, "CHANGELOG.md": {}, "CLAUDE.md": {}, "CODE_OF_CONDUCT.md": {},
	"CONTRIBUTING.md": {}, "LICENSE": {}, "README.md": {}, "SECURITY.md": {},
	"agents": {}, "bin": {}, "docs": {}, "examples": {}, "features": {}, "plugins": {},
	"prompts": {}, "reports": {}, "schemas": {}, "scripts": {}, "skills": {}, "specs": {},
	"templates": {}, "tests": {}, "tools": {}, "version.txt": {}, "virtualboard.json": {},
}

func shouldSkipTemplateFile(relPath string) bool {
	normalized := filepath.ToSlash(relPath)
	normalized = strings.TrimPrefix(normalized, "./")
	topLevel := strings.SplitN(normalized, "/", 2)[0]
	if _, allowed := scaffoldTopLevel[topLevel]; !allowed {
		return true
	}
	if normalized == ".git" || strings.HasPrefix(normalized, ".git/") ||
		normalized == ".state" || strings.HasPrefix(normalized, ".state/") {
		return true
	}
	if normalized == "features/INDEX.md" {
		return true
	}
	parts := strings.Split(normalized, "/")
	if len(parts) >= 3 && parts[0] == "features" && strings.HasSuffix(parts[len(parts)-1], ".md") {
		switch parts[1] {
		case "backlog", "in-progress", "blocked", "review", "done":
			return true
		}
	}
	return false
}
