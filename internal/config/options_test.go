package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOptionsInitWithExplicitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "features"), 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := New()
	if err := opts.Init(root, true, false, true, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSamePath(t, opts.RootDir, root)
	if !opts.JSONOutput || !opts.DryRun {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if err := opts.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestOptionsInitInfersRootFromCWD(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "features"), 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	cwd, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	defer os.Chdir(cwd)

	opts := New()
	if err := opts.Init("", false, false, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSamePath(t, opts.RootDir, root)
	if err := opts.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestOptionsInitFallsBackToSrcFeatures(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(filepath.Join(srcRoot, "features"), 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	opts := New()
	if err := opts.Init(root, false, false, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSamePath(t, opts.RootDir, srcRoot)
	if err := opts.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestOptionsInitWithLogFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "features"), 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	logPath := filepath.Join(root, "cli.log")
	opts := New()
	if err := opts.Init(root, false, true, false, logPath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("expected log file: %v", err)
	}
	if err := opts.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

func TestOptionsInitErrors(t *testing.T) {
	opts := New()
	if err := opts.Init("/no/such/root", false, false, false, ""); err == nil {
		t.Fatalf("expected error for invalid root")
	}

	SetCurrent(nil)
	if _, err := Current(); err == nil {
		t.Fatalf("expected error when current not set")
	}

	goodRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(goodRoot, "features"), 0o755); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	goodOpts := New()
	if err := goodOpts.Init(goodRoot, false, false, false, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	SetCurrent(goodOpts)
	cur, err := Current()
	if err != nil || cur != goodOpts {
		t.Fatalf("unexpected current: %v %v", cur, err)
	}
}

func assertSamePath(t *testing.T, actual, expected string) {
	t.Helper()
	actualResolved, err := filepath.EvalSymlinks(actual)
	if err != nil {
		t.Fatalf("failed to resolve path %s: %v", actual, err)
	}
	expectedResolved, err := filepath.EvalSymlinks(expected)
	if err != nil {
		t.Fatalf("failed to resolve path %s: %v", expected, err)
	}
	if actualResolved != expectedResolved {
		t.Fatalf("expected root %s, got %s", expectedResolved, actualResolved)
	}
}
