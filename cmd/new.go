package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
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
			feat, err := manager.CreateFeature(title, labels)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			rel, _ := filepath.Rel(opts.RootDir, feat.Path)
			message := fmt.Sprintf("Created feature %s at %s", feat.FrontMatter.ID, rel)
			data := map[string]interface{}{
				"id":     feat.FrontMatter.ID,
				"path":   rel,
				"title":  feat.FrontMatter.Title,
				"labels": feat.FrontMatter.Labels,
			}
			if err := respond(cmd, opts, true, message, data); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}
