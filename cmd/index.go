package cmd

import (
	"fmt"
	"os"
	"path/filepath"
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

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Generate the features index",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			format = strings.ToLower(format)
			if format == "" {
				format = "md"
			}

			target := output
			if target == "" && format == "md" {
				target = filepath.Join("features", "INDEX.md")
			}

			mgr := feature.NewManager(opts)
			gen := indexer.NewGenerator(mgr)
			data, err := gen.Build()
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			// Read and parse old index for change detection (only for markdown format)
			var oldData *indexer.Data
			var diff *indexer.Diff
			if format == "md" || format == "markdown" {
				if target != "" && target != "-" {
					absPath := filepath.Join(opts.RootDir, target)
					// #nosec G304 -- absPath is scoped to opts.RootDir which is validated during Init
					if oldContent, err := os.ReadFile(absPath); err == nil {
						oldData, _ = indexer.ParseMarkdown(string(oldContent))
					}
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

			if opts.JSONOutput {
				payload := map[string]interface{}{
					"format":    format,
					"generated": data.Generated,
					"total":     len(data.Features),
				}
				if diff != nil {
					payload["changes"] = map[string]interface{}{
						"added":   diff.Added,
						"removed": diff.Removed,
						"changed": diff.Changed,
					}
				}
				if target != "" && target != "-" {
					absPath := filepath.Join(opts.RootDir, target)
					relPath, _ := filepath.Rel(opts.RootDir, absPath)
					payload["path"] = relPath
					payload["written"] = true
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

			absPath := filepath.Join(opts.RootDir, target)
			relPath, _ := filepath.Rel(opts.RootDir, absPath)
			if opts.DryRun {
				message := fmt.Sprintf("Dry-run: index would be written to %s", relPath)
				return respond(cmd, opts, true, message, map[string]interface{}{
					"format":  format,
					"path":    relPath,
					"written": false,
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
				"written": true,
			}
			if diff != nil {
				dataMap["changes"] = map[string]interface{}{
					"added":   diff.Added,
					"removed": diff.Removed,
					"changed": diff.Changed,
				}
			}
			if err := respond(cmd, opts, true, message, dataMap); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "md", "Index format: md, json, html")
	cmd.Flags().StringVar(&output, "output", "", "Output destination (default: features/INDEX.md for md format)")
	cmd.Flags().CountVarP(&verbosity, "verbose", "v", "Increase verbosity level (-v, -vv)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Only output if there are changes")
	return cmd
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
			statuses := []string{}
			for status, count := range data.Summary {
				statuses = append(statuses, fmt.Sprintf("%d %s", count, status))
			}
			b.WriteString(strings.Join(statuses, ", "))
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
		statuses := []string{}
		for status, count := range data.Summary {
			statuses = append(statuses, fmt.Sprintf("%d %s", count, status))
		}
		b.WriteString(strings.Join(statuses, ", "))
		b.WriteString(")\n")
		b.WriteString(fmt.Sprintf("Index written to %s", relPath))
		return b.String()
	}

	// Very verbose output (-vv): Show detailed changes
	b.WriteString("Changes:\n\n")
	b.WriteString(diff.FormatVeryVerbose())
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Total: %d features (", len(data.Features)))
	statuses := []string{}
	for status, count := range data.Summary {
		statuses = append(statuses, fmt.Sprintf("%d %s", count, status))
	}
	b.WriteString(strings.Join(statuses, ", "))
	b.WriteString(")\n")
	b.WriteString(fmt.Sprintf("Index written to %s", relPath))
	return b.String()
}
