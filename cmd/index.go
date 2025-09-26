package cmd

import (
	"fmt"
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

			if err := util.WriteFileAtomic(absPath, []byte(content), 0o644); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			message := fmt.Sprintf("Index written to %s", relPath)
			dataMap := map[string]interface{}{
				"format":  format,
				"path":    relPath,
				"written": true,
			}
			if err := respond(cmd, opts, true, message, dataMap); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "md", "Index format: md, json, html")
	cmd.Flags().StringVar(&output, "output", "", "Output destination (default: features/INDEX.md for md format)")
	return cmd
}
