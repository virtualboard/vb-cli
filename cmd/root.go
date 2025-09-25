package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/config"
)

var (
	rootCmd = &cobra.Command{
		Use:           "vb",
		Short:         "VirtualBoard CLI for managing feature specs",
		Long:          "VirtualBoard CLI provides a single entry point for managing feature specs, indexes, validation, templates, and locks.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if _, err := config.Current(); err == nil {
				return nil
			}
			opts := config.New()
			if err := opts.Init(flagRoot, flagJSON, flagVerbose, flagDryRun, flagLogFile); err != nil {
				return err
			}
			cmd.SetContext(opts.WithContext(cmd.Context()))
			return nil
		},
	}

	flagJSON    bool
	flagVerbose bool
	flagDryRun  bool
	flagRoot    string
	flagLogFile string
)

// Execute runs the root command.
func Execute() error {
	registerCommands()
	if err := rootCmd.Execute(); err != nil {
		return err
	}
	opts, err := config.Current()
	if err == nil {
		if cerr := opts.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "failed to close resources: %v\n", cerr)
		}
	}
	return nil
}

// RootCommand returns the configured root command; primarily for testing scenarios.
func RootCommand() *cobra.Command {
	registerCommands()
	return rootCmd
}

// registerCommands ensures all subcommands are attached before execution.
func registerCommands() {
	if len(rootCmd.Commands()) > 0 {
		return
	}
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output machine-readable JSON")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "Enable verbose logging")
	rootCmd.PersistentFlags().BoolVar(&flagDryRun, "dry-run", false, "Simulate actions without modifying files")
	rootCmd.PersistentFlags().StringVar(&flagRoot, "root", "", "Path to repository root (default: current directory)")
	rootCmd.PersistentFlags().StringVar(&flagLogFile, "log-file", "", "File to write verbose logs")

	rootCmd.AddCommand(newNewCommand())
	rootCmd.AddCommand(newMoveCommand())
	rootCmd.AddCommand(newUpdateCommand())
	rootCmd.AddCommand(newDeleteCommand())
	rootCmd.AddCommand(newIndexCommand())
	rootCmd.AddCommand(newValidateCommand())
	rootCmd.AddCommand(newTemplateCommand())
	rootCmd.AddCommand(newLockCommand())
	rootCmd.AddCommand(newInitCommand())
	rootCmd.AddCommand(newVersionCommand())
}
