package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/util"
)

// Supported IDE targets for installation
const (
	ideClaudeCode = "claude"
	ideCursor     = "cursor"
	ideOpenCode   = "opencode"
)

const (
	cursorRuleFile          = "virtualboard.mdc"
	cursorIntegrationSource = "docs/.cursor/rules/virtualboard.mdc"
	openCodeIntegrationRoot = "docs/.opencode"
	openCodeRequiredSkill   = "skill/virtualboard/SKILL.md"
	claudeMarketplaceSource = "virtualboard/template-base#" + templateRelease
	claudePluginIdentifier  = "virtualboard@virtualboard-marketplace"
	safeIntegrationFileMode = os.FileMode(0o644)
	safeIntegrationDirMode  = os.FileMode(0o755)
	maxIntegrationFiles     = 128
	maxIntegrationFileBytes = 2 * 1024 * 1024
	maxIntegrationTotalSize = 16 * 1024 * 1024
	// Existing OpenCode trees are preserved during an install. Bound that
	// preservation snapshot so a repository-controlled tree cannot force an
	// unbounded read before the user can review a replacement.
	maxIntegrationDestinationEntries   = 10_000
	maxIntegrationDestinationFileBytes = 20 * 1024 * 1024
	maxIntegrationDestinationTotalSize = 100 * 1024 * 1024
)

type integrationManifestEntry struct {
	Path   string
	SHA256 string
}

type authorizedIntegrationFile struct {
	Path    string
	Content []byte
}

type integrationDestinationEntryState struct {
	Directory bool
	Mode      os.FileMode
	Size      int64
	SHA256    [sha256.Size]byte
}

type integrationDestinationSnapshot struct {
	Exists  bool
	Entries map[string]integrationDestinationEntryState
}

type cursorDestinationSnapshot struct {
	Exists  bool
	Mode    os.FileMode
	Content []byte
}

// openCodeIntegrationManifest is an authenticated inventory compiled into the
// version-pinned CLI. It authorizes the exact OpenCode payload shipped by the
// coordinated template release. Tests replace it with fixture-specific entries.
var openCodeIntegrationManifest = []integrationManifestEntry{
	{
		Path:   openCodeRequiredSkill,
		SHA256: "dce51b2e3e221d778184d055f549acbf0f595971e18961e6cc430ba9575e0001",
	},
}

// cursorIntegrationSHA256 authenticates the single fixed Cursor payload from
// the coordinated template release. Tests replace it with fixture content.
var cursorIntegrationSHA256 = "d047218c7da962b57bf2f827f6ccf654edb2c01aefa9b1bb52bb09de80a30285"

// Function variables for testability
var (
	execLookPath                     = exec.LookPath
	execCommand                      = execCommandFunc
	confirmReplace                   = confirmReplaceFunc
	promptYesNo                      = util.PromptYesNo
	afterCursorConsent               func()
	afterCursorDestinationCaptured   func()
	afterOpenCodeConsent             func()
	afterOpenCodeDestinationCaptured func()
	beforeOpenCodeExclusivePublish   func()
)

func execCommandFunc(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- command args are from validated internal sources
	return cmd.CombinedOutput()
}

func confirmReplaceFunc(opts *config.Options, prompt string) (bool, error) {
	if opts.JSONOutput {
		return false, nil
	}
	return promptYesNo(prompt)
}

func newInstallCommand() *cobra.Command {
	var forceFlag bool

	cmd := &cobra.Command{
		Use:   "install <ide>",
		Short: "Install VirtualBoard integration for an IDE",
		Long: `Install VirtualBoard integration for a supported IDE.

Supported IDEs:
  claude    - Claude Code (installs from the exact pinned template tag)
  cursor    - Cursor IDE (verifies and copies the authenticated Cursor rule)
  opencode  - OpenCode (installs only the compiled path/SHA-256 inventory)

Examples:
  vb install claude     # Install Claude Code plugin
  vb install cursor     # Install Cursor rules
  vb install opencode   # Install OpenCode skill`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			ide := strings.ToLower(args[0])
			switch ide {
			case ideClaudeCode:
				return installClaudeCode(cmd, opts)
			case ideCursor:
				return installCursor(cmd, opts, forceFlag)
			case ideOpenCode:
				return installOpenCode(cmd, opts, forceFlag)
			default:
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("unsupported IDE: %s. Supported: claude, cursor, opencode", ide))
			}
		},
	}

	cmd.Flags().BoolVar(&forceFlag, "force", false, "Replace existing files without confirmation")
	cmd.SilenceUsage = true
	return cmd
}

// installClaudeCode installs the VirtualBoard plugin for Claude Code
func installClaudeCode(cmd *cobra.Command, opts *config.Options) error {
	log := opts.Logger().WithField("ide", "claude")

	// Check if claude binary is installed
	claudePath, err := execLookPath("claude")
	if err != nil {
		log.WithError(err).Debug("Claude CLI not found")
		return WrapCLIError(ExitCodeNotFound, fmt.Errorf("Claude Code CLI ('claude') not found in PATH. Please install Claude Code first"))
	}
	log.WithField("path", claudePath).Debug("Found Claude CLI")

	marketplaceCommand := fmt.Sprintf("claude plugin marketplace add %s", claudeMarketplaceSource)
	pluginCommand := fmt.Sprintf("claude plugin install %s", claudePluginIdentifier)
	if opts.DryRun {
		msg := "Dry-run: would install VirtualBoard plugin for Claude Code"
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"ide":               "claude",
			"framework_release": templateRelease,
			"commands":          []string{marketplaceCommand, pluginCommand},
		})
	}

	// Add plugin from marketplace
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "Adding VirtualBoard to Claude Code marketplace...")
	}

	output, err := execCommand(claudePath, "plugin", "marketplace", "add", claudeMarketplaceSource)
	if err != nil {
		log.WithError(err).WithField("output", string(output)).Debug("Failed to add plugin to marketplace")
		return WrapCLIError(ExitCodeExternalCommand, fmt.Errorf("failed to add VirtualBoard to marketplace: %s", strings.TrimSpace(string(output))))
	}
	log.Debug("Added plugin to marketplace")

	// Install the plugin
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "Installing VirtualBoard plugin...")
	}

	output, err = execCommand(claudePath, "plugin", "install", claudePluginIdentifier)
	if err != nil {
		log.WithError(err).WithField("output", string(output)).Debug("Failed to install plugin")
		return WrapCLIError(ExitCodeExternalCommand, fmt.Errorf("failed to install VirtualBoard plugin: %s", strings.TrimSpace(string(output))))
	}
	log.Debug("Installed plugin")

	msg := "VirtualBoard plugin installed successfully for Claude Code"
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"ide":               "claude",
		"installed":         true,
		"framework_release": templateRelease,
		"plugin":            claudePluginIdentifier,
	})
}

// getProjectRoot returns the project root directory, handling the case where
// opts.RootDir is the .virtualboard directory itself
func getProjectRoot(opts *config.Options) string {
	projectRoot := opts.RootDir
	if filepath.Base(projectRoot) == ".virtualboard" {
		projectRoot = filepath.Dir(projectRoot)
	}
	return projectRoot
}

// getVirtualBoardRoot resolves either a nested initialized workspace or the
// template-repository layout. The caller still validates required sources.
func getVirtualBoardRoot(opts *config.Options) string {
	if filepath.Base(opts.RootDir) == ".virtualboard" {
		return opts.RootDir
	}
	rootContract := filepath.Join(opts.RootDir, "virtualboard.json")
	if info, err := os.Stat(rootContract); err == nil && info.Mode().IsRegular() {
		return opts.RootDir
	}
	return filepath.Join(opts.RootDir, ".virtualboard")
}

// validateInstallDestination proves that target is beneath root and that every
// existing component below root is a real directory or regular leaf, never a
// symbolic link. When createParents is true it creates missing directories one
// component at a time with the normalized integration-directory mode.
func validateInstallDestination(root, target string, createParents bool) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve integration target: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target %s escapes project root %s", target, root)
	}

	rootInfo, err := os.Lstat(rootAbs)
	if err != nil {
		return fmt.Errorf("inspect project root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("project root must not be a symbolic link: %s", rootAbs)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("project root is not a directory: %s", rootAbs)
	}

	parts := strings.Split(rel, string(filepath.Separator))
	current := rootAbs
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid destination component %q", part)
		}
		current = filepath.Join(current, part)
		isLeaf := index == len(parts)-1
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if !os.IsNotExist(statErr) {
				return fmt.Errorf("inspect destination component %s: %w", current, statErr)
			}
			if isLeaf || !createParents {
				continue
			}
			if err := os.Mkdir(current, safeIntegrationDirMode); err != nil {
				if !os.IsExist(err) {
					return fmt.Errorf("create integration directory %s: %w", current, err)
				}
				info, statErr = os.Lstat(current)
				if statErr != nil {
					return fmt.Errorf("reinspect integration directory %s: %w", current, statErr)
				}
			} else {
				if err := os.Chmod(current, safeIntegrationDirMode); err != nil {
					return fmt.Errorf("normalize integration directory %s: %w", current, err)
				}
				continue
			}
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in integration destinations: %s", current)
		}
		if isLeaf {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("integration destination is not a regular file: %s", current)
			}
			continue
		}
		if !info.IsDir() {
			return fmt.Errorf("integration destination parent is not a directory: %s", current)
		}
		if createParents && runtime.GOOS != "windows" {
			normalized := info.Mode().Perm() &^ 0o022
			if normalized != info.Mode().Perm() {
				if err := os.Chmod(current, normalized); err != nil {
					return fmt.Errorf("normalize integration directory %s: %w", current, err)
				}
			}
		}
	}
	return nil
}

// installCursor installs VirtualBoard rules for Cursor IDE
func installCursor(cmd *cobra.Command, opts *config.Options, force bool) error {
	log := opts.Logger().WithField("ide", "cursor")
	projectRoot := getProjectRoot(opts)
	vbPath := getVirtualBoardRoot(opts)

	if exists, err := pathExists(vbPath); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to check VirtualBoard workspace: %w", err))
	} else if !exists {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("VirtualBoard workspace not found. Run 'vb init' first"))
	}

	sourceFile := filepath.Join(vbPath, filepath.FromSlash(cursorIntegrationSource))
	ruleContent, _, err := readRequiredIntegrationFile(sourceFile, cursorIntegrationSource)
	if err != nil {
		return WrapCLIError(ExitCodeValidation, err)
	}
	if err := verifyIntegrationDigest(cursorIntegrationSource, ruleContent, cursorIntegrationSHA256); err != nil {
		return WrapCLIError(ExitCodeValidation, err)
	}
	cursorPath := filepath.Join(projectRoot, ".cursor")
	rulesPath := filepath.Join(cursorPath, "rules")
	targetFile := filepath.Join(rulesPath, cursorRuleFile)
	if err := validateInstallDestination(projectRoot, targetFile, false); err != nil {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("unsafe Cursor destination: %w", err))
	}

	if opts.DryRun {
		msg := fmt.Sprintf("Dry-run: would install VirtualBoard rules to %s", targetFile)
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"ide":         "cursor",
			"source_file": sourceFile,
			"target_file": targetFile,
		})
	}

	// Create and validate missing parents before opening a root-scoped handle.
	// Every subsequent read, capture, publication, and restore is relative to
	// that verified root, so a repository path cannot redirect the operation.
	if err := validateInstallDestination(projectRoot, targetFile, true); err != nil {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("unsafe Cursor destination: %w", err))
	}
	projectFS, err := openVerifiedInstallRoot(projectRoot)
	if err != nil {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("unsafe Cursor destination: %w", err))
	}
	defer projectFS.Close()
	targetRelative, err := filepath.Rel(projectRoot, targetFile)
	if err != nil || targetRelative == "." || filepath.IsAbs(targetRelative) || targetRelative == ".." || strings.HasPrefix(targetRelative, ".."+string(filepath.Separator)) {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("unsafe Cursor destination: target escapes project root"))
	}
	if recovery, err := findCursorRecoveryFile(projectFS, targetRelative); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("inspect Cursor recovery storage: %w", err))
	} else if recovery != "" {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("an interrupted Cursor replacement retained recovery data at %s; reconcile it before installing", filepath.Join(projectRoot, recovery)))
	}
	expected, err := captureCursorDestinationSnapshot(projectFS, targetRelative)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to inspect Cursor destination: %w", err))
	}
	if expected.Exists && !force {
		if bytes.Equal(expected.Content, ruleContent) && integrationFileModeIsSafe(expected.Mode) {
			msg := "VirtualBoard rule for Cursor is already up to date"
			return respond(cmd, opts, true, msg, map[string]interface{}{
				"ide":         "cursor",
				"target_file": targetFile,
				"changed":     false,
			})
		}
		if !bytes.Equal(expected.Content, ruleContent) && opts.JSONOutput {
			return WrapCLIError(ExitCodeValidation, fmt.Errorf("Cursor rule differs at %s; rerun with --force to replace it non-interactively", targetFile))
		}

		if !bytes.Equal(expected.Content, ruleContent) {
			// Ask for confirmation only for a content replacement. An identical
			// file with unsafe permissions is normalized without a content change.
			confirmed, err := confirmReplace(opts, fmt.Sprintf("File %s already exists and differs. Replace it?", targetFile))
			if err != nil {
				return WrapCLIError(ExitCodeUnknown, fmt.Errorf("failed to get confirmation: %w", err))
			}
			if !confirmed {
				msg := "Installation cancelled by user"
				return respond(cmd, opts, true, msg, map[string]interface{}{
					"ide":       "cursor",
					"cancelled": true,
				})
			}
		}
	}
	if afterCursorConsent != nil {
		afterCursorConsent()
	}
	if err := installCursorRuleCAS(projectFS, targetRelative, expected, ruleContent); err != nil {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("Cursor destination changed after approval or could not be published safely: %w", err))
	}

	log.WithField("path", targetFile).Debug("Installed Cursor rule")

	msg := fmt.Sprintf("VirtualBoard rule installed at %s", targetFile)
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"ide":         "cursor",
		"source_file": sourceFile,
		"target_file": targetFile,
		"installed":   true,
	})
}

func openVerifiedInstallRoot(path string) (*os.Root, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect project root: %w", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("project root is not a real directory: %s", path)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open project root: %w", err)
	}
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("project root changed while opening: %s", path)
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(before, current) {
		_ = root.Close()
		return nil, fmt.Errorf("project root changed while opening: %s", path)
	}
	return root, nil
}

func captureCursorDestinationSnapshot(root *os.Root, target string) (cursorDestinationSnapshot, error) {
	content, mode, err := readSecureRegularRootFileState(root, target, maxIntegrationFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return cursorDestinationSnapshot{}, nil
	}
	if err != nil {
		return cursorDestinationSnapshot{}, err
	}
	return cursorDestinationSnapshot{Exists: true, Mode: mode.Perm(), Content: content}, nil
}

func cursorSnapshotsEqual(left, right cursorDestinationSnapshot) bool {
	return left.Exists == right.Exists && left.Mode.Perm() == right.Mode.Perm() && bytes.Equal(left.Content, right.Content)
}

func installCursorRuleCAS(root *os.Root, target string, expected cursorDestinationSnapshot, content []byte) error {
	if root == nil {
		return errors.New("Cursor project root is required")
	}
	if !expected.Exists {
		if err := writeIntegrationFileExclusiveRoot(root, target, content, safeIntegrationFileMode); err != nil {
			return fmt.Errorf("publish absent Cursor target exclusively: %w", err)
		}
		return nil
	}

	nonce, err := randomDirectoryReplaceNonce()
	if err != nil {
		return err
	}
	held := filepath.Join(filepath.Dir(target), ".vb-cursor-recovery-"+nonce+".md")
	if err := renameRootSiblingExclusive(root, target, held); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("Cursor target was removed after approval")
		}
		return fmt.Errorf("capture Cursor target in recovery storage: %w", err)
	}
	if afterCursorDestinationCaptured != nil {
		afterCursorDestinationCaptured()
	}

	captured, captureErr := captureCursorDestinationSnapshot(root, held)
	if captureErr != nil || !cursorSnapshotsEqual(captured, expected) {
		restoreErr := restoreCapturedCursorTarget(root, held, target)
		if captureErr != nil {
			return fmt.Errorf("inspect captured Cursor target: %v; %v", captureErr, restoreErr)
		}
		return fmt.Errorf("captured Cursor target differs from the approved bytes or mode; %v", restoreErr)
	}
	if err := writeIntegrationFileExclusiveRoot(root, target, content, safeIntegrationFileMode); err != nil {
		restoreErr := restoreCapturedCursorTarget(root, held, target)
		return fmt.Errorf("publish Cursor rule exclusively: %v; %v", err, restoreErr)
	}
	if err := root.Remove(held); err != nil {
		return fmt.Errorf("Cursor rule installed but recovery cleanup failed at %s: %w", held, err)
	}
	return nil
}

func restoreCapturedCursorTarget(root *os.Root, held, target string) error {
	if err := renameRootSiblingExclusive(root, held, target); err != nil {
		return fmt.Errorf("could not restore without replacing a concurrently recreated target; recovery data retained at %s: %w", held, err)
	}
	return errors.New("captured target restored without replacement")
}

func renameRootSiblingExclusive(root *os.Root, source, destination string) error {
	if root == nil || filepath.Dir(source) != filepath.Dir(destination) || source == destination {
		return errors.New("exclusive root rename requires distinct sibling paths")
	}
	directory := filepath.Dir(source)
	parent, err := root.Open(directory)
	if err != nil {
		return fmt.Errorf("open rooted rename parent: %w", err)
	}
	defer parent.Close()
	info, err := parent.Stat()
	if err != nil || !info.IsDir() {
		return fmt.Errorf("rooted rename parent is not a directory: %s", directory)
	}
	return renameDirectoryExclusive(parent, filepath.Base(source), filepath.Base(destination))
}

func findCursorRecoveryFile(root *os.Root, target string) (string, error) {
	directory := filepath.Dir(target)
	parent, err := root.Open(directory)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	prefix := ".vb-cursor-recovery-"
	entriesSeen := 0
	for {
		entries, readErr := parent.ReadDir(128)
		for _, entry := range entries {
			entriesSeen++
			if entriesSeen > maxIntegrationDestinationEntries {
				return "", fmt.Errorf("Cursor rules directory exceeds %d entries", maxIntegrationDestinationEntries)
			}
			if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".md") {
				return filepath.Join(directory, entry.Name()), nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return "", nil
		}
		if readErr != nil {
			return "", readErr
		}
	}
}

func writeIntegrationFileExclusiveRoot(root *os.Root, target string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(target)
	for range 100 {
		nonce, err := randomDirectoryReplaceNonce()
		if err != nil {
			return err
		}
		stage := filepath.Join(directory, ".vb-integration-file-stage-"+nonce)
		file, err := root.OpenFile(stage, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create Cursor staging file: %w", err)
		}
		removeStage := true
		defer func() {
			_ = file.Close()
			if removeStage {
				_ = root.Remove(stage)
			}
		}()
		written, err := file.Write(content)
		if err == nil && written != len(content) {
			err = io.ErrShortWrite
		}
		if err != nil {
			return fmt.Errorf("write Cursor staging file: %w", err)
		}
		if err := file.Chmod(mode); err != nil {
			return fmt.Errorf("set Cursor staging mode: %w", err)
		}
		if err := file.Sync(); err != nil {
			return fmt.Errorf("sync Cursor staging file: %w", err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close Cursor staging file: %w", err)
		}
		if err := root.Link(stage, target); err != nil {
			return err
		}
		if err := root.Remove(stage); err != nil {
			return fmt.Errorf("remove published Cursor staging name: %w", err)
		}
		removeStage = false
		return nil
	}
	return errors.New("could not allocate a unique Cursor staging file")
}

func integrationFileModeIsSafe(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		// Windows exposes ACL-backed writable regular files as 0666 regardless
		// of the requested Unix permission bits.
		return mode.Perm()&0o200 != 0
	}
	return mode.Perm() == safeIntegrationFileMode
}

func integrationDirectoryModeIsSafe(mode os.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return mode.Perm()&0o022 == 0
}

// installOpenCode installs the scaffolded VirtualBoard OpenCode integration.
func installOpenCode(cmd *cobra.Command, opts *config.Options, force bool) error {
	log := opts.Logger().WithField("ide", "opencode")
	projectRoot := getProjectRoot(opts)
	vbPath := getVirtualBoardRoot(opts)

	if exists, err := pathExists(vbPath); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to check VirtualBoard workspace: %w", err))
	} else if !exists {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("VirtualBoard workspace not found. Run 'vb init' first"))
	}

	sourceRoot := filepath.Join(vbPath, filepath.FromSlash(openCodeIntegrationRoot))
	files, err := loadAuthorizedIntegrationFiles(sourceRoot, openCodeIntegrationManifest)
	if err != nil {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf("unauthorized OpenCode integration source: %w", err))
	}

	opencodePath := filepath.Join(projectRoot, ".opencode")
	for _, file := range files {
		target := filepath.Join(opencodePath, filepath.FromSlash(file.Path))
		if err := validateInstallDestination(projectRoot, target, false); err != nil {
			return WrapCLIError(ExitCodeValidation, fmt.Errorf("unsafe OpenCode destination: %w", err))
		}
	}

	if opts.DryRun {
		msg := fmt.Sprintf("Dry-run: would copy OpenCode integration from %s to %s", sourceRoot, opencodePath)
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"ide":         "opencode",
			"source":      sourceRoot,
			"destination": opencodePath,
			"files":       len(files),
		})
	}

	cancelled := false
	operationExitCode := ExitCodeFilesystem
	err = withIntegrationReplacementLease(opencodePath, func(lease *directoryReplaceLease) error {
		snapshot, snapshotErr := captureIntegrationDestinationSnapshot(opencodePath)
		if snapshotErr != nil {
			return snapshotErr
		}
		conflicts, conflictErr := integrationConflictsFromSnapshot(snapshot, files)
		if conflictErr != nil {
			return conflictErr
		}
		if len(conflicts) > 0 && !force {
			if opts.JSONOutput {
				operationExitCode = ExitCodeValidation
				return fmt.Errorf("%d OpenCode integration file(s) differ; rerun with --force to replace them non-interactively", len(conflicts))
			}
			confirmed, confirmErr := confirmReplace(opts, fmt.Sprintf("%d OpenCode integration file(s) differ. Replace them?", len(conflicts)))
			if confirmErr != nil {
				operationExitCode = ExitCodeUnknown
				return fmt.Errorf("failed to get confirmation: %w", confirmErr)
			}
			if !confirmed {
				cancelled = true
				return nil
			}
		}
		if afterOpenCodeConsent != nil {
			afterOpenCodeConsent()
		}
		if err := assertIntegrationDestinationSnapshot(opencodePath, snapshot); err != nil {
			operationExitCode = ExitCodeValidation
			return fmt.Errorf("OpenCode destination changed after approval: %w", err)
		}
		if err := installIntegrationTreeWithLease(opencodePath, files, lease, snapshot); err != nil {
			return fmt.Errorf("failed to copy OpenCode integration: %w", err)
		}
		return nil
	})
	if err != nil {
		return WrapCLIError(operationExitCode, err)
	}
	if cancelled {
		return respond(cmd, opts, true, "Installation cancelled by user", map[string]interface{}{
			"ide":       "opencode",
			"cancelled": true,
		})
	}

	log.WithField("path", opencodePath).Debug("Installed OpenCode integration")

	msg := fmt.Sprintf("VirtualBoard OpenCode integration installed at %s", opencodePath)
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"ide":         "opencode",
		"source":      sourceRoot,
		"destination": opencodePath,
		"files":       len(files),
		"installed":   true,
	})
}

func readRequiredIntegrationFile(path, displayPath string) ([]byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("required integration source %s is unavailable: %w", displayPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("required integration source %s is not a regular file", displayPath)
	}
	content, err := os.ReadFile(path) // #nosec G304 -- path is a fixed integration source beneath the workspace
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read integration source %s: %w", displayPath, err)
	}
	if len(content) == 0 {
		return nil, 0, fmt.Errorf("required integration source %s is empty", displayPath)
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0o600
	}
	return content, mode, nil
}

func verifyIntegrationDigest(displayPath string, content []byte, expected string) error {
	if len(expected) != sha256.Size*2 || strings.ToLower(expected) != expected {
		return fmt.Errorf("compiled integration SHA-256 is invalid for %s", displayPath)
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("compiled integration SHA-256 is invalid for %s", displayPath)
	}
	digest := sha256.Sum256(content)
	if !bytes.Equal(digest[:], decoded) {
		return fmt.Errorf("integration source checksum mismatch: %s", displayPath)
	}
	return nil
}

func collectIntegrationFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			if !entry.IsDir() {
				return fmt.Errorf("source root is not a directory")
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular integration source: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func validateIntegrationManifest(entries []integrationManifestEntry) error {
	if len(entries) == 0 {
		return fmt.Errorf("compiled integration manifest is empty")
	}
	if len(entries) > maxIntegrationFiles {
		return fmt.Errorf("compiled integration manifest exceeds %d files", maxIntegrationFiles)
	}
	seen := make(map[string]struct{}, len(entries))
	previous := ""
	requiredSkillFound := false
	for _, entry := range entries {
		if entry.Path == "" || strings.Contains(entry.Path, "\\") || filepath.IsAbs(filepath.FromSlash(entry.Path)) {
			return fmt.Errorf("invalid manifest path %q", entry.Path)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(entry.Path)))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != entry.Path {
			return fmt.Errorf("non-canonical manifest path %q", entry.Path)
		}
		if previous != "" && entry.Path <= previous {
			return fmt.Errorf("integration manifest paths must be unique and bytewise sorted")
		}
		previous = entry.Path
		if _, exists := seen[entry.Path]; exists {
			return fmt.Errorf("duplicate manifest path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if len(entry.SHA256) != sha256.Size*2 || strings.ToLower(entry.SHA256) != entry.SHA256 {
			return fmt.Errorf("invalid SHA-256 for %s", entry.Path)
		}
		decoded, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("invalid SHA-256 for %s", entry.Path)
		}
		if entry.Path == openCodeRequiredSkill {
			requiredSkillFound = true
		}
	}
	if !requiredSkillFound {
		return fmt.Errorf("compiled integration manifest is missing required %s", openCodeRequiredSkill)
	}
	return nil
}

func loadAuthorizedIntegrationFiles(root string, manifest []integrationManifestEntry) ([]authorizedIntegrationFile, error) {
	if err := validateIntegrationManifest(manifest); err != nil {
		return nil, err
	}
	actualFiles, err := collectIntegrationFiles(root)
	if err != nil {
		return nil, err
	}
	actualByPath := make(map[string]string, len(actualFiles))
	for _, rel := range actualFiles {
		normalized := filepath.ToSlash(rel)
		if _, exists := actualByPath[normalized]; exists {
			return nil, fmt.Errorf("duplicate integration source path %q", normalized)
		}
		actualByPath[normalized] = rel
	}
	expected := make(map[string]struct{}, len(manifest))
	for _, entry := range manifest {
		expected[entry.Path] = struct{}{}
		if _, exists := actualByPath[entry.Path]; !exists {
			return nil, fmt.Errorf("authorized integration file is missing: %s", entry.Path)
		}
	}
	for actual := range actualByPath {
		if _, authorized := expected[actual]; !authorized {
			return nil, fmt.Errorf("unlisted integration source file is not authorized: %s", actual)
		}
	}

	files := make([]authorizedIntegrationFile, 0, len(manifest))
	totalSize := 0
	for _, entry := range manifest {
		rel := actualByPath[entry.Path]
		source := filepath.Join(root, rel)
		info, err := os.Lstat(source)
		if err != nil {
			return nil, fmt.Errorf("inspect authorized integration file %s: %w", entry.Path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("authorized integration source is not a regular file: %s", entry.Path)
		}
		if info.Size() < 0 || info.Size() > maxIntegrationFileBytes {
			return nil, fmt.Errorf("integration source %s exceeds %d bytes", entry.Path, maxIntegrationFileBytes)
		}
		content, err := os.ReadFile(source) // #nosec G304 -- source is from the validated compiled manifest.
		if err != nil {
			return nil, fmt.Errorf("read authorized integration file %s: %w", entry.Path, err)
		}
		if int64(len(content)) != info.Size() {
			return nil, fmt.Errorf("integration source changed while reading: %s", entry.Path)
		}
		totalSize += len(content)
		if totalSize > maxIntegrationTotalSize {
			return nil, fmt.Errorf("integration payload exceeds %d bytes", maxIntegrationTotalSize)
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != entry.SHA256 {
			return nil, fmt.Errorf("integration source checksum mismatch: %s", entry.Path)
		}
		files = append(files, authorizedIntegrationFile{Path: entry.Path, Content: content})
	}
	return files, nil
}

func integrationConflicts(destinationRoot string, files []authorizedIntegrationFile) ([]string, error) {
	snapshot, err := captureIntegrationDestinationSnapshot(destinationRoot)
	if err != nil {
		return nil, err
	}
	return integrationConflictsFromSnapshot(snapshot, files)
}

func integrationConflictsFromSnapshot(snapshot integrationDestinationSnapshot, files []authorizedIntegrationFile) ([]string, error) {
	var conflicts []string
	for _, file := range files {
		state, exists := snapshot.Entries[filepath.ToSlash(filepath.Clean(file.Path))]
		if !exists {
			continue
		}
		if state.Directory {
			return nil, fmt.Errorf("failed to inspect OpenCode destination %s: destination is not a regular file", file.Path)
		}
		digest := sha256.Sum256(file.Content)
		if state.SHA256 != digest {
			conflicts = append(conflicts, file.Path)
		}
	}
	return conflicts, nil
}

func captureIntegrationDestinationSnapshot(destinationRoot string) (integrationDestinationSnapshot, error) {
	snapshot := integrationDestinationSnapshot{Entries: make(map[string]integrationDestinationEntryState)}
	rootInfo, err := os.Lstat(destinationRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return snapshot, nil
		}
		return snapshot, fmt.Errorf("failed to inspect OpenCode destination %s: %w", destinationRoot, err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return snapshot, fmt.Errorf("failed to inspect OpenCode destination %s: destination is not a directory", destinationRoot)
	}
	root, err := os.OpenRoot(destinationRoot)
	if err != nil {
		return integrationDestinationSnapshot{}, fmt.Errorf("failed to open OpenCode destination %s: %w", destinationRoot, err)
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(rootInfo, opened) {
		return integrationDestinationSnapshot{}, fmt.Errorf("failed to inspect OpenCode destination %s: root changed while opening", destinationRoot)
	}
	current, err := os.Lstat(destinationRoot)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(rootInfo, current) {
		return integrationDestinationSnapshot{}, fmt.Errorf("failed to inspect OpenCode destination %s: root changed while opening", destinationRoot)
	}
	return captureIntegrationRootSnapshot(root, destinationRoot)
}

func captureIntegrationRootSnapshot(root *os.Root, displayRoot string) (integrationDestinationSnapshot, error) {
	snapshot := integrationDestinationSnapshot{Exists: true, Entries: make(map[string]integrationDestinationEntryState)}
	entryCount := 0
	var totalBytes int64
	err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryCount++
		if entryCount > maxIntegrationDestinationEntries {
			return fmt.Errorf("OpenCode destination exceeds %d entries", maxIntegrationDestinationEntries)
		}
		rel := filepath.ToSlash(path)
		info, err := root.Lstat(filepath.FromSlash(path))
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("OpenCode destination contains a symbolic link: %s", rel)
		}
		state := integrationDestinationEntryState{Mode: info.Mode().Perm()}
		switch {
		case info.IsDir():
			state.Directory = true
		case info.Mode().IsRegular():
			content, mode, err := readSecureRegularRootFileState(root, filepath.FromSlash(path), maxIntegrationDestinationFileBytes)
			if err != nil {
				return err
			}
			state.Mode = mode
			state.Size = int64(len(content))
			state.SHA256 = sha256.Sum256(content)
			totalBytes += state.Size
			if totalBytes > maxIntegrationDestinationTotalSize {
				return fmt.Errorf("OpenCode destination exceeds %d bytes", maxIntegrationDestinationTotalSize)
			}
		default:
			return fmt.Errorf("OpenCode destination contains a non-regular path: %s", rel)
		}
		snapshot.Entries[rel] = state
		return nil
	})
	if err != nil {
		return integrationDestinationSnapshot{}, fmt.Errorf("failed to inspect OpenCode destination %s: %w", displayRoot, err)
	}
	return snapshot, nil
}

func assertIntegrationDestinationSnapshot(destinationRoot string, expected integrationDestinationSnapshot) error {
	current, err := captureIntegrationDestinationSnapshot(destinationRoot)
	if err != nil {
		return err
	}
	return compareIntegrationDestinationSnapshots(current, expected)
}

func compareIntegrationDestinationSnapshots(current, expected integrationDestinationSnapshot) error {
	if current.Exists != expected.Exists || len(current.Entries) != len(expected.Entries) {
		return fmt.Errorf("destination tree identity changed")
	}
	for path, expectedState := range expected.Entries {
		if currentState, ok := current.Entries[path]; !ok || currentState != expectedState {
			return fmt.Errorf("destination entry changed: %s", path)
		}
	}
	return nil
}

func integrationDestinationSnapshotDigest(snapshot integrationDestinationSnapshot) string {
	hash := sha256.New()
	if snapshot.Exists {
		_, _ = hash.Write([]byte{1})
	} else {
		_, _ = hash.Write([]byte{0})
	}
	paths := make([]string, 0, len(snapshot.Entries))
	for path := range snapshot.Entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		state := snapshot.Entries[path]
		_, _ = fmt.Fprintf(hash, "%d:%s\x00%t\x00%#o\x00%d\x00", len(path), path, state.Directory, state.Mode.Perm(), state.Size)
		_, _ = hash.Write(state.SHA256[:])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func integrationDestinationTreeDigest(root string) (string, error) {
	snapshot, err := captureIntegrationDestinationSnapshot(root)
	if err != nil {
		return "", err
	}
	if !snapshot.Exists {
		return "", fmt.Errorf("integration tree is absent: %s", root)
	}
	return integrationDestinationSnapshotDigest(snapshot), nil
}

func copyIntegrationFiles(destinationRoot string, files []authorizedIntegrationFile) error {
	for _, file := range files {
		destination := filepath.Join(destinationRoot, filepath.FromSlash(file.Path))
		if err := validateInstallDestination(destinationRoot, destination, true); err != nil {
			return fmt.Errorf("unsafe integration destination %s: %w", destination, err)
		}
		if err := util.WriteFileAtomic(destination, file.Content, safeIntegrationFileMode); err != nil {
			return fmt.Errorf("failed to write file %s: %w", destination, err)
		}
	}
	return nil
}

func copyApprovedIntegrationTree(sourceRoot, destinationRoot string, expected integrationDestinationSnapshot) error {
	source, err := openVerifiedInstallRoot(sourceRoot)
	if err != nil {
		return fmt.Errorf("open approved OpenCode tree: %w", err)
	}
	defer source.Close()

	entryCount := 0
	var totalBytes int64
	var rootMode os.FileMode
	err = fs.WalkDir(source.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		entryCount++
		if entryCount > maxIntegrationDestinationEntries {
			return fmt.Errorf("OpenCode destination exceeds %d entries", maxIntegrationDestinationEntries)
		}
		relative := filepath.FromSlash(path)
		info, err := source.Lstat(relative)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("OpenCode destination contains a symbolic link: %s", path)
		}
		if path == "." {
			if !info.IsDir() {
				return errors.New("OpenCode destination root is not a directory")
			}
			rootMode = info.Mode().Perm()
			return nil
		}
		destination := filepath.Join(destinationRoot, relative)
		switch {
		case info.IsDir():
			if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(destination, info.Mode().Perm())
		case info.Mode().IsRegular():
			content, mode, err := readSecureRegularRootFileState(source, relative, maxIntegrationDestinationFileBytes)
			if err != nil {
				return err
			}
			totalBytes += int64(len(content))
			if totalBytes > maxIntegrationDestinationTotalSize {
				return fmt.Errorf("OpenCode destination exceeds %d bytes", maxIntegrationDestinationTotalSize)
			}
			return util.WriteFileExclusiveAtomicWithin(destinationRoot, destination, content, mode)
		default:
			return fmt.Errorf("OpenCode destination contains a non-regular path: %s", path)
		}
	})
	if err != nil {
		return fmt.Errorf("copy approved OpenCode tree: %w", err)
	}
	if err := os.Chmod(destinationRoot, rootMode); err != nil {
		return fmt.Errorf("preserve approved OpenCode root mode: %w", err)
	}
	copied, err := captureIntegrationDestinationSnapshot(destinationRoot)
	if err != nil {
		return err
	}
	if err := compareIntegrationDestinationSnapshots(copied, expected); err != nil {
		return fmt.Errorf("staged OpenCode tree differs from approved state: %w", err)
	}
	return nil
}

func withIntegrationReplacementLease(destinationRoot string, operation func(*directoryReplaceLease) error) (returnErr error) {
	parent := filepath.Dir(destinationRoot)
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("failed to create integration parent: %w", err)
	}
	lease, previousNonce, err := acquireDirectoryReplaceLease(destinationRoot)
	if err != nil {
		return fmt.Errorf("acquire OpenCode integration replacement lease: %w", err)
	}
	defer func() {
		if releaseErr := lease.release(); releaseErr != nil {
			if returnErr == nil {
				returnErr = fmt.Errorf("integration replacement lease cleanup failed: %w", releaseErr)
			} else {
				returnErr = fmt.Errorf("%w; integration replacement lease cleanup failed: %v", returnErr, releaseErr)
			}
		}
	}()
	if err := recoverDirectoryReplacementWithLease(destinationRoot, lease, previousNonce); err != nil {
		return fmt.Errorf("recover interrupted OpenCode integration replacement: %w", err)
	}
	return operation(lease)
}

func installIntegrationTreeAtomically(destinationRoot string, files []authorizedIntegrationFile) error {
	return withIntegrationReplacementLease(destinationRoot, func(lease *directoryReplaceLease) error {
		snapshot, err := captureIntegrationDestinationSnapshot(destinationRoot)
		if err != nil {
			return err
		}
		return installIntegrationTreeWithLease(destinationRoot, files, lease, snapshot)
	})
}

func installIntegrationTreeWithLease(destinationRoot string, files []authorizedIntegrationFile, lease *directoryReplaceLease, expected integrationDestinationSnapshot) error {
	parent := filepath.Dir(destinationRoot)
	if err := assertIntegrationDestinationSnapshot(destinationRoot, expected); err != nil {
		return fmt.Errorf("OpenCode destination changed before staging: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".vb-integration-stage-*")
	if err != nil {
		return fmt.Errorf("failed to create integration staging directory: %w", err)
	}
	stageLive := true
	defer func() {
		if stageLive {
			_ = os.RemoveAll(stage)
		}
	}()

	if expected.Exists {
		if err := copyApprovedIntegrationTree(destinationRoot, stage, expected); err != nil {
			return fmt.Errorf("failed to preserve approved integration files: %w", err)
		}
	}
	if err := copyIntegrationFiles(stage, files); err != nil {
		return err
	}
	stageInfo, err := os.Stat(stage)
	if err != nil {
		return fmt.Errorf("inspect integration stage: %w", err)
	}
	rootMode := stageInfo.Mode().Perm() &^ 0o022
	if !expected.Exists {
		rootMode = safeIntegrationDirMode
	}
	if err := os.Chmod(stage, rootMode); err != nil {
		return fmt.Errorf("normalize integration root mode: %w", err)
	}
	if err := assertIntegrationDestinationSnapshot(destinationRoot, expected); err != nil {
		return fmt.Errorf("OpenCode destination changed during staging: %w", err)
	}

	if !expected.Exists {
		if beforeOpenCodeExclusivePublish != nil {
			beforeOpenCodeExclusivePublish()
		}
		if err := renameReplacementDirectoryExclusive(stage, destinationRoot); err != nil {
			return fmt.Errorf("failed to activate integration: %w", err)
		}
		stageLive = false
		return nil
	}

	nonce, err := randomDirectoryReplaceNonce()
	if err != nil {
		return err
	}
	backup := filepath.Join(parent, ".vb-template-backup-"+nonce)
	journalPath := directoryReplaceJournalPath(destinationRoot)
	journal := directoryReplaceJournal{
		Version:            directoryReplaceJournalVersion,
		Target:             filepath.Base(destinationRoot),
		Stage:              filepath.Base(stage),
		Backup:             filepath.Base(backup),
		ExpectedTreeSHA256: integrationDestinationSnapshotDigest(expected),
		Nonce:              lease.record.Nonce,
		PID:                lease.record.PID,
		Host:               lease.record.Host,
		StartedAt:          time.Now().UTC(),
	}
	if err := writeDirectoryReplaceJournal(journalPath, journal); err != nil {
		return fmt.Errorf("create integration replacement journal: %w", err)
	}
	if err := renameReplacementDirectoryExclusive(destinationRoot, backup); err != nil {
		if cleanupErr := removeDirectoryReplaceJournal(journalPath, lease.record.Nonce); cleanupErr != nil {
			return fmt.Errorf("capture approved OpenCode tree: %v; failed to remove unused replacement journal: %w", err, cleanupErr)
		}
		return fmt.Errorf("capture approved OpenCode tree: %w", err)
	}
	if afterOpenCodeDestinationCaptured != nil {
		afterOpenCodeDestinationCaptured()
	}
	captured, err := captureIntegrationDestinationSnapshot(backup)
	if err != nil {
		retainStage, restoreErr := restoreCapturedIntegrationTree(destinationRoot, stage, backup, journalPath, lease.record.Nonce, fmt.Errorf("inspect captured OpenCode tree: %w", err))
		if retainStage {
			stageLive = false
		}
		return restoreErr
	}
	if err := compareIntegrationDestinationSnapshots(captured, expected); err != nil {
		retainStage, restoreErr := restoreCapturedIntegrationTree(destinationRoot, stage, backup, journalPath, lease.record.Nonce, fmt.Errorf("captured OpenCode tree differs from approved state: %w", err))
		if retainStage {
			stageLive = false
		}
		return restoreErr
	}
	if beforeOpenCodeExclusivePublish != nil {
		beforeOpenCodeExclusivePublish()
	}
	if err := renameReplacementDirectoryExclusive(stage, destinationRoot); err != nil {
		retainStage, restoreErr := restoreCapturedIntegrationTree(destinationRoot, stage, backup, journalPath, lease.record.Nonce, fmt.Errorf("activate staged OpenCode tree exclusively: %w", err))
		if retainStage {
			stageLive = false
		}
		return restoreErr
	}
	stageLive = false
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("OpenCode integration activated but recovery backup cleanup failed at %s: %w", backup, err)
	}
	if err := removeDirectoryReplaceJournal(journalPath, lease.record.Nonce); err != nil {
		return fmt.Errorf("OpenCode integration activated but replacement journal cleanup failed: %w", err)
	}
	return nil
}

func restoreCapturedIntegrationTree(destinationRoot, stage, backup, journalPath, nonce string, cause error) (bool, error) {
	if err := renameReplacementDirectoryExclusive(backup, destinationRoot); err != nil {
		return true, fmt.Errorf("%v; could not restore without replacing a concurrently recreated destination; stage, captured tree, and journal retained at %s, %s, and %s: %w", cause, stage, backup, journalPath, err)
	}
	if err := removeDirectoryReplaceJournal(journalPath, nonce); err != nil {
		return false, fmt.Errorf("%v; captured tree restored, but replacement journal cleanup failed at %s: %w", cause, journalPath, err)
	}
	return false, fmt.Errorf("%v; captured tree restored without replacement", cause)
}
