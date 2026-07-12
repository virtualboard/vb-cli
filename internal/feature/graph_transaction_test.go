package feature

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/util"
)

func TestBoardGraphGuardPreventsConcurrentDependencyCycleCommits(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	first, err := mgr.CreateFeature("Cycle first", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mgr.CreateFeature("Cycle second", nil)
	if err != nil {
		t.Fatal(err)
	}

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, mutateErr := mgr.MutateFeature(first.FrontMatter.ID, func(feat *Feature) error {
			feat.FrontMatter.Dependencies = []string{second.FrontMatter.ID}
			close(firstEntered)
			<-releaseFirst
			return nil
		})
		firstDone <- mutateErr
	}()
	<-firstEntered

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		_, mutateErr := mgr.MutateFeature(second.FrontMatter.ID, func(feat *Feature) error {
			close(secondEntered)
			feat.FrontMatter.Dependencies = []string{first.FrontMatter.ID}
			return nil
		})
		secondDone <- mutateErr
	}()
	select {
	case <-secondEntered:
		close(releaseFirst)
		t.Fatal("second graph mutation entered while the board guard was held")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first dependency commit failed: %v", err)
	}
	select {
	case err := <-secondDone:
		if !errors.Is(err, ErrDependencyCycle) {
			t.Fatalf("second dependency commit error = %v, want ErrDependencyCycle", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second graph mutation did not finish")
	}
	reloaded, err := mgr.LoadByID(second.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.FrontMatter.Dependencies) != 0 {
		t.Fatalf("cycle-producing dependency was persisted: %v", reloaded.FrontMatter.Dependencies)
	}
}

func TestDeletePolicyRejectsReferencedAndNonBacklogFeatures(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	target, err := mgr.CreateFeature("Referenced deletion target", nil)
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := mgr.CreateFeature("Deletion dependent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.MutateFeature(dependent.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Dependencies = []string{target.FrontMatter.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.DeleteFeature(target.FrontMatter.ID); !errors.Is(err, ErrDeleteConflict) {
		t.Fatalf("referenced backlog deletion error = %v, want ErrDeleteConflict", err)
	}

	doneTarget, err := mgr.CreateFeature("Done deletion target", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(doneTarget.FrontMatter.ID, "in-progress", opts.Actor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(doneTarget.FrontMatter.ID, "review", "reviewer"); err != nil {
		t.Fatal(err)
	}
	opts.Actor = "reviewer"
	if _, _, err := mgr.MoveFeature(doneTarget.FrontMatter.ID, "done", "reviewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.DeleteFeature(doneTarget.FrontMatter.ID); !errors.Is(err, ErrDeleteConflict) {
		t.Fatalf("done deletion error = %v, want ErrDeleteConflict", err)
	}
}

func TestDeletePlanBindsApprovalToExactSourceAndGraph(t *testing.T) {
	t.Run("source identity", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := NewManager(opts)
		target, err := mgr.CreateFeature("Approved source deletion", nil)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := mgr.PrepareDelete(target.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(target.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := util.WriteFileAtomic(target.Path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.DeleteFeaturePlanned(plan); !errors.Is(err, ErrStaleFeature) {
			t.Fatalf("stale approved deletion error = %v, want ErrStaleFeature", err)
		}
		if _, err := os.Lstat(target.Path); err != nil {
			t.Fatalf("stale approved deletion removed replacement: %v", err)
		}
	})

	t.Run("reverse dependency", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := NewManager(opts)
		target, err := mgr.CreateFeature("Approved graph deletion", nil)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := mgr.PrepareDelete(target.FrontMatter.ID)
		if err != nil {
			t.Fatal(err)
		}
		dependent, err := mgr.CreateFeature("Late reverse dependency", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.MutateFeature(dependent.FrontMatter.ID, func(feat *Feature) error {
			feat.FrontMatter.Dependencies = []string{target.FrontMatter.ID}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.DeleteFeaturePlanned(plan); !errors.Is(err, ErrDeleteConflict) {
			t.Fatalf("stale graph deletion error = %v, want ErrDeleteConflict", err)
		}
	})
}

func TestBoardGraphSnapshotBlocksFeatureCommitUntilPublishReturns(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	target, err := mgr.CreateFeature("Snapshot serialization", nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshotEntered := make(chan struct{})
	releaseSnapshot := make(chan struct{})
	snapshotDone := make(chan error, 1)
	go func() {
		snapshotDone <- mgr.WithBoardGraphSnapshot(func() error {
			close(snapshotEntered)
			<-releaseSnapshot
			return nil
		})
	}()
	<-snapshotEntered
	mutationDone := make(chan error, 1)
	go func() {
		_, mutationErr := mgr.MutateFeature(target.FrontMatter.ID, func(feat *Feature) error {
			feat.FrontMatter.Title = "committed after snapshot"
			return nil
		})
		mutationDone <- mutationErr
	}()
	select {
	case err := <-mutationDone:
		close(releaseSnapshot)
		t.Fatalf("mutation committed while snapshot callback was live: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseSnapshot)
	if err := <-snapshotDone; err != nil {
		t.Fatal(err)
	}
	if err := <-mutationDone; err != nil {
		t.Fatal(err)
	}
}
