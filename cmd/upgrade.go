package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/upgrade"
	"github.com/virtualboard/vb-cli/internal/version"
)

func newUpgradeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade vb to the latest version",
		Long:  "Check for a newer version of vb on GitHub releases and upgrade the binary if available.",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			// Create upgrader with logger from options
			upgrader := upgrade.NewUpgrader(opts.Logger())

			// Perform the upgrade
			if err := upgrader.Upgrade(version.Current); err != nil {
				if opts.JSONOutput {
					payload := map[string]interface{}{
						"error": err.Error(),
					}
					return respond(cmd, opts, false, "upgrade failed", payload)
				}
				return fmt.Errorf("upgrade failed: %w", err)
			}

			// Success response
			payload := map[string]interface{}{
				"message": "Upgrade completed successfully",
			}

			if opts.JSONOutput {
				return respond(cmd, opts, true, "upgrade", payload)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Upgrade completed successfully")
			return nil
		},
	}
}
