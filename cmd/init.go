package cmd

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/templatediff"
	"github.com/virtualboard/vb-cli/internal/util"
)

const initDirName = ".virtualboard"
const templateZipURL = "https://github.com/virtualboard/template-base/archive/refs/heads/main.zip"
const templateVersionURL = "https://raw.githubusercontent.com/virtualboard/template-base/main/version.txt"
const maxTemplateBytes int64 = 50 * 1024 * 1024
const templateVersionFile = ".template-version"

type fetchTemplateFunc func(workdir, dest string) error
type fetchTemplateToDirFunc func() (string, error)
type fetchTemplateVersionFunc func() (string, error)

var fetchTemplate fetchTemplateFunc = func(workdir, dest string) error {
	resp, err := http.Get(templateZipURL)
	if err != nil {
		return fmt.Errorf("failed to download template archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d fetching template", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "vb-template-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		if cerr := tmpFile.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "vb-cli: failed to close temp file: %v\n", cerr)
		}
		if remErr := os.Remove(tmpFile.Name()); remErr != nil {
			fmt.Fprintf(os.Stderr, "vb-cli: failed to remove temp file: %v\n", remErr)
		}
	}()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("failed to save template archive: %w", err)
	}

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind template archive: %w", err)
	}

	stat, err := tmpFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat template archive: %w", err)
	}

	archive, err := zip.NewReader(tmpFile, stat.Size())
	if err != nil {
		return fmt.Errorf("failed to open template archive: %w", err)
	}

	targetRoot := filepath.Join(workdir, dest)
	if err := os.MkdirAll(targetRoot, 0o750); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	for _, file := range archive.File {
		parts := strings.SplitN(file.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		relative := parts[1]
		outPath := filepath.Join(targetRoot, relative)
		clean := filepath.Clean(outPath)
		if !strings.HasPrefix(clean, targetRoot) {
			return fmt.Errorf("archive entry escapes target directory: %s", file.Name)
		}

		if file.UncompressedSize64 > uint64(maxTemplateBytes) {
			return fmt.Errorf("archive entry too large: %s", file.Name)
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
			out, err := os.OpenFile(clean, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", clean, err)
			}
			defer out.Close()
			if _, err := io.CopyN(out, src, int64(file.UncompressedSize64)); err != nil {
				return fmt.Errorf("failed to write file %s: %w", clean, err)
			}
			return nil
		}(); err != nil {
			return err
		}
	}
	return nil
}

var fetchTemplateToTempDir fetchTemplateToDirFunc = func() (string, error) {
	tempDir, err := os.MkdirTemp("", "vb-template-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	if err := fetchTemplate("", tempDir); err != nil {
		// Clean up on failure
		_ = os.RemoveAll(tempDir)
		return "", err
	}

	return tempDir, nil
}

var fetchTemplateVersionVar fetchTemplateVersionFunc = func() (string, error) {
	resp, err := http.Get(templateVersionURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch template version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d fetching template version", resp.StatusCode)
	}

	versionBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read template version: %w", err)
	}

	return strings.TrimSpace(string(versionBytes)), nil
}

func saveTemplateVersion(targetPath, version string) error {
	versionPath := filepath.Join(targetPath, templateVersionFile)
	return util.WriteFileAtomic(versionPath, []byte(version+"\n"), 0o600)
}

func readTemplateVersion(targetPath string) (string, error) {
	versionPath := filepath.Join(targetPath, templateVersionFile)
	data, err := os.ReadFile(versionPath) // #nosec G304 -- path is from validated directory
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
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

	// Get current version
	currentVersion, err := readTemplateVersion(targetPath)
	if err != nil {
		opts.Logger().WithError(err).Warn("Failed to read current template version")
	}

	// Fetch latest template to temp directory
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "Fetching latest template...")
	}

	tempDir, err := fetchTemplateToTempDir()
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to fetch template: %w", err))
	}
	defer os.RemoveAll(tempDir) // Clean up temp directory

	// Get new version
	newVersion, err := fetchTemplateVersionVar()
	if err != nil {
		opts.Logger().WithError(err).Warn("Failed to fetch new template version")
	}

	// Compare directories
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "Comparing with local .virtualboard/...")
	}

	diff, err := templatediff.CompareDirectories(targetPath, tempDir)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to compare templates: %w", err))
	}

	// Filter files if specified
	if len(fileFilter) > 0 {
		diff = filterDiff(diff, fileFilter)
	}

	// Check if there are changes
	if !diff.HasChanges() {
		msg := "Template is already up to date"
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"current_version": currentVersion,
			"latest_version":  newVersion,
			"changes":         0,
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

	// Interactive update process
	applied, err := applyUpdates(opts, targetPath, diff, autoYes)
	if err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to apply updates: %w", err))
	}

	// Save new template version if any changes were applied
	if applied > 0 && newVersion != "" {
		if err := saveTemplateVersion(targetPath, newVersion); err != nil {
			opts.Logger().WithError(err).Warn("Failed to save new template version")
		}
	}

	msg := fmt.Sprintf("Template update complete. Applied %d change(s)", applied)
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"current_version": currentVersion,
		"new_version":     newVersion,
		"applied":         applied,
		"total_changes":   diff.TotalChanges(),
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
		if opts.JSONOutput {
			// In JSON mode, apply all changes automatically
			if err := applyFileDiff(targetPath, &fd); err != nil {
				return applied, err
			}
			applied++
			continue
		}

		fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
		fmt.Fprintf(os.Stderr, "New file: %s\n", fd.Path)
		fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
		fmt.Fprintln(os.Stderr, string(fd.RemoteContent))
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
		if opts.JSONOutput {
			// In JSON mode, apply all changes automatically
			if err := applyFileDiff(targetPath, &fd); err != nil {
				return applied, err
			}
			applied++
			continue
		}

		fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
		fmt.Fprintf(os.Stderr, "Modified file: %s\n", fd.Path)
		fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
		// Colorize diff output for better readability
		colorizedDiff := util.ColorizeDiff(fd.UnifiedDiff)
		fmt.Fprintln(os.Stderr, colorizedDiff)
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
		if opts.JSONOutput {
			// In JSON mode, apply all changes automatically
			if err := applyFileDiff(targetPath, &fd); err != nil {
				return applied, err
			}
			applied++
			continue
		}

		fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
		fmt.Fprintf(os.Stderr, "Removed file: %s\n", fd.Path)
		fmt.Fprintln(os.Stderr, strings.Repeat("-", 60))
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
	fullPath := filepath.Join(targetPath, fd.Path)

	switch fd.Status {
	case templatediff.FileStatusAdded:
		// Create parent directory if needed
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", fd.Path, err)
		}
		// Write new file
		return util.WriteFileAtomic(fullPath, fd.RemoteContent, 0o600)

	case templatediff.FileStatusModified:
		// Overwrite existing file
		return util.WriteFileAtomic(fullPath, fd.RemoteContent, 0o600)

	case templatediff.FileStatusRemoved:
		// Remove file
		return os.Remove(fullPath)

	default:
		return fmt.Errorf("unknown file status: %s", fd.Status)
	}
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

By default, creates a new .virtualboard/ directory with the latest template.
Use --update to update an existing workspace to the latest template version.
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
			if exists, err := pathExists(targetPath); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			} else if exists && !force {
				detail := fmt.Sprintf("VirtualBoard workspace already initialised at %s. Use --force to re-create it or --update to update it. We recommend managing this directory with git.", initDirName)
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

			if force {
				if err := os.RemoveAll(targetPath); err != nil {
					return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to remove existing workspace: %w", err))
				}
			}

			if err := fetchTemplate(projectRoot, initDirName); err != nil {
				return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to prepare template: %w", err))
			}

			// Save template version
			version, err := fetchTemplateVersionVar()
			if err != nil {
				opts.Logger().WithError(err).Warn("Failed to fetch template version")
			} else if err := saveTemplateVersion(targetPath, version); err != nil {
				opts.Logger().WithError(err).Warn("Failed to save template version")
			}

			msg := fmt.Sprintf("VirtualBoard project initialised in %s. Review the files under %s.", initDirName, initDirName)
			return respond(cmd, opts, true, msg, map[string]interface{}{
				"path":    initDirName,
				"source":  templateZipURL,
				"version": version,
			})
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Recreate the VirtualBoard workspace even if it already exists")
	cmd.Flags().BoolVar(&update, "update", false, "Update existing workspace to latest template version")
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
