package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
)

func newUpdateCommand() *cobra.Command {
	var fieldPairs []string
	var sectionPairs []string

	cmd := &cobra.Command{
		Use:   "update <id> [--field key=value ...] [--body-section section=content ...]",
		Short: "Update frontmatter fields or body sections of a feature",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			id := args[0]
			if len(fieldPairs) == 0 && len(sectionPairs) == 0 {
				return fmt.Errorf("no updates provided")
			}

			mgr := feature.NewManager(opts)
			feat, err := mgr.LoadByID(id)
			if err != nil {
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			for _, pair := range fieldPairs {
				key, value, err := splitPair(pair)
				if err != nil {
					return WrapCLIError(ExitCodeValidation, err)
				}
				if err := feat.SetField(key, value); err != nil {
					return WrapCLIError(ExitCodeValidation, err)
				}
			}

			for _, pair := range sectionPairs {
				key, value, err := splitPair(pair)
				if err != nil {
					return WrapCLIError(ExitCodeValidation, err)
				}
				if err := feat.SetSection(key, value); err != nil {
					return WrapCLIError(ExitCodeValidation, err)
				}
			}

			if err := mgr.UpdateFeature(feat); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			message := fmt.Sprintf("Updated feature %s", feat.FrontMatter.ID)
			data := map[string]interface{}{
				"id":       feat.FrontMatter.ID,
				"fields":   fieldPairs,
				"sections": sectionPairs,
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
