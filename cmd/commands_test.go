package cmd

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/feature"
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
			Complexity: "low",
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
