package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/validator"
)

func newNewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <title> [labels...]",
		Short: "Create a new feature spec in backlog",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			title := args[0]
			labels := []string{}
			if len(args) > 1 {
				labels = args[1:]
			}

			manager := feature.NewManager(opts)
			candidateValidator, err := validator.New(opts, manager)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			feat, err := manager.CreateFeatureValidated(title, labels, candidateValidator.ValidateCandidate)
			if err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}

			rel, _ := filepath.Rel(opts.RootDir, feat.Path)
			message := fmt.Sprintf("Created feature %s at %s", feat.FrontMatter.ID, rel)
			if opts.DryRun {
				message = fmt.Sprintf("Dry-run: would create feature %s at %s", feat.FrontMatter.ID, rel)
			}
			data := map[string]interface{}{
				"id":      feat.FrontMatter.ID,
				"path":    rel,
				"title":   feat.FrontMatter.Title,
				"labels":  feat.FrontMatter.Labels,
				"dry_run": opts.DryRun,
				"written": !opts.DryRun,
			}
			if err := respond(cmd, opts, true, message, data); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}
