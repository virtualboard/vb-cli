package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/validator"
)

func newUpdateCommand() *cobra.Command {
	var fieldPairs []string
	var sectionPairs []string

	cmd := &cobra.Command{
		Use:   "update <id> [--field key=value ...] [--body-section section=content ...]",
		Short: "Update frontmatter fields or body sections of a feature",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			id := args[0]
			if len(fieldPairs) == 0 && len(sectionPairs) == 0 {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("no updates provided"))
			}

			mgr := feature.NewManager(opts)
			candidateValidator, err := validator.New(opts, mgr)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			var candidateErr error
			feat, err := mgr.MutateFeature(id, func(feat *feature.Feature) error {
				for _, pair := range fieldPairs {
					key, value, err := splitPair(pair)
					if err != nil {
						candidateErr = err
						return err
					}
					if err := feat.SetField(key, value); err != nil {
						candidateErr = err
						return err
					}
				}

				for _, pair := range sectionPairs {
					key, value, err := splitPair(pair)
					if err != nil {
						candidateErr = err
						return err
					}
					if err := feat.SetSection(key, value); err != nil {
						candidateErr = err
						return err
					}
				}

				feat.UpdateTimestamp()
				if err := candidateValidator.ValidateCandidate(feat); err != nil {
					candidateErr = err
					return err
				}
				return nil
			})
			if err != nil {
				if candidateErr != nil {
					return WrapCLIError(ExitCodeValidation, candidateErr)
				}
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return wrapMutationError(err)
			}
			mgr.RecordAudit("update", feat.FrontMatter.ID, fmt.Sprintf("fields=%d sections=%d", len(fieldPairs), len(sectionPairs)))

			message := fmt.Sprintf("Updated feature %s", feat.FrontMatter.ID)
			if opts.DryRun {
				message = fmt.Sprintf("Dry-run: would update feature %s", feat.FrontMatter.ID)
			}
			data := map[string]interface{}{
				"id":       feat.FrontMatter.ID,
				"fields":   fieldPairs,
				"sections": sectionPairs,
				"dry_run":  opts.DryRun,
				"written":  !opts.DryRun,
			}
			if err := respond(cmd, opts, true, message, data); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&fieldPairs, "field", nil, "Frontmatter field to update (key=value)")
	cmd.Flags().StringArrayVar(&sectionPairs, "body-section", nil, "Body section to update (name=content)")
	return cmd
}

func splitPair(input string) (string, string, error) {
	parts := strings.SplitN(input, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s (expected key=value)", input)
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	if key == "" {
		return "", "", fmt.Errorf("empty key in pair: %s", input)
	}
	return key, value, nil
}
