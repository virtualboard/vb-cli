package cmd

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
)

func newMoveCommand() *cobra.Command {
	var ownerFlag string
	cmd := &cobra.Command{
		Use:   "move <id> <status> [owner]",
		Short: "Move a feature to a new status and optionally assign an owner",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("requires feature id and status")
			}
			if len(args) > 3 {
				return fmt.Errorf("too many arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			id := args[0]
			status := args[1]
			owner := ownerFlag
			if owner == "" && len(args) == 3 {
				owner = args[2]
			}

			mgr := feature.NewManager(opts)
			feat, summary, err := mgr.MoveFeature(id, status, owner)
			if err != nil {
				switch {
				case errors.Is(err, feature.ErrNotFound):
					return WrapCLIError(ExitCodeNotFound, err)
				case errors.Is(err, feature.ErrInvalidTransition):
					return WrapCLIError(ExitCodeInvalidTransition, err)
				case errors.Is(err, feature.ErrDependencyBlocked):
					return WrapCLIError(ExitCodeDependency, err)
				default:
					return WrapCLIError(ExitCodeFilesystem, err)
				}
			}

			rel, _ := filepath.Rel(opts.RootDir, feat.Path)
			data := map[string]interface{}{
				"id":      feat.FrontMatter.ID,
				"status":  feat.FrontMatter.Status,
				"owner":   feat.FrontMatter.Owner,
				"path":    rel,
				"summary": summary,
			}
			if err := respond(cmd, opts, true, summary, data); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ownerFlag, "owner", "", "Set the owner while moving")
	return cmd
}
