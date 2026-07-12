package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/indexer"
	"github.com/virtualboard/vb-cli/internal/util"
)

func newIndexCommand() *cobra.Command {
	var format string
	var output string
	var verbosity int
	var quiet bool
	var check bool

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Generate the features index",
		Long: `Generate a deterministic feature index from current feature state.

Use --check to compare the canonical output with an existing file without
writing. A missing or different target exits with validation status.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			format = strings.ToLower(strings.TrimSpace(format))
			if format == "" {
				format = "md"
			}

			target := output
			mgr := feature.NewManager(opts)
			if _, err := mgr.Lifecycle(); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if target == "" && (format == "md" || format == "markdown") {
				target, err = filepath.Rel(opts.RootDir, mgr.IndexPath())
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("resolve canonical index path: %w", err))
				}
				target = filepath.ToSlash(target)
			}
			if check && (target == "" || target == "-") {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--check requires a file output target"))
			}

			return mgr.WithBoardGraphSnapshot(func() error {
				gen := indexer.NewGenerator(mgr)
				data, err := gen.Build()
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}

				// Read the target once for semantic change reporting and exact drift
				// detection. Fail closed on read errors other than a missing file.
				var oldData *indexer.Data
				var diff *indexer.Diff
				var existingContent []byte
				targetMissing := false
				var absPath string
				var relPath string
				if target != "" && target != "-" {
					absPath, relPath, err = resolveWorkspaceWritePath(opts.RootDir, target)
					if err != nil {
						return WrapCLIError(ExitCodeValidation, err)
					}
					// #nosec G304 -- absPath is scoped to opts.RootDir which is validated during Init
					existingContent, err = os.ReadFile(absPath)
					if err != nil {
						if !os.IsNotExist(err) {
							return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to read existing index %s: %w", relPath, err))
						}
						targetMissing = true
						existingContent = nil
					}
				}
				if format == "md" || format == "markdown" {
					if !targetMissing && existingContent != nil {
						oldData, _ = indexer.ParseMarkdown(string(existingContent))
					}
					diff = indexer.ComputeDiff(oldData, data)
				}

				var content string
				switch format {
				case "md", "markdown":
					content, err = gen.Markdown(data)
				case "json":
					content, err = gen.JSON(data)
				case "html":
					content, err = gen.HTML(data)
				default:
					return WrapCLIError(ExitCodeValidation, fmt.Errorf("unknown format %s", format))
				}
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}

				hasFileTarget := target != "" && target != "-"
				drift := hasFileTarget && (targetMissing || !bytes.Equal(existingContent, []byte(content)))
				if check {
					payload := map[string]interface{}{
						"format":    format,
						"generated": data.Generated,
						"total":     len(data.Features),
						"path":      relPath,
						"checked":   true,
						"written":   false,
						"drift":     drift,
						"missing":   targetMissing,
						"dry_run":   opts.DryRun,
					}
					if diff != nil {
						payload["changes"] = indexChangesPayload(diff)
					}
					if drift {
						message := fmt.Sprintf("Index drift detected at %s", relPath)
						if targetMissing {
							message = fmt.Sprintf("Index is missing at %s", relPath)
						}
						if err := respond(cmd, opts, false, message, payload); err != nil {
							return err
						}
						return WrapCLIError(ExitCodeValidation, fmt.Errorf("index check failed for %s", relPath))
					}
					if quiet && !opts.JSONOutput {
						return nil
					}
					return respond(cmd, opts, true, fmt.Sprintf("Index is up to date at %s", relPath), payload)
				}

				if opts.JSONOutput {
					payload := map[string]interface{}{
						"format":    format,
						"generated": data.Generated,
						"total":     len(data.Features),
						"dry_run":   opts.DryRun,
						"checked":   false,
						"drift":     drift,
						"missing":   targetMissing,
					}
					if diff != nil {
						payload["changes"] = indexChangesPayload(diff)
					}
					if hasFileTarget {
						payload["path"] = relPath
						payload["written"] = !opts.DryRun
						if !opts.DryRun {
							if err := util.WriteFileAtomic(absPath, []byte(content), 0o644); err != nil {
								return WrapCLIError(ExitCodeFilesystem, err)
							}
						}
					} else {
						payload["written"] = false
						payload["content"] = content
					}
					return respond(cmd, opts, true, "index generated", payload)
				}

				if target == "" || target == "-" {
					fmt.Fprint(cmd.OutOrStdout(), content)
					return nil
				}

				if opts.DryRun {
					message := fmt.Sprintf("Dry-run: index would be written to %s", relPath)
					return respond(cmd, opts, true, message, map[string]interface{}{
						"format":  format,
						"path":    relPath,
						"checked": false,
						"written": false,
						"drift":   drift,
						"missing": targetMissing,
						"dry_run": true,
					})
				}

				// Write the file
				if err := util.WriteFileAtomic(absPath, []byte(content), 0o644); err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}

				// Handle quiet mode - only output if there are changes
				if quiet && diff != nil && !diff.HasChanges() {
					return nil
				}

				// Format output based on verbosity level
				message := formatIndexOutput(diff, data, relPath, verbosity)

				dataMap := map[string]interface{}{
					"format":  format,
					"path":    relPath,
					"checked": false,
					"written": true,
					"drift":   drift,
					"missing": targetMissing,
					"dry_run": false,
				}
				if diff != nil {
					dataMap["changes"] = indexChangesPayload(diff)
				}
				if err := respond(cmd, opts, true, message, dataMap); err != nil {
					return err
				}
				return nil
			})
		},
	}

	cmd.Flags().StringVar(&format, "format", "md", "Index format: md, json, html")
	cmd.Flags().StringVar(&output, "output", "", "Output destination (default: <configured features>/INDEX.md for md format)")
	cmd.Flags().CountVarP(&verbosity, "verbose", "v", "Increase verbosity level (-v, -vv)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only output if there are changes")
	cmd.Flags().BoolVar(&check, "check", false, "Fail without writing when the generated index differs from the target")
	return cmd
}

func indexChangesPayload(diff *indexer.Diff) map[string]interface{} {
	return map[string]interface{}{
		"added":   diff.Added,
		"removed": diff.Removed,
		"changed": diff.Changed,
	}
}

// formatIndexOutput formats the output message based on verbosity level and changes
func formatIndexOutput(diff *indexer.Diff, data *indexer.Data, relPath string, verbosity int) string {
	var b strings.Builder

	// If no diff available (non-markdown format), show simple message
	if diff == nil {
		b.WriteString(fmt.Sprintf("Index written to %s\n", relPath))
		return b.String()
	}

	// Default output (verbosity 0): Summary only
	if verbosity == 0 {
		if !diff.HasChanges() {
			b.WriteString(fmt.Sprintf("✓ No changes (%d features indexed)\n", len(data.Features)))
		} else {
			b.WriteString(fmt.Sprintf("✓ %s\n", diff.FormatSummary()))
			// Show status summary
			b.WriteString(fmt.Sprintf("Total: %d features (", len(data.Features)))
			b.WriteString(formatStatusSummary(data.Summary))
			b.WriteString(")\n")
		}
		b.WriteString(fmt.Sprintf("\nIndex written to %s", relPath))
		return b.String()
	}

	// Verbose output (-v): Show feature IDs with changes
	if verbosity == 1 {
		if !diff.HasChanges() {
			b.WriteString(fmt.Sprintf("✓ No changes detected (%d features indexed)\n\n", len(data.Features)))
		} else {
			b.WriteString(fmt.Sprintf("✓ Changes detected: %s\n\n", diff.FormatSummary()))
			b.WriteString(diff.FormatVerbose())
			b.WriteString("\n")
		}
		// Show status summary
		b.WriteString(fmt.Sprintf("\nTotal: %d features (", len(data.Features)))
		b.WriteString(formatStatusSummary(data.Summary))
		b.WriteString(")\n")
		b.WriteString(fmt.Sprintf("Index written to %s", relPath))
		return b.String()
	}

	// Very verbose output (-vv): Show detailed changes
	b.WriteString("Changes:\n\n")
	b.WriteString(diff.FormatVeryVerbose())
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Total: %d features (", len(data.Features)))
	b.WriteString(formatStatusSummary(data.Summary))
	b.WriteString(")\n")
	b.WriteString(fmt.Sprintf("Index written to %s", relPath))
	return b.String()
}

func formatStatusSummary(summary map[string]int) string {
	statuses := make([]string, 0, len(summary))
	for status := range summary {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%d %s", summary[status], status))
	}
	return strings.Join(parts, ", ")
}
