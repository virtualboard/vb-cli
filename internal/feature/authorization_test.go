package feature

import (
	"errors"
	"testing"

	"github.com/virtualboard/vb-cli/internal/lock"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestMutationsEnforceFeatureOwner(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Owned Feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	feat.FrontMatter.Owner = "alice"
	if err := mgr.Save(feat); err != nil {
		t.Fatal(err)
	}
	opts.Actor = "bob"

	loaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateFeature(loaded); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("update bypassed owner: %v", err)
	}
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "bob"); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("move bypassed owner: %v", err)
	}
	if _, err := mgr.DeleteFeature(feat.FrontMatter.ID); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("delete bypassed owner: %v", err)
	}
}

func TestMutationsEnforceActiveFeatureLock(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Locked Feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.NewManager(opts).Acquire(feat.FrontMatter.ID, "alice", 5, false); err != nil {
		t.Fatal(err)
	}
	opts.Actor = "bob"

	loaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateFeature(loaded); !errors.Is(err, ErrLockConflict) {
		t.Fatalf("update bypassed lock: %v", err)
	}
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "bob"); !errors.Is(err, ErrLockConflict) {
		t.Fatalf("move bypassed lock: %v", err)
	}
	if _, err := mgr.DeleteFeature(feat.FrontMatter.ID); !errors.Is(err, ErrLockConflict) {
		t.Fatalf("delete bypassed lock: %v", err)
	}
}

func TestInitialClaimOwnerMustMatchActor(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "claimer"
	mgr := NewManager(opts)
	feat, err := mgr.CreateFeature("Claim Identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.MoveFeature(feat.FrontMatter.ID, "in-progress", "victim"); !errors.Is(err, ErrOwnershipConflict) {
		t.Fatalf("actor claimed feature as another owner: %v", err)
	}
	reloaded, err := mgr.LoadByID(feat.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.FrontMatter.Status != "backlog" || reloaded.FrontMatter.Owner != "unassigned" {
		t.Fatalf("failed claim mutated feature: %+v", reloaded.FrontMatter)
	}
}
