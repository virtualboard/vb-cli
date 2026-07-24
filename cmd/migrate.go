package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/migration"
)

func newMigrateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run explicit, preflighted workspace migrations",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newMigrateLifecycleMetadataCommand())
	return cmd
}

func newMigrateLifecycleMetadataCommand() *cobra.Command {
	var implementationOwnerFlags []string
	var statusChangedFlags []string
	var force bool
	cmd := &cobra.Command{
		Use:   "lifecycle-metadata",
		Short: "Repair implementation_owner and status_changed on legacy features",
		Long: `Repair lifecycle provenance without guessing ambiguous review/done history.

The command preflights every feature before writing any file. Backlog metadata
can be derived from unassigned/created, and in-progress or blocked implementation
ownership can be derived from a concrete current owner. Review/done ownership
and every non-backlog status_changed value require explicit mappings.

--force is an administrative owner override for multi-owner legacy boards. It
never bypasses active locks, still requires an explicit actor, and is recorded
in the canonical audit log.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			actor, err := requireWorkspaceActor(opts)
			if err != nil {
				return wrapMutationError(err)
			}
			implementationOwners, err := parseMigrationMappings(implementationOwnerFlags, "--implementation-owner")
			if err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}
			statusChanged, err := parseMigrationMappings(statusChangedFlags, "--status-changed")
			if err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}

			mgr := feature.NewManager(opts)
			plan, err := migration.PreflightLifecycleMetadata(opts, mgr, migration.Assignments{
				ImplementationOwner: implementationOwners,
				StatusChanged:       statusChanged,
				AllowOwnerOverride:  force,
			})
			if err != nil {
				if errors.Is(err, migration.ErrPreflight) {
					if opts.JSONOutput {
						if responseErr := respond(cmd, opts, false, "lifecycle metadata migration preflight failed", map[string]interface{}{
							"actor": actor,
							"error": err.Error(),
						}); responseErr != nil {
							return responseErr
						}
					}
					return WrapCLIError(ExitCodeValidation, err)
				}
				return wrapMutationError(err)
			}
			if err := plan.Apply(opts.DryRun); err != nil {
				return wrapMutationError(err)
			}
			if !opts.DryRun {
				for _, change := range plan.Changes {
					mgr.RecordAudit("migrate-lifecycle-metadata", change.Feature.FrontMatter.ID,
						fmt.Sprintf("fields=%s administrative_override=%t", strings.Join(change.Fields, ","), force))
				}
			}
			message := fmt.Sprintf("Lifecycle metadata migration complete: %d of %d feature(s) changed", len(plan.Changes), plan.Total)
			if opts.DryRun {
				message = fmt.Sprintf("Dry-run: %d of %d feature(s) would change", len(plan.Changes), plan.Total)
			}
			return respond(cmd, opts, true, message, map[string]interface{}{
				"actor":                   actor,
				"total":                   plan.Total,
				"changed":                 len(plan.Changes),
				"features":                plan.IDs(),
				"dry_run":                 opts.DryRun,
				"administrative_override": force,
			})
		},
	}
	cmd.Flags().StringArrayVar(&implementationOwnerFlags, "implementation-owner", nil, "Explicit FTR-####=actor mapping (repeatable)")
	cmd.Flags().StringArrayVar(&statusChangedFlags, "status-changed", nil, "Explicit FTR-####=YYYY-MM-DD mapping (repeatable)")
	cmd.Flags().BoolVar(&force, "force", false, "Administratively override feature ownership (active locks still enforced; audited)")
	return cmd
}

func parseMigrationMappings(values []string, flagName string) (map[string]string, error) {
	mappings := make(map[string]string, len(values))
	for _, raw := range values {
		id, value, err := splitPair(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", flagName, err)
		}
		id = strings.ToUpper(strings.TrimSpace(id))
		if !strings.HasPrefix(id, "FTR-") {
			return nil, fmt.Errorf("%s: invalid feature ID %q", flagName, id)
		}
		if existing, ok := mappings[id]; ok && existing != value {
			return nil, fmt.Errorf("%s: conflicting mappings for %s", flagName, id)
		}
		mappings[id] = value
	}
	return mappings, nil
}
