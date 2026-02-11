package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

// mockHTTPResponse creates a mock HTTP response
type mockHTTPResponse struct {
	body       string
	statusCode int
	err        error
}

func (m *mockHTTPResponse) Read(p []byte) (n int, err error) {
	return strings.NewReader(m.body).Read(p)
}

func (m *mockHTTPResponse) Close() error {
	return nil
}

// Test install command structure
func TestInstallCommandStructure(t *testing.T) {
	cmd := newInstallCommand()
	if cmd.Use != "install <ide>" {
		t.Errorf("expected Use to be 'install <ide>', got %s", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("expected Short description to be set")
	}
}

func TestInstallCommand_UnsupportedIDE(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"unsupported"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unsupported IDE")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Errorf("expected validation exit code, got %d", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "unsupported IDE") {
		t.Errorf("expected error message to mention unsupported IDE, got: %s", err.Error())
	}
}

func TestInstallCommand_MissingArgument(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing argument")
	}
}

// Claude Code installation tests
func TestInstallClaudeCode_NotInstalled(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock exec.LookPath to return error (claude not found)
	original := execLookPath
	execLookPath = func(file string) (string, error) {
		return "", errors.New("executable file not found")
	}
	t.Cleanup(func() { execLookPath = original })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"claude"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when claude is not installed")
	}
	if ExitCode(err) != ExitCodeNotFound {
		t.Errorf("expected not found exit code, got %d", ExitCode(err))
	}
	if !strings.Contains(err.Error(), "Claude Code CLI") {
		t.Errorf("expected error message to mention Claude Code CLI, got: %s", err.Error())
	}
}

func TestInstallClaudeCode_DryRun(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, true) // dry-run
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock exec.LookPath to find claude
	original := execLookPath
	execLookPath = func(file string) (string, error) {
		if file == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { execLookPath = original })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"claude"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success in dry-run, got %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if result["success"] != true {
		t.Error("expected success to be true")
	}
	data := result["data"].(map[string]interface{})
	if data["ide"] != "claude" {
		t.Errorf("expected ide to be 'claude', got %v", data["ide"])
	}
}

func TestInstallClaudeCode_Success(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock exec.LookPath to find claude
	originalLookPath := execLookPath
	execLookPath = func(file string) (string, error) {
		if file == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { execLookPath = originalLookPath })

	// Mock execCommand to simulate successful plugin commands
	commandsRun := []string{}
	originalExec := execCommand
	execCommand = func(name string, args ...string) ([]byte, error) {
		commandsRun = append(commandsRun, name+" "+strings.Join(args, " "))
		return []byte("success"), nil
	}
	t.Cleanup(func() { execCommand = originalExec })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"claude"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify both commands were run
	if len(commandsRun) != 2 {
		t.Errorf("expected 2 commands to be run, got %d", len(commandsRun))
	}
	if !strings.Contains(commandsRun[0], "marketplace add") {
		t.Errorf("expected first command to be marketplace add, got: %s", commandsRun[0])
	}
	if !strings.Contains(commandsRun[1], "plugin install") {
		t.Errorf("expected second command to be plugin install, got: %s", commandsRun[1])
	}
}

func TestInstallClaudeCode_MarketplaceAddFails(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	originalLookPath := execLookPath
	execLookPath = func(file string) (string, error) {
		return "/usr/local/bin/claude", nil
	}
	t.Cleanup(func() { execLookPath = originalLookPath })

	originalExec := execCommand
	execCommand = func(name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "marketplace") {
			return []byte("error: network failure"), errors.New("command failed")
		}
		return []byte("success"), nil
	}
	t.Cleanup(func() { execCommand = originalExec })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"claude"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when marketplace add fails")
	}
	if ExitCode(err) != ExitCodeExternalCommand {
		t.Errorf("expected external command exit code, got %d", ExitCode(err))
	}
}

func TestInstallClaudeCode_PluginInstallFails(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	originalLookPath := execLookPath
	execLookPath = func(file string) (string, error) {
		return "/usr/local/bin/claude", nil
	}
	t.Cleanup(func() { execLookPath = originalLookPath })

	callCount := 0
	originalExec := execCommand
	execCommand = func(name string, args ...string) ([]byte, error) {
		callCount++
		if callCount == 2 { // Second call (plugin install)
			return []byte("error: install failed"), errors.New("command failed")
		}
		return []byte("success"), nil
	}
	t.Cleanup(func() { execCommand = originalExec })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"claude"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when plugin install fails")
	}
	if ExitCode(err) != ExitCodeExternalCommand {
		t.Errorf("expected external command exit code, got %d", ExitCode(err))
	}
}

// Cursor installation tests
func TestInstallCursor_NoVirtualBoard(t *testing.T) {
	// Create a fixture without .virtualboard
	root := t.TempDir()
	opts := config.New()
	if err := opts.Init(root, false, false, false, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when .virtualboard doesn't exist")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Errorf("expected validation exit code, got %d", ExitCode(err))
	}
}

func TestInstallCursor_DryRun(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, true) // dry-run
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success in dry-run, got %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if result["success"] != true {
		t.Error("expected success to be true")
	}
}

func TestInstallCursor_Success(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("---\ndescription: VirtualBoard rule\n---")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify file was created
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules", cursorRuleFile)
	if _, err := os.Stat(rulesPath); os.IsNotExist(err) {
		t.Error("expected cursor rule file to be created")
	}
}

func TestInstallCursor_FileExistsAndIdentical(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create existing cursor rules directory and file
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	ruleContent := "existing content"
	if err := os.WriteFile(filepath.Join(rulesPath, cursorRuleFile), []byte(ruleContent), 0o600); err != nil {
		t.Fatalf("failed to write rule file: %v", err)
	}

	// Mock HTTP GET to return same content
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(ruleContent)),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	if !strings.Contains(buf.String(), "already up to date") {
		t.Errorf("expected 'already up to date' message, got: %s", buf.String())
	}
}

func TestInstallCursor_FileExistsAndDifferent_UserDeclines(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create existing file with different content
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesPath, cursorRuleFile), []byte("old content"), 0o600); err != nil {
		t.Fatalf("failed to write rule file: %v", err)
	}

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("new content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	// Mock confirmation to decline
	originalConfirm := confirmReplace
	confirmReplace = func(opts *config.Options, prompt string) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { confirmReplace = originalConfirm })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success when user cancels, got %v", err)
	}

	// Verify file was NOT modified
	content, _ := os.ReadFile(filepath.Join(rulesPath, cursorRuleFile))
	if string(content) != "old content" {
		t.Error("file should not have been modified when user declines")
	}
}

func TestInstallCursor_FileExistsAndDifferent_UserAccepts(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create existing file with different content
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesPath, cursorRuleFile), []byte("old content"), 0o600); err != nil {
		t.Fatalf("failed to write rule file: %v", err)
	}

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("new content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	// Mock confirmation to accept
	originalConfirm := confirmReplace
	confirmReplace = func(opts *config.Options, prompt string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { confirmReplace = originalConfirm })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify file was modified
	content, _ := os.ReadFile(filepath.Join(rulesPath, cursorRuleFile))
	if string(content) != "new content" {
		t.Errorf("expected new content, got: %s", string(content))
	}
}

func TestInstallCursor_ForceFlag(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create existing file with different content
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesPath, cursorRuleFile), []byte("old content"), 0o600); err != nil {
		t.Fatalf("failed to write rule file: %v", err)
	}

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("new content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	cmd.Flags().Set("force", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success with --force, got %v", err)
	}

	// Verify file was modified
	content, _ := os.ReadFile(filepath.Join(rulesPath, cursorRuleFile))
	if string(content) != "new content" {
		t.Errorf("expected new content with --force, got: %s", string(content))
	}
}

func TestInstallCursor_HTTPError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock HTTP GET to return error
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return nil, errors.New("network error")
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when HTTP fails")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

func TestInstallCursor_HTTPNotOK(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock HTTP GET to return 404
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when HTTP returns 404")
	}
}

func TestInstallCursor_ConfirmError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create existing file
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesPath, cursorRuleFile), []byte("old content"), 0o600); err != nil {
		t.Fatalf("failed to write rule file: %v", err)
	}

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("new content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	// Mock confirmation to return error
	originalConfirm := confirmReplace
	confirmReplace = func(opts *config.Options, prompt string) (bool, error) {
		return false, errors.New("stdin closed")
	}
	t.Cleanup(func() { confirmReplace = originalConfirm })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when confirmation fails")
	}
}

// OpenCode installation tests
func TestInstallOpenCode_NoVirtualBoard(t *testing.T) {
	root := t.TempDir()
	opts := config.New()
	if err := opts.Init(root, false, false, false, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when .virtualboard doesn't exist")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Errorf("expected validation exit code, got %d", ExitCode(err))
	}
}

func TestInstallOpenCode_NoAgents(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Remove agents directory
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	os.RemoveAll(agentsPath)

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when .virtualboard/agents doesn't exist")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Errorf("expected validation exit code, got %d", ExitCode(err))
	}
}

func TestInstallOpenCode_DryRun(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, true) // dry-run
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create agents directory
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	if err := os.MkdirAll(agentsPath, 0o750); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsPath, "test.md"), []byte("test"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success in dry-run, got %v", err)
	}

	// Verify .opencode was NOT created
	opencodeDir := filepath.Join(fix.Root, ".opencode")
	if _, err := os.Stat(opencodeDir); err == nil {
		t.Error(".opencode should not be created in dry-run mode")
	}
}

func TestInstallOpenCode_Success(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create agents directory with files
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	if err := os.MkdirAll(agentsPath, 0o750); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsPath, "pm.md"), []byte("# PM Agent"), 0o600); err != nil {
		t.Fatalf("failed to write pm.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsPath, "dev.md"), []byte("# Dev Agent"), 0o600); err != nil {
		t.Fatalf("failed to write dev.md: %v", err)
	}

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify files were copied
	destDir := filepath.Join(fix.Root, ".opencode", "agent")
	pmFile := filepath.Join(destDir, "pm.md")
	devFile := filepath.Join(destDir, "dev.md")

	if _, err := os.Stat(pmFile); os.IsNotExist(err) {
		t.Error("expected pm.md to be copied")
	}
	if _, err := os.Stat(devFile); os.IsNotExist(err) {
		t.Error("expected dev.md to be copied")
	}

	content, _ := os.ReadFile(pmFile)
	if string(content) != "# PM Agent" {
		t.Errorf("expected pm.md content to match, got: %s", string(content))
	}
}

func TestInstallOpenCode_SuccessWithSubdirectory(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create nested agents directory structure
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	subDir := filepath.Join(agentsPath, "subdir")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatalf("failed to create subdirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsPath, "root.md"), []byte("root"), 0o600); err != nil {
		t.Fatalf("failed to write root.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.md"), []byte("nested"), 0o600); err != nil {
		t.Fatalf("failed to write nested.md: %v", err)
	}

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	// Verify nested structure was copied
	destNestedFile := filepath.Join(fix.Root, ".opencode", "agent", "subdir", "nested.md")
	if _, err := os.Stat(destNestedFile); os.IsNotExist(err) {
		t.Error("expected nested file to be copied")
	}

	content, _ := os.ReadFile(destNestedFile)
	if string(content) != "nested" {
		t.Errorf("expected nested content, got: %s", string(content))
	}
}

// copyDir function tests
func TestCopyDir_ReadError(t *testing.T) {
	// Test with non-existent source
	err := copyDir("/nonexistent/path", t.TempDir())
	if err == nil {
		t.Error("expected error for non-existent source")
	}
}

func TestCopyDir_CreateDirError(t *testing.T) {
	srcDir := t.TempDir()
	subDir := filepath.Join(srcDir, "subdir")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	// Create a file where directory should be created
	destDir := t.TempDir()
	destSubDir := filepath.Join(destDir, "subdir")
	if err := os.WriteFile(destSubDir, []byte("file"), 0o600); err != nil {
		t.Fatalf("failed to create blocking file: %v", err)
	}

	err := copyDir(srcDir, destDir)
	if err == nil {
		t.Error("expected error when directory creation fails")
	}
}

// fetchCursorRule tests
func TestFetchCursorRule_Success(t *testing.T) {
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("rule content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	content, err := fetchCursorRule()
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if string(content) != "rule content" {
		t.Errorf("expected 'rule content', got: %s", string(content))
	}
}

func TestFetchCursorRule_HTTPError(t *testing.T) {
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return nil, errors.New("network error")
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	_, err := fetchCursorRule()
	if err == nil {
		t.Error("expected error for HTTP failure")
	}
}

func TestFetchCursorRule_NonOKStatus(t *testing.T) {
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("error")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	_, err := fetchCursorRule()
	if err == nil {
		t.Error("expected error for non-OK status")
	}
}

// Test IDE case insensitivity
func TestInstallCommand_CaseInsensitive(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, true) // dry-run
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create agents directory
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	if err := os.MkdirAll(agentsPath, 0o750); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsPath, "test.md"), []byte("test"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	testCases := []string{"OPENCODE", "OpenCode", "openCode", "CURSOR", "Cursor", "CLAUDE", "Claude"}

	for _, tc := range testCases {
		t.Run(tc, func(t *testing.T) {
			// Mock necessary functions based on IDE
			if strings.ToLower(tc) == "claude" {
				originalLookPath := execLookPath
				execLookPath = func(file string) (string, error) {
					return "/usr/local/bin/claude", nil
				}
				t.Cleanup(func() { execLookPath = originalLookPath })
			}

			cmd := newInstallCommand()
			cmd.SetArgs([]string{tc})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			// Should not fail due to case
			err := cmd.Execute()
			if err != nil && strings.Contains(err.Error(), "unsupported IDE") {
				t.Errorf("IDE '%s' should be recognized (case insensitive)", tc)
			}
		})
	}
}

// Test confirmReplaceFunc
func TestConfirmReplaceFunc_JSONMode(t *testing.T) {
	opts := &config.Options{JSONOutput: true}
	result, err := confirmReplaceFunc(opts, "test prompt")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result {
		t.Error("expected true in JSON mode")
	}
}

// Test confirmReplaceFunc non-JSON mode
func TestConfirmReplaceFunc_NonJSONMode(t *testing.T) {
	// Mock promptYesNo
	originalPrompt := promptYesNo
	promptYesNo = func(prompt string) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { promptYesNo = originalPrompt })

	opts := &config.Options{JSONOutput: false}
	result, err := confirmReplaceFunc(opts, "test prompt")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !result {
		t.Error("expected true from mocked prompt")
	}
}

// Test confirmReplaceFunc non-JSON mode returns false
func TestConfirmReplaceFunc_NonJSONMode_ReturnsFalse(t *testing.T) {
	// Mock promptYesNo to return false
	originalPrompt := promptYesNo
	promptYesNo = func(prompt string) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { promptYesNo = originalPrompt })

	opts := &config.Options{JSONOutput: false}
	result, err := confirmReplaceFunc(opts, "test prompt")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result {
		t.Error("expected false from mocked prompt")
	}
}

// Test execCommandFunc with valid command
func TestExecCommandFunc(t *testing.T) {
	// Test with a command that exists on all systems
	output, err := execCommandFunc("echo", "hello")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !strings.Contains(string(output), "hello") {
		t.Errorf("expected 'hello' in output, got: %s", string(output))
	}
}

// Test Cursor installation with read error on existing file
func TestInstallCursor_ReadExistingFileError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create a directory where the file should be (to cause read error)
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	targetFilePath := filepath.Join(rulesPath, cursorRuleFile)
	if err := os.MkdirAll(targetFilePath, 0o750); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("new content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when reading existing file fails")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

// Test response output in plain text mode
func TestInstallOpenCode_PlainTextOutput(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = false
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create agents directory with files
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	if err := os.MkdirAll(agentsPath, 0o750); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsPath, "pm.md"), []byte("# PM Agent"), 0o600); err != nil {
		t.Fatalf("failed to write pm.md: %v", err)
	}

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "VirtualBoard agents installed") {
		t.Errorf("expected success message in plain text output, got: %s", output)
	}
}

// Test Cursor read error for existing file but file is unreadable
func TestInstallCursor_ExistingFileReadPermissionError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create cursor rules directory
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}

	// Create file with no read permissions
	targetFile := filepath.Join(rulesPath, cursorRuleFile)
	if err := os.WriteFile(targetFile, []byte("content"), 0o000); err != nil {
		t.Fatalf("failed to write rule file: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(targetFile, 0o644)
	})

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("new content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when reading file with no permissions")
	}
}

// Test OpenCode with file read error
func TestInstallOpenCode_FileReadError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create agents directory with unreadable file
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	if err := os.MkdirAll(agentsPath, 0o750); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	unreadableFile := filepath.Join(agentsPath, "unreadable.md")
	if err := os.WriteFile(unreadableFile, []byte("content"), 0o000); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(unreadableFile, 0o644)
	})

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when file cannot be read")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

// Test FetchCursorRule with read body error
func TestFetchCursorRule_ReadBodyError(t *testing.T) {
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(&errorReader{}),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	_, err := fetchCursorRule()
	if err == nil {
		t.Error("expected error when reading body fails")
	}
}

// errorReader is a mock reader that always returns an error
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("read error")
}

// Test Claude Code with plain text output
func TestInstallClaudeCode_PlainTextOutput(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = false
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock exec.LookPath to find claude
	originalLookPath := execLookPath
	execLookPath = func(file string) (string, error) {
		return "/usr/local/bin/claude", nil
	}
	t.Cleanup(func() { execLookPath = originalLookPath })

	// Mock execCommand
	originalExec := execCommand
	execCommand = func(name string, args ...string) ([]byte, error) {
		return []byte("success"), nil
	}
	t.Cleanup(func() { execCommand = originalExec })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"claude"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "VirtualBoard plugin installed successfully") {
		t.Errorf("expected success message in plain text output, got: %s", output)
	}
}

// Test Cursor with JSON auto-confirm
func TestInstallCursor_JSONModeAutoConfirm(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true // JSON mode auto-confirms
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create existing file with different content
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rulesPath, cursorRuleFile), []byte("old content"), 0o600); err != nil {
		t.Fatalf("failed to write rule file: %v", err)
	}

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("new content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success in JSON mode, got %v", err)
	}

	// Verify file was modified (auto-confirmed)
	content, _ := os.ReadFile(filepath.Join(rulesPath, cursorRuleFile))
	if string(content) != "new content" {
		t.Error("expected file to be auto-updated in JSON mode")
	}
}

// Test install command when config is not initialized
func TestInstallCommand_ConfigNotInitialized(t *testing.T) {
	// Clear current config
	config.SetCurrent(nil)

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"claude"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when config not initialized")
	}
}

// Test getProjectRoot function
func TestGetProjectRoot(t *testing.T) {
	tests := []struct {
		name     string
		rootDir  string
		expected string
	}{
		{
			name:     "root is project directory",
			rootDir:  "/project",
			expected: "/project",
		},
		{
			name:     "root is .virtualboard directory",
			rootDir:  "/project/.virtualboard",
			expected: "/project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &config.Options{RootDir: tt.rootDir}
			result := getProjectRoot(opts)
			if result != tt.expected {
				t.Errorf("getProjectRoot() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// Test Cursor installation with MkdirAll error
func TestInstallCursor_MkdirAllError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create a file where the directory should be (to cause MkdirAll error)
	cursorPath := filepath.Join(fix.Root, ".cursor")
	if err := os.WriteFile(cursorPath, []byte("file"), 0o600); err != nil {
		t.Fatalf("failed to create blocking file: %v", err)
	}

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when MkdirAll fails")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

// Test OpenCode installation with MkdirAll error
func TestInstallOpenCode_MkdirAllError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create agents directory
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	if err := os.MkdirAll(agentsPath, 0o750); err != nil {
		t.Fatalf("failed to create agents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentsPath, "test.md"), []byte("test"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Create a file where the directory should be (to cause MkdirAll error)
	opencodePath := filepath.Join(fix.Root, ".opencode")
	if err := os.WriteFile(opencodePath, []byte("file"), 0o600); err != nil {
		t.Fatalf("failed to create blocking file: %v", err)
	}

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when MkdirAll fails")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

// Test copyDir with write error
func TestCopyDir_WriteError(t *testing.T) {
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("content"), 0o600); err != nil {
		t.Fatalf("failed to create source file: %v", err)
	}

	// Create read-only destination directory
	destDir := t.TempDir()
	if err := os.Chmod(destDir, 0o444); err != nil {
		t.Fatalf("failed to make dest read-only: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(destDir, 0o755)
	})

	err := copyDir(srcDir, destDir)
	if err == nil {
		t.Error("expected error when write fails")
	}
}

// Test Cursor installation with pathExists error for vbPath
// pathExists returns an error when Stat fails with something other than "not exists"
// This happens when a path component has no execute permission
func TestInstallCursor_PathExistsError(t *testing.T) {
	root := t.TempDir()

	// Create a directory that we'll make inaccessible
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("failed to create parent dir: %v", err)
	}

	// Create .virtualboard inside parent
	vbInParent := filepath.Join(parentDir, ".virtualboard")
	if err := os.MkdirAll(vbInParent, 0o755); err != nil {
		t.Fatalf("failed to create .virtualboard: %v", err)
	}

	// Initialize options properly
	opts := config.New()
	if err := opts.Init(parentDir, false, false, false, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}

	// Now remove execute permission from parent so Stat fails with permission error
	if err := os.Chmod(parentDir, 0o000); err != nil {
		t.Fatalf("failed to chmod parent: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(parentDir, 0o755)
	})

	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when pathExists fails with permission error")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

// Test OpenCode installation with pathExists error for vbPath
func TestInstallOpenCode_PathExistsError(t *testing.T) {
	root := t.TempDir()

	// Create a directory that we'll make inaccessible
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("failed to create parent dir: %v", err)
	}

	// Create .virtualboard inside parent
	vbInParent := filepath.Join(parentDir, ".virtualboard")
	if err := os.MkdirAll(vbInParent, 0o755); err != nil {
		t.Fatalf("failed to create .virtualboard: %v", err)
	}

	// Initialize options properly
	opts := config.New()
	if err := opts.Init(parentDir, false, false, false, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}

	// Now remove execute permission from parent so Stat fails with permission error
	if err := os.Chmod(parentDir, 0o000); err != nil {
		t.Fatalf("failed to chmod parent: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(parentDir, 0o755)
	})

	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when pathExists fails with permission error")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

// Test OpenCode installation with pathExists error for agents
func TestInstallOpenCode_AgentsPathExistsError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create the agents directory and then make its parent inaccessible
	agentsPath := filepath.Join(fix.Root, ".virtualboard", "agents")
	if err := os.MkdirAll(agentsPath, 0o755); err != nil {
		t.Fatalf("failed to create agents directory: %v", err)
	}

	// Create a nested path within agents that will fail permission check
	vbPath := filepath.Join(fix.Root, ".virtualboard")

	// Make the .virtualboard directory inaccessible
	if err := os.Chmod(vbPath, 0o000); err != nil {
		t.Fatalf("failed to chmod: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(vbPath, 0o755)
	})

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"opencode"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when pathExists fails for agents")
	}
}

// Test Cursor installation with WriteFileAtomic error
func TestInstallCursor_WriteFileError(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Create rules directory but make it read-only so WriteFileAtomic fails
	rulesPath := filepath.Join(fix.Root, ".cursor", "rules")
	if err := os.MkdirAll(rulesPath, 0o750); err != nil {
		t.Fatalf("failed to create rules dir: %v", err)
	}
	// Make the directory read-only to cause write failure
	if err := os.Chmod(rulesPath, 0o444); err != nil {
		t.Fatalf("failed to make dir read-only: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(rulesPath, 0o755)
	})

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("content")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when WriteFileAtomic fails")
	}
	if ExitCode(err) != ExitCodeFilesystem {
		t.Errorf("expected filesystem exit code, got %d", ExitCode(err))
	}
}

// Test copyDir with nested directory creation failure
func TestCopyDir_NestedDirError(t *testing.T) {
	srcDir := t.TempDir()
	nestedDir := filepath.Join(srcDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o750); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}

	// Create destination with a file where directory should be
	destDir := t.TempDir()
	destNested := filepath.Join(destDir, "nested")
	if err := os.WriteFile(destNested, []byte("blocking"), 0o600); err != nil {
		t.Fatalf("failed to create blocking file: %v", err)
	}

	err := copyDir(srcDir, destDir)
	if err == nil {
		t.Error("expected error when nested directory creation fails")
	}
}

// Test copyDir with empty source directory
func TestCopyDir_EmptySource(t *testing.T) {
	srcDir := t.TempDir()
	destDir := t.TempDir()

	// Empty source should not error
	err := copyDir(srcDir, destDir)
	if err != nil {
		t.Errorf("expected success for empty source, got %v", err)
	}
}

// Test copyDir with recursive subdirectory error
// The recursive call to copyDir should propagate errors
func TestCopyDir_RecursiveSubdirError(t *testing.T) {
	srcDir := t.TempDir()
	// Create nested structure with a file in subdirectory
	subDir := filepath.Join(srcDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file.txt"), []byte("content"), 0o600); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	destDir := t.TempDir()
	// Create the subdirectory in dest
	destSubDir := filepath.Join(destDir, "sub")
	if err := os.MkdirAll(destSubDir, 0o755); err != nil {
		t.Fatalf("failed to create dest subdir: %v", err)
	}
	// Make dest subdir read-only to cause the recursive write to fail
	if err := os.Chmod(destSubDir, 0o444); err != nil {
		t.Fatalf("failed to chmod dest subdir: %v", err)
	}
	t.Cleanup(func() {
		os.Chmod(destSubDir, 0o755)
	})

	err := copyDir(srcDir, destDir)
	if err == nil {
		t.Error("expected error when recursive copy fails")
	}
}

// Test Cursor installation plain text with successful write
func TestInstallCursor_PlainTextSuccess(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = false
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	// Mock HTTP GET
	originalHTTP := httpGet
	httpGet = func(url string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("---\ndescription: VirtualBoard rule\n---")),
		}, nil
	}
	t.Cleanup(func() { httpGet = originalHTTP })

	cmd := newInstallCommand()
	cmd.SetArgs([]string{"cursor"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "VirtualBoard rule installed") {
		t.Errorf("expected success message, got: %s", output)
	}
}
