package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/spec"
	tpl "github.com/virtualboard/vb-cli/internal/template"
	"github.com/virtualboard/vb-cli/internal/validator"
)

func newValidateCommand() *cobra.Command {
	var fix bool
	var onlyFeatures bool
	var onlySpecs bool

	cmd := &cobra.Command{
		Use:   "validate [id|name|all]",
		Short: "Validate feature specs and system specs against their schemas",
		Long: `Validate feature specs and system specs against their respective JSON schemas.

By default, validates both features and specs. Use --only-features or --only-specs to validate specific types.

Examples:
  vb validate                    # Validate all features and specs
  vb validate --only-features    # Validate only features
  vb validate --only-specs       # Validate only specs
  vb validate FEAT-001           # Validate specific feature
  vb validate tech-stack.md      # Validate specific spec`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			// Validate mutually exclusive flags
			if onlyFeatures && onlySpecs {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--only-features and --only-specs are mutually exclusive"))
			}

			target := "all"
			if len(args) == 1 {
				target = args[0]
			}

			// Determine what to validate based on flags and target
			validateFeatures := !onlySpecs
			validateSpecs := !onlyFeatures

			// If a specific target is provided, determine its type
			isSpecificTarget := target != "all"
			isFeatureID := isSpecificTarget && strings.HasPrefix(strings.ToUpper(target), "FTR-")
			isSpecName := isSpecificTarget && (strings.HasSuffix(target, ".md") || (!isFeatureID && target != "all"))

			if isSpecificTarget {
				if isFeatureID {
					validateSpecs = false
					validateFeatures = true
				} else if isSpecName {
					validateFeatures = false
					validateSpecs = true
				}
			}

			var featureSummary *validator.Summary
			var specSummary *spec.Summary
			var totalErrors int

			// Validate features
			if validateFeatures {
				mgr := feature.NewManager(opts)
				v, err := validator.New(opts, mgr)
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}

				if fix {
					ids := []string{}
					if isFeatureID {
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

				if isFeatureID {
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
				}

				featureSummary, err = v.ValidateAll()
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				totalErrors += featureSummary.Invalid
			}

			// Validate specs
			if validateSpecs {
				specMgr := spec.NewManager(opts)
				specValidator, err := spec.NewValidator(opts, specMgr)
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}

				if isSpecName {
					result, err := specValidator.ValidateName(target)
					if err != nil {
						if errors.Is(err, spec.ErrNotFound) {
							return WrapCLIError(ExitCodeNotFound, err)
						}
						return WrapCLIError(ExitCodeFilesystem, err)
					}

					if opts.JSONOutput {
						payload := map[string]interface{}{
							"name":   target,
							"errors": result.Errors,
							"status": result.Spec.FrontMatter.Status,
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
						"name": target,
					}
					return respond(cmd, opts, true, message, data)
				}

				specSummary, err = specValidator.ValidateAll()
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				totalErrors += specSummary.Invalid
			}

			// Handle combined summary output
			if opts.JSONOutput {
				payload := buildCombinedPayload(featureSummary, specSummary, fix, target)
				return respond(cmd, opts, totalErrors == 0, "validation complete", payload)
			}

			// Print failures
			if featureSummary != nil && featureSummary.HasErrors() {
				fmt.Fprintf(cmd.OutOrStdout(), "Feature validation failures:\n")
				printFeatureSummaryFailures(cmd, featureSummary)
			}
			if specSummary != nil && specSummary.HasErrors() {
				fmt.Fprintf(cmd.OutOrStdout(), "Spec validation failures:\n")
				printSpecSummaryFailures(cmd, specSummary)
			}

			if totalErrors > 0 {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("validation failed with %d error(s)", totalErrors))
			}

			// Build success message
			var messageParts []string
			if featureSummary != nil {
				messageParts = append(messageParts, fmt.Sprintf("%d features", featureSummary.Total))
			}
			if specSummary != nil {
				messageParts = append(messageParts, fmt.Sprintf("%d specs", specSummary.Total))
			}
			message := fmt.Sprintf("Validated %s", strings.Join(messageParts, " and "))

			data := map[string]interface{}{
				"fix_applied": fix,
			}
			if featureSummary != nil {
				data["features"] = map[string]interface{}{
					"total":   featureSummary.Total,
					"valid":   featureSummary.Valid,
					"invalid": featureSummary.Invalid,
				}
			}
			if specSummary != nil {
				data["specs"] = map[string]interface{}{
					"total":   specSummary.Total,
					"valid":   specSummary.Valid,
					"invalid": specSummary.Invalid,
				}
			}

			return respond(cmd, opts, true, message, data)
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "Apply safe fixes before validating (features only)")
	cmd.Flags().BoolVar(&onlyFeatures, "only-features", false, "Validate only feature specs")
	cmd.Flags().BoolVar(&onlySpecs, "only-specs", false, "Validate only system specs")
	return cmd
}

func printFeatureSummaryFailures(cmd *cobra.Command, summary *validator.Summary) {
	ids := make([]string, 0)
	for id, res := range summary.Results {
		if len(res.Errors) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := cmd.OutOrStdout()
	for _, id := range ids {
		fmt.Fprintf(out, "  %s:\n", id)
		for _, msg := range summary.Results[id].Errors {
			fmt.Fprintf(out, "    - %s\n", msg)
		}
	}
}

func printSpecSummaryFailures(cmd *cobra.Command, summary *spec.Summary) {
	names := make([]string, 0)
	for name, res := range summary.Results {
		if len(res.Errors) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := cmd.OutOrStdout()
	for _, name := range names {
		fmt.Fprintf(out, "  %s:\n", name)
		for _, msg := range summary.Results[name].Errors {
			fmt.Fprintf(out, "    - %s\n", msg)
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

func buildCombinedPayload(featureSummary *validator.Summary, specSummary *spec.Summary, fix bool, target string) map[string]interface{} {
	payload := map[string]interface{}{
		"target":      target,
		"fix_applied": fix,
	}

	if featureSummary != nil {
		featureResults := make(map[string]interface{})
		for id, res := range featureSummary.Results {
			featureResults[id] = map[string]interface{}{
				"errors": res.Errors,
			}
			if res.Feature != nil {
				featureResults[id].(map[string]interface{})["status"] = res.Feature.FrontMatter.Status
			}
		}
		payload["features"] = map[string]interface{}{
			"total":   featureSummary.Total,
			"valid":   featureSummary.Valid,
			"invalid": featureSummary.Invalid,
			"results": featureResults,
		}
	}

	if specSummary != nil {
		specResults := make(map[string]interface{})
		for name, res := range specSummary.Results {
			specResults[name] = map[string]interface{}{
				"errors": res.Errors,
			}
			if res.Spec != nil {
				specResults[name].(map[string]interface{})["status"] = res.Spec.FrontMatter.Status
			}
		}
		payload["specs"] = map[string]interface{}{
			"total":   specSummary.Total,
			"valid":   specSummary.Valid,
			"invalid": specSummary.Invalid,
			"results": specResults,
		}
	}

	return payload
}
