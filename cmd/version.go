package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/version"
)

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			payload := map[string]interface{}{
				"version": version.Current,
			}

			if opts.JSONOutput {
				return respond(cmd, opts, true, "version", payload)
			}

			fmt.Fprintln(cmd.OutOrStdout(), version.Current)
			return nil
		},
	}
}
