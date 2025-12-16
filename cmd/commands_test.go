package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/indexer"
	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/util"
	"github.com/virtualboard/vb-cli/internal/validator"
)

func setupOptions(t *testing.T, fix *testutil.Fixture, jsonOut, verbose, dry bool) (*config.Options, *bytes.Buffer) {
	t.Helper()
	opts := fix.Options(t, jsonOut, verbose, dry)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })
	var buf bytes.Buffer
	return opts, &buf
}

func TestNewAndUpdateCommands(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)

	newCmd := newNewCommand()
	newCmd.SetOut(buf)
	newCmd.SetErr(buf)
	newCmd.SetArgs([]string{"Amazing Feature", "label1"})
	if err := newCmd.Execute(); err != nil {
		t.Fatalf("new command failed: %v", err)
	}

	mgr := feature.NewManager(opts)
	features, err := mgr.List()
	if err != nil || len(features) == 0 {
		t.Fatalf("expected created feature: %v", err)
	}
	id := features[0].FrontMatter.ID

	updateCmd := newUpdateCommand()
	updateCmd.SetOut(buf)
	updateCmd.SetErr(buf)
	updateCmd.SetArgs([]string{id, "--field", "owner=bob", "--body-section", "Summary=Updated"})
	if err := updateCmd.Execute(); err != nil {
		t.Fatalf("update command failed: %v", err)
	}

	updated, err := mgr.LoadByID(id)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if updated.FrontMatter.Owner != "bob" || !strings.Contains(updated.Body, "Updated") {
		t.Fatalf("expected updates applied")
	}
}

func TestMoveAndIndexCommands(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)
	workspace := filepath.Join(fix.Root, ".virtualboard")
	if opts.RootDir != workspace {
		t.Fatalf("unexpected root: %s", opts.RootDir)
	}

	feat, err := mgr.CreateFeature("Move Feature", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	moveCmd := newMoveCommand()
	moveCmd.SetOut(buf)
	moveCmd.SetErr(buf)
	moveCmd.SetArgs([]string{feat.FrontMatter.ID, "in-progress"})
	if err := moveCmd.Execute(); err != nil {
		t.Fatalf("move command failed: %v", err)
	}

	_, bufJSON := setupOptions(t, fix, true, false, true)
	indexCmd := newIndexCommand()
	indexCmd.SetOut(bufJSON)
	indexCmd.SetErr(bufJSON)
	indexCmd.SetArgs([]string{"--format", "json"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index command failed: %v", err)
	}
	if !bytes.Contains(bufJSON.Bytes(), []byte("\"format\"")) {
		t.Fatalf("expected json output")
	}

	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index command write failed: %v", err)
	}
}

func TestDeleteAndTemplateCommands(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)
	feat, err := mgr.CreateFeature("Delete Feature", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	deleteCmd := newDeleteCommand()
	deleteCmd.SetOut(buf)
	deleteCmd.SetErr(buf)
	deleteCmd.SetArgs([]string{feat.FrontMatter.ID, "--force"})
	if err := deleteCmd.Execute(); err != nil {
		t.Fatalf("delete command failed: %v", err)
	}

	feat2, err := mgr.CreateFeature("Template Target", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}
	feat2.FrontMatter.Priority = ""
	if err := mgr.Save(feat2); err != nil {
		t.Fatalf("save feature failed: %v", err)
	}

	templateCmd := newTemplateApplyCommand()
	templateCmd.SetOut(buf)
	templateCmd.SetErr(buf)
	templateCmd.SetArgs([]string{feat2.FrontMatter.ID})
	if err := templateCmd.Execute(); err != nil {
		t.Fatalf("template apply failed: %v", err)
	}
}

func TestValidateCommand(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)
	feat := buildFeatureFile(t, fix, mgr, "FTR-5000", "backlog", "Validate Feature")
	v, err := validator.New(opts, mgr)
	if err != nil {
		t.Fatalf("validator init failed: %v", err)
	}
	statusDir := filepath.Join(mgr.FeaturesDir(), filepath.Base(feature.DirectoryForStatus("backlog")))
	if exp, act := statusDir, filepath.Dir(feat.Path); exp != act {
		t.Fatalf("expected dir %s, got %s", exp, act)
	}
	res, err := v.ValidateID(feat.FrontMatter.ID)
	if err != nil || len(res.Errors) > 0 {
		t.Fatalf("direct validation failed: %v %v", err, res.Errors)
	}

	validateCmd := newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{feat.FrontMatter.ID})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate command failed: %v", err)
	}

	_, bufJSON := setupOptions(t, fix, true, false, false)
	validateCmd = newValidateCommand()
	validateCmd.SetOut(bufJSON)
	validateCmd.SetErr(bufJSON)
	validateCmd.SetArgs([]string{"--fix"})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate all failed: %v", err)
	}
	if !bytes.Contains(bufJSON.Bytes(), []byte("\"target\"")) {
		t.Fatalf("expected json output for validate")
	}
}

func TestValidateFixRenamesFiles(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)

	// Create a feature with one title
	feat, err := mgr.CreateFeature("Original Title", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	id := feat.FrontMatter.ID
	originalPath := feat.Path

	// Manually update the title in frontmatter without renaming
	feat.FrontMatter.Title = "Updated Title Name"
	if err := mgr.Save(feat); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Verify the filename doesn't match the title yet
	expectedNewFilename := fmt.Sprintf("%s-updated-title-name.md", id)
	if filepath.Base(feat.Path) == expectedNewFilename {
		t.Fatalf("filename should not match updated title yet")
	}

	// Run validate --fix
	validateCmd := newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{"--fix"})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate --fix failed: %v", err)
	}

	// Verify old file is gone
	if _, err := os.Stat(originalPath); !os.IsNotExist(err) {
		t.Fatalf("old file should not exist after fix")
	}

	// Verify new file exists with correct name
	newPath := filepath.Join(filepath.Dir(originalPath), expectedNewFilename)
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new file should exist at %s: %v", newPath, err)
	}

	// Verify we can still load by ID
	loaded, err := mgr.LoadByID(id)
	if err != nil {
		t.Fatalf("load by ID failed after fix: %v", err)
	}
	if loaded.FrontMatter.Title != "Updated Title Name" {
		t.Fatalf("title should be preserved")
	}
	if filepath.Base(loaded.Path) != expectedNewFilename {
		t.Fatalf("loaded feature should have new filename")
	}
}

func TestLockAndDeleteInteractive(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)

	lockCmd := newLockCommand()
	lockCmd.SetOut(buf)
	lockCmd.SetErr(buf)
	lockCmd.SetArgs([]string{"FTR-LOCK", "--status", "--release"})
	if err := lockCmd.Execute(); err == nil {
		t.Fatalf("expected error when combining status and release")
	}

	lockCmd = newLockCommand()
	lockCmd.SetOut(buf)
	lockCmd.SetErr(buf)
	lockCmd.SetArgs([]string{"FTR-LOCK", "--ttl", "0"})
	if err := lockCmd.Execute(); err == nil {
		t.Fatalf("expected error for non-positive ttl")
	}

	lockCmd = newLockCommand()
	lockCmd.SetOut(buf)
	lockCmd.SetErr(buf)
	lockCmd.SetArgs([]string{"FTR-LOCK", "--ttl", "1", "--owner", "tester"})
	if err := lockCmd.Execute(); err != nil {
		t.Fatalf("lock acquire failed: %v", err)
	}

	statusCmd := newLockCommand()
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	statusCmd.SetArgs([]string{"FTR-LOCK", "--status"})
	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("lock status failed: %v", err)
	}

	releaseCmd := newLockCommand()
	releaseCmd.SetOut(buf)
	releaseCmd.SetErr(buf)
	releaseCmd.SetArgs([]string{"FTR-LOCK", "--release"})
	if err := releaseCmd.Execute(); err != nil {
		t.Fatalf("lock release failed: %v", err)
	}

	statusCmd = newLockCommand()
	statusCmd.SetOut(buf)
	statusCmd.SetErr(buf)
	statusCmd.SetArgs([]string{"FTR-LOCK", "--status"})
	if err := statusCmd.Execute(); err != nil {
		t.Fatalf("lock status after release failed: %v", err)
	}

	mgr := feature.NewManager(opts)
	feat, err := mgr.CreateFeature("Interactive Delete", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	deleteCmd := newDeleteCommand()
	deleteCmd.SetOut(buf)
	deleteCmd.SetErr(buf)
	deleteCmd.SetIn(bytes.NewBufferString("no\n"))
	deleteCmd.SetArgs([]string{feat.FrontMatter.ID})
	if err := deleteCmd.Execute(); err != nil {
		t.Fatalf("delete command should succeed on cancel: %v", err)
	}

	deleteCmd = newDeleteCommand()
	deleteCmd.SetOut(buf)
	deleteCmd.SetErr(buf)
	deleteCmd.SetIn(bytes.NewBufferString("yes\n"))
	deleteCmd.SetArgs([]string{feat.FrontMatter.ID})
	if err := deleteCmd.Execute(); err != nil {
		t.Fatalf("delete command with confirmation failed: %v", err)
	}
}

func TestRegisterCommandsIdempotent(t *testing.T) {
	registerCommands()
	count := len(rootCmd.Commands())
	registerCommands()
	if len(rootCmd.Commands()) != count {
		t.Fatalf("expected registerCommands to be idempotent")
	}
}

func TestVersionCommand(t *testing.T) {
	fix := testutil.NewFixture(t)
	_, buf := setupOptions(t, fix, false, false, false)
	cmd := newVersionCommand()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	if strings.TrimSpace(buf.String()) == "" {
		t.Fatalf("expected version output")
	}

	_, jsonBuf := setupOptions(t, fix, true, false, false)
	cmd = newVersionCommand()
	cmd.SetOut(jsonBuf)
	cmd.SetErr(jsonBuf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command (json) failed: %v", err)
	}
	if !bytes.Contains(jsonBuf.Bytes(), []byte("\"version\"")) {
		t.Fatalf("expected json version output: %s", jsonBuf.String())
	}
}

func TestIndexCommandVerbosity(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)

	// Create initial feature
	feat1, err := mgr.CreateFeature("Feature One", []string{"tag1"})
	if err != nil {
		t.Fatalf("create feature 1 failed: %v", err)
	}

	// Generate initial index
	indexCmd := newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("initial index command failed: %v", err)
	}

	// Run again with default verbosity (should show no changes)
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("second index command failed: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "No changes") {
		t.Fatalf("expected 'No changes' in output, got: %s", output)
	}

	// Add another feature
	_, err = mgr.CreateFeature("Feature Two", []string{"tag2"})
	if err != nil {
		t.Fatalf("create feature 2 failed: %v", err)
	}

	// Run with default verbosity (should show summary)
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index after add failed: %v", err)
	}
	output = buf.String()
	if !strings.Contains(output, "1 added") {
		t.Fatalf("expected '1 added' in output, got: %s", output)
	}

	// Run with -v (should show feature IDs)
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md", "-v"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index with -v failed: %v", err)
	}
	output = buf.String()
	if !strings.Contains(output, "No changes detected") {
		t.Fatalf("expected 'No changes detected' with -v, got: %s", output)
	}

	// Move a feature to test status change
	if _, _, err := mgr.MoveFeature(feat1.FrontMatter.ID, "in-progress", ""); err != nil {
		t.Fatalf("move failed: %v", err)
	}

	// Run with -v (should show status changes)
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md", "-v"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index after move failed: %v", err)
	}
	output = buf.String()
	if !strings.Contains(output, "Status Changes:") {
		t.Fatalf("expected 'Status Changes:' with -v, got: %s", output)
	}
	if !strings.Contains(output, feat1.FrontMatter.ID) {
		t.Fatalf("expected feature ID in verbose output, got: %s", output)
	}

	// Run with -vv (should show very detailed changes)
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md", "-vv"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index with -vv failed: %v", err)
	}
	output = buf.String()
	if !strings.Contains(output, "No changes detected") {
		t.Fatalf("expected 'No changes detected' with -vv after no changes, got: %s", output)
	}
}

func TestIndexCommandQuiet(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)

	// Create initial feature
	if _, err := mgr.CreateFeature("Feature One", []string{"tag1"}); err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	// Generate initial index
	indexCmd := newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("initial index command failed: %v", err)
	}

	// Run again with --quiet (should produce no output since no changes)
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md", "--quiet"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index with --quiet failed: %v", err)
	}
	output := buf.String()
	if strings.TrimSpace(output) != "" {
		t.Fatalf("expected no output with --quiet when no changes, got: %s", output)
	}

	// Add another feature
	if _, err := mgr.CreateFeature("Feature Two", []string{"tag2"}); err != nil {
		t.Fatalf("create second feature failed: %v", err)
	}

	// Run with --quiet (should show output since there are changes)
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md", "--quiet"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index with --quiet after changes failed: %v", err)
	}
	output = buf.String()
	if !strings.Contains(output, "1 added") {
		t.Fatalf("expected output with --quiet when changes exist, got: %s", output)
	}

	// Test -q short flag
	buf.Reset()
	config.SetCurrent(opts)
	indexCmd = newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "md", "--output", "features/INDEX.md", "-q"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index with -q failed: %v", err)
	}
	output = buf.String()
	if strings.TrimSpace(output) != "" {
		t.Fatalf("expected no output with -q when no changes, got: %s", output)
	}
}

func TestIndexCommandNonMarkdownFormat(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)
	mgr := feature.NewManager(opts)

	// Create a feature
	if _, err := mgr.CreateFeature("Feature One", []string{"tag1"}); err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	// Test HTML format (should not show diff info)
	indexCmd := newIndexCommand()
	indexCmd.SetOut(buf)
	indexCmd.SetErr(buf)
	indexCmd.SetArgs([]string{"--format", "html", "--output", "features/INDEX.html"})
	if err := indexCmd.Execute(); err != nil {
		t.Fatalf("index html failed: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Index written to") {
		t.Fatalf("expected simple message for HTML format, got: %s", output)
	}
}

func TestFormatIndexOutput(t *testing.T) {
	fix := testutil.NewFixture(t)
	fix.Options(t, false, false, false)

	// Create test data
	data := &indexer.Data{
		Features: []indexer.Entry{
			{ID: "FEAT-001", Status: "backlog"},
			{ID: "FEAT-002", Status: "in-progress"},
		},
		Summary: map[string]int{
			"backlog":     1,
			"in-progress": 1,
		},
	}

	// Test with nil diff
	output := formatIndexOutput(nil, data, "features/INDEX.md", 0)
	if !strings.Contains(output, "Index written to") {
		t.Fatalf("expected simple output with nil diff")
	}

	// Test with empty diff (no changes)
	diff := &indexer.Diff{}
	output = formatIndexOutput(diff, data, "features/INDEX.md", 0)
	if !strings.Contains(output, "No changes") {
		t.Fatalf("expected 'No changes' in output")
	}

	// Test with changes at different verbosity levels
	diff = &indexer.Diff{
		Added: 1,
		Changes: []indexer.Change{
			{Type: indexer.ChangeTypeAdded, FeatureID: "FEAT-003", NewStatus: "backlog"},
		},
	}

	// Verbosity 0
	output = formatIndexOutput(diff, data, "features/INDEX.md", 0)
	if !strings.Contains(output, "1 added") {
		t.Fatalf("expected summary at verbosity 0")
	}

	// Verbosity 1
	output = formatIndexOutput(diff, data, "features/INDEX.md", 1)
	if !strings.Contains(output, "Added:") || !strings.Contains(output, "FEAT-003") {
		t.Fatalf("expected detailed list at verbosity 1")
	}

	// Verbosity 2
	output = formatIndexOutput(diff, data, "features/INDEX.md", 2)
	if !strings.Contains(output, "✓ FEAT-003 added") {
		t.Fatalf("expected very verbose output at verbosity 2")
	}
}

func TestValidateCommandWithSpecs(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, false, false, false)

	// Create a feature
	mgr := feature.NewManager(opts)
	feat := buildFeatureFile(t, fix, mgr, "FTR-6000", "backlog", "Test Feature")

	// Create a spec
	specContent := `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
---

## Stack
Technology details.`

	workspace := filepath.Join(fix.Root, ".virtualboard")
	specsDir := filepath.Join(workspace, "specs")
	if err := os.WriteFile(filepath.Join(specsDir, "tech-stack.md"), []byte(specContent), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	// Test: validate all (both features and specs)
	validateCmd := newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate all failed: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "features") || !strings.Contains(output, "specs") {
		t.Errorf("expected output to mention both features and specs, got: %s", output)
	}

	// Test: validate --only-features
	buf.Reset()
	validateCmd = newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{"--only-features"})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate --only-features failed: %v", err)
	}
	output = buf.String()
	if !strings.Contains(output, "feature") {
		t.Errorf("expected output to mention features, got: %s", output)
	}

	// Test: validate --only-specs
	buf.Reset()
	validateCmd = newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{"--only-specs"})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate --only-specs failed: %v", err)
	}
	output = buf.String()
	if !strings.Contains(output, "spec") {
		t.Errorf("expected output to mention specs, got: %s", output)
	}

	// Test: validate specific feature by ID
	buf.Reset()
	validateCmd = newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{feat.FrontMatter.ID})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate feature by ID failed: %v", err)
	}

	// Test: validate specific spec by name
	buf.Reset()
	validateCmd = newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{"tech-stack.md"})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate spec by name failed: %v", err)
	}
}

func TestValidateCommandMutuallyExclusiveFlags(t *testing.T) {
	fix := testutil.NewFixture(t)
	_, buf := setupOptions(t, fix, false, false, false)

	validateCmd := newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{"--only-features", "--only-specs"})
	err := validateCmd.Execute()
	if err == nil {
		t.Fatal("expected error for mutually exclusive flags")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutually exclusive error, got: %v", err)
	}
}

func TestValidateCommandWithInvalidSpec(t *testing.T) {
	fix := testutil.NewFixture(t)
	_, buf := setupOptions(t, fix, false, false, false)

	// Create an invalid spec (missing required field)
	invalidSpecContent := `---
spec_type: tech-stack
title: Invalid Spec
status: invalid-status
last_updated: 2024-01-15
applicability:
  - backend
---

## Content
Details.`

	workspace := filepath.Join(fix.Root, ".virtualboard")
	specsDir := filepath.Join(workspace, "specs")
	if err := os.WriteFile(filepath.Join(specsDir, "invalid-spec.md"), []byte(invalidSpecContent), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	validateCmd := newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{"--only-specs"})
	err := validateCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid spec")
	}

	// Verify error output mentions the spec
	if !strings.Contains(buf.String(), "invalid-spec.md") {
		t.Errorf("expected output to mention invalid-spec.md")
	}
}

func TestValidateCommandJSONOutputWithSpecs(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts, buf := setupOptions(t, fix, true, false, false)

	// Create a feature
	mgr := feature.NewManager(opts)
	buildFeatureFile(t, fix, mgr, "FTR-7000", "backlog", "JSON Test Feature")

	// Create a spec
	specContent := `---
spec_type: database-schema
title: Database Schema
status: draft
last_updated: 2024-01-15
applicability:
  - backend
---

## Schema
Schema details.`

	workspace := filepath.Join(fix.Root, ".virtualboard")
	specsDir := filepath.Join(workspace, "specs")
	if err := os.WriteFile(filepath.Join(specsDir, "database-schema.md"), []byte(specContent), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	validateCmd := newValidateCommand()
	validateCmd.SetOut(buf)
	validateCmd.SetErr(buf)
	validateCmd.SetArgs([]string{})
	if err := validateCmd.Execute(); err != nil {
		t.Fatalf("validate command failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "\"features\"") {
		t.Error("expected JSON output to contain features key")
	}
	if !strings.Contains(output, "\"specs\"") {
		t.Error("expected JSON output to contain specs key")
	}
}

func buildFeatureFile(t *testing.T, fix *testutil.Fixture, mgr *feature.Manager, id, status, title string) *feature.Feature {
	t.Helper()
	statusDir := filepath.Join(mgr.FeaturesDir(), filepath.Base(feature.DirectoryForStatus(status)))
	feat := &feature.Feature{
		Path: filepath.Join(statusDir, fmt.Sprintf("%s-%s.md", id, util.Slugify(title))),
		FrontMatter: feature.FrontMatter{
			ID:         id,
			Title:      title,
			Status:     status,
			Owner:      "owner",
			Priority:   "medium",
			Complexity: "S",
			Created:    "2023-01-01",
			Updated:    "2023-01-01",
		},
		Body: "## Summary\n\nSummary\n\n## Details\n\nDetails\n",
	}
	data, err := feat.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	workspace := filepath.Join(fix.Root, ".virtualboard")
	rel, err := filepath.Rel(workspace, feat.Path)
	if err != nil {
		t.Fatalf("rel failed: %v", err)
	}
	fix.WriteFile(t, rel, data)
	return feat
}
