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
	if mgr.TemplatePath() != filepath.Join(opts.RootDir, "templates", "feature.md") {
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

	if _, err := mgr.DeleteFeature(feat.FrontMatter.ID); !errors.Is(err, ErrDeleteConflict) {
		t.Fatalf("non-backlog referenced feature deletion error = %v, want ErrDeleteConflict", err)
	}

	deletable, err := mgr.CreateFeature("Unreferenced Backlog Deletion", nil)
	if err != nil {
		t.Fatal(err)
	}
	if path, err := mgr.DeleteFeature(deletable.FrontMatter.ID); err != nil {
		t.Fatalf("delete unreferenced backlog feature: %v", err)
	} else if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected backlog feature removed")
	}
	if _, err := mgr.LoadByID(deletable.FrontMatter.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted backlog feature not found, got %v", err)
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
	if _, err := mgr.List(); err == nil {
		t.Fatal("missing configured features root was treated as an empty board")
	}

	if _, _, err := mgr.MoveFeature("missing", "backlog", ""); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("expected invalid feature identity for malformed ID: %v", err)
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
			Priority:     "P2",
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

func TestListWithInvalidFiles(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	// Create a valid feature
	feat := newTestFeature(fix, "FTR-0001", "backlog", "Valid", []string{})
	mustWriteFeature(t, fix, feat)

	// Create an invalid markdown file (missing frontmatter)
	fix.WriteFile(t, "features/backlog/invalid1.md", []byte("# Just a heading\nNo frontmatter here"))

	// Create another invalid file (malformed frontmatter)
	fix.WriteFile(t, "features/backlog/invalid2.md", []byte("---\ninvalid yaml: [\n---\n"))

	// List should fail with InvalidFileError
	_, err := mgr.List()
	if err == nil {
		t.Fatalf("expected error when listing with invalid files")
	}

	var invalidErr *InvalidFileError
	if !errors.As(err, &invalidErr) {
		t.Fatalf("expected InvalidFileError, got: %T", err)
	}

	if len(invalidErr.Files) != 2 {
		t.Fatalf("expected 2 invalid files, got: %d", len(invalidErr.Files))
	}

	// Check that the error message contains file paths
	errMsg := err.Error()
	if !containsString(errMsg, "invalid1.md") {
		t.Fatalf("expected error to mention invalid1.md, got: %s", errMsg)
	}
	if !containsString(errMsg, "invalid2.md") {
		t.Fatalf("expected error to mention invalid2.md, got: %s", errMsg)
	}
	if !containsString(errMsg, "review and move") {
		t.Fatalf("expected error to include guidance, got: %s", errMsg)
	}
}

func TestRenameToMatchTitle(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Original Title", nil)
	if err != nil {
		t.Fatal(err)
	}
	originalPath := feat.Path
	feat.FrontMatter.Title = "Updated Title With Changes"
	if err := mgr.Save(feat); err != nil {
		t.Fatal(err)
	}
	renamed, err := mgr.RenameToMatchTitle(feat)
	if err != nil {
		t.Fatal(err)
	}
	if renamed {
		t.Fatal("immutable feature basename was renamed")
	}
	if feat.Path != originalPath {
		t.Fatalf("path changed from %s to %s", originalPath, feat.Path)
	}
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("immutable file missing: %v", err)
	}
	loaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.FrontMatter.Title != "Updated Title With Changes" {
		t.Fatal("title update was not preserved")
	}
}

func TestCreateFeatureLocking(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	feat, err := mgr.CreateFeature("Locked Create", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}
	if feat.FrontMatter.ID != "FTR-0001" {
		t.Fatalf("expected FTR-0001, got %s", feat.FrontMatter.ID)
	}

	// Internal serialization uses a callback-scoped guard, never a TTL record.
	lockPath := filepath.Join(opts.RootDir, "locks", "op-create-feature.lock")
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("create left an obsolete operational TTL lock")
	}
}

func TestMoveFeatureLocking(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	feat, err := mgr.CreateFeature("Locked Move", nil)
	if err != nil {
		t.Fatalf("create feature failed: %v", err)
	}

	opts.Actor = "tester"
	_, _, err = mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "tester")
	if err != nil {
		t.Fatalf("move feature failed: %v", err)
	}

	// Internal serialization uses a callback-scoped guard, never a TTL record.
	lockPath := filepath.Join(opts.RootDir, "locks", featureMutationLockID(feat.FrontMatter.ID)+".lock")
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("move left an obsolete operational TTL lock")
	}
}

func TestWithLockNilLockMgr(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := &Manager{
		opts:    opts,
		log:     opts.Logger().WithField("component", "feature"),
		lockMgr: nil,
	}

	called := false
	err := mgr.withLock("test", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("withLock with nil lockMgr should not error: %v", err)
	}
	if !called {
		t.Fatal("fn should have been called")
	}
}

func TestWithLockDryRun(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, true) // dry-run mode
	mgr := NewManager(opts)

	called := false
	err := mgr.withLock("test", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("withLock in dry-run should not error: %v", err)
	}
	if !called {
		t.Fatal("fn should have been called in dry-run")
	}
}

func TestRenameToMatchTitleDryRun(t *testing.T) {
	fix := testutil.NewFixture(t)
	dryOpts := fix.Options(t, false, false, true)
	mgr := NewManager(dryOpts)

	// Create a feature
	feat := newTestFeature(fix, "FTR-0500", "backlog", "Original", nil)
	mustWriteFeature(t, fix, feat)

	originalPath := feat.Path

	// Update title
	feat.FrontMatter.Title = "Updated Name"

	// Rename in dry-run mode
	renamed, err := mgr.RenameToMatchTitle(feat)
	if err != nil {
		t.Fatalf("dry run rename failed: %v", err)
	}
	if renamed {
		t.Fatalf("dry-run proposed an immutable basename rename")
	}

	if feat.Path != originalPath {
		t.Fatalf("dry-run changed immutable path in struct")
	}

	// Verify old file still exists (dry-run doesn't modify disk)
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("dry-run should not remove old file: %v", err)
	}
}
