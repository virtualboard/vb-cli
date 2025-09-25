package cmd

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	tpl "github.com/virtualboard/vb-cli/internal/template"
	"github.com/virtualboard/vb-cli/internal/validator"
)

func newValidateCommand() *cobra.Command {
	var fix bool

	cmd := &cobra.Command{
		Use:   "validate [id|all]",
		Short: "Validate feature specs against schema and workflow rules",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			target := "all"
			if len(args) == 1 {
				target = args[0]
			}

			mgr := feature.NewManager(opts)
			v, err := validator.New(opts, mgr)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			if fix {
				ids := []string{}
				if target != "all" {
					ids = []string{target}
				}
				processor, err := tpl.NewProcessor(mgr)
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				feats, err := v.CollectFeatures(ids...)
				if err != nil {
					if errors.Is(err, feature.ErrNotFound) {
						return WrapCLIError(ExitCodeNotFound, err)
					}
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				if err := v.ApplyFixes(feats, processor.Apply); err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}
			}

			if target == "all" {
				summary, err := v.ValidateAll()
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}

				if opts.JSONOutput {
					payload := buildSummaryPayload(summary, fix, target)
					return respond(cmd, opts, !summary.HasErrors(), "validation complete", payload)
				}

				if summary.HasErrors() {
					printSummaryFailures(cmd, summary)
					return WrapCLIError(ExitCodeValidation, fmt.Errorf("validation failed for %d features", summary.Invalid))
				}

				message := fmt.Sprintf("Validated %d features", summary.Total)
				data := map[string]interface{}{
					"total":       summary.Total,
					"invalid":     summary.Invalid,
					"fix_applied": fix,
				}
				return respond(cmd, opts, true, message, data)
			}

			result, err := v.ValidateID(target)
			if err != nil {
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			if opts.JSONOutput {
				payload := map[string]interface{}{
					"id":          target,
					"errors":      result.Errors,
					"status":      result.Feature.FrontMatter.Status,
					"fix_applied": fix,
				}
				success := len(result.Errors) == 0
				return respond(cmd, opts, success, "validation complete", payload)
			}

			if len(result.Errors) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", target)
				for _, msg := range result.Errors {
					fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", msg)
				}
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("validation failed for %s", target))
			}

			message := fmt.Sprintf("%s passed validation", target)
			data := map[string]interface{}{
				"id":          target,
				"fix_applied": fix,
			}
			return respond(cmd, opts, true, message, data)
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "Apply safe fixes before validating")
	return cmd
}

func printSummaryFailures(cmd *cobra.Command, summary *validator.Summary) {
	ids := make([]string, 0)
	for id, res := range summary.Results {
		if len(res.Errors) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := cmd.OutOrStdout()
	for _, id := range ids {
		fmt.Fprintf(out, "%s:\n", id)
		for _, msg := range summary.Results[id].Errors {
			fmt.Fprintf(out, "  - %s\n", msg)
		}
	}
}

func buildSummaryPayload(summary *validator.Summary, fix bool, target string) map[string]interface{} {
	results := make(map[string]interface{})
	for id, res := range summary.Results {
		results[id] = map[string]interface{}{
			"errors": res.Errors,
		}
		if res.Feature != nil {
			results[id].(map[string]interface{})["status"] = res.Feature.FrontMatter.Status
		}
	}
	return map[string]interface{}{
		"target":      target,
		"total":       summary.Total,
		"invalid":     summary.Invalid,
		"valid":       summary.Valid,
		"results":     results,
		"fix_applied": fix,
	}
}
