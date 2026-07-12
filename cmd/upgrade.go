package cmd

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/upgrade"
	"github.com/virtualboard/vb-cli/internal/version"
)

var runUpgrade = func(logger *logrus.Logger, currentVersion string) (*upgrade.UpgradeResult, error) {
	return upgrade.NewUpgrader(logger).Upgrade(currentVersion)
}

var runUpgradeCheck = func(logger *logrus.Logger, currentVersion string) (*upgrade.UpgradeResult, error) {
	return upgrade.NewUpgrader(logger).Check(currentVersion)
}

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

			runner := runUpgrade
			if opts.DryRun {
				runner = runUpgradeCheck
			}
			result, err := runner(opts.Logger(), version.Current)
			if err != nil {
				exitCode := ExitCodeExternalCommand
				if errors.Is(err, fs.ErrPermission) {
					exitCode = ExitCodeFilesystem
				}
				if opts.JSONOutput {
					payload := map[string]interface{}{
						"error":           err.Error(),
						"current_version": version.Current,
					}
					if responseErr := respond(cmd, opts, false, "upgrade failed", payload); responseErr != nil {
						return responseErr
					}
				}
				return WrapCLIError(exitCode, fmt.Errorf("upgrade failed: %w", err))
			}

			// Handle different upgrade results
			if opts.JSONOutput {
				payload := map[string]interface{}{
					"message":          result.Message,
					"current_version":  result.CurrentVersion,
					"latest_version":   result.LatestVersion,
					"upgraded":         result.Upgraded,
					"update_available": result.UpdateAvailable,
					"dry_run":          opts.DryRun,
				}
				return respond(cmd, opts, true, "upgrade", payload)
			}

			fmt.Fprintln(cmd.OutOrStdout(), result.Message)
			return nil
		},
	}
}
