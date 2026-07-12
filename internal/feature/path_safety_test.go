package feature

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestLoadByIDRejectsFilenameFrontmatterIdentityMismatchBeforeAuthorization(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "alice"
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Identity Lock Guard", nil)
	if err != nil {
		t.Fatal(err)
	}
	originalID := feat.FrontMatter.ID
	feat.FrontMatter.ID = "FTR-9999"
	corrupt, err := feat.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(feat.Path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	opts.Actor = "mallory"
	if _, err := mgr.LoadByID(originalID); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("mismatched feature identity was accepted: %v", err)
	}
	data, err := os.ReadFile(feat.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "id: FTR-9999") {
		t.Fatal("identity rejection unexpectedly rewrote feature")
	}
}

func TestFeatureDiscoveryRejectsSymbolicLinkFiles(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	target := filepath.Join(t.TempDir(), "outside-feature.md")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(opts.RootDir, "features", "backlog", "FTR-0999-linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mgr := NewManager(opts)

	for name, operation := range map[string]func() error{
		"list":    func() error { _, err := mgr.List(); return err },
		"load":    func() error { _, err := mgr.LoadByID("FTR-0999"); return err },
		"next-id": func() error { _, err := mgr.NextID(); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); err == nil || !strings.Contains(err.Error(), "symbolic link") {
				t.Fatalf("symbolic-link feature was accepted: %v", err)
			}
		})
	}
}

func TestFeatureDiscoveryRejectsNonRegularMarkdownPaths(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	nonRegular := filepath.Join(opts.RootDir, "features", "backlog", "FTR-0998-directory.md")
	if err := os.Mkdir(nonRegular, 0o750); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(opts)

	for name, operation := range map[string]func() error{
		"list":    func() error { _, err := mgr.List(); return err },
		"load":    func() error { _, err := mgr.LoadByID("FTR-0998"); return err },
		"next-id": func() error { _, err := mgr.NextID(); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); err == nil || !strings.Contains(err.Error(), "not a regular file") {
				t.Fatalf("non-regular feature path was accepted: %v", err)
			}
		})
	}
}

func TestFeatureDiscoveryRejectsLinkedFeaturesRoot(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	features := filepath.Join(opts.RootDir, "features")
	realFeatures := filepath.Join(opts.RootDir, "features-real")
	if err := os.Rename(features, realFeatures); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realFeatures, features); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewManager(opts).List(); err == nil || !strings.Contains(err.Error(), "features root is a symbolic link") {
		t.Fatalf("linked features root was treated as an empty board: %v", err)
	}
}

func TestFeatureDiscoveryRejectsMissingFeaturesRootAndStatus(t *testing.T) {
	t.Run("features root", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		if err := os.RemoveAll(filepath.Join(opts.RootDir, "features")); err != nil {
			t.Fatal(err)
		}
		if _, err := NewManager(opts).List(); err == nil || !strings.Contains(err.Error(), "configured features root is missing") {
			t.Fatalf("missing features root was accepted: %v", err)
		}
	})

	t.Run("status directory", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		if err := os.Remove(filepath.Join(opts.RootDir, "features", "review")); err != nil {
			t.Fatal(err)
		}
		if _, err := NewManager(opts).NextID(); err == nil || !strings.Contains(err.Error(), "inspect status directory review") {
			t.Fatalf("missing status directory was accepted: %v", err)
		}
	})
}

func TestFeatureDiscoveryRejectsLinkedStatusDirectory(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	backlog := filepath.Join(opts.RootDir, "features", "backlog")
	realBacklog := filepath.Join(opts.RootDir, "features", "backlog-real")
	if err := os.Rename(backlog, realBacklog); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realBacklog, backlog); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewManager(opts).NextID(); err == nil || !strings.Contains(err.Error(), "status directory is a symbolic link") {
		t.Fatalf("linked status directory was treated as empty: %v", err)
	}
}

func TestFeatureDiscoveryRejectsNestedStatusContent(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	nested := filepath.Join(opts.RootDir, "features", "backlog", "nested")
	if err := os.Mkdir(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := NewManager(opts).List(); err == nil || !strings.Contains(err.Error(), "nested directories are forbidden") {
		t.Fatalf("nested feature content was accepted: %v", err)
	}
}

func TestFeatureDiscoveryEnforcesEntryAndAggregateLimits(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	backlog := filepath.Join(opts.RootDir, "features", "backlog")
	if err := os.WriteFile(filepath.Join(backlog, "one.txt"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backlog, "two.txt"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(opts)
	if _, err := mgr.discoverFeatureFilesWithLimits(1, maxFeatureAggregateBytes); err == nil || !strings.Contains(err.Error(), "entry limit exceeded") {
		t.Fatalf("entry limit was not enforced: %v", err)
	}
	if err := os.Remove(filepath.Join(backlog, "one.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(backlog, "two.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backlog, "FTR-0996-aggregate.md"), []byte("12"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.discoverFeatureFilesWithLimits(maxFeatureDirectoryEntries, 1); err == nil || !strings.Contains(err.Error(), "aggregate feature byte limit exceeded") {
		t.Fatalf("aggregate byte limit was not enforced: %v", err)
	}
}

func TestSaveRejectsFeatureOutsideCanonicalStatusDirectory(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat := newTestFeature(fix, "FTR-0997", "backlog", "misplaced", nil)
	feat.Path = filepath.Join(opts.RootDir, "FTR-0997-misplaced.md")

	if err := mgr.Save(feat); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("misplaced feature save error = %v, want ErrIdentityMismatch", err)
	}
	if _, err := os.Lstat(feat.Path); !os.IsNotExist(err) {
		t.Fatalf("misplaced feature was written: %v", err)
	}
}
