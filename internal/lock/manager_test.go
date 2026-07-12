package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/testutil"
)

func TestInfoExpiry(t *testing.T) {
	info := Info{ID: "x", StartedAt: time.Now().UTC().Add(-2 * time.Minute), TTLMinutes: 1}
	if !info.Expired() {
		t.Fatalf("expected lock to be expired")
	}
	if info.ExpiresAt().Before(info.StartedAt) {
		t.Fatalf("expires at should be after start")
	}

	noTTL := Info{StartedAt: time.Now().UTC(), TTLMinutes: 0}
	if noTTL.Expired() {
		t.Fatalf("expected no TTL to always appear active")
	}
}

func TestManagerAcquireLoadRelease(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	if _, err := mgr.Acquire("feat", "", 0, false); err == nil {
		t.Fatalf("expected error for non-positive ttl")
	}

	info, err := mgr.Acquire("feat", "owner", 1, false)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if info.Owner != "owner" {
		t.Fatalf("unexpected owner: %s", info.Owner)
	}

	lockPath := mgr.Path("feat")
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected lock file: %v", err)
	}

	if _, err := mgr.Acquire("feat", "owner", 1, false); !errors.Is(err, ErrActiveLock) {
		t.Fatalf("expected active lock error, got %v", err)
	}

	forced, err := mgr.Acquire("feat", "owner2", 1, true)
	if err != nil {
		t.Fatalf("force acquire failed: %v", err)
	}

	loaded, err := mgr.Load("feat")
	if err != nil || loaded == nil {
		t.Fatalf("load failed: %v", err)
	}

	if err := mgr.Release("feat", forced.Token); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock removed")
	}

	dryOpts := fix.Options(t, false, false, true)
	dryMgr := NewManager(dryOpts)
	if err := dryMgr.Release("feat", strings.Repeat("e", 64)); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("dry release with stale token error = %v, want ErrLockChanged", err)
	}
	if _, err := dryMgr.Acquire("feat", "owner", 1, false); err != nil {
		t.Fatalf("dry acquire should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dryOpts.RootDir, "locks", "feat.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry acquire should not create file, got %v", err)
	}
}

func TestAcquireConcurrent(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)

	const goroutines = 10
	var successes atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			mgr := NewManager(opts)
			_, err := mgr.Acquire("race-test", fmt.Sprintf("owner-%d", idx), 5, false)
			if err == nil {
				successes.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("expected exactly 1 successful acquire, got %d", got)
	}
}

func TestAcquireExpiredLock(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	// Create a lock that is already expired
	info := &Info{
		ID:         "expired-test",
		Owner:      "old-owner",
		StartedAt:  time.Now().UTC().Add(-10 * time.Minute),
		TTLMinutes: 1,
	}
	payload, _ := json.MarshalIndent(info, "", "  ")
	path := mgr.Path("expired-test")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	// Should succeed because the existing lock is expired
	newInfo, err := mgr.Acquire("expired-test", "new-owner", 5, false)
	if err != nil {
		t.Fatalf("acquire of expired lock failed: %v", err)
	}
	if newInfo.Owner != "new-owner" {
		t.Fatalf("unexpected owner: %s", newInfo.Owner)
	}
}

func TestAcquireForceOverwrite(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	// Acquire normally
	_, err := mgr.Acquire("force-test", "owner1", 5, false)
	if err != nil {
		t.Fatalf("initial acquire failed: %v", err)
	}

	// Force acquire should overwrite
	info, err := mgr.Acquire("force-test", "owner2", 5, true)
	if err != nil {
		t.Fatalf("force acquire failed: %v", err)
	}
	if info.Owner != "owner2" {
		t.Fatalf("expected owner2, got %s", info.Owner)
	}
}

func TestAcquireAssignsUniqueTokensAndPersistsThem(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	first, err := mgr.Acquire("token-test", "owner1", 5, false)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	second, err := mgr.Acquire("token-test", "owner2", 5, true)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if !lockTokenPattern.MatchString(first.Token) || !lockTokenPattern.MatchString(second.Token) {
		t.Fatalf("tokens are not 256-bit lowercase hexadecimal values: %q %q", first.Token, second.Token)
	}
	if first.Token == second.Token {
		t.Fatal("separate acquisitions reused a token")
	}
	loaded, err := mgr.Load("token-test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil || loaded.Token != second.Token {
		t.Fatalf("persisted token = %#v, want %q", loaded, second.Token)
	}

	if _, err := parseLockData([]byte(`{"id":"legacy","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`), "legacy"); err != nil {
		t.Fatalf("legacy tokenless record no longer parses: %v", err)
	}
	if _, err := parseLockData([]byte(`{"id":"bad","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5,"token":"guessable"}`), "bad"); err == nil {
		t.Fatal("malformed acquisition token was accepted")
	}
}

func TestReleaseByTokenCannotDeleteNewerAcquisition(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	oldLock, err := mgr.Acquire("stale-release", "same-owner", 5, false)
	if err != nil {
		t.Fatalf("old acquire: %v", err)
	}
	newLock, err := mgr.Acquire("stale-release", "same-owner", 5, true)
	if err != nil {
		t.Fatalf("replacement acquire: %v", err)
	}
	if err := mgr.Release("stale-release", oldLock.Token); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("stale release error = %v, want ErrLockChanged", err)
	}
	loaded, err := mgr.Load("stale-release")
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if loaded == nil || loaded.Token != newLock.Token {
		t.Fatalf("newer lock was removed or changed: %#v", loaded)
	}
}

func TestReleaseOwnedRereadsIdentityBeforeRemoval(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	replaceLockFileAtomic(t, mgr.Path("release-cas"), &Info{
		ID:         "release-cas",
		Owner:      "owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
	})
	replacement := &Info{
		ID:         "release-cas",
		Owner:      "owner",
		StartedAt:  time.Now().UTC().Add(time.Second),
		TTLMinutes: 5,
		Token:      strings.Repeat("a", 64),
	}
	mgr.testHooks = &managerTestHooks{
		beforeRemove: func(operation, id string) {
			if operation == "release-owned" && id == replacement.ID {
				replaceLockFileAtomic(t, mgr.Path(id), replacement)
			}
		},
	}
	if err := mgr.ReleaseOwned("release-cas", "owner", false); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("release after replacement error = %v, want ErrLockChanged", err)
	}
	loaded, err := mgr.Load("release-cas")
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if loaded == nil || loaded.Token != replacement.Token {
		t.Fatalf("release deleted newer same-owner lock: %#v", loaded)
	}
}

func TestReleaseOwnedSnapshotCannotDeleteNewerSameOwnerAcquisition(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	seed := NewManager(opts)
	replaceLockFileAtomic(t, seed.Path("owned-snapshot"), &Info{
		ID:         "owned-snapshot",
		Owner:      "same-owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
	})

	snapshotTaken := make(chan struct{})
	allowRelease := make(chan struct{})
	releaseMgr := NewManager(opts)
	releaseMgr.testHooks = &managerTestHooks{
		afterSnapshot: func(operation, id string) {
			if operation == "release-owned" && id == "owned-snapshot" {
				close(snapshotTaken)
				<-allowRelease
			}
		},
	}
	releaseDone := make(chan error, 1)
	go func() {
		releaseDone <- releaseMgr.ReleaseOwned("owned-snapshot", "same-owner", false)
	}()
	<-snapshotTaken

	acquireMgr := NewManager(opts)
	newLock, err := acquireMgr.Acquire("owned-snapshot", "same-owner", 5, true)
	if err != nil {
		t.Fatalf("replacement acquire: %v", err)
	}
	close(allowRelease)
	if err := <-releaseDone; !errors.Is(err, ErrLockChanged) {
		t.Fatalf("release error = %v, want ErrLockChanged", err)
	}
	loaded, err := acquireMgr.Load("owned-snapshot")
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if loaded == nil || loaded.Token != newLock.Token {
		t.Fatalf("release deleted newer same-owner acquisition: %#v", loaded)
	}
}

func TestReleaseOwnedRejectsTokenBearingAcquisition(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	info, err := mgr.Acquire("token-required", "same-owner", 5, false)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := mgr.ReleaseOwned(info.ID, info.Owner, false); !errors.Is(err, ErrLockTokenRequired) {
		t.Fatalf("owner-only release error = %v, want ErrLockTokenRequired", err)
	}
	loaded, err := mgr.Load(info.ID)
	if err != nil || loaded == nil || loaded.Token != info.Token {
		t.Fatalf("owner-only release changed current lock: %#v, %v", loaded, err)
	}
	if err := mgr.Release(info.ID, info.Token); err != nil {
		t.Fatalf("exact-token release: %v", err)
	}
}

func TestDryRunReleaseValidatesCurrentTokenWithoutGuardMutation(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, true)
	mgr := NewManager(opts)
	currentToken := strings.Repeat("a", 64)
	writeLockFile(t, mgr.Path("dry-token"), &Info{
		ID:         "dry-token",
		Owner:      "owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
		Token:      currentToken,
	})
	guardPath := mgr.guardPath("dry-token")
	if err := mgr.Release("dry-token", strings.Repeat("b", 64)); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("stale dry-run token error = %v, want ErrLockChanged", err)
	}
	if err := mgr.Release("dry-token", currentToken); err != nil {
		t.Fatalf("current dry-run token rejected: %v", err)
	}
	loaded, err := mgr.Load("dry-token")
	if err != nil || loaded == nil || loaded.Token != currentToken {
		t.Fatalf("dry-run token release mutated the lock: %#v, %v", loaded, err)
	}
	if _, err := os.Stat(guardPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dry-run token release created a guard: %v", err)
	}
}

func TestDryRunReleaseOwnedPerformsReadOnlyOwnerAndLegacyChecks(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, true)
	mgr := NewManager(opts)
	writeLockFile(t, mgr.Path("dry-owned-token"), &Info{
		ID:         "dry-owned-token",
		Owner:      "owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
		Token:      strings.Repeat("c", 64),
	})
	if err := mgr.ReleaseOwned("dry-owned-token", "owner", false); !errors.Is(err, ErrLockTokenRequired) {
		t.Fatalf("token-bearing dry owner release error = %v, want ErrLockTokenRequired", err)
	}

	writeLockFile(t, mgr.Path("dry-owned-legacy"), &Info{
		ID:         "dry-owned-legacy",
		Owner:      "owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
	})
	if err := mgr.ReleaseOwned("dry-owned-legacy", "other-owner", false); !errors.Is(err, ErrLockOwner) {
		t.Fatalf("wrong-owner dry release error = %v, want ErrLockOwner", err)
	}
	if err := mgr.ReleaseOwned("dry-owned-legacy", "owner", false); err != nil {
		t.Fatalf("valid tokenless dry owner release: %v", err)
	}
	if _, err := os.Stat(mgr.Path("dry-owned-legacy")); err != nil {
		t.Fatalf("dry owner release removed legacy lock: %v", err)
	}
	for _, id := range []string{"dry-owned-token", "dry-owned-legacy"} {
		if _, err := os.Stat(mgr.guardPath(id)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("dry owner release created guard for %s: %v", id, err)
		}
	}
}

func TestExpiredCleanupRereadsIdentityBeforeRemoval(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	expired := &Info{
		ID:         "expiry-cas",
		Owner:      "old-owner",
		StartedAt:  time.Now().UTC().Add(-10 * time.Minute),
		TTLMinutes: 1,
		Token:      strings.Repeat("b", 64),
	}
	writeLockFile(t, mgr.Path(expired.ID), expired)
	replacement := &Info{
		ID:         expired.ID,
		Owner:      "new-owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
		Token:      strings.Repeat("c", 64),
	}
	mgr.testHooks = &managerTestHooks{
		beforeRemove: func(operation, id string) {
			if operation == "expired-replace" && id == replacement.ID {
				replaceLockFileAtomic(t, mgr.Path(id), replacement)
			}
		},
	}
	if _, err := mgr.Acquire(expired.ID, "contender", 5, false); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("expired replacement error = %v, want ErrLockChanged", err)
	}
	loaded, err := mgr.Load(expired.ID)
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if loaded == nil || loaded.Token != replacement.Token {
		t.Fatalf("expiry cleanup deleted newer lock: %#v", loaded)
	}
}

func TestForceAcquireRereadsIdentityBeforeReplacement(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)

	if _, err := mgr.Acquire("force-cas", "old-owner", 5, false); err != nil {
		t.Fatalf("seed acquire: %v", err)
	}
	replacement := &Info{
		ID:         "force-cas",
		Owner:      "newer-owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
		Token:      strings.Repeat("d", 64),
	}
	mgr.testHooks = &managerTestHooks{
		beforeAtomicSwap: func(operation, id string) {
			if operation == "force-acquire" && id == replacement.ID {
				replaceLockFileAtomic(t, mgr.Path(id), replacement)
			}
		},
	}
	if _, err := mgr.Acquire(replacement.ID, "force-owner", 5, true); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("force replacement error = %v, want ErrLockChanged", err)
	}
	loaded, err := mgr.Load(replacement.ID)
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if loaded == nil || loaded.Token != replacement.Token {
		t.Fatalf("force acquisition overwrote newer lock: %#v", loaded)
	}
}

func TestReleaseOwnedAndAcquireSerializeOnPerLockGuard(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	seed := NewManager(opts)
	writeLockFile(t, seed.Path("guarded"), &Info{
		ID:         "guarded",
		Owner:      "owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
	})

	releaseReachedRemove := make(chan struct{})
	allowRelease := make(chan struct{})
	releaseMgr := NewManager(opts)
	releaseMgr.testHooks = &managerTestHooks{
		beforeRemove: func(operation, id string) {
			if operation == "release-owned" && id == "guarded" {
				close(releaseReachedRemove)
				<-allowRelease
			}
		},
	}
	releaseDone := make(chan error, 1)
	go func() {
		releaseDone <- releaseMgr.ReleaseOwned("guarded", "owner", false)
	}()
	<-releaseReachedRemove

	acquireStarted := make(chan struct{})
	acquireGotGuard := make(chan struct{})
	acquireDone := make(chan struct {
		info *Info
		err  error
	}, 1)
	acquireMgr := NewManager(opts)
	acquireMgr.testHooks = &managerTestHooks{
		afterGuard: func(operation, id string) {
			if operation == "force-acquire" && id == "guarded" {
				close(acquireGotGuard)
			}
		},
	}
	go func() {
		close(acquireStarted)
		info, err := acquireMgr.Acquire("guarded", "new-owner", 5, true)
		acquireDone <- struct {
			info *Info
			err  error
		}{info: info, err: err}
	}()
	<-acquireStarted
	select {
	case <-acquireGotGuard:
		t.Fatal("replacement acquisition entered while release held the per-lock guard")
	case <-time.After(100 * time.Millisecond):
	}
	close(allowRelease)
	if err := <-releaseDone; err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case <-acquireGotGuard:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement acquisition did not obtain guard after release")
	}
	result := <-acquireDone
	if result.err != nil {
		t.Fatalf("replacement acquire: %v", result.err)
	}
	loaded, err := acquireMgr.Load("guarded")
	if err != nil {
		t.Fatalf("load replacement: %v", err)
	}
	if loaded == nil || result.info == nil || loaded.Token != result.info.Token {
		t.Fatalf("replacement lock was deleted: loaded=%#v result=%#v", loaded, result.info)
	}
}

func TestOperationGuardDoesNotExpireWhileCallbackIsLive(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	first := NewManager(opts)
	second := NewManager(opts)

	enteredFirst := make(chan struct{})
	allowFirstToReturn := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithOperationGuard("long-operation", func() error {
			close(enteredFirst)
			<-allowFirstToReturn
			return nil
		})
	}()
	<-enteredFirst
	if _, err := os.Stat(first.Path("long-operation")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("operation guard created a TTL lock record: %v", err)
	}
	expiredOldLease := Info{StartedAt: time.Now().UTC().Add(-2 * time.Minute), TTLMinutes: 1}
	if !expiredOldLease.Expired() {
		t.Fatal("test setup did not exceed the former one-minute lease")
	}

	secondGotGuard := make(chan struct{})
	secondDone := make(chan error, 1)
	second.testHooks = &managerTestHooks{
		afterGuard: func(operation, id string) {
			if operation == "operation" && id == "long-operation" {
				close(secondGotGuard)
			}
		},
	}
	go func() {
		secondDone <- second.WithOperationGuard("long-operation", func() error { return nil })
	}()
	select {
	case <-secondGotGuard:
		t.Fatal("live operation guard was treated as expired")
	case <-time.After(100 * time.Millisecond):
	}
	close(allowFirstToReturn)
	if err := <-firstDone; err != nil {
		t.Fatalf("first operation: %v", err)
	}
	select {
	case <-secondGotGuard:
	case <-time.After(2 * time.Second):
		t.Fatal("second operation did not acquire the released guard")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second operation: %v", err)
	}
	if err := first.WithOperationGuard("long-operation", nil); err == nil {
		t.Fatal("nil operation callback was accepted")
	}
}

func TestContractLockPathAndLegacyReadIsSideEffectFree(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	writeLockContract(t, opts.RootDir)
	mgr := NewManager(opts)

	if _, err := mgr.Acquire("canonical", "owner", 5, false); err != nil {
		t.Fatalf("acquire canonical lock: %v", err)
	}
	canonicalPath := filepath.Join(opts.RootDir, ".state", "locks", "canonical.lock")
	if mgr.Path("canonical") != canonicalPath {
		t.Fatalf("canonical path = %s, want %s", mgr.Path("canonical"), canonicalPath)
	}
	if _, err := os.Stat(canonicalPath); err != nil {
		t.Fatalf("canonical lock missing: %v", err)
	}

	legacyInfo := &Info{ID: "legacy", Owner: "legacy-owner", StartedAt: time.Now().UTC(), TTLMinutes: 5}
	legacyPath := filepath.Join(opts.RootDir, "locks", "legacy.lock")
	writeLockFile(t, legacyPath, legacyInfo)
	loaded, err := mgr.Load("legacy")
	if err != nil || loaded == nil || loaded.Owner != "legacy-owner" {
		t.Fatalf("load legacy lock: %#v %v", loaded, err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("read-only legacy lookup changed the legacy lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.RootDir, ".state", "locks", "legacy.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only legacy lookup created a canonical lock: %v", err)
	}
	if err := mgr.CheckMutation("legacy", "other-owner"); !errors.Is(err, ErrLockOwner) {
		t.Fatalf("legacy lock did not block mutation: %v", err)
	}
	if err := mgr.ReleaseOwned("legacy", "legacy-owner", false); err != nil {
		t.Fatalf("owner could not release tokenless legacy lock: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tokenless legacy lock remains after owner release: %v", err)
	}
}

func TestConflictingCanonicalAndLegacyLocksRequireForce(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	writeLockContract(t, opts.RootDir)
	mgr := NewManager(opts)

	writeLockFile(t, mgr.Path("split"), &Info{ID: "split", Owner: "canonical-owner", StartedAt: time.Now().UTC(), TTLMinutes: 5})
	writeLockFile(t, filepath.Join(opts.RootDir, "locks", "split.lock"), &Info{ID: "split", Owner: "legacy-owner", StartedAt: time.Now().UTC(), TTLMinutes: 5})
	if _, err := mgr.Load("split"); !errors.Is(err, ErrLockStorageConflict) {
		t.Fatalf("expected storage conflict, got %v", err)
	}
	if _, err := mgr.Acquire("split", "resolved-owner", 5, false); !errors.Is(err, ErrLockStorageConflict) {
		t.Fatalf("non-force acquisition bypassed split lock: %v", err)
	}
	if _, err := mgr.Acquire("split", "resolved-owner", 5, true); err != nil {
		t.Fatalf("force acquisition did not resolve split lock: %v", err)
	}
	loaded, err := mgr.Load("split")
	if err != nil || loaded == nil || loaded.Owner != "resolved-owner" {
		t.Fatalf("resolved lock: %#v %v", loaded, err)
	}
	if _, err := os.Stat(filepath.Join(opts.RootDir, "locks", "split.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy split lock remains: %v", err)
	}
}

func TestLegacyLockSymlinkEscapeFailsClosedWithoutTouchingOutsideFile(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	writeLockContract(t, opts.RootDir)
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "escaped.lock")
	writeLockFile(t, outsidePath, &Info{ID: "escaped", Owner: "outside-owner", StartedAt: time.Now().UTC(), TTLMinutes: 5})
	legacyDir := filepath.Join(opts.RootDir, "locks")
	if err := os.RemoveAll(legacyDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, legacyDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mgr := NewManager(opts)

	if _, err := mgr.Load("escaped"); err == nil {
		t.Fatal("legacy lock lookup followed a symlink outside the workspace")
	}
	if err := mgr.Release("escaped", strings.Repeat("f", 64)); err == nil {
		t.Fatal("legacy lock release followed a symlink outside the workspace")
	}
	if _, err := mgr.Acquire("escaped", "attacker", 5, true); err == nil {
		t.Fatal("force acquisition bypassed unsafe legacy lock storage")
	}
	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatalf("outside lock was removed: %v", err)
	}
	if !strings.Contains(string(data), "outside-owner") {
		t.Fatal("outside lock was modified")
	}
}

func TestLockIDTraversalIsRejected(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	if _, err := mgr.Acquire("../../escaped", "owner", 5, false); err == nil {
		t.Fatal("path-traversing lock ID was accepted")
	}
	if filepath.Dir(mgr.Path("../../escaped")) != filepath.Dir(mgr.Path("safe")) {
		t.Fatalf("invalid lock path escaped canonical directory: %s", mgr.Path("../../escaped"))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(opts.RootDir), "escaped.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside lock file created: %v", err)
	}
}

func TestLockDataStrictValidation(t *testing.T) {
	validToken := strings.Repeat("a", 64)
	valid := fmt.Sprintf(`{"id":"strict","owner":"owner-1","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5,"token":%q}`, validToken)
	if _, err := parseLockData([]byte(valid), "strict"); err != nil {
		t.Fatalf("valid strict record rejected: %v", err)
	}
	if _, err := parseLockData([]byte(`{"id":"strict","owner":"owner-1","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`), "strict"); err != nil {
		t.Fatalf("valid tokenless legacy record rejected: %v", err)
	}

	tests := []struct {
		name       string
		payload    string
		expectedID string
	}{
		{"unknown-field", `{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5,"surprise":true}`, "strict"},
		{"trailing-object", `{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}{}`, "strict"},
		{"trailing-garbage", `{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}oops`, "strict"},
		{"top-level-array", `[{"id":"strict"}]`, "strict"},
		{"mismatched-id", `{"id":"other","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`, "strict"},
		{"missing-owner", `{"id":"strict","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`, "strict"},
		{"invalid-owner", `{"id":"strict","owner":"owner/path","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`, "strict"},
		{"whitespace-owner", `{"id":"strict","owner":" owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`, "strict"},
		{"unassigned-owner", `{"id":"strict","owner":"unassigned","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`, "strict"},
		{"unknown-owner", `{"id":"strict","owner":"UNKNOWN","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5}`, "strict"},
		{"missing-started-at", `{"id":"strict","owner":"owner","ttl_minutes":5}`, "strict"},
		{"invalid-started-at", `{"id":"strict","owner":"owner","started_at":"not-a-date","ttl_minutes":5}`, "strict"},
		{"non-utc-started-at", `{"id":"strict","owner":"owner","started_at":"2026-07-11T01:00:00+01:00","ttl_minutes":5}`, "strict"},
		{"zero-ttl", `{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":0}`, "strict"},
		{"negative-ttl", `{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":-1}`, "strict"},
		{"oversized-ttl", fmt.Sprintf(`{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":%d}`, MaxTTLMinutes+1), "strict"},
		{"null-token", `{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5,"token":null}`, "strict"},
		{"short-token", `{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5,"token":"abc"}`, "strict"},
		{"uppercase-token", fmt.Sprintf(`{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5,"token":%q}`, strings.Repeat("A", 64)), "strict"},
		{"nonhex-token", fmt.Sprintf(`{"id":"strict","owner":"owner","started_at":"2026-07-11T00:00:00Z","ttl_minutes":5,"token":%q}`, strings.Repeat("g", 64)), "strict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if info, err := parseLockData([]byte(test.payload), test.expectedID); err == nil {
				t.Fatalf("invalid lock data accepted: %#v", info)
			}
		})
	}
}

func TestAcquireRejectsInvalidOwnerAndTTLBounds(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))
	for _, owner := range []string{"", "unassigned", "unknown", "owner/path", " owner"} {
		if _, err := mgr.Acquire("invalid-owner", owner, 5, false); err == nil {
			t.Fatalf("invalid owner %q accepted", owner)
		}
	}
	if _, err := mgr.Acquire("invalid-ttl", "owner", MaxTTLMinutes+1, false); err == nil {
		t.Fatal("oversized TTL accepted")
	}
}

func TestManagerLoadRejectsMismatchedRecordID(t *testing.T) {
	fix := testutil.NewFixture(t)
	mgr := NewManager(fix.Options(t, false, false, false))
	writeLockFile(t, mgr.Path("expected"), &Info{
		ID:         "different",
		Owner:      "owner",
		StartedAt:  time.Now().UTC(),
		TTLMinutes: 5,
	})
	if _, err := mgr.Load("expected"); err == nil {
		t.Fatal("record whose ID does not match its filename was accepted")
	}
}

func TestCanonicalLockStorageRejectsSymlinkedComponents(t *testing.T) {
	for _, component := range []string{"state", "locks"} {
		t.Run(component, func(t *testing.T) {
			fix := testutil.NewFixture(t)
			opts := fix.Options(t, false, false, false)
			writeLockContract(t, opts.RootDir)
			mgr := NewManager(opts)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			if component == "state" {
				if err := os.Symlink(outside, filepath.Join(opts.RootDir, ".state")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(opts.RootDir, ".state"), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(opts.RootDir, ".state", "locks")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if _, err := mgr.Load("symlinked"); err == nil {
				t.Fatal("canonical lookup accepted a symlinked component")
			}
			if _, err := mgr.Acquire("symlinked", "owner", 5, true); err == nil {
				t.Fatal("canonical force acquisition accepted a symlinked component")
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "unchanged" {
				t.Fatalf("outside sentinel changed: %q, %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(outside, "symlinked.lock")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("outside lock was created: %v", err)
			}
		})
	}
}

func TestCanonicalLockAndGuardLeafSymlinksFailClosed(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	writeLockContract(t, opts.RootDir)
	mgr := NewManager(opts)
	outside := t.TempDir()
	outsideLock := filepath.Join(outside, "outside.lock")
	writeLockFile(t, outsideLock, &Info{ID: "leaf", Owner: "outside-owner", StartedAt: time.Now().UTC(), TTLMinutes: 5})
	if err := os.MkdirAll(filepath.Dir(mgr.Path("leaf")), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideLock, mgr.Path("leaf")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := mgr.Load("leaf"); err == nil {
		t.Fatal("canonical lock leaf symlink was accepted")
	}
	if _, err := mgr.Acquire("leaf", "owner", 5, true); err == nil {
		t.Fatal("force acquisition replaced a canonical lock leaf symlink")
	}
	loadedOutside, err := os.ReadFile(outsideLock)
	if err != nil || !strings.Contains(string(loadedOutside), "outside-owner") {
		t.Fatalf("outside lock changed: %v", err)
	}

	guardTarget := filepath.Join(outside, "guard-target")
	if err := os.WriteFile(guardTarget, []byte("guard"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(guardTarget, mgr.guardPath("guard-leaf")); err != nil {
		t.Skipf("guard symlinks unavailable: %v", err)
	}
	called := false
	if err := mgr.WithOperationGuard("guard-leaf", func() error { called = true; return nil }); err == nil {
		t.Fatal("guard leaf symlink was accepted")
	}
	if called {
		t.Fatal("operation callback ran through an unsafe guard")
	}
}

func TestCanonicalStoragePathSwapAfterGuardFailsClosed(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	writeLockContract(t, opts.RootDir)
	mgr := NewManager(opts)
	lockDir := filepath.Join(opts.RootDir, ".state", "locks")
	movedDir := filepath.Join(opts.RootDir, ".state", "locks-moved")
	mgr.testHooks = &managerTestHooks{
		afterGuard: func(operation, id string) {
			if operation != "acquire" || id != "path-swap" {
				return
			}
			if err := os.Rename(lockDir, movedDir); err != nil {
				t.Skipf("cannot swap an open lock directory on this platform: %v", err)
			}
			if err := os.Mkdir(lockDir, 0o750); err != nil {
				t.Fatal(err)
			}
		},
	}
	if _, err := mgr.Acquire("path-swap", "owner", 5, false); err == nil {
		t.Fatal("acquisition succeeded after canonical storage path swap")
	}
	for _, path := range []string{filepath.Join(lockDir, "path-swap.lock"), filepath.Join(movedDir, "path-swap.lock")} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("lock was published despite directory swap at %s: %v", path, err)
		}
	}
}

func TestLockAndGuardHardlinksFailClosed(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	writeLockFile(t, mgr.Path("hardlinked"), &Info{ID: "hardlinked", Owner: "owner", StartedAt: time.Now().UTC(), TTLMinutes: 5})
	if err := os.Link(mgr.Path("hardlinked"), filepath.Join(opts.RootDir, "lock-alias")); err != nil {
		t.Skipf("hardlinks unavailable: %v", err)
	}
	if _, err := mgr.Load("hardlinked"); err == nil {
		t.Fatal("hardlinked lock file was accepted")
	}

	guardPath := mgr.guardPath("hardlinked-guard")
	if err := os.WriteFile(guardPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(guardPath, filepath.Join(opts.RootDir, "guard-alias")); err != nil {
		t.Skipf("guard hardlinks unavailable: %v", err)
	}
	called := false
	if err := mgr.WithOperationGuard("hardlinked-guard", func() error { called = true; return nil }); err == nil {
		t.Fatal("hardlinked operation guard was accepted")
	}
	if called {
		t.Fatal("callback ran through a hardlinked guard")
	}
}

func TestByteIdenticalInodeSwapBeforeReplaceFailsClosed(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	seed, err := mgr.Acquire("identical-replace", "owner", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(mgr.Path(seed.ID))
	if err != nil {
		t.Fatal(err)
	}
	mgr.testHooks = &managerTestHooks{
		beforeAtomicSwap: func(operation, id string) {
			if operation == "force-acquire" && id == seed.ID {
				replaceLockBytesAtomic(t, mgr.Path(id), original)
			}
		},
	}
	if _, err := mgr.Acquire(seed.ID, "new-owner", 5, true); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("byte-identical inode replacement error = %v, want ErrLockChanged", err)
	}
	loaded, err := mgr.Load(seed.ID)
	if err != nil || loaded == nil || loaded.Token != seed.Token {
		t.Fatalf("replacement lock was overwritten: %#v, %v", loaded, err)
	}
}

func TestByteIdenticalInodeSwapBeforeRemoveFailsClosed(t *testing.T) {
	fix := testutil.NewFixture(t)
	opts := fix.Options(t, false, false, false)
	mgr := NewManager(opts)
	info := &Info{ID: "identical-remove", Owner: "owner", StartedAt: time.Now().UTC(), TTLMinutes: 5}
	writeLockFile(t, mgr.Path(info.ID), info)
	original, err := os.ReadFile(mgr.Path(info.ID))
	if err != nil {
		t.Fatal(err)
	}
	mgr.testHooks = &managerTestHooks{
		beforeRemove: func(operation, id string) {
			if operation == "release-owned" && id == info.ID {
				replaceLockBytesAtomic(t, mgr.Path(id), original)
			}
		},
	}
	if err := mgr.ReleaseOwned(info.ID, info.Owner, false); !errors.Is(err, ErrLockChanged) {
		t.Fatalf("byte-identical inode removal error = %v, want ErrLockChanged", err)
	}
	if _, err := os.Stat(mgr.Path(info.ID)); err != nil {
		t.Fatalf("replacement lock was removed: %v", err)
	}
}

func writeLockContract(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "virtualboard.json"), contract.CanonicalJSON(), 0o600); err != nil {
		t.Fatalf("write contract: %v", err)
	}
}

func writeLockFile(t *testing.T, path string, info *Info) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func replaceLockFileAtomic(t *testing.T, path string, info *Info) {
	t.Helper()
	payload, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".test-lock-replace-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpPath := tmp.Name()
	t.Cleanup(func() { _ = os.Remove(tmpPath) })
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		t.Fatal(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replaceTestFile(tmpPath, path); err != nil {
		t.Fatal(err)
	}
}

func replaceLockBytesAtomic(t *testing.T, path string, payload []byte) {
	t.Helper()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".test-lock-bytes-replace-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpPath := tmp.Name()
	t.Cleanup(func() { _ = os.Remove(tmpPath) })
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		t.Fatal(err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replaceTestFile(tmpPath, path); err != nil {
		t.Fatal(err)
	}
}
