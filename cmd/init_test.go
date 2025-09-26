package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestInitCommandCreatesWorkspace(t *testing.T) {
	fix := testutil.NewFixture(t)
	workspace := filepath.Join(fix.Root, initDirName)
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatalf("failed to remove workspace: %v", err)
	}
	opts := fix.Options(t, false, false, false)
	opts.JSONOutput = true
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	target := filepath.Join(fix.Root, initDirName)

	called := false
	original := fetchTemplate
	fetchTemplate = func(workdir, dest string) error {
		called = true
		if workdir != fix.Root {
			t.Fatalf("unexpected workdir: %s", workdir)
		}
		if dest != initDirName {
			t.Fatalf("unexpected dest: %s", dest)
		}
		return os.MkdirAll(target, 0o755)
	}
	t.Cleanup(func() { fetchTemplate = original })

	cmd := newInitCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !called {
		t.Fatalf("expected git clone to be called")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected directory created: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("VirtualBoard project initialised")) {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

func TestInitCommandWarnsWhenExists(t *testing.T) {
	fix := testutil.NewFixture(t)
	target := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	called := false
	original := fetchTemplate
	fetchTemplate = func(workdir, dest string) error {
		called = true
		return nil
	}
	t.Cleanup(func() { fetchTemplate = original })

	cmd := newInitCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when workspace exists")
	}
	if ExitCode(err) != ExitCodeValidation {
		t.Fatalf("expected validation exit code, got %v", err)
	}
	if called {
		t.Fatalf("git clone should not be called")
	}
	expected := "VirtualBoard workspace already initialised at .virtualboard. Use --force to re-create it. We recommend managing this directory with git."
	if err.Error() != expected {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestInitCommandForceRecreates(t *testing.T) {
	fix := testutil.NewFixture(t)
	target := filepath.Join(fix.Root, initDirName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	file := filepath.Join(target, "stale.txt")
	if err := os.WriteFile(file, []byte("stale"), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := fix.Options(t, false, false, false)
	config.SetCurrent(opts)
	t.Cleanup(func() { config.SetCurrent(nil) })

	original := fetchTemplate
	fetchTemplate = func(workdir, dest string) error {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Fatalf("expected original directory removed")
		}
		return os.MkdirAll(filepath.Join(workdir, dest), 0o755)
	}
	t.Cleanup(func() { fetchTemplate = original })

	cmd := newInitCommand()
	cmd.Flags().Set("force", "true")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	projectRoot := filepath.Dir(opts.RootDir)
	if _, err := os.Stat(filepath.Join(projectRoot, initDirName)); err != nil {
		t.Fatalf("expected directory recreated: %v", err)
	}
}
