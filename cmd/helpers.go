package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/util"
)

func options() (*config.Options, error) {
	return config.Current()
}

func respond(cmd *cobra.Command, opts *config.Options, success bool, message string, data interface{}) error {
	if opts.JSONOutput {
		payload := util.StructuredResult(success, message, data)
		return util.PrintJSON(cmd.OutOrStdout(), payload)
	}
	if message != "" {
		fmt.Fprintln(cmd.OutOrStdout(), message)
	}
	return nil
}
