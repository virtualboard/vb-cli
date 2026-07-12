package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	tpl "github.com/virtualboard/vb-cli/internal/template"
	"github.com/virtualboard/vb-cli/internal/validator"
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
			processor, err := tpl.NewProcessor(mgr)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			candidateValidator, err := validator.New(opts, mgr)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			var processorErr, candidateErr error
			feat, err := mgr.MutateFeature(id, func(feat *feature.Feature) error {
				if err := processor.Apply(feat); err != nil {
					processorErr = err
					return err
				}
				feat.UpdateTimestamp()
				if err := candidateValidator.ValidateCandidate(feat); err != nil {
					candidateErr = err
					return err
				}
				return nil
			})
			if err != nil {
				if processorErr != nil {
					return WrapCLIError(ExitCodeFilesystem, processorErr)
				}
				if candidateErr != nil {
					return WrapCLIError(ExitCodeValidation, candidateErr)
				}
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return wrapMutationError(err)
			}
			mgr.RecordAudit("template-apply", feat.FrontMatter.ID, "required sections reconciled")

			message := fmt.Sprintf("Template applied to %s", id)
			if opts.DryRun {
				message = fmt.Sprintf("Dry-run: would apply template to %s", id)
			}
			data := map[string]interface{}{
				"id":      id,
				"dry_run": opts.DryRun,
				"written": !opts.DryRun,
			}
			return respond(cmd, opts, true, message, data)
		},
	}
}
