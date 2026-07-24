package feature

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestManagerUsesWorkspaceLifecycleContract(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	if err := os.WriteFile(filepath.Join(opts.RootDir, "virtualboard.json"), contract.CanonicalJSON(), 0o600); err != nil {
		t.Fatalf("write contract: %v", err)
	}
	mgr := NewManager(opts)
	if mgr.TemplatePath() != filepath.Join(opts.RootDir, "templates", "feature.md") {
		t.Fatalf("template path: %s", mgr.TemplatePath())
	}
	if mgr.SchemaPath() != filepath.Join(opts.RootDir, "schemas", "frontmatter.schema.json") {
		t.Fatalf("schema path: %s", mgr.SchemaPath())
	}
	if mgr.IndexPath() != filepath.Join(opts.RootDir, "features", "INDEX.md") {
		t.Fatalf("index path: %s", mgr.IndexPath())
	}
	feat, err := mgr.CreateFeature("Contract Feature", nil)
	if err != nil {
		t.Fatalf("create feature: %v", err)
	}
	if feat.FrontMatter.Status != "backlog" {
		t.Fatalf("initial status = %s, want backlog", feat.FrontMatter.Status)
	}
	wantReadyDir := filepath.Join(opts.RootDir, "features", "backlog")
	if filepath.Dir(feat.Path) != wantReadyDir {
		t.Fatalf("feature directory = %s, want %s", filepath.Dir(feat.Path), wantReadyDir)
	}

	opts.Actor = "builder"
	moved, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "builder")
	if err != nil {
		t.Fatalf("contract transition: %v", err)
	}
	if moved.FrontMatter.Owner != "builder" || filepath.Dir(moved.Path) != filepath.Join(opts.RootDir, "features", "in-progress") {
		t.Fatalf("unexpected moved feature: owner=%s path=%s", moved.FrontMatter.Owner, moved.Path)
	}
	opts.Actor = "builder"
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "backlog", ""); err == nil {
		t.Fatal("undeclared reverse transition was accepted")
	}
}

func copyContractFixtureFile(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidWorkspaceContractFailsClosed(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	if err := os.WriteFile(filepath.Join(opts.RootDir, "virtualboard.json"), []byte(`{"feature":`), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(opts)
	if _, err := mgr.CreateFeature("Must Fail", nil); err == nil {
		t.Fatal("invalid contract fell back to compiled lifecycle")
	}
}
