package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	tpl "github.com/virtualboard/vb-cli/internal/template"
)

func newTemplateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Template operations",
	}
	cmd.AddCommand(newTemplateApplyCommand())
	return cmd
}

func newTemplateApplyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "apply <id>",
		Short: "Re-apply the feature template to ensure required sections",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			id := args[0]
			mgr := feature.NewManager(opts)
			feat, err := mgr.LoadByID(id)
			if err != nil {
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			processor, err := tpl.NewProcessor(mgr)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if err := processor.Apply(feat); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if err := mgr.Save(feat); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			message := fmt.Sprintf("Template applied to %s", id)
			data := map[string]interface{}{
				"id": id,
			}
			return respond(cmd, opts, true, message, data)
		},
	}
}
