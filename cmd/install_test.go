package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/util"
)

const (
	testCursorRule = "---\ndescription: VirtualBoard\n---\n"
	testOpenSkill  = "---\nname: virtualboard\n---\n# VirtualBoard\n"
)

func initInstallOptions(t *testing.T, root string, jsonOutput, dryRun bool) *config.Options {
	t.Helper()
	opts := config.New()
	if err := opts.Init(root, jsonOutput, false, dryRun, ""); err != nil {
		t.Fatalf("initialize options: %v", err)
	}
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })
	return opts
}

func newNestedInstallWorkspace(t *testing.T, jsonOutput, dryRun bool) (string, string, *config.Options) {
	t.Helper()
	appRoot := t.TempDir()
	vbRoot := filepath.Join(appRoot, ".virtualboard")
	if err := os.MkdirAll(vbRoot, 0o750); err != nil {
		t.Fatalf("create nested VirtualBoard workspace: %v", err)
	}
	return appRoot, vbRoot, initInstallOptions(t, appRoot, jsonOutput, dryRun)
}

func newTemplateInstallWorkspace(t *testing.T, jsonOutput, dryRun bool) (string, *config.Options) {
	t.Helper()
	root := t.TempDir()
	writeInstallFile(t, filepath.Join(root, "virtualboard.json"), "{}\n", 0o600)
	if err := os.MkdirAll(filepath.Join(root, "features"), 0o750); err != nil {
		t.Fatalf("create template features directory: %v", err)
	}
	return root, initInstallOptions(t, root, jsonOutput, dryRun)
}

func writeInstallFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeCursorSource(t *testing.T, vbRoot, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(vbRoot, filepath.FromSlash(cursorIntegrationSource))
	writeInstallFile(t, path, content, mode)
	setCursorDigest(t, content)
	return path
}

func setCursorDigest(t *testing.T, content string) {
	t.Helper()
	digest := sha256.Sum256([]byte(content))
	original := cursorIntegrationSHA256
	cursorIntegrationSHA256 = fmt.Sprintf("%x", digest)
	t.Cleanup(func() { cursorIntegrationSHA256 = original })
}

func writeOpenCodeSource(t *testing.T, vbRoot string, extra map[string]string) string {
	t.Helper()
	root := filepath.Join(vbRoot, filepath.FromSlash(openCodeIntegrationRoot))
	contents := map[string]string{openCodeRequiredSkill: testOpenSkill}
	for rel, content := range extra {
		contents[filepath.ToSlash(rel)] = content
	}
	for rel, content := range contents {
		writeInstallFile(t, filepath.Join(root, filepath.FromSlash(rel)), content, 0o777)
	}
	setOpenCodeManifest(t, contents)
	return root
}

func setOpenCodeManifest(t *testing.T, contents map[string]string) {
	t.Helper()
	paths := make([]string, 0, len(contents))
	for path := range contents {
		paths = append(paths, filepath.ToSlash(path))
	}
	sort.Strings(paths)
	entries := make([]integrationManifestEntry, 0, len(paths))
	for _, path := range paths {
		digest := sha256.Sum256([]byte(contents[path]))
		entries = append(entries, integrationManifestEntry{Path: path, SHA256: fmt.Sprintf("%x", digest)})
	}
	original := openCodeIntegrationManifest
	openCodeIntegrationManifest = entries
	t.Cleanup(func() { openCodeIntegrationManifest = original })
}

func runInstall(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newInstallCommand()
	cmd.SetArgs(args)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}

func readInstallFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func parseInstallJSON(t *testing.T, output string) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("parse JSON response %q: %v", output, err)
	}
	return result
}

func TestInstallCommandStructureAndArguments(t *testing.T) {
	cmd := newInstallCommand()
	if cmd.Use != "install <ide>" {
		t.Fatalf("Use = %q, want install <ide>", cmd.Use)
	}
	if cmd.Short == "" || !strings.Contains(cmd.Long, "compiled path/SHA-256 inventory") {
		t.Fatalf("install command help does not describe authenticated integrations")
	}
	if cmd.Flags().Lookup("force") == nil {
		t.Fatal("install command must expose --force")
	}

	root := t.TempDir()
	initInstallOptions(t, root, false, false)
	if _, err := runInstall(t); err == nil {
		t.Fatal("missing IDE argument should fail")
	}
	_, err := runInstall(t, "unsupported")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "unsupported IDE") {
		t.Fatalf("unsupported IDE error = %v", err)
	}
}

func TestCompiledIntegrationAuthorizationMatchesTemplateRelease(t *testing.T) {
	if claudeMarketplaceSource != "virtualboard/template-base#v0.8.0" {
		t.Fatalf("Claude marketplace source = %q", claudeMarketplaceSource)
	}
	if claudePluginIdentifier != "virtualboard@virtualboard-marketplace" {
		t.Fatalf("Claude plugin identifier = %q", claudePluginIdentifier)
	}
	if cursorIntegrationSHA256 != "d047218c7da962b57bf2f827f6ccf654edb2c01aefa9b1bb52bb09de80a30285" {
		t.Fatalf("unexpected compiled Cursor digest %q", cursorIntegrationSHA256)
	}
	if len(openCodeIntegrationManifest) != 1 ||
		openCodeIntegrationManifest[0].Path != openCodeRequiredSkill ||
		openCodeIntegrationManifest[0].SHA256 != "dce51b2e3e221d778184d055f549acbf0f595971e18961e6cc430ba9575e0001" {
		t.Fatalf("unexpected compiled OpenCode inventory: %#v", openCodeIntegrationManifest)
	}
}

func TestInstallCommandRequiresInitializedConfig(t *testing.T) {
	config.SetCurrent(nil)
	if _, err := runInstall(t, "cursor"); err == nil {
		t.Fatal("uninitialized configuration should fail")
	}
}

func TestInstallCommandRecognizesIDECaseInsensitively(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, true)
	writeCursorSource(t, vbRoot, testCursorRule, 0o600)
	writeOpenCodeSource(t, vbRoot, nil)

	originalLookPath := execLookPath
	execLookPath = func(file string) (string, error) { return "/usr/bin/claude", nil }
	t.Cleanup(func() { execLookPath = originalLookPath })

	for _, ide := range []string{"CURSOR", "Cursor", "OPENCODE", "OpenCode", "CLAUDE", "Claude"} {
		t.Run(ide, func(t *testing.T) {
			output, err := runInstall(t, ide)
			if err != nil {
				t.Fatalf("install %s: %v", ide, err)
			}
			if parseInstallJSON(t, output)["success"] != true {
				t.Fatalf("install %s did not report success", ide)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(appRoot, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("dry-run case-insensitivity test must not create integrations")
	}
}

func TestInstallClaudeCodeNotInstalled(t *testing.T) {
	initInstallOptions(t, t.TempDir(), false, false)
	original := execLookPath
	execLookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { execLookPath = original })

	_, err := runInstall(t, "claude")
	if err == nil || ExitCode(err) != ExitCodeNotFound || !strings.Contains(err.Error(), "Claude Code CLI") {
		t.Fatalf("missing Claude error = %v", err)
	}
}

func TestInstallClaudeCodeDryRun(t *testing.T) {
	initInstallOptions(t, t.TempDir(), true, true)
	originalLookPath := execLookPath
	originalExec := execCommand
	execLookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	execCommand = func(string, ...string) ([]byte, error) {
		t.Fatal("dry-run must not execute Claude commands")
		return nil, nil
	}
	t.Cleanup(func() {
		execLookPath = originalLookPath
		execCommand = originalExec
	})

	output, err := runInstall(t, "claude")
	if err != nil {
		t.Fatalf("Claude dry-run: %v", err)
	}
	result := parseInstallJSON(t, output)
	data := result["data"].(map[string]interface{})
	if data["ide"] != "claude" || len(data["commands"].([]interface{})) != 2 {
		t.Fatalf("unexpected Claude dry-run data: %#v", data)
	}
	wantCommands := []string{
		"claude plugin marketplace add " + claudeMarketplaceSource,
		"claude plugin install " + claudePluginIdentifier,
	}
	for index, want := range wantCommands {
		if got := data["commands"].([]interface{})[index]; got != want {
			t.Fatalf("Claude dry-run command %d = %q, want %q", index, got, want)
		}
	}
	if data["framework_release"] != templateRelease {
		t.Fatalf("Claude dry-run release = %#v, want %s", data["framework_release"], templateRelease)
	}
}

func TestInstallClaudeCodeSuccess(t *testing.T) {
	initInstallOptions(t, t.TempDir(), true, false)
	originalLookPath := execLookPath
	originalExec := execCommand
	execLookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	type invocation struct {
		name string
		args []string
	}
	var calls []invocation
	execCommand = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, invocation{name: name, args: append([]string(nil), args...)})
		return []byte("ok"), nil
	}
	t.Cleanup(func() {
		execLookPath = originalLookPath
		execCommand = originalExec
	})

	output, err := runInstall(t, "claude")
	if err != nil {
		t.Fatalf("install Claude: %v", err)
	}
	want := []invocation{
		{name: "/usr/bin/claude", args: []string{"plugin", "marketplace", "add", claudeMarketplaceSource}},
		{name: "/usr/bin/claude", args: []string{"plugin", "install", claudePluginIdentifier}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected Claude commands: %#v", calls)
	}
	if parseInstallJSON(t, output)["success"] != true {
		t.Fatal("Claude success response is not successful")
	}
}

func TestInstallClaudeCodePlainTextSuccess(t *testing.T) {
	initInstallOptions(t, t.TempDir(), false, false)
	originalLookPath := execLookPath
	originalExec := execCommand
	execLookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	execCommand = func(string, ...string) ([]byte, error) { return []byte("ok"), nil }
	t.Cleanup(func() {
		execLookPath = originalLookPath
		execCommand = originalExec
	})

	output, err := runInstall(t, "claude")
	if err != nil || !strings.Contains(output, "plugin installed successfully") {
		t.Fatalf("plain-text Claude install: output=%q err=%v", output, err)
	}
}

func TestInstallClaudeCodeCommandFailures(t *testing.T) {
	for _, test := range []struct {
		name        string
		failureCall int
		message     string
	}{
		{name: "marketplace", failureCall: 1, message: "failed to add VirtualBoard"},
		{name: "plugin", failureCall: 2, message: "failed to install VirtualBoard plugin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			initInstallOptions(t, t.TempDir(), true, false)
			originalLookPath := execLookPath
			originalExec := execCommand
			execLookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
			calls := 0
			execCommand = func(string, ...string) ([]byte, error) {
				calls++
				if calls == test.failureCall {
					return []byte("command rejected"), errors.New("exit 1")
				}
				return []byte("ok"), nil
			}
			t.Cleanup(func() {
				execLookPath = originalLookPath
				execCommand = originalExec
			})

			_, err := runInstall(t, "claude")
			if err == nil || ExitCode(err) != ExitCodeExternalCommand || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Claude %s failure = %v", test.name, err)
			}
		})
	}
}

func TestExecCommandFunc(t *testing.T) {
	output, err := execCommandFunc("echo", "hello")
	if err != nil || !strings.Contains(string(output), "hello") {
		t.Fatalf("execCommandFunc: output=%q err=%v", output, err)
	}
}

func TestConfirmReplaceFunc(t *testing.T) {
	if confirmed, err := confirmReplaceFunc(&config.Options{JSONOutput: true}, "ignored"); err != nil || confirmed {
		t.Fatalf("JSON mode must not silently confirm replacement: %v, %v", confirmed, err)
	}

	original := promptYesNo
	t.Cleanup(func() { promptYesNo = original })
	for _, want := range []bool{true, false} {
		promptYesNo = func(string) (bool, error) { return want, nil }
		confirmed, err := confirmReplaceFunc(&config.Options{}, "replace?")
		if err != nil || confirmed != want {
			t.Fatalf("interactive confirmation = %v, %v; want %v", confirmed, err, want)
		}
	}
	promptYesNo = func(string) (bool, error) { return false, errors.New("stdin closed") }
	if _, err := confirmReplaceFunc(&config.Options{}, "replace?"); err == nil {
		t.Fatal("prompt error must be propagated")
	}
}

func TestWorkspaceRootResolution(t *testing.T) {
	templateRoot := t.TempDir()
	writeInstallFile(t, filepath.Join(templateRoot, "virtualboard.json"), "{}", 0o600)
	contractDirectoryRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(contractDirectoryRoot, "virtualboard.json"), 0o750); err != nil {
		t.Fatalf("create contract-shaped directory: %v", err)
	}

	tests := []struct {
		name        string
		root        string
		projectRoot string
		vbRoot      string
	}{
		{name: "nested", root: filepath.Join("/app", ".virtualboard"), projectRoot: filepath.Dir(filepath.Join("/app", ".virtualboard")), vbRoot: filepath.Join("/app", ".virtualboard")},
		{name: "template", root: templateRoot, projectRoot: templateRoot, vbRoot: templateRoot},
		{name: "default nested", root: contractDirectoryRoot, projectRoot: contractDirectoryRoot, vbRoot: filepath.Join(contractDirectoryRoot, ".virtualboard")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := &config.Options{RootDir: test.root}
			if got := getProjectRoot(opts); got != test.projectRoot {
				t.Fatalf("getProjectRoot() = %q, want %q", got, test.projectRoot)
			}
			if got := getVirtualBoardRoot(opts); got != test.vbRoot {
				t.Fatalf("getVirtualBoardRoot() = %q, want %q", got, test.vbRoot)
			}
		})
	}
}

func TestInstallCursorRequiresWorkspaceAndSource(t *testing.T) {
	t.Run("workspace missing", func(t *testing.T) {
		initInstallOptions(t, t.TempDir(), false, false)
		_, err := runInstall(t, "cursor")
		if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "workspace not found") {
			t.Fatalf("missing workspace error = %v", err)
		}
	})

	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{name: "source missing", setup: func(*testing.T, string) {}, want: "unavailable"},
		{name: "source empty", setup: func(t *testing.T, root string) { writeCursorSource(t, root, "", 0o600) }, want: "empty"},
		{name: "source is directory", setup: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(cursorIntegrationSource)), 0o750); err != nil {
				t.Fatal(err)
			}
		}, want: "not a regular file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
			test.setup(t, vbRoot)
			_, err := runInstall(t, "cursor")
			if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s error = %v", test.name, err)
			}
		})
	}
}

func TestInstallCursorDryRunUsesScaffoldedSource(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, true)
	source := writeCursorSource(t, vbRoot, testCursorRule, 0o640)

	output, err := runInstall(t, "cursor")
	if err != nil {
		t.Fatalf("Cursor dry-run: %v", err)
	}
	data := parseInstallJSON(t, output)["data"].(map[string]interface{})
	if data["source_file"] != source {
		t.Fatalf("dry-run source = %v, want %s", data["source_file"], source)
	}
	if _, err := os.Stat(filepath.Join(appRoot, ".cursor")); !os.IsNotExist(err) {
		t.Fatal("Cursor dry-run created .cursor")
	}
}

func TestInstallCursorRejectsModifiedAuthenticatedSource(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, true)
	source := writeCursorSource(t, vbRoot, testCursorRule, 0o644)
	if err := os.WriteFile(source, []byte("tampered rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runInstall(t, "cursor")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered Cursor source error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(appRoot, ".cursor")); !os.IsNotExist(err) {
		t.Fatalf("tampered Cursor source created destination: %v", err)
	}
}

func TestInstallCursorNestedWorkspaceEndToEnd(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	source := writeCursorSource(t, vbRoot, testCursorRule, 0o640)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)

	output, err := runInstall(t, "cursor")
	if err != nil {
		t.Fatalf("install Cursor: %v", err)
	}
	if got := readInstallFile(t, target); got != testCursorRule {
		t.Fatalf("Cursor target content = %q", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat Cursor target: %v", err)
	}
	if !integrationFileModeIsSafe(info.Mode()) {
		t.Fatalf("Cursor target mode = %v; want %v", info.Mode().Perm(), safeIntegrationFileMode)
	}
	data := parseInstallJSON(t, output)["data"].(map[string]interface{})
	if data["source_file"] != source || data["target_file"] != target || data["installed"] != true {
		t.Fatalf("unexpected Cursor response: %#v", data)
	}
}

func TestInstallCursorTemplateWorkspaceEndToEnd(t *testing.T) {
	root, _ := newTemplateInstallWorkspace(t, false, false)
	writeCursorSource(t, root, testCursorRule, 0o600)

	output, err := runInstall(t, "cursor")
	if err != nil {
		t.Fatalf("install Cursor from template layout: %v", err)
	}
	target := filepath.Join(root, ".cursor", "rules", cursorRuleFile)
	if readInstallFile(t, target) != testCursorRule || !strings.Contains(output, "rule installed") {
		t.Fatalf("template-layout Cursor install output=%q", output)
	}
}

func TestInstallCursorIdempotent(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	writeCursorSource(t, vbRoot, testCursorRule, 0o600)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	writeInstallFile(t, target, testCursorRule, safeIntegrationFileMode)

	original := confirmReplace
	confirmReplace = func(*config.Options, string) (bool, error) {
		t.Fatal("identical Cursor rule must not prompt")
		return false, nil
	}
	t.Cleanup(func() { confirmReplace = original })

	output, err := runInstall(t, "cursor")
	if err != nil || !strings.Contains(output, "already up to date") {
		t.Fatalf("idempotent Cursor install: output=%q err=%v", output, err)
	}
	data := parseInstallJSON(t, output)["data"].(map[string]interface{})
	if data["changed"] != false {
		t.Fatalf("idempotent Cursor response = %#v", data)
	}
}

func TestInstallCursorNormalizesUnsafeModeWithoutPrompt(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	writeCursorSource(t, vbRoot, testCursorRule, 0o777)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	writeInstallFile(t, target, testCursorRule, 0o777)

	original := confirmReplace
	confirmReplace = func(*config.Options, string) (bool, error) {
		t.Fatal("mode-only normalization must not prompt for content replacement")
		return false, nil
	}
	t.Cleanup(func() { confirmReplace = original })

	if _, err := runInstall(t, "cursor"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !integrationFileModeIsSafe(info.Mode()) {
		t.Fatalf("normalized Cursor mode = %v, want %v", info.Mode().Perm(), safeIntegrationFileMode)
	}
}

func TestInstallCursorConflictDeclineIsFailClosed(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeCursorSource(t, vbRoot, "new rule\n", 0o600)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	writeInstallFile(t, target, "local rule\n", 0o600)

	original := confirmReplace
	confirmReplace = func(_ *config.Options, prompt string) (bool, error) {
		if !strings.Contains(prompt, target) {
			t.Fatalf("confirmation prompt does not identify target: %q", prompt)
		}
		return false, nil
	}
	t.Cleanup(func() { confirmReplace = original })

	output, err := runInstall(t, "cursor")
	if err != nil || !strings.Contains(output, "cancelled") {
		t.Fatalf("declined Cursor conflict: output=%q err=%v", output, err)
	}
	if got := readInstallFile(t, target); got != "local rule\n" {
		t.Fatalf("declined Cursor conflict mutated target to %q", got)
	}
}

func TestInstallCursorConflictAcceptAndForce(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "accept", args: []string{"cursor"}},
		{name: "force", args: []string{"cursor", "--force"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
			writeCursorSource(t, vbRoot, "new rule\n", 0o600)
			target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
			writeInstallFile(t, target, "local rule\n", 0o600)

			original := confirmReplace
			confirmReplace = func(*config.Options, string) (bool, error) {
				if test.name == "force" {
					t.Fatal("--force must bypass confirmation")
				}
				return true, nil
			}
			t.Cleanup(func() { confirmReplace = original })

			if _, err := runInstall(t, test.args...); err != nil {
				t.Fatalf("Cursor %s: %v", test.name, err)
			}
			if got := readInstallFile(t, target); got != "new rule\n" {
				t.Fatalf("Cursor %s content = %q", test.name, got)
			}
		})
	}
}

func TestInstallCursorRestoresEditMadeAfterConsent(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeCursorSource(t, vbRoot, "new rule\n", 0o600)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	writeInstallFile(t, target, "approved old rule\n", 0o600)

	originalConfirm := confirmReplace
	originalHook := afterCursorConsent
	confirmReplace = func(*config.Options, string) (bool, error) { return true, nil }
	afterCursorConsent = func() {
		if err := os.WriteFile(target, []byte("racing edit\n"), 0o600); err != nil {
			t.Fatalf("write racing Cursor edit: %v", err)
		}
	}
	t.Cleanup(func() {
		confirmReplace = originalConfirm
		afterCursorConsent = originalHook
	})

	_, err := runInstall(t, "cursor")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "captured Cursor target differs") {
		t.Fatalf("post-consent Cursor race error = %v", err)
	}
	if got := readInstallFile(t, target); got != "racing edit\n" {
		t.Fatalf("post-consent Cursor race lost edit: %q", got)
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".vb-cursor-recovery-*.md")); len(matches) != 0 {
		t.Fatalf("successfully restored Cursor race left recovery files: %v", matches)
	}
}

func TestInstallCursorRetainsCapturedStateWhenTargetRecreated(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeCursorSource(t, vbRoot, "new rule\n", 0o600)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	writeInstallFile(t, target, "approved old rule\n", 0o600)

	originalConfirm := confirmReplace
	originalHook := afterCursorDestinationCaptured
	confirmReplace = func(*config.Options, string) (bool, error) { return true, nil }
	afterCursorDestinationCaptured = func() {
		writeInstallFile(t, target, "concurrently recreated\n", 0o600)
	}
	t.Cleanup(func() {
		confirmReplace = originalConfirm
		afterCursorDestinationCaptured = originalHook
	})

	_, err := runInstall(t, "cursor")
	if err == nil || !strings.Contains(err.Error(), "recovery data retained") {
		t.Fatalf("Cursor capture-to-publish race error = %v", err)
	}
	if got := readInstallFile(t, target); got != "concurrently recreated\n" {
		t.Fatalf("Cursor race replaced concurrent target: %q", got)
	}
	recovery, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".vb-cursor-recovery-*.md"))
	if len(recovery) != 1 || readInstallFile(t, recovery[0]) != "approved old rule\n" {
		t.Fatalf("Cursor recovery files = %v", recovery)
	}

	afterCursorDestinationCaptured = nil
	_, err = runInstall(t, "cursor", "--force")
	if err == nil || !strings.Contains(err.Error(), "interrupted Cursor replacement") {
		t.Fatalf("subsequent install ignored retained Cursor recovery: %v", err)
	}
}

func TestInstallCursorAbsentTargetPublicationIsExclusive(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeCursorSource(t, vbRoot, "new rule\n", 0o600)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	originalHook := afterCursorConsent
	afterCursorConsent = func() {
		writeInstallFile(t, target, "concurrently created\n", 0o600)
	}
	t.Cleanup(func() { afterCursorConsent = originalHook })

	_, err := runInstall(t, "cursor")
	if err == nil || !strings.Contains(err.Error(), "publish absent Cursor target exclusively") {
		t.Fatalf("absent Cursor publication race error = %v", err)
	}
	if got := readInstallFile(t, target); got != "concurrently created\n" {
		t.Fatalf("absent Cursor publication overwrote concurrent target: %q", got)
	}
}

func TestInstallCursorConflictConfirmationError(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeCursorSource(t, vbRoot, "new\n", 0o600)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	writeInstallFile(t, target, "old\n", 0o600)
	original := confirmReplace
	confirmReplace = func(*config.Options, string) (bool, error) { return false, errors.New("stdin closed") }
	t.Cleanup(func() { confirmReplace = original })

	_, err := runInstall(t, "cursor")
	if err == nil || ExitCode(err) != ExitCodeUnknown || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("Cursor confirmation error = %v", err)
	}
	if readInstallFile(t, target) != "old\n" {
		t.Fatal("confirmation error mutated Cursor target")
	}
}

func TestInstallCursorJSONConflictRequiresForce(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	writeCursorSource(t, vbRoot, "new\n", 0o600)
	target := filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)
	writeInstallFile(t, target, "old\n", 0o600)

	_, err := runInstall(t, "cursor")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("JSON Cursor conflict must require --force: %v", err)
	}
	if readInstallFile(t, target) != "old\n" {
		t.Fatal("JSON Cursor conflict mutated the target without --force")
	}
}

func TestInstallCursorFilesystemFailures(t *testing.T) {
	t.Run("existing target is not readable as a file", func(t *testing.T) {
		appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
		writeCursorSource(t, vbRoot, "new\n", 0o600)
		if err := os.MkdirAll(filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile), 0o750); err != nil {
			t.Fatal(err)
		}
		_, err := runInstall(t, "cursor")
		if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("Cursor existing-target error = %v", err)
		}
	})

	t.Run("rules directory blocked", func(t *testing.T) {
		appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
		writeCursorSource(t, vbRoot, "new\n", 0o600)
		writeInstallFile(t, filepath.Join(appRoot, ".cursor"), "blocking file", 0o600)
		_, err := runInstall(t, "cursor")
		if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "parent is not a directory") {
			t.Fatalf("Cursor directory error = %v", err)
		}
	})

	t.Run("target is directory under force", func(t *testing.T) {
		appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
		writeCursorSource(t, vbRoot, "new\n", 0o600)
		if err := os.MkdirAll(filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile), 0o750); err != nil {
			t.Fatal(err)
		}
		_, err := runInstall(t, "cursor", "--force")
		if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("Cursor atomic-write error = %v", err)
		}
	})
}

func TestInstallCursorRejectsDestinationSymlinks(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, string, string) string
		args  []string
	}{
		{
			name: "cursor parent",
			setup: func(t *testing.T, appRoot, outside string) string {
				link := filepath.Join(appRoot, ".cursor")
				if err := os.Symlink(outside, link); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return filepath.Join(outside, "rules", cursorRuleFile)
			},
			args: []string{"cursor"},
		},
		{
			name: "rules parent in dry-run",
			setup: func(t *testing.T, appRoot, outside string) string {
				if err := os.MkdirAll(filepath.Join(appRoot, ".cursor"), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(appRoot, ".cursor", "rules")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return filepath.Join(outside, cursorRuleFile)
			},
			args: []string{"cursor"},
		},
		{
			name: "leaf under force",
			setup: func(t *testing.T, appRoot, outside string) string {
				rules := filepath.Join(appRoot, ".cursor", "rules")
				if err := os.MkdirAll(rules, 0o750); err != nil {
					t.Fatal(err)
				}
				sentinel := filepath.Join(outside, "sentinel.mdc")
				writeInstallFile(t, sentinel, "outside sentinel\n", 0o600)
				if err := os.Symlink(sentinel, filepath.Join(rules, cursorRuleFile)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return sentinel
			},
			args: []string{"cursor", "--force"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			appRoot, vbRoot, opts := newNestedInstallWorkspace(t, true, strings.Contains(test.name, "dry-run"))
			writeCursorSource(t, vbRoot, "authorized rule\n", 0o777)
			outside := t.TempDir()
			externalTarget := test.setup(t, appRoot, outside)
			before, beforeErr := os.ReadFile(externalTarget)

			_, err := runInstall(t, test.args...)
			if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "symbolic links are not allowed") {
				t.Fatalf("destination symlink error = %v", err)
			}
			if opts.DryRun && !strings.Contains(test.name, "dry-run") {
				t.Fatal("unexpected dry-run fixture")
			}
			after, afterErr := os.ReadFile(externalTarget)
			if beforeErr == nil {
				if afterErr != nil || !bytes.Equal(after, before) {
					t.Fatalf("external sentinel changed: before=%q after=%q err=%v", before, after, afterErr)
				}
			} else if !os.IsNotExist(afterErr) {
				t.Fatalf("outside destination was created or became unreadable: %v", afterErr)
			}
		})
	}
}

func TestInstallOpenCodeRequiresWorkspaceAndSkill(t *testing.T) {
	t.Run("workspace missing", func(t *testing.T) {
		initInstallOptions(t, t.TempDir(), false, false)
		_, err := runInstall(t, "opencode")
		if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "workspace not found") {
			t.Fatalf("missing workspace error = %v", err)
		}
	})

	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
		want  string
	}{
		{name: "skill missing", setup: func(t *testing.T, root string) {
			writeInstallFile(t, filepath.Join(root, filepath.FromSlash(openCodeIntegrationRoot), "README.md"), "integration", 0o600)
		}, want: "authorized integration file is missing"},
		{name: "skill empty", setup: func(t *testing.T, root string) {
			writeInstallFile(t, filepath.Join(root, filepath.FromSlash(openCodeIntegrationRoot), filepath.FromSlash(openCodeRequiredSkill)), "", 0o600)
		}, want: "checksum mismatch"},
		{name: "skill is directory", setup: func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(openCodeIntegrationRoot), filepath.FromSlash(openCodeRequiredSkill)), 0o750); err != nil {
				t.Fatal(err)
			}
		}, want: "authorized integration file is missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
			test.setup(t, vbRoot)
			_, err := runInstall(t, "opencode")
			if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s error = %v", test.name, err)
			}
		})
	}
}

func TestInstallOpenCodeRejectsUnsafeSourceTree(t *testing.T) {
	_, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	sourceRoot := writeOpenCodeSource(t, vbRoot, nil)
	target := filepath.Join(sourceRoot, "linked.md")
	if err := os.Symlink(filepath.Join(sourceRoot, filepath.FromSlash(openCodeRequiredSkill)), target); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := runInstall(t, "opencode")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "symbolic links are not allowed") {
		t.Fatalf("unsafe OpenCode source error = %v", err)
	}
}

func TestInstallOpenCodeManifestFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{
			name: "modified authorized file",
			mutate: func(t *testing.T, sourceRoot string) {
				writeInstallFile(t, filepath.Join(sourceRoot, filepath.FromSlash(openCodeRequiredSkill)), "tampered skill\n", 0o644)
			},
			want: "checksum mismatch",
		},
		{
			name: "unlisted extra file",
			mutate: func(t *testing.T, sourceRoot string) {
				writeInstallFile(t, filepath.Join(sourceRoot, "plugin", "unexpected.js"), "export default {}\n", 0o755)
			},
			want: "unlisted integration source file",
		},
		{
			name: "missing authorized file",
			mutate: func(t *testing.T, sourceRoot string) {
				if err := os.Remove(filepath.Join(sourceRoot, filepath.FromSlash(openCodeRequiredSkill))); err != nil {
					t.Fatal(err)
				}
			},
			want: "authorized integration file is missing",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, true)
			sourceRoot := writeOpenCodeSource(t, vbRoot, nil)
			test.mutate(t, sourceRoot)

			_, err := runInstall(t, "opencode")
			if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("OpenCode manifest error = %v; want %q", err, test.want)
			}
			if _, err := os.Stat(filepath.Join(appRoot, ".opencode")); !os.IsNotExist(err) {
				t.Fatalf("unauthorized source created destination: %v", err)
			}
		})
	}
}

func TestValidateIntegrationManifestIsStrict(t *testing.T) {
	hash := strings.Repeat("a", sha256.Size*2)
	invalid := [][]integrationManifestEntry{
		nil,
		{{Path: "../escape", SHA256: hash}, {Path: openCodeRequiredSkill, SHA256: hash}},
		{{Path: openCodeRequiredSkill, SHA256: strings.ToUpper(hash)}},
		{{Path: openCodeRequiredSkill, SHA256: hash}, {Path: openCodeRequiredSkill, SHA256: hash}},
		{{Path: "z.md", SHA256: hash}, {Path: openCodeRequiredSkill, SHA256: hash}},
		{{Path: "other.md", SHA256: hash}},
	}
	for _, manifest := range invalid {
		if err := validateIntegrationManifest(manifest); err == nil {
			t.Fatalf("invalid manifest was accepted: %#v", manifest)
		}
	}
	valid := []integrationManifestEntry{{Path: openCodeRequiredSkill, SHA256: hash}}
	if err := validateIntegrationManifest(valid); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestInstallOpenCodeDryRunUsesScaffoldedTree(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, true)
	source := writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "# PM\n"})

	output, err := runInstall(t, "opencode")
	if err != nil {
		t.Fatalf("OpenCode dry-run: %v", err)
	}
	data := parseInstallJSON(t, output)["data"].(map[string]interface{})
	if data["source"] != source || data["destination"] != filepath.Join(appRoot, ".opencode") || data["files"] != float64(2) {
		t.Fatalf("unexpected OpenCode dry-run response: %#v", data)
	}
	if _, err := os.Stat(filepath.Join(appRoot, ".opencode")); !os.IsNotExist(err) {
		t.Fatal("OpenCode dry-run created .opencode")
	}
}

func TestInstallOpenCodeNestedWorkspaceEndToEnd(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	writeInstallFile(t, filepath.Join(appRoot, ".opencode", "settings.json"), "user settings\n", 0o600)
	writeOpenCodeSource(t, vbRoot, map[string]string{
		"command/pm.md":        "# PM\n",
		"command/work-on.md":   "# Work on\n",
		"plugin/manifest.json": "{}\n",
	})

	output, err := runInstall(t, "opencode")
	if err != nil {
		t.Fatalf("install OpenCode: %v", err)
	}
	for rel, want := range map[string]string{
		openCodeRequiredSkill:  testOpenSkill,
		"command/pm.md":        "# PM\n",
		"command/work-on.md":   "# Work on\n",
		"plugin/manifest.json": "{}\n",
	} {
		if got := readInstallFile(t, filepath.Join(appRoot, ".opencode", filepath.FromSlash(rel))); got != want {
			t.Fatalf("OpenCode %s = %q, want %q", rel, got, want)
		}
	}
	skillInfo, err := os.Stat(filepath.Join(appRoot, ".opencode", filepath.FromSlash(openCodeRequiredSkill)))
	if err != nil {
		t.Fatalf("stat OpenCode skill: %v", err)
	}
	if !integrationFileModeIsSafe(skillInfo.Mode()) {
		t.Fatalf("OpenCode skill mode = %v; want %v", skillInfo.Mode().Perm(), safeIntegrationFileMode)
	}
	skillDirInfo, err := os.Stat(filepath.Dir(filepath.Join(appRoot, ".opencode", filepath.FromSlash(openCodeRequiredSkill))))
	if err != nil {
		t.Fatalf("stat OpenCode skill directory: %v", err)
	}
	if !integrationDirectoryModeIsSafe(skillDirInfo.Mode()) {
		t.Fatalf("OpenCode skill directory mode = %v; want %v", skillDirInfo.Mode().Perm(), safeIntegrationDirMode)
	}
	data := parseInstallJSON(t, output)["data"].(map[string]interface{})
	if data["installed"] != true || data["files"] != float64(4) {
		t.Fatalf("unexpected OpenCode response: %#v", data)
	}
	if got := readInstallFile(t, filepath.Join(appRoot, ".opencode", "settings.json")); got != "user settings\n" {
		t.Fatalf("OpenCode install did not preserve unrelated app configuration: %q", got)
	}
}

func TestInstallOpenCodeNormalizesUnsafeInstalledModes(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	writeOpenCodeSource(t, vbRoot, nil)

	root := filepath.Join(appRoot, ".opencode")
	installedDirs := []string{
		root,
		filepath.Join(root, "skill"),
		filepath.Join(root, "skill", "virtualboard"),
	}
	for _, directory := range installedDirs {
		if err := os.MkdirAll(directory, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0o777); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := runInstall(t, "opencode"); err != nil {
		t.Fatalf("install OpenCode over unsafe directory modes: %v", err)
	}
	for _, directory := range installedDirs {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		if !integrationDirectoryModeIsSafe(info.Mode()) {
			t.Fatalf("OpenCode directory remains group/world writable: %s mode=%v", directory, info.Mode().Perm())
		}
	}
	target := filepath.Join(root, filepath.FromSlash(openCodeRequiredSkill))
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !integrationFileModeIsSafe(info.Mode()) {
		t.Fatalf("OpenCode payload mode = %v, want %v", info.Mode().Perm(), safeIntegrationFileMode)
	}
}

func TestInstallOpenCodeTemplateWorkspaceEndToEnd(t *testing.T) {
	root, _ := newTemplateInstallWorkspace(t, false, false)
	writeOpenCodeSource(t, root, map[string]string{"README.md": "OpenCode integration\n"})

	output, err := runInstall(t, "opencode")
	if err != nil {
		t.Fatalf("install OpenCode from template layout: %v", err)
	}
	if readInstallFile(t, filepath.Join(root, ".opencode", filepath.FromSlash(openCodeRequiredSkill))) != testOpenSkill {
		t.Fatal("template-layout install did not copy required OpenCode skill")
	}
	if !strings.Contains(output, "OpenCode integration installed") {
		t.Fatalf("unexpected OpenCode plain-text output: %q", output)
	}
}

func TestInitThenInstallCursorAndOpenCodeEndToEnd(t *testing.T) {
	appRoot := t.TempDir()
	initInstallOptions(t, appRoot, false, false)
	mockPinnedTemplateDownload(t, makePinnedTemplateArchive(t, templateVersion, nil))

	initCommand := newInitCommand()
	var initOutput bytes.Buffer
	initCommand.SetOut(&initOutput)
	initCommand.SetErr(&initOutput)
	if err := initCommand.Execute(); err != nil {
		t.Fatalf("vb init: %v", err)
	}
	if !strings.Contains(initOutput.String(), "VirtualBoard project initialised") {
		t.Fatalf("unexpected vb init output: %q", initOutput.String())
	}

	workspace := filepath.Join(appRoot, ".virtualboard")
	if _, err := os.Stat(filepath.Join(workspace, "features", "in-progress", "FTR-0001-template-history.md")); !os.IsNotExist(err) {
		t.Fatalf("vb init scaffolded template feature history: %v", err)
	}
	index := readInstallFile(t, filepath.Join(workspace, "features", "INDEX.md"))
	if strings.Contains(index, "FTR-0001") || !strings.Contains(index, "**Total**: 0 features") {
		t.Fatalf("vb init did not reset feature index: %q", index)
	}

	setCursorDigest(t, "cursor-rule\n")
	if _, err := runInstall(t, "cursor"); err != nil {
		t.Fatalf("vb install cursor after init: %v", err)
	}
	if got := readInstallFile(t, filepath.Join(appRoot, ".cursor", "rules", cursorRuleFile)); got != "cursor-rule\n" {
		t.Fatalf("installed Cursor rule = %q", got)
	}

	setOpenCodeManifest(t, map[string]string{openCodeRequiredSkill: "opencode-skill\n"})
	if _, err := runInstall(t, "opencode"); err != nil {
		t.Fatalf("vb install opencode after init: %v", err)
	}
	if got := readInstallFile(t, filepath.Join(appRoot, ".opencode", filepath.FromSlash(openCodeRequiredSkill))); got != "opencode-skill\n" {
		t.Fatalf("installed OpenCode skill = %q", got)
	}
	if _, err := os.Stat(filepath.Join(appRoot, ".opencode", "agent")); !os.IsNotExist(err) {
		t.Fatalf("legacy agents directory should not be synthesized: %v", err)
	}
}

func TestInstallOpenCodeIdempotent(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "# PM\n"})

	if _, err := runInstall(t, "opencode"); err != nil {
		t.Fatalf("first OpenCode install: %v", err)
	}
	original := confirmReplace
	confirmReplace = func(*config.Options, string) (bool, error) {
		t.Fatal("identical OpenCode tree must not prompt")
		return false, nil
	}
	t.Cleanup(func() { confirmReplace = original })
	output, err := runInstall(t, "opencode")
	if err != nil {
		t.Fatalf("second OpenCode install: %v", err)
	}
	if readInstallFile(t, filepath.Join(appRoot, ".opencode", "command", "pm.md")) != "# PM\n" {
		t.Fatal("idempotent OpenCode install changed content")
	}
	if parseInstallJSON(t, output)["success"] != true {
		t.Fatal("idempotent OpenCode install did not report success")
	}
}

func TestInstallOpenCodeConflictDeclineIsAtomicAndFailClosed(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeOpenCodeSource(t, vbRoot, map[string]string{
		"command/pm.md":      "new PM\n",
		"command/new.md":     "new command\n",
		"plugin/config.json": "{}\n",
	})
	conflict := filepath.Join(appRoot, ".opencode", "command", "pm.md")
	writeInstallFile(t, conflict, "local PM\n", 0o600)

	original := confirmReplace
	confirmReplace = func(_ *config.Options, prompt string) (bool, error) {
		if !strings.Contains(prompt, "1 OpenCode integration file") {
			t.Fatalf("unexpected OpenCode conflict prompt: %q", prompt)
		}
		return false, nil
	}
	t.Cleanup(func() { confirmReplace = original })

	output, err := runInstall(t, "opencode")
	if err != nil || !strings.Contains(output, "cancelled") {
		t.Fatalf("declined OpenCode conflict: output=%q err=%v", output, err)
	}
	if readInstallFile(t, conflict) != "local PM\n" {
		t.Fatal("declined OpenCode conflict overwrote local file")
	}
	for _, rel := range []string{openCodeRequiredSkill, "command/new.md", "plugin/config.json"} {
		if _, err := os.Stat(filepath.Join(appRoot, ".opencode", filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("declined conflict partially copied %s", rel)
		}
	}
}

func TestInstallOpenCodeConflictAcceptAndForce(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "accept", args: []string{"opencode"}},
		{name: "force", args: []string{"opencode", "--force"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
			writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "new PM\n"})
			conflict := filepath.Join(appRoot, ".opencode", "command", "pm.md")
			writeInstallFile(t, conflict, "local PM\n", 0o600)

			original := confirmReplace
			confirmReplace = func(*config.Options, string) (bool, error) {
				if test.name == "force" {
					t.Fatal("OpenCode --force must bypass confirmation")
				}
				return true, nil
			}
			t.Cleanup(func() { confirmReplace = original })

			if _, err := runInstall(t, test.args...); err != nil {
				t.Fatalf("OpenCode %s: %v", test.name, err)
			}
			if got := readInstallFile(t, conflict); got != "new PM\n" {
				t.Fatalf("OpenCode %s content = %q", test.name, got)
			}
			if readInstallFile(t, filepath.Join(appRoot, ".opencode", filepath.FromSlash(openCodeRequiredSkill))) != testOpenSkill {
				t.Fatalf("OpenCode %s did not install required skill", test.name)
			}
		})
	}
}

func TestInstallOpenCodeAbortsWhenDestinationChangesAfterConsent(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "new PM\n"})
	conflict := filepath.Join(appRoot, ".opencode", "command", "pm.md")
	writeInstallFile(t, conflict, "local PM\n", 0o600)

	originalConfirm := confirmReplace
	confirmReplace = func(*config.Options, string) (bool, error) { return true, nil }
	originalHook := afterOpenCodeConsent
	afterOpenCodeConsent = func() {
		if err := os.WriteFile(conflict, []byte("racing edit\n"), 0o600); err != nil {
			t.Fatalf("write racing edit: %v", err)
		}
	}
	t.Cleanup(func() {
		confirmReplace = originalConfirm
		afterOpenCodeConsent = originalHook
	})

	_, err := runInstall(t, "opencode")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "changed after approval") {
		t.Fatalf("OpenCode post-consent race error = %v", err)
	}
	if got := readInstallFile(t, conflict); got != "racing edit\n" {
		t.Fatalf("post-consent race overwrote local edit: %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(appRoot, ".opencode", filepath.FromSlash(openCodeRequiredSkill))); !os.IsNotExist(statErr) {
		t.Fatalf("post-consent race partially installed integration: %v", statErr)
	}
}

func TestInstallOpenCodeRetainsJournalWhenDestinationRecreatedAfterCapture(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "new PM\n"})
	target := filepath.Join(appRoot, ".opencode")
	conflict := filepath.Join(target, "command", "pm.md")
	writeInstallFile(t, conflict, "approved local PM\n", 0o600)

	originalConfirm := confirmReplace
	originalHook := afterOpenCodeDestinationCaptured
	confirmReplace = func(*config.Options, string) (bool, error) { return true, nil }
	afterOpenCodeDestinationCaptured = func() {
		writeInstallFile(t, filepath.Join(target, "racing.md"), "concurrently recreated\n", 0o600)
	}
	t.Cleanup(func() {
		confirmReplace = originalConfirm
		afterOpenCodeDestinationCaptured = originalHook
	})

	_, err := runInstall(t, "opencode")
	if err == nil || !strings.Contains(err.Error(), "stage, captured tree, and journal retained") {
		t.Fatalf("OpenCode capture-to-publish race error = %v", err)
	}
	if got := readInstallFile(t, filepath.Join(target, "racing.md")); got != "concurrently recreated\n" {
		t.Fatalf("OpenCode race replaced concurrent target: %q", got)
	}
	journalPath := directoryReplaceJournalPath(target)
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read retained replacement journal: %v", err)
	}
	var journal directoryReplaceJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		t.Fatalf("parse retained replacement journal: %v", err)
	}
	stage := filepath.Join(appRoot, journal.Stage)
	backup := filepath.Join(appRoot, journal.Backup)
	if readInstallFile(t, filepath.Join(backup, "command", "pm.md")) != "approved local PM\n" {
		t.Fatal("retained OpenCode capture does not contain the approved local tree")
	}
	if readInstallFile(t, filepath.Join(stage, "command", "pm.md")) != "new PM\n" {
		t.Fatal("retained OpenCode stage does not contain the authenticated integration")
	}

	afterOpenCodeDestinationCaptured = nil
	_, err = runInstall(t, "opencode", "--force")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("subsequent OpenCode install ignored ambiguous retained state: %v", err)
	}
	for _, retained := range []string{target, stage, backup, journalPath} {
		if _, statErr := os.Lstat(retained); statErr != nil {
			t.Fatalf("ambiguous recovery removed %s: %v", retained, statErr)
		}
	}
}

func TestInstallOpenCodeAbsentTargetPublicationIsExclusive(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeOpenCodeSource(t, vbRoot, nil)
	target := filepath.Join(appRoot, ".opencode")
	originalHook := beforeOpenCodeExclusivePublish
	beforeOpenCodeExclusivePublish = func() {
		writeInstallFile(t, filepath.Join(target, "racing.md"), "concurrently created\n", 0o600)
	}
	t.Cleanup(func() { beforeOpenCodeExclusivePublish = originalHook })

	_, err := runInstall(t, "opencode")
	if err == nil || !strings.Contains(err.Error(), "failed to activate integration") {
		t.Fatalf("absent OpenCode publication race error = %v", err)
	}
	if got := readInstallFile(t, filepath.Join(target, "racing.md")); got != "concurrently created\n" {
		t.Fatalf("absent OpenCode publication overwrote concurrent tree: %q", got)
	}
	if _, statErr := os.Lstat(filepath.Join(target, filepath.FromSlash(openCodeRequiredSkill))); !os.IsNotExist(statErr) {
		t.Fatalf("absent OpenCode race partially installed authenticated tree: %v", statErr)
	}
}

func TestInstallOpenCodeConflictConfirmationErrorIsFailClosed(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
	writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "new PM\n"})
	conflict := filepath.Join(appRoot, ".opencode", "command", "pm.md")
	writeInstallFile(t, conflict, "local PM\n", 0o600)
	original := confirmReplace
	confirmReplace = func(*config.Options, string) (bool, error) { return false, errors.New("stdin closed") }
	t.Cleanup(func() { confirmReplace = original })

	_, err := runInstall(t, "opencode")
	if err == nil || ExitCode(err) != ExitCodeUnknown || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("OpenCode confirmation error = %v", err)
	}
	if readInstallFile(t, conflict) != "local PM\n" {
		t.Fatal("OpenCode confirmation error overwrote conflict")
	}
	if _, err := os.Stat(filepath.Join(appRoot, ".opencode", filepath.FromSlash(openCodeRequiredSkill))); !os.IsNotExist(err) {
		t.Fatal("OpenCode confirmation error partially copied source")
	}
}

func TestInstallOpenCodeJSONConflictRequiresForce(t *testing.T) {
	appRoot, vbRoot, _ := newNestedInstallWorkspace(t, true, false)
	writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "new\n"})
	target := filepath.Join(appRoot, ".opencode", "command", "pm.md")
	writeInstallFile(t, target, "old\n", 0o600)

	_, err := runInstall(t, "opencode")
	if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("JSON OpenCode conflict must require --force: %v", err)
	}
	if readInstallFile(t, target) != "old\n" {
		t.Fatal("JSON OpenCode conflict mutated the target without --force")
	}
	if _, err := os.Stat(filepath.Join(appRoot, ".opencode", filepath.FromSlash(openCodeRequiredSkill))); !os.IsNotExist(err) {
		t.Fatal("JSON OpenCode conflict partially copied source files")
	}
}

func TestInstallOpenCodeFilesystemFailures(t *testing.T) {
	t.Run("destination inspection", func(t *testing.T) {
		appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
		writeOpenCodeSource(t, vbRoot, map[string]string{"command/pm.md": "# PM\n"})
		if err := os.MkdirAll(filepath.Join(appRoot, ".opencode", "command", "pm.md"), 0o750); err != nil {
			t.Fatal(err)
		}
		_, err := runInstall(t, "opencode")
		if err == nil || ExitCode(err) != ExitCodeValidation || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("OpenCode inspection error = %v", err)
		}
	})

	t.Run("destination hierarchy blocked", func(t *testing.T) {
		appRoot, vbRoot, _ := newNestedInstallWorkspace(t, false, false)
		writeOpenCodeSource(t, vbRoot, nil)
		writeInstallFile(t, filepath.Join(appRoot, ".opencode", "skill"), "blocking file", 0o600)
		_, err := runInstall(t, "opencode")
		if err == nil || ExitCode(err) != ExitCodeValidation {
			t.Fatalf("OpenCode blocked hierarchy error = %v", err)
		}
	})
}

func TestReadRequiredIntegrationFile(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	if _, _, err := readRequiredIntegrationFile(missing, "missing"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing required source error = %v", err)
	}

	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readRequiredIntegrationFile(directory, "directory"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory required source error = %v", err)
	}

	empty := filepath.Join(root, "empty")
	writeInstallFile(t, empty, "", 0o600)
	if _, _, err := readRequiredIntegrationFile(empty, "empty"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty required source error = %v", err)
	}

	valid := filepath.Join(root, "valid")
	writeInstallFile(t, valid, "content", 0o640)
	content, mode, err := readRequiredIntegrationFile(valid, "valid")
	if err != nil || string(content) != "content" || !util.PermMatchesRequested(mode, 0o640) {
		t.Fatalf("valid required source = %q, %v, %v", content, mode, err)
	}
}

func TestCollectIntegrationFiles(t *testing.T) {
	root := t.TempDir()
	writeInstallFile(t, filepath.Join(root, "a.md"), "a", 0o600)
	writeInstallFile(t, filepath.Join(root, "nested", "b.md"), "b", 0o600)
	files, err := collectIntegrationFiles(root)
	if err != nil || strings.Join(files, ",") != strings.Join([]string{"a.md", filepath.Join("nested", "b.md")}, ",") {
		t.Fatalf("collectIntegrationFiles = %#v, %v", files, err)
	}

	fileRoot := filepath.Join(t.TempDir(), "file")
	writeInstallFile(t, fileRoot, "content", 0o600)
	if _, err := collectIntegrationFiles(fileRoot); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file root error = %v", err)
	}
	if _, err := collectIntegrationFiles(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing integration root should fail")
	}

	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "a.md"), link); err == nil {
		if _, err := collectIntegrationFiles(root); err == nil || !strings.Contains(err.Error(), "symbolic links") {
			t.Fatalf("symlink source error = %v", err)
		}
	}
}

func TestIntegrationConflicts(t *testing.T) {
	destination := t.TempDir()
	writeInstallFile(t, filepath.Join(destination, "same"), "same", 0o600)
	writeInstallFile(t, filepath.Join(destination, "different"), "destination", 0o600)
	files := []authorizedIntegrationFile{
		{Path: "same", Content: []byte("same")},
		{Path: "different", Content: []byte("source")},
		{Path: "new", Content: []byte("new")},
	}

	conflicts, err := integrationConflicts(destination, files)
	if err != nil || len(conflicts) != 1 || conflicts[0] != "different" {
		t.Fatalf("integrationConflicts = %#v, %v", conflicts, err)
	}
	if err := os.Mkdir(filepath.Join(destination, "new"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := integrationConflicts(destination, []authorizedIntegrationFile{{Path: "new", Content: []byte("new")}}); err == nil || !strings.Contains(err.Error(), "failed to inspect") {
		t.Fatalf("destination inspection error = %v", err)
	}
}

func TestCopyIntegrationFiles(t *testing.T) {
	destination := t.TempDir()
	files := []authorizedIntegrationFile{{Path: "nested/file.md", Content: []byte("content")}}
	if err := copyIntegrationFiles(destination, files); err != nil {
		t.Fatalf("copyIntegrationFiles: %v", err)
	}
	target := filepath.Join(destination, "nested", "file.md")
	if readInstallFile(t, target) != "content" {
		t.Fatal("copyIntegrationFiles content mismatch")
	}
	info, _ := os.Stat(target)
	if !integrationFileModeIsSafe(info.Mode()) {
		t.Fatalf("copied mode = %v, want %v", info.Mode().Perm(), safeIntegrationFileMode)
	}
	if err := os.Mkdir(filepath.Join(destination, "blocked"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := copyIntegrationFiles(destination, []authorizedIntegrationFile{{Path: "blocked", Content: []byte("file")}}); err == nil || !strings.Contains(err.Error(), "unsafe integration destination") {
		t.Fatalf("copyIntegrationFiles write error = %v", err)
	}
}

func TestInstallIntegrationTreeIsAtomicOnCopyFailure(t *testing.T) {
	destination := filepath.Join(t.TempDir(), ".opencode")
	writeInstallFile(t, filepath.Join(destination, "settings.json"), "user settings\n", 0o600)
	writeInstallFile(t, filepath.Join(destination, "blocked"), "blocking file\n", 0o600)

	err := installIntegrationTreeAtomically(destination, []authorizedIntegrationFile{
		{Path: "first.md", Content: []byte("new file\n")},
		{Path: "blocked/nested.md", Content: []byte("must fail\n")},
	})
	if err == nil {
		t.Fatal("atomic integration install should fail when a staged source is missing")
	}
	if got := readInstallFile(t, filepath.Join(destination, "settings.json")); got != "user settings\n" {
		t.Fatalf("failed atomic install changed user settings: %q", got)
	}
	if _, err := os.Stat(filepath.Join(destination, "first.md")); !os.IsNotExist(err) {
		t.Fatalf("failed atomic install partially copied first.md: %v", err)
	}
}

func TestOpenCodeIntegrationRecoversInterruptedDirectoryActivation(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, ".opencode")
	stage := filepath.Join(parent, ".vb-integration-stage-recovery")
	backup := filepath.Join(parent, ".vb-template-backup-recovery")
	writeInstallFile(t, filepath.Join(stage, "new.md"), "new integration\n", 0o600)
	writeInstallFile(t, filepath.Join(backup, "old.md"), "old integration\n", 0o600)
	journal := directoryReplaceJournal{
		Version:   directoryReplaceJournalVersion,
		Target:    filepath.Base(target),
		Stage:     filepath.Base(stage),
		Backup:    filepath.Base(backup),
		StartedAt: time.Now().Add(-2 * time.Hour),
	}
	journalPath := directoryReplaceJournalPath(target)
	writeRecoverableDirectoryReplaceJournal(t, target, &journal)

	if err := recoverDirectoryReplacement(target); err != nil {
		t.Fatalf("recover interrupted OpenCode activation: %v", err)
	}
	if got := readInstallFile(t, filepath.Join(target, "new.md")); got != "new integration\n" {
		t.Fatalf("recovered OpenCode content = %q", got)
	}
	for _, stale := range []string{stage, backup, journalPath} {
		if _, err := os.Lstat(stale); !os.IsNotExist(err) {
			t.Fatalf("recovery left stale path %s: %v", stale, err)
		}
	}
}

func TestOpenCodeCrashRecoveryRejectsCapturedTreeThatWasNotApproved(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, ".opencode")
	stage := filepath.Join(parent, ".vb-integration-stage-recovery-mismatch")
	backup := filepath.Join(parent, ".vb-template-backup-recovery-mismatch")
	approved := filepath.Join(parent, "approved-snapshot")
	writeInstallFile(t, filepath.Join(approved, "local.md"), "approved before consent\n", 0o600)
	writeInstallFile(t, filepath.Join(stage, "installed.md"), "authenticated integration\n", 0o600)
	writeInstallFile(t, filepath.Join(backup, "local.md"), "newer edit captured atomically\n", 0o600)
	expected, err := captureIntegrationDestinationSnapshot(approved)
	if err != nil {
		t.Fatal(err)
	}
	journal := directoryReplaceJournal{
		Version:            directoryReplaceJournalVersion,
		Target:             filepath.Base(target),
		Stage:              filepath.Base(stage),
		Backup:             filepath.Base(backup),
		ExpectedTreeSHA256: integrationDestinationSnapshotDigest(expected),
		StartedAt:          time.Now().Add(-2 * time.Hour),
	}
	journalPath := directoryReplaceJournalPath(target)
	writeRecoverableDirectoryReplaceJournal(t, target, &journal)

	if err := recoverDirectoryReplacement(target); err != nil {
		t.Fatalf("restore mismatched captured tree: %v", err)
	}
	if got := readInstallFile(t, filepath.Join(target, "local.md")); got != "newer edit captured atomically\n" {
		t.Fatalf("recovery activated an unapproved stage instead of restoring capture: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(target, "installed.md")); !os.IsNotExist(err) {
		t.Fatalf("recovery activated stage for mismatched capture: %v", err)
	}
	for _, stale := range []string{stage, backup, journalPath} {
		if _, err := os.Lstat(stale); !os.IsNotExist(err) {
			t.Fatalf("safe mismatch recovery left stale path %s: %v", stale, err)
		}
	}
}

func TestOpenCodeCrashRecoveryActivatesStageForExactApprovedCapture(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, ".opencode")
	stage := filepath.Join(parent, ".vb-integration-stage-recovery-approved")
	backup := filepath.Join(parent, ".vb-template-backup-recovery-approved")
	writeInstallFile(t, filepath.Join(stage, "installed.md"), "authenticated integration\n", 0o600)
	writeInstallFile(t, filepath.Join(backup, "local.md"), "approved local state\n", 0o600)
	expected, err := captureIntegrationDestinationSnapshot(backup)
	if err != nil {
		t.Fatal(err)
	}
	journal := directoryReplaceJournal{
		Version:            directoryReplaceJournalVersion,
		Target:             filepath.Base(target),
		Stage:              filepath.Base(stage),
		Backup:             filepath.Base(backup),
		ExpectedTreeSHA256: integrationDestinationSnapshotDigest(expected),
		StartedAt:          time.Now().Add(-2 * time.Hour),
	}
	writeRecoverableDirectoryReplaceJournal(t, target, &journal)

	if err := recoverDirectoryReplacement(target); err != nil {
		t.Fatalf("activate exact approved recovery stage: %v", err)
	}
	if got := readInstallFile(t, filepath.Join(target, "installed.md")); got != "authenticated integration\n" {
		t.Fatalf("exact approved recovery did not activate stage: %q", got)
	}
}
