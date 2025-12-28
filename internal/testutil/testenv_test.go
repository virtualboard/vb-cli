package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureLifecycle(t *testing.T) {
	fix := NewFixture(t)
	workspace := filepath.Join(fix.Root, ".virtualboard")
	if _, err := os.Stat(filepath.Join(workspace, "templates", "feature.md")); err != nil {
		t.Fatalf("expected template file: %v", err)
	}

	fix.WriteFile(t, filepath.Join("features", "backlog", "sample.md"), []byte("content"))
	opts := fix.Options(t, true, true, false)
	if opts.RootDir != workspace {
		t.Fatalf("unexpected root: %s", opts.RootDir)
	}

	if opts.JSONOutput != true || opts.Verbose != true {
		t.Fatalf("unexpected options: %+v", opts)
	}
}
