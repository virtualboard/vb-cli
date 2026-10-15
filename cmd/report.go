package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/virtualboard/vb-cli/internal/report"
)

func newReportCommand() *cobra.Command {
	command := &cobra.Command{Use: "report", Short: "Render validated HTML reports"}
	command.AddCommand(newReportRenderCommand())
	return command
}

func newReportRenderCommand() *cobra.Command {
	var templateName, dataPath, outputPath, templateDir string
	var allowHTML, force bool
	command := &cobra.Command{
		Use:   "render --template NAME --data FILE --output FILE",
		Short: "Render a report template using JSON data and contextual escaping",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			if templateName == "" || dataPath == "" || outputPath == "" {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--template, --data, and --output are required"))
			}
			if templateDir == "" {
				templateDir = filepath.Join(opts.RootDir, "templates", "reports", "html")
			}
			// #nosec G304 -- data and output paths are explicit CLI arguments.
			file, err := os.Open(dataPath)
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			data, decodeErr := report.Decode(file)
			closeErr := file.Close()
			if decodeErr != nil {
				return WrapCLIError(ExitCodeValidation, decodeErr)
			}
			if closeErr != nil {
				return WrapCLIError(ExitCodeFilesystem, closeErr)
			}
			rendered, err := report.Render(templateDir, templateName, data, report.Options{AllowHTML: allowHTML})
			if err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}
			if _, err := os.Lstat(outputPath); err == nil && !force {
				return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("output %s exists; choose another path or pass --force", outputPath))
			} else if err != nil && !os.IsNotExist(err) {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			for _, input := range []string{dataPath, filepath.Join(templateDir, templateName)} {
				in, err := filepath.Abs(input)
				if err != nil {
					return err
				}
				out, err := filepath.Abs(outputPath)
				if err != nil {
					return err
				}
				if in == out {
					return WrapCLIError(ExitCodeValidation, fmt.Errorf("report output must differ from its inputs"))
				}
				inputInfo, inputErr := os.Stat(input)
				outputInfo, outputErr := os.Stat(outputPath)
				if inputErr == nil && outputErr == nil && os.SameFile(inputInfo, outputInfo) {
					return WrapCLIError(ExitCodeValidation, fmt.Errorf("report output aliases an input"))
				}
			}
			if !opts.DryRun {
				if err := report.WriteOutput(outputPath, rendered, force); err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}
			}
			message := fmt.Sprintf("Rendered report to %s", outputPath)
			if opts.DryRun {
				message = fmt.Sprintf("Would render report to %s", outputPath)
			}
			return respond(cmd, opts, true, message, map[string]any{"output": outputPath, "bytes": len(rendered), "dry_run": opts.DryRun})
		},
	}
	command.Flags().StringVar(&templateName, "template", "", "Template path relative to the report template directory")
	command.Flags().StringVar(&templateDir, "template-dir", "", "Report template directory (default: workspace templates/reports/html)")
	command.Flags().StringVar(&dataPath, "data", "", "JSON object containing report values and lists")
	command.Flags().StringVar(&outputPath, "output", "", "Destination HTML file")
	command.Flags().BoolVar(&allowHTML, "allow-html", false, "Allow explicitly annotated, reviewed HTML fragments")
	command.Flags().BoolVar(&force, "force", false, "Replace an existing report output")
	return command
}
