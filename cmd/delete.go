package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
)

func newDeleteCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an unreferenced backlog feature spec",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			id := args[0]
			mgr := feature.NewManager(opts)
			plan, err := mgr.PrepareDelete(id)
			if err != nil {
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if opts.JSONOutput && !force && !opts.DryRun {
				if responseErr := respond(cmd, opts, false, "JSON deletion requires --force; no file was changed", map[string]interface{}{
					"id":      id,
					"deleted": false,
					"dry_run": false,
				}); responseErr != nil {
					return responseErr
				}
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--force is required for non-interactive JSON deletion"))
			}

			if !force && !opts.DryRun {
				prompt := fmt.Sprintf("Delete feature %s? Type 'yes' to confirm: ", id)
				fmt.Fprint(cmd.OutOrStdout(), prompt)
				reader := bufio.NewReader(cmd.InOrStdin())
				input, err := reader.ReadString('\n')
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("confirmation failed: %w", err))
				}
				if strings.TrimSpace(input) != "yes" {
					if err := respond(cmd, opts, true, "Deletion cancelled; no file was changed", map[string]interface{}{
						"id":        id,
						"deleted":   false,
						"cancelled": true,
					}); err != nil {
						return err
					}
					return nil
				}
			}

			path, err := mgr.DeleteFeaturePlanned(plan)
			if err != nil {
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return wrapMutationError(err)
			}

			rel, _ := filepath.Rel(opts.RootDir, path)
			message := fmt.Sprintf("Deleted feature %s", id)
			if opts.DryRun {
				message = fmt.Sprintf("Dry-run: would delete feature %s", id)
			}
			data := map[string]interface{}{
				"id":      id,
				"path":    rel,
				"deleted": !opts.DryRun,
				"dry_run": opts.DryRun,
			}
			if err := respond(cmd, opts, true, message, data); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Delete without confirmation")
	return cmd
}
