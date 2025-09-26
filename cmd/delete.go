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
		Short: "Delete a feature spec",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			id := args[0]

			if !force {
				prompt := fmt.Sprintf("Delete feature %s? Type 'yes' to confirm: ", id)
				fmt.Fprint(cmd.OutOrStdout(), prompt)
				reader := bufio.NewReader(cmd.InOrStdin())
				input, err := reader.ReadString('\n')
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("confirmation failed: %w", err))
				}
				if strings.TrimSpace(input) != "yes" {
					if err := respond(cmd, opts, false, "Deletion cancelled", nil); err != nil {
						return err
					}
					return nil
				}
			}

			mgr := feature.NewManager(opts)
			path, err := mgr.DeleteFeature(id)
			if err != nil {
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			rel, _ := filepath.Rel(opts.RootDir, path)
			message := fmt.Sprintf("Deleted feature %s", id)
			data := map[string]interface{}{
				"id":   id,
				"path": rel,
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
