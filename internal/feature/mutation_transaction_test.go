package feature

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/lock"
	"github.com/virtualboard/vb-cli/internal/testutil"
	"github.com/virtualboard/vb-cli/internal/util"
)

func TestStaleUpdateCannotRecreateSourceAfterMove(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Guarded stale update", nil)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := stale.Path

	moved, _, err := mgr.MoveFeature(created.FrontMatter.ID, "in-progress", opts.Actor)
	if err != nil {
		t.Fatal(err)
	}
	stale.FrontMatter.Title = "stale writer"
	if err := mgr.UpdateFeature(stale); !errors.Is(err, ErrStaleFeature) {
		t.Fatalf("stale update error = %v, want ErrStaleFeature", err)
	}
	if _, err := os.Lstat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale writer recreated old lifecycle path: %v", err)
	}
	reloaded, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Path != moved.Path || reloaded.FrontMatter.Status != "in-progress" {
		t.Fatalf("move was not preserved: %+v", reloaded)
	}
}

func TestMutationCASRejectsContentAndIdentityChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, path string)
	}{
		{
			name: "content changed in place",
			change: func(t *testing.T, path string) {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, append(data, []byte("\nexternal edit\n")...), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "identity replaced with equal bytes",
			change: func(t *testing.T, path string) {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := util.WriteFileAtomic(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fix := testutil.NewFixture(t)
			opts := fix.Options(t, false, false, false)
			mgr := NewManager(opts)
			created, err := mgr.CreateFeature("CAS source", nil)
			if err != nil {
				t.Fatal(err)
			}

			_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
				test.change(t, feat.Path)
				feat.FrontMatter.Title = "must not overwrite external state"
				feat.UpdateTimestamp()
				return nil
			})
			if !errors.Is(err, ErrStaleFeature) {
				t.Fatalf("mutation error = %v, want ErrStaleFeature", err)
			}
			data, readErr := os.ReadFile(created.Path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(data), "must not overwrite external state") {
				t.Fatal("stale mutation overwrote the concurrent source")
			}
		})
	}
}

func TestExclusiveFeatureRenameNeverReplacesDestination(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	source := filepath.Join(mgr.LocksDir(), ".exclusive-source")
	destination := filepath.Join(mgr.LocksDir(), ".exclusive-destination")
	if err := os.WriteFile(source, []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("newer destination\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceRelative, err := relativeTransactionPath(opts.RootDir, source)
	if err != nil {
		t.Fatal(err)
	}
	destinationRelative, err := relativeTransactionPath(opts.RootDir, destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.renameWithinFeatureRootExclusive(sourceRelative, destinationRelative); err == nil {
		t.Fatal("exclusive rename replaced an existing destination")
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != "source\n" {
		t.Fatalf("exclusive rename removed its source on conflict: %q, %v", data, err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "newer destination\n" {
		t.Fatalf("exclusive rename overwrote destination: %q, %v", data, err)
	}
}

func TestEveryMutationSharesPerFeatureGuard(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Manager, string, string) error
	}{
		{
			name: "update",
			run: func(mgr *Manager, id, _ string) error {
				_, err := mgr.MutateFeature(id, func(feat *Feature) error {
					feat.FrontMatter.Title = "second mutation"
					feat.UpdateTimestamp()
					return nil
				})
				return err
			},
		},
		{
			name: "move",
			run: func(mgr *Manager, id, actor string) error {
				_, _, err := mgr.MoveFeature(id, "in-progress", actor)
				return err
			},
		},
		{
			name: "delete",
			run: func(mgr *Manager, id, _ string) error {
				_, err := mgr.DeleteFeature(id)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fix := testutil.NewFixture(t)
			opts := fix.Options(t, false, false, false)
			mgr := NewManager(opts)
			created, err := mgr.CreateFeature("Shared transaction guard "+test.name, nil)
			if err != nil {
				t.Fatal(err)
			}

			entered := make(chan struct{})
			release := make(chan struct{})
			firstDone := make(chan error, 1)
			go func() {
				_, mutationErr := mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
					close(entered)
					<-release
					feat.FrontMatter.Title = "first mutation"
					feat.UpdateTimestamp()
					return nil
				})
				firstDone <- mutationErr
			}()

			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("first mutation never acquired the feature guard")
			}

			contenderDone := make(chan error, 1)
			go func() {
				contenderDone <- test.run(mgr, created.FrontMatter.ID, opts.Actor)
			}()
			select {
			case err := <-contenderDone:
				close(release)
				t.Fatalf("%s completed while the live feature guard was held: %v", test.name, err)
			case <-time.After(100 * time.Millisecond):
			}

			close(release)
			select {
			case err := <-firstDone:
				if err != nil {
					t.Fatalf("guarded mutation failed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("guarded mutation did not finish")
			}
			select {
			case err := <-contenderDone:
				if err != nil {
					t.Fatalf("serialized %s failed after guard release: %v", test.name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("serialized %s did not run after guard release", test.name)
			}
		})
	}
}

func TestMutationBatchAcquiresFeatureGuardsInStableIDOrder(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))
	acquired := []string{}
	mgr.batchGuardAcquired = func(id string) {
		acquired = append(acquired, id)
	}

	err := mgr.WithFeatureMutationBatch([]string{
		"FTR-0003", "FTR-0001", "FTR-0002", "FTR-0001",
	}, func(batch *MutationBatch) error {
		if batch == nil || !batch.active {
			t.Fatal("mutation batch was not active inside its callback")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"FTR-0001", "FTR-0002", "FTR-0003"}
	if strings.Join(acquired, ",") != strings.Join(want, ",") {
		t.Fatalf("guard acquisition order = %v, want %v", acquired, want)
	}
}

func TestUserLockAcquisitionWaitsForFeatureMutation(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Lock linearization", nil)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	releaseMutation := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		_, mutateErr := mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
			close(entered)
			<-releaseMutation
			feat.FrontMatter.Title = "mutated under guard"
			return nil
		})
		mutationDone <- mutateErr
	}()
	<-entered

	type acquireResult struct {
		info *lock.Info
		err  error
	}
	lockDone := make(chan acquireResult, 1)
	go func() {
		info, acquireErr := mgr.lockMgr.Acquire(created.FrontMatter.ID, "reviewer", 30, true)
		lockDone <- acquireResult{info: info, err: acquireErr}
	}()
	select {
	case result := <-lockDone:
		close(releaseMutation)
		t.Fatalf("user lock bypassed a live feature mutation: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseMutation)
	if err := <-mutationDone; err != nil {
		t.Fatalf("feature mutation failed: %v", err)
	}
	select {
	case result := <-lockDone:
		if result.err != nil || result.info == nil {
			t.Fatalf("lock did not acquire after mutation: %+v", result)
		}
		if err := mgr.lockMgr.Release(created.FrontMatter.ID, result.info.Token); err != nil {
			t.Fatalf("release test lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lock remained blocked after mutation completed")
	}
}

func TestSaveCapturePreservesEditInFormerFinalRenameWindow(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Captured update race", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := afterFeatureSourceCaptured
	afterFeatureSourceCaptured = func(operation featureMutationOperation, id, _, heldPath string) {
		if operation != featureMutationReplace || id != created.FrontMatter.ID {
			return
		}
		data, readErr := os.ReadFile(heldPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(heldPath, append(data, []byte("\nexternal edit after capture\n")...), 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	t.Cleanup(func() { afterFeatureSourceCaptured = originalHook })

	_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Title = "must not win"
		return nil
	})
	if !errors.Is(err, ErrStaleFeature) {
		t.Fatalf("captured edit error = %v, want ErrStaleFeature", err)
	}
	data, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "external edit after capture") || strings.Contains(string(data), "must not win") {
		t.Fatalf("external content was not restored without overwrite:\n%s", data)
	}
	if _, err := os.Lstat(mgr.transactionPath(created.FrontMatter.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rolled-back transaction journal remains: %v", err)
	}
}

func TestMutationBatchCapturePreservesConcurrentEdit(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Batch capture race", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := afterFeatureSourceCaptured
	afterFeatureSourceCaptured = func(operation featureMutationOperation, id, _, heldPath string) {
		if operation == featureMutationReplace && id == created.FrontMatter.ID {
			if err := os.WriteFile(heldPath, []byte("external batch content\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { afterFeatureSourceCaptured = originalHook })

	err = mgr.WithFeatureMutationBatch([]string{created.FrontMatter.ID}, func(batch *MutationBatch) error {
		snapshot, loadErr := batch.Load(created.FrontMatter.ID)
		if loadErr != nil {
			return loadErr
		}
		replacement := append([]byte(nil), snapshot.Data...)
		replacement = append(replacement, []byte("\nbatch replacement\n")...)
		return batch.CompareAndSwap(created.FrontMatter.ID, snapshot.Feature.Path, snapshot.Data, replacement, snapshot.Mode)
	})
	if !errors.Is(err, ErrStaleFeature) {
		t.Fatalf("batch CAS error = %v, want ErrStaleFeature", err)
	}
	data, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external batch content\n" {
		t.Fatalf("batch CAS overwrote concurrent content: %q", data)
	}
}

func TestDeleteRetainsCapturedEditForManualRecovery(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Delete capture race", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := beforeCapturedFeatureRemoval
	beforeCapturedFeatureRemoval = func(operation featureMutationOperation, id, heldPath string) {
		if operation == featureMutationDelete && id == created.FrontMatter.ID {
			if err := os.WriteFile(heldPath, []byte("external delete edit\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { beforeCapturedFeatureRemoval = originalHook })

	if _, err := mgr.DeleteFeature(created.FrontMatter.ID); !errors.Is(err, ErrPendingMove) {
		t.Fatalf("delete race error = %v, want pending transaction", err)
	}
	if _, err := os.Lstat(created.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete left a live source path: %v", err)
	}
	held := singleHeldTransactionPath(t, mgr, created.FrontMatter.ID)
	data, err := os.ReadFile(held)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external delete edit\n" {
		t.Fatalf("delete did not preserve captured concurrent content: %q", data)
	}
	beforeCapturedFeatureRemoval = nil
	if err := mgr.recoverPendingMoves(); !errors.Is(err, ErrPendingMove) || !strings.Contains(err.Error(), "manual reconciliation") {
		t.Fatalf("ambiguous captured edit was not retained for manual recovery: %v", err)
	}
}

func TestCapturedCleanupQuarantinePreservesSwappedNewerPath(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Captured cleanup swap", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := beforeFeaturePathQuarantine
	beforeFeaturePathQuarantine = func(path string) {
		if filepath.Base(path) == "held" {
			if err := util.WriteFileAtomic(path, []byte("newer held path\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { beforeFeaturePathQuarantine = originalHook })

	_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Title = "published before cleanup"
		return nil
	})
	if !errors.Is(err, ErrPendingMove) {
		t.Fatalf("captured cleanup swap error = %v, want pending transaction", err)
	}
	held := singleHeldTransactionPath(t, mgr, created.FrontMatter.ID)
	data, err := os.ReadFile(held)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "newer held path\n" {
		t.Fatalf("cleanup removed or replaced the swapped newer held path: %q", data)
	}
}

func TestJournalCleanupQuarantinePreservesRecreatedJournal(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Journal cleanup recreation", nil)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := mgr.transactionPath(created.FrontMatter.ID)

	originalHook := afterFeaturePathQuarantined
	afterFeaturePathQuarantined = func(originalPath, _ string) {
		if filepath.Clean(originalPath) == filepath.Clean(journalPath) {
			if err := os.WriteFile(originalPath, []byte("newer journal path\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { afterFeaturePathQuarantined = originalHook })

	_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Title = "journal cleanup published"
		return nil
	})
	if !errors.Is(err, ErrPendingMove) {
		t.Fatalf("journal cleanup recreation error = %v, want pending transaction", err)
	}
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "newer journal path\n" {
		t.Fatalf("cleanup removed or replaced the recreated journal: %q", data)
	}
}

func TestStageCleanupQuarantinePreservesSwappedNewerPath(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Stage cleanup swap", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := beforeFeaturePathQuarantine
	beforeFeaturePathQuarantine = func(path string) {
		if filepath.Base(path) == "stage" {
			if err := util.WriteFileAtomic(path, []byte("newer stage path\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { beforeFeaturePathQuarantine = originalHook })

	_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Title = "stage cleanup published"
		return nil
	})
	if !errors.Is(err, ErrPendingMove) {
		t.Fatalf("stage cleanup swap error = %v, want pending transaction", err)
	}
	matches, err := filepath.Glob(filepath.Join(mgr.LocksDir(), ".mutation-"+created.FrontMatter.ID+"-*", "stage"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("stage paths = %v, err=%v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "newer stage path\n" {
		t.Fatalf("cleanup removed or replaced the swapped newer stage: %q", data)
	}
}

func TestMoveNeverRemovesRecreatedSourcePath(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Move recreated source", nil)
	if err != nil {
		t.Fatal(err)
	}
	originalHook := afterFeatureSourceCaptured
	afterFeatureSourceCaptured = func(operation featureMutationOperation, id, sourcePath, _ string) {
		if operation == featureMutationMove && id == created.FrontMatter.ID {
			if err := os.WriteFile(sourcePath, []byte("external source recreation\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { afterFeatureSourceCaptured = originalHook })

	_, _, err = mgr.MoveFeature(created.FrontMatter.ID, "in-progress", opts.Actor)
	if !errors.Is(err, ErrPendingMove) || !errors.Is(err, ErrStaleFeature) {
		t.Fatalf("move source recreation error = %v, want stale pending transaction", err)
	}
	data, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external source recreation\n" {
		t.Fatalf("move removed or replaced recreated source: %q", data)
	}
	newPath := filepath.Join(opts.RootDir, DirectoryForStatus("in-progress"), filepath.Base(created.Path))
	if _, err := os.Lstat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("move published a destination after source recreation: %v", err)
	}
	if _, err := os.Lstat(singleHeldTransactionPath(t, mgr, created.FrontMatter.ID)); err != nil {
		t.Fatalf("move did not retain captured source: %v", err)
	}
}

func TestCrashRecoveryRollsBackCapturedUpdate(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Captured crash rollback", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := interruptFeatureTransaction
	interruptFeatureTransaction = func(point string, operation featureMutationOperation, id string) error {
		if point == "captured" && operation == featureMutationReplace && id == created.FrontMatter.ID {
			return errors.New("simulated process death after capture")
		}
		return nil
	}
	t.Cleanup(func() { interruptFeatureTransaction = originalHook })
	_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Title = "unpublished replacement"
		return nil
	})
	if !errors.Is(err, ErrPendingMove) {
		t.Fatalf("interrupted update error = %v, want pending transaction", err)
	}
	interruptFeatureTransaction = nil
	recovered, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatalf("recover captured update: %v", err)
	}
	if recovered.FrontMatter.Title != "Captured crash rollback" {
		t.Fatalf("captured-only update was not rolled back: %s", recovered.FrontMatter.Title)
	}
	if _, err := os.Lstat(mgr.transactionPath(created.FrontMatter.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left update journal: %v", err)
	}
}

func TestCrashRecoveryFinalizesPublishedUpdate(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Published crash commit", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := interruptFeatureTransaction
	interruptFeatureTransaction = func(point string, operation featureMutationOperation, id string) error {
		if point == "published" && operation == featureMutationReplace && id == created.FrontMatter.ID {
			return errors.New("simulated process death after publish")
		}
		return nil
	}
	t.Cleanup(func() { interruptFeatureTransaction = originalHook })
	_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Title = "published replacement"
		return nil
	})
	if !errors.Is(err, ErrPendingMove) {
		t.Fatalf("interrupted update error = %v, want pending transaction", err)
	}
	interruptFeatureTransaction = nil
	recovered, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatalf("recover published update: %v", err)
	}
	if recovered.FrontMatter.Title != "published replacement" {
		t.Fatalf("published update was not finalized: %s", recovered.FrontMatter.Title)
	}
	if _, err := os.Lstat(mgr.transactionPath(created.FrontMatter.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left update journal: %v", err)
	}
}

func TestCrashRecoveryFinalizesUpdateAfterCapturedSourceRetired(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Retired crash commit", nil)
	if err != nil {
		t.Fatal(err)
	}

	originalHook := interruptFeatureTransaction
	interruptFeatureTransaction = func(point string, operation featureMutationOperation, id string) error {
		if point == "retired" && operation == featureMutationReplace && id == created.FrontMatter.ID {
			return errors.New("simulated process death after retiring capture")
		}
		return nil
	}
	t.Cleanup(func() { interruptFeatureTransaction = originalHook })
	_, err = mgr.MutateFeature(created.FrontMatter.ID, func(feat *Feature) error {
		feat.FrontMatter.Title = "retired published replacement"
		return nil
	})
	if !errors.Is(err, ErrPendingMove) {
		t.Fatalf("interrupted retired update error = %v, want pending transaction", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(mgr.LocksDir(), ".mutation-"+created.FrontMatter.ID+"-*", "held")); len(matches) != 0 {
		t.Fatalf("captured source was not retired before interruption: %v", matches)
	}
	interruptFeatureTransaction = nil
	recovered, err := mgr.LoadByID(created.FrontMatter.ID)
	if err != nil {
		t.Fatalf("recover retired update: %v", err)
	}
	if recovered.FrontMatter.Title != "retired published replacement" {
		t.Fatalf("retired published update was not finalized: %s", recovered.FrontMatter.Title)
	}
}

func TestCrashRecoveryFinalizesCapturedDelete(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	created, err := mgr.CreateFeature("Delete crash commit", nil)
	if err != nil {
		t.Fatal(err)
	}
	originalHook := interruptFeatureTransaction
	interruptFeatureTransaction = func(point string, operation featureMutationOperation, id string) error {
		if point == "captured" && operation == featureMutationDelete && id == created.FrontMatter.ID {
			return errors.New("simulated process death after delete capture")
		}
		return nil
	}
	t.Cleanup(func() { interruptFeatureTransaction = originalHook })
	if _, err := mgr.DeleteFeature(created.FrontMatter.ID); !errors.Is(err, ErrPendingMove) {
		t.Fatalf("interrupted delete error = %v, want pending transaction", err)
	}
	interruptFeatureTransaction = nil
	if err := mgr.recoverPendingMoves(); err != nil {
		t.Fatalf("recover captured delete: %v", err)
	}
	if _, err := os.Lstat(created.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered delete restored the live feature: %v", err)
	}
	if _, err := os.Lstat(mgr.transactionPath(created.FrontMatter.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery left delete journal: %v", err)
	}
}

func singleHeldTransactionPath(t *testing.T, mgr *Manager, id string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(mgr.LocksDir(), ".mutation-"+id+"-*", "held"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("held transaction paths = %v, want exactly one", matches)
	}
	return matches[0]
}
