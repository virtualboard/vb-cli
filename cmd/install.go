package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/util"
)

// Supported IDE targets for installation
const (
	ideClaudeCode = "claude"
	ideCursor     = "cursor"
	ideOpenCode   = "opencode"
)

// URLs for downloading Cursor configuration files
const (
	cursorRulesBaseURL = "https://raw.githubusercontent.com/virtualboard/template-base/main/docs/.cursor/rules/"
	cursorRuleFile     = "virtualboard.mdc"
)

// Function variables for testability
var (
	execLookPath   = exec.LookPath
	execCommand    = execCommandFunc
	httpGet        = http.Get
	confirmReplace = confirmReplaceFunc
	promptYesNo    = util.PromptYesNo
)

func execCommandFunc(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...) // #nosec G204 -- command args are from validated internal sources
	return cmd.CombinedOutput()
}

func confirmReplaceFunc(opts *config.Options, prompt string) (bool, error) {
	if opts.JSONOutput {
		return true, nil // Auto-confirm in JSON mode
	}
	return promptYesNo(prompt)
}

func newInstallCommand() *cobra.Command {
	var forceFlag bool

	cmd := &cobra.Command{
		Use:   "install <ide>",
		Short: "Install VirtualBoard integration for an IDE",
		Long: `Install VirtualBoard integration for a supported IDE.

Supported IDEs:
  claude    - Claude Code (requires 'claude' CLI to be installed)
  cursor    - Cursor IDE (copies .cursor/rules to your project)
  opencode  - OpenCode (copies agents to .opencode/agent)

Examples:
  vb install claude     # Install Claude Code plugin
  vb install cursor     # Install Cursor rules
  vb install opencode   # Install OpenCode agents`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			ide := strings.ToLower(args[0])
			switch ide {
			case ideClaudeCode:
				return installClaudeCode(cmd, opts)
			case ideCursor:
				return installCursor(cmd, opts, forceFlag)
			case ideOpenCode:
				return installOpenCode(cmd, opts)
			default:
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("unsupported IDE: %s. Supported: claude, cursor, opencode", ide))
			}
		},
	}

	cmd.Flags().BoolVar(&forceFlag, "force", false, "Replace existing files without confirmation")
	cmd.SilenceUsage = true
	return cmd
}

// installClaudeCode installs the VirtualBoard plugin for Claude Code
func installClaudeCode(cmd *cobra.Command, opts *config.Options) error {
	log := opts.Logger().WithField("ide", "claude")

	// Check if claude binary is installed
	claudePath, err := execLookPath("claude")
	if err != nil {
		log.WithError(err).Debug("Claude CLI not found")
		return WrapCLIError(ExitCodeNotFound, fmt.Errorf("Claude Code CLI ('claude') not found in PATH. Please install Claude Code first"))
	}
	log.WithField("path", claudePath).Debug("Found Claude CLI")

	if opts.DryRun {
		msg := "Dry-run: would install VirtualBoard plugin for Claude Code"
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"ide":      "claude",
			"commands": []string{"claude plugin marketplace add virtualboard/template-base", "claude plugin install virtualboard"},
		})
	}

	// Add plugin from marketplace
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "Adding VirtualBoard to Claude Code marketplace...")
	}

	output, err := execCommand("claude", "plugin", "marketplace", "add", "virtualboard/template-base")
	if err != nil {
		log.WithError(err).WithField("output", string(output)).Debug("Failed to add plugin to marketplace")
		return WrapCLIError(ExitCodeExternalCommand, fmt.Errorf("failed to add VirtualBoard to marketplace: %s", strings.TrimSpace(string(output))))
	}
	log.Debug("Added plugin to marketplace")

	// Install the plugin
	if !opts.JSONOutput {
		fmt.Fprintln(os.Stderr, "Installing VirtualBoard plugin...")
	}

	output, err = execCommand("claude", "plugin", "install", "virtualboard")
	if err != nil {
		log.WithError(err).WithField("output", string(output)).Debug("Failed to install plugin")
		return WrapCLIError(ExitCodeExternalCommand, fmt.Errorf("failed to install VirtualBoard plugin: %s", strings.TrimSpace(string(output))))
	}
	log.Debug("Installed plugin")

	msg := "VirtualBoard plugin installed successfully for Claude Code"
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"ide":       "claude",
		"installed": true,
	})
}

// getProjectRoot returns the project root directory, handling the case where
// opts.RootDir is the .virtualboard directory itself
func getProjectRoot(opts *config.Options) string {
	projectRoot := opts.RootDir
	if filepath.Base(projectRoot) == ".virtualboard" {
		projectRoot = filepath.Dir(projectRoot)
	}
	return projectRoot
}

// installCursor installs VirtualBoard rules for Cursor IDE
func installCursor(cmd *cobra.Command, opts *config.Options, force bool) error {
	log := opts.Logger().WithField("ide", "cursor")
	projectRoot := getProjectRoot(opts)

	// Check if .virtualboard exists
	vbPath := filepath.Join(projectRoot, ".virtualboard")
	if exists, err := pathExists(vbPath); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to check .virtualboard: %w", err))
	} else if !exists {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf(".virtualboard not found. Run 'vb init' first"))
	}

	cursorPath := filepath.Join(projectRoot, ".cursor")
	rulesPath := filepath.Join(cursorPath, "rules")
	targetFile := filepath.Join(rulesPath, cursorRuleFile)

	if opts.DryRun {
		msg := fmt.Sprintf("Dry-run: would install VirtualBoard rules to %s", targetFile)
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"ide":         "cursor",
			"target_file": targetFile,
		})
	}

	// Fetch the rule file from GitHub
	ruleContent, err := fetchCursorRule()
	if err != nil {
		log.WithError(err).Debug("Failed to fetch Cursor rule")
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to fetch Cursor rule: %w", err))
	}

	// Check if target file already exists and compare
	if exists, _ := pathExists(targetFile); exists && !force {
		existingContent, err := os.ReadFile(targetFile) // #nosec G304 -- path is from validated directory
		if err != nil {
			return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to read existing file: %w", err))
		}

		if bytes.Equal(existingContent, ruleContent) {
			msg := "VirtualBoard rule for Cursor is already up to date"
			return respond(cmd, opts, true, msg, map[string]interface{}{
				"ide":         "cursor",
				"target_file": targetFile,
				"changed":     false,
			})
		}

		// Ask for confirmation
		confirmed, err := confirmReplace(opts, fmt.Sprintf("File %s already exists and differs. Replace it?", targetFile))
		if err != nil {
			return WrapCLIError(ExitCodeUnknown, fmt.Errorf("failed to get confirmation: %w", err))
		}
		if !confirmed {
			msg := "Installation cancelled by user"
			return respond(cmd, opts, true, msg, map[string]interface{}{
				"ide":       "cursor",
				"cancelled": true,
			})
		}
	}

	// Create directories if needed
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to create .cursor/rules directory: %w", err))
	}

	// Write the rule file
	if err := util.WriteFileAtomic(targetFile, ruleContent, 0o600); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to write rule file: %w", err))
	}

	log.WithField("path", targetFile).Debug("Installed Cursor rule")

	msg := fmt.Sprintf("VirtualBoard rule installed at %s", targetFile)
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"ide":         "cursor",
		"target_file": targetFile,
		"installed":   true,
	})
}

// fetchCursorRule fetches the virtualboard.mdc rule file from GitHub
func fetchCursorRule() ([]byte, error) {
	url := cursorRulesBaseURL + cursorRuleFile
	resp, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("failed to download rule: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d fetching rule", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read rule content: %w", err)
	}

	return content, nil
}

// installOpenCode installs VirtualBoard agents for OpenCode
func installOpenCode(cmd *cobra.Command, opts *config.Options) error {
	log := opts.Logger().WithField("ide", "opencode")
	projectRoot := getProjectRoot(opts)

	// Check if .virtualboard exists
	vbPath := filepath.Join(projectRoot, ".virtualboard")
	if exists, err := pathExists(vbPath); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to check .virtualboard: %w", err))
	} else if !exists {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf(".virtualboard not found. Run 'vb init' first"))
	}

	// Check if .virtualboard/agents exists
	agentsSource := filepath.Join(vbPath, "agents")
	if exists, err := pathExists(agentsSource); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to check agents directory: %w", err))
	} else if !exists {
		return WrapCLIError(ExitCodeValidation, fmt.Errorf(".virtualboard/agents not found. Run 'vb init' to initialize the workspace"))
	}

	opencodePath := filepath.Join(projectRoot, ".opencode")
	agentPath := filepath.Join(opencodePath, "agent")

	if opts.DryRun {
		msg := fmt.Sprintf("Dry-run: would copy agents from %s to %s", agentsSource, agentPath)
		return respond(cmd, opts, true, msg, map[string]interface{}{
			"ide":         "opencode",
			"source":      agentsSource,
			"destination": agentPath,
		})
	}

	// Create .opencode/agent directory
	if err := os.MkdirAll(agentPath, 0o750); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to create .opencode/agent directory: %w", err))
	}

	// Copy agents directory contents recursively
	if err := copyDir(agentsSource, agentPath); err != nil {
		return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to copy agents: %w", err))
	}

	log.WithField("path", agentPath).Debug("Installed OpenCode agents")

	msg := fmt.Sprintf("VirtualBoard agents installed at %s", agentPath)
	return respond(cmd, opts, true, msg, map[string]interface{}{
		"ide":         "opencode",
		"destination": agentPath,
		"installed":   true,
	})
}

// copyDir recursively copies a directory from src to dst
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %w", err)
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o750); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dstPath, err)
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			content, err := os.ReadFile(srcPath) // #nosec G304 -- path is from validated source directory
			if err != nil {
				return fmt.Errorf("failed to read file %s: %w", srcPath, err)
			}
			if err := util.WriteFileAtomic(dstPath, content, 0o600); err != nil {
				return fmt.Errorf("failed to write file %s: %w", dstPath, err)
			}
		}
	}

	return nil
}
