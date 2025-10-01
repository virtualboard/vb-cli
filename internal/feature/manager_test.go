package feature

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/util"
)

func TestManagerLifecycle(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	if mgr.FeaturesDir() != filepath.Join(opts.RootDir, "features") {
		t.Fatalf("unexpected features dir")
	}
	if mgr.TemplatePath() != filepath.Join(opts.RootDir, "templates", "spec.md") {
		t.Fatalf("unexpected template path")
	}
	if mgr.SchemaPath() != filepath.Join(opts.RootDir, "schemas", "frontmatter.schema.json") {
		t.Fatalf("unexpected schema path")
	}
	if mgr.LocksDir() != filepath.Join(opts.RootDir, "locks") {
		t.Fatalf("unexpected locks dir")
	}

	firstID, err := mgr.NextID()
	if err != nil || firstID != "FTR-0001" {
		t.Fatalf("expected first id FTR-0001, got %s (%v)", firstID, err)
	}

	feat, err := mgr.CreateFeature("First Feature", []string{"alpha"})
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}
	if _, err := os.Stat(feat.Path); err != nil {
		t.Fatalf("expected feature on disk: %v", err)
	}

	secondID, err := mgr.NextID()
	if err != nil || secondID != "FTR-0002" {
		t.Fatalf("expected second id FTR-0002, got %s (%v)", secondID, err)
	}

	loaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatalf("load feature failed: %v", err)
	}
	loaded.FrontMatter.Title = "Updated"
	if err := mgr.UpdateFeature(loaded); err != nil {
		t.Fatalf("update feature failed: %v", err)
	}

	invalidMove := "done"
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, invalidMove, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition error, got %v", err)
	}

	feat2 := newTestFeature(fix, "FTR-0100", "backlog", "Dependent", []string{})
	feat2.FrontMatter.Dependencies = []string{feat.FrontMatter.ID}
	mustWriteFeature(t, fix, feat2)

	if _, _, err := mgr.MoveFeature("FTR-0100", "in-progress", ""); !errors.Is(err, ErrDependencyBlocked) {
		t.Fatalf("expected dependency blocked error, got %v", err)
	}

	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", ""); err != nil {
		t.Fatalf("move feature failed: %v", err)
	}

	list, err := mgr.List()
	if err != nil || len(list) < 1 {
		t.Fatalf("expected list to return entries: %v (%d)", err, len(list))
	}

	if path, err := mgr.DeleteFeature(feat.FrontMatter.ID); err != nil {
		t.Fatalf("delete feature failed: %v", err)
	} else if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected file removed")
	}

	if _, err := mgr.LoadByID(feat.FrontMatter.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}

	indexPath := filepath.Join(mgr.FeaturesDir(), "INDEX.md")
	if err := os.WriteFile(indexPath, []byte("# index\n"), 0o600); err != nil {
		t.Fatalf("failed to write index file: %v", err)
	}
	if _, err := mgr.List(); err != nil {
		t.Fatalf("list should ignore index file: %v", err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("cleanup index file failed: %v", err)
	}

	dryOpts := fix.Options(t, false, false, true)
	dryMgr := NewManager(dryOpts)
	if err := dryMgr.Save(newTestFeature(fix, "FTR-0200", "backlog", "dry", nil)); err != nil {
		t.Fatalf("save in dry run should not error: %v", err)
	}

	feat3 := newTestFeature(fix, "FTR-0300", "backlog", "to-delete", nil)
	mustWriteFeature(t, fix, feat3)
	if path, err := dryMgr.DeleteFeature("FTR-0300"); err != nil {
		t.Fatalf("dry run delete failed: %v", err)
	} else if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("dry run should keep file: %v", statErr)
	}

	os.RemoveAll(filepath.Join(opts.RootDir, "features"))
	if _, err := mgr.List(); err != nil {
		t.Fatalf("expected list to handle missing directory: %v", err)
	}

	if _, _, err := mgr.MoveFeature("missing", "backlog", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found for missing feature: %v", err)
	}
}

func TestNormalizeList(t *testing.T) {
	values := []string{" one ", "", "two"}
	got := normalizeList(values)
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("unexpected normalized list: %#v", got)
	}
}

func newTestFeature(fix *testutil.Fixture, id, status, title string, labels []string) *Feature {
	workspace := filepath.Join(fix.Root, ".virtualboard")
	return &Feature{
		Path: filepath.Join(workspace, DirectoryForStatus(status), fmt.Sprintf("%s-%s.md", id, util.Slugify(title))),
		FrontMatter: FrontMatter{
			ID:           id,
			Title:        title,
			Status:       status,
			Owner:        "owner",
			Priority:     "medium",
			Complexity:   "M",
			Created:      "2023-01-01",
			Updated:      "2023-01-01",
			Labels:       labels,
			Dependencies: []string{},
		},
		Body: "## Summary\n\nSummary text.\n\n## Details\n\nDetailed text.\n",
	}
}

func mustWriteFeature(t *testing.T, fix *testutil.Fixture, feat *Feature) {
	data, err := feat.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	workspace := filepath.Join(fix.Root, ".virtualboard")
	rel, err := filepath.Rel(workspace, feat.Path)
	if err != nil {
		t.Fatalf("rel path failed: %v", err)
	}
	fix.WriteFile(t, rel, data)
}
