package migration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/lock"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestLifecycleMigrationInferenceApplyAndIdempotency(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "implementer"
	mgr := feature.NewManager(opts)
	backlog := writeLegacyFeature(t, mgr, "FTR-0300", "backlog", "unassigned", "2024-01-01")
	inProgress := writeLegacyFeature(t, mgr, "FTR-0301", "in-progress", "implementer", "2024-02-01")

	plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{
		ImplementationOwner: map[string]string{"FTR-0301": "implementer"},
		StatusChanged:       map[string]string{"FTR-0301": "2024-02-15"},
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if plan.Total != 2 || len(plan.Changes) != 2 {
		t.Fatalf("unexpected plan: total=%d changes=%d", plan.Total, len(plan.Changes))
	}
	if err := plan.Apply(false); err != nil {
		t.Fatalf("apply: %v", err)
	}

	backlogReloaded, err := mgr.LoadByID(backlog.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if backlogReloaded.FrontMatter.ImplementationOwner != "unassigned" || backlogReloaded.FrontMatter.StatusChanged != "2024-01-01" {
		t.Fatalf("backlog inference: %+v", backlogReloaded.FrontMatter)
	}
	inProgressReloaded, err := mgr.LoadByID(inProgress.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inProgressReloaded.FrontMatter.ImplementationOwner != "implementer" || inProgressReloaded.FrontMatter.StatusChanged != "2024-02-15" {
		t.Fatalf("in-progress inference: %+v", inProgressReloaded.FrontMatter)
	}
	if !strings.Contains(inProgressReloaded.Body, "Legacy body must survive byte-for-byte semantically.") {
		t.Fatalf("legacy body was not preserved: %q", inProgressReloaded.Body)
	}
	if inProgressReloaded.FrontMatter.Updated != time.Now().Format("2006-01-02") {
		t.Fatalf("updated not refreshed: %s", inProgressReloaded.FrontMatter.Updated)
	}

	second, err := PreflightLifecycleMetadata(opts, mgr, Assignments{})
	if err != nil {
		t.Fatalf("idempotent preflight: %v", err)
	}
	if len(second.Changes) != 0 {
		t.Fatalf("idempotent migration planned %d changes", len(second.Changes))
	}
}

func TestLifecycleMigrationAmbiguityFailsBeforeAnyWrite(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "reviewer"
	mgr := feature.NewManager(opts)
	backlog := writeLegacyFeature(t, mgr, "FTR-0310", "backlog", "unassigned", "2024-01-01")
	review := writeLegacyFeature(t, mgr, "FTR-0311", "review", "reviewer", "2024-03-01")
	before, err := os.ReadFile(backlog.Path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := PreflightLifecycleMetadata(opts, mgr, Assignments{}); !errors.Is(err, ErrPreflight) {
		t.Fatalf("ambiguous review metadata accepted: %v", err)
	}
	after, err := os.ReadFile(backlog.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("preflight failure partially migrated backlog feature")
	}

	plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{
		ImplementationOwner: map[string]string{review.FrontMatter.ID: "original-implementer"},
		StatusChanged:       map[string]string{review.FrontMatter.ID: "2024-03-05"},
	})
	if err != nil {
		t.Fatalf("explicit review migration: %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("expected backlog and review changes, got %d", len(plan.Changes))
	}
}

func TestLifecycleMigrationRejectsUnknownMappingsAndDryRunWritesNothing(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	legacy := writeLegacyFeature(t, mgr, "FTR-0320", "backlog", "unassigned", "2024-01-01")
	if _, err := PreflightLifecycleMetadata(opts, mgr, Assignments{
		StatusChanged: map[string]string{"FTR-9999": "2024-01-01"},
	}); !errors.Is(err, ErrPreflight) {
		t.Fatalf("unknown mapping accepted: %v", err)
	}
	before, _ := os.ReadFile(legacy.Path)
	plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(legacy.Path)
	if !bytes.Equal(before, after) {
		t.Fatal("dry-run wrote lifecycle metadata")
	}
}

func TestLifecycleMigrationAdministrativeOverrideSupportsMultiOwnerBoard(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "migration-admin"
	mgr := feature.NewManager(opts)
	writeLegacyFeature(t, mgr, "FTR-0330", "in-progress", "alice", "2024-01-01")
	writeLegacyFeature(t, mgr, "FTR-0331", "in-progress", "bob", "2024-01-01")
	statusMappings := map[string]string{"FTR-0330": "2024-01-10", "FTR-0331": "2024-01-11"}
	ownerMappings := map[string]string{"FTR-0330": "alice", "FTR-0331": "bob"}

	if _, err := PreflightLifecycleMetadata(opts, mgr, Assignments{ImplementationOwner: ownerMappings, StatusChanged: statusMappings}); !errors.Is(err, ErrPreflight) {
		t.Fatalf("multi-owner board migrated without administrative override: %v", err)
	}
	plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{ImplementationOwner: ownerMappings, StatusChanged: statusMappings, AllowOwnerOverride: true})
	if err != nil {
		t.Fatalf("administrative migration preflight: %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("administrative plan changes = %d, want 2", len(plan.Changes))
	}
}

func TestLifecycleMigrationAdministrativeOverrideNeverBypassesLocks(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	opts.Actor = "migration-admin"
	mgr := feature.NewManager(opts)
	legacy := writeLegacyFeature(t, mgr, "FTR-0340", "in-progress", "alice", "2024-01-01")
	if _, err := lock.NewManager(opts).Acquire(legacy.FrontMatter.ID, "alice", 5, false); err != nil {
		t.Fatal(err)
	}
	if _, err := PreflightLifecycleMetadata(opts, mgr, Assignments{
		ImplementationOwner: map[string]string{legacy.FrontMatter.ID: "alice"},
		StatusChanged:       map[string]string{legacy.FrontMatter.ID: "2024-01-10"},
		AllowOwnerOverride:  true,
	}); !errors.Is(err, ErrPreflight) {
		t.Fatalf("administrative override bypassed active lock: %v", err)
	}
}

func TestLifecycleMigrationApplyRejectsEditsAfterPreflight(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	legacy := writeLegacyFeature(t, mgr, "FTR-0350", "backlog", "unassigned", "2024-01-01")
	plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{})
	if err != nil {
		t.Fatal(err)
	}
	concurrent := append([]byte{}, plan.Changes[0].OriginalData...)
	concurrent = append(concurrent, []byte("\nConcurrent note.\n")...)
	if err := os.WriteFile(legacy.Path, concurrent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(false); !errors.Is(err, ErrConcurrentChange) {
		t.Fatalf("post-preflight edit was overwritten: %v", err)
	}
	after, err := os.ReadFile(legacy.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, concurrent) {
		t.Fatal("migration changed concurrent content")
	}
}

func TestLifecycleMigrationApplyReauthorizesUserLockAfterPreflight(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	legacy := writeLegacyFeature(t, mgr, "FTR-0351", "backlog", "unassigned", "2024-01-01")
	plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(legacy.Path)
	if err != nil {
		t.Fatal(err)
	}
	lockMgr := lock.NewManager(opts)
	info, err := lockMgr.Acquire(legacy.FrontMatter.ID, "another-actor", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockMgr.Release(legacy.FrontMatter.ID, info.Token) })

	if err := plan.Apply(false); !errors.Is(err, feature.ErrLockConflict) {
		t.Fatalf("migration ignored user lock acquired after preflight: %v", err)
	}
	after, err := os.ReadFile(legacy.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("migration wrote feature after guarded reauthorization failed")
	}
}

func TestLifecycleMigrationRollbackPreservesConcurrentContent(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := feature.NewManager(opts)
	first := writeLegacyFeature(t, mgr, "FTR-0360", "backlog", "unassigned", "2024-01-01")
	writeLegacyFeature(t, mgr, "FTR-0361", "backlog", "unassigned", "2024-01-01")
	plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{})
	if err != nil {
		t.Fatal(err)
	}
	originalWriter := replaceMigrationFile
	calls := 0
	concurrent := []byte("concurrent replacement\n")
	replaceMigrationFile = func(batch *feature.MutationBatch, change Change, expected, replacement []byte) error {
		calls++
		if calls == 2 {
			if err := os.WriteFile(first.Path, concurrent, 0o600); err != nil {
				return err
			}
			return errors.New("injected second write failure")
		}
		return originalWriter(batch, change, expected, replacement)
	}
	t.Cleanup(func() { replaceMigrationFile = originalWriter })
	if err := plan.Apply(false); err == nil || !strings.Contains(err.Error(), "concurrent content preserved") {
		t.Fatalf("rollback conflict was not surfaced: %v", err)
	}
	after, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, concurrent) {
		t.Fatal("rollback destroyed concurrent content")
	}
}

func TestLifecycleMigrationHoldsFeatureGuardsThroughApplyAndRollback(t *testing.T) {
	t.Run("apply", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		first := writeLegacyFeature(t, mgr, "FTR-0370", "backlog", "unassigned", "2024-01-01")
		writeLegacyFeature(t, mgr, "FTR-0371", "backlog", "unassigned", "2024-01-02")
		plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{})
		if err != nil {
			t.Fatal(err)
		}

		originalReplacer := replaceMigrationFile
		entered := make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		calls := 0
		replaceMigrationFile = func(batch *feature.MutationBatch, change Change, expected, replacement []byte) error {
			calls++
			err := originalReplacer(batch, change, expected, replacement)
			if calls == 1 && err == nil {
				close(entered)
				<-release
			}
			return err
		}
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(release) })
			replaceMigrationFile = originalReplacer
		})

		applyDone := make(chan error, 1)
		go func() { applyDone <- plan.Apply(false) }()
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("migration did not enter the guarded apply phase")
		}

		observed := make(chan string, 1)
		contenderDone := make(chan error, 1)
		go func() {
			_, mutateErr := mgr.MutateFeature(first.FrontMatter.ID, func(current *feature.Feature) error {
				observed <- current.FrontMatter.ImplementationOwner + "|" + current.FrontMatter.StatusChanged
				current.FrontMatter.Title = "contender"
				return nil
			})
			contenderDone <- mutateErr
		}()
		select {
		case err := <-contenderDone:
			t.Fatalf("concurrent mutation bypassed migration apply guard: %v", err)
		case <-time.After(100 * time.Millisecond):
		}

		releaseOnce.Do(func() { close(release) })
		if err := <-applyDone; err != nil {
			t.Fatalf("migration apply failed: %v", err)
		}
		if err := <-contenderDone; err != nil {
			t.Fatalf("serialized mutation failed: %v", err)
		}
		if value := <-observed; value != "unassigned|2024-01-01" {
			t.Fatalf("serialized mutation observed lifecycle metadata %q", value)
		}
	})

	t.Run("rollback", func(t *testing.T) {
		fix := testutil.NewFixture(t)
		opts := fix.Options(t, false, false, false)
		mgr := feature.NewManager(opts)
		first := writeLegacyFeature(t, mgr, "FTR-0380", "backlog", "unassigned", "2024-01-01")
		writeLegacyFeature(t, mgr, "FTR-0381", "backlog", "unassigned", "2024-01-02")
		plan, err := PreflightLifecycleMetadata(opts, mgr, Assignments{})
		if err != nil {
			t.Fatal(err)
		}

		originalReplacer := replaceMigrationFile
		rollbackEntered := make(chan struct{})
		releaseRollback := make(chan struct{})
		var releaseOnce sync.Once
		calls := 0
		replaceMigrationFile = func(batch *feature.MutationBatch, change Change, expected, replacement []byte) error {
			calls++
			switch calls {
			case 2:
				return errors.New("injected second migration failure")
			case 3:
				close(rollbackEntered)
				<-releaseRollback
			}
			return originalReplacer(batch, change, expected, replacement)
		}
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(releaseRollback) })
			replaceMigrationFile = originalReplacer
		})

		applyDone := make(chan error, 1)
		go func() { applyDone <- plan.Apply(false) }()
		select {
		case <-rollbackEntered:
		case <-time.After(5 * time.Second):
			t.Fatal("migration did not enter guarded rollback")
		}

		observed := make(chan string, 1)
		contenderDone := make(chan error, 1)
		go func() {
			_, mutateErr := mgr.MutateFeature(first.FrontMatter.ID, func(current *feature.Feature) error {
				observed <- current.FrontMatter.ImplementationOwner + "|" + current.FrontMatter.StatusChanged
				current.FrontMatter.Title = "contender"
				return nil
			})
			contenderDone <- mutateErr
		}()
		select {
		case err := <-contenderDone:
			t.Fatalf("concurrent mutation bypassed migration rollback guard: %v", err)
		case <-time.After(100 * time.Millisecond):
		}

		releaseOnce.Do(func() { close(releaseRollback) })
		if err := <-applyDone; err == nil || !strings.Contains(err.Error(), "injected second migration failure") {
			t.Fatalf("migration rollback error = %v", err)
		}
		if err := <-contenderDone; err != nil {
			t.Fatalf("serialized mutation failed: %v", err)
		}
		if value := <-observed; value != "|" {
			t.Fatalf("serialized mutation observed unrolled lifecycle metadata %q", value)
		}
	})
}

func writeLegacyFeature(t *testing.T, mgr *feature.Manager, id, status, owner, created string) *feature.Feature {
	t.Helper()
	feat := &feature.Feature{
		Path: filepath.Join(mgr.FeaturesDir(), status, id+"-legacy-feature.md"),
		FrontMatter: feature.FrontMatter{
			ID: id, Title: "Legacy Feature", Status: status, Owner: owner,
			Priority: "P2", Complexity: "M", Created: created, Updated: created,
			Labels: []string{}, Dependencies: []string{}, RiskNotes: "",
		},
		Body: "## Summary\n\nLegacy body must survive byte-for-byte semantically.\n",
	}
	data, err := feat.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(feat.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return feat
}
