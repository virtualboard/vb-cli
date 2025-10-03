package cmd

import (
	"fmt"
	"strings"

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
			result, err := upgrader.Upgrade(version.Current)
			if err != nil {
				// Check if it's a permission error
				if strings.Contains(err.Error(), "permission denied") {
					errorMsg := "upgrade failed: permission denied. Please run with sudo to upgrade the binary"
					if opts.JSONOutput {
						payload := map[string]interface{}{
							"error": errorMsg,
							"current_version": version.Current,
							"suggestion": "Run 'sudo vb upgrade' to upgrade the binary",
						}
						return respond(cmd, opts, false, "upgrade failed", payload)
					}
					return fmt.Errorf("%s", errorMsg)
				}

				if opts.JSONOutput {
					payload := map[string]interface{}{
						"error": err.Error(),
						"current_version": version.Current,
					}
					return respond(cmd, opts, false, "upgrade failed", payload)
				}
				return fmt.Errorf("upgrade failed: %w", err)
			}

			// Handle different upgrade results
			if opts.JSONOutput {
				payload := map[string]interface{}{
					"message": result.Message,
					"current_version": result.CurrentVersion,
					"latest_version": result.LatestVersion,
					"upgraded": result.Upgraded,
				}
				return respond(cmd, opts, true, "upgrade", payload)
			}

			fmt.Fprintln(cmd.OutOrStdout(), result.Message)
			return nil
		},
	}
}
