package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/audit"
)

func newAuditCommand() *cobra.Command {
	var (
		actions    []string
		actors     []string
		featureIDs []string
		since      string
		until      string
		contains   string
		limit      int
		tail       bool
		format     string
		verify     bool
	)

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect the hash-chained audit log",
		Long: `Read and filter .virtualboard/audit.jsonl.

The audit log is the append-only, SHA-256-chained record of every locking and
feature-mutation action. Use this command to query it without resorting to
jq / grep, and to optionally verify the integrity of the hash chain.

Examples:
  vb audit                                              # default human format
  vb audit --format table
  vb audit --format jsonl --action lock --action unlock
  vb audit --format json --actor netors --since 2026-04-16
  vb audit --format agent --feature-id FTR-0019
  vb audit --contains "ttl=1" --limit 5 --tail
  vb audit --verify                                     # exit 1 on tampering`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			sinceTime, err := audit.ParseTime(since)
			if err != nil {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--since: %w", err))
			}
			untilTime, err := audit.ParseTime(until)
			if err != nil {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--until: %w", err))
			}

			fmtKind, err := audit.ParseFormat(format)
			if err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}

			path := filepath.Join(opts.RootDir, "audit.jsonl")
			entries, parseErrs, err := audit.Read(path)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if opts.Verbose && len(parseErrs) > 0 {
				log := opts.Logger().WithField("component", "audit")
				for _, perr := range parseErrs {
					log.Warnf("skipped malformed audit entry: %v", perr)
				}
			}

			filtered := audit.Filter{
				Actions:    actions,
				Actors:     actors,
				FeatureIDs: featureIDs,
				Since:      sinceTime,
				Until:      untilTime,
				Contains:   contains,
				Limit:      limit,
				Tail:       tail,
			}.Apply(entries)

			if verify {
				if verr := audit.Verify(entries); verr != nil {
					return WrapCLIError(ExitCodeValidation, fmt.Errorf("audit chain verification failed: %w", verr))
				}
			}

			if opts.JSONOutput {
				data := map[string]interface{}{
					"path":         relOrAbs(opts.RootDir, path),
					"total":        len(entries),
					"count":        len(filtered),
					"entries":      filtered,
					"verified":     verify,
					"parse_errors": len(parseErrs),
				}
				return respond(cmd, opts, true, "audit entries returned", data)
			}

			content, err := audit.Render(filtered, fmtKind, audit.RenderOptions{IncludeHashes: opts.Verbose})
			if err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}
			fmt.Fprint(cmd.OutOrStdout(), content)
			if verify {
				fmt.Fprintln(cmd.OutOrStdout(), "Audit chain verified: OK")
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&actions, "action", nil, "Filter by action (repeatable; OR within flag)")
	cmd.Flags().StringSliceVar(&actors, "actor", nil, "Filter by actor (repeatable; OR within flag)")
	cmd.Flags().StringSliceVar(&featureIDs, "feature-id", nil, "Filter by feature_id (repeatable; OR within flag)")
	cmd.Flags().StringVar(&since, "since", "", "Lower-bound timestamp (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "Upper-bound timestamp (RFC3339 or YYYY-MM-DD)")
	cmd.Flags().StringVar(&contains, "contains", "", "Case-insensitive substring match on details")
	cmd.Flags().IntVar(&limit, "limit", 0, "Cap the number of returned entries (0 = no cap)")
	cmd.Flags().BoolVar(&tail, "tail", false, "With --limit, return the last N entries instead of the first N")
	cmd.Flags().StringVar(&format, "format", "human", "Output format: human, table, jsonl, json, xml, agent. Ignored when --json is set.")
	cmd.Flags().BoolVar(&verify, "verify", false, "Walk the hash chain and fail on tampering")

	return cmd
}

// relOrAbs returns the path relative to root if possible, otherwise the absolute path.
// Falling back to the absolute path keeps JSON consumers safe when the audit
// file lives outside the workspace (e.g. a symlinked or remote-mounted log).
func relOrAbs(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}
