package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	if _, err := mgr.Acquire("feat", "owner2", 1, true); err != nil {
		t.Fatalf("force acquire failed: %v", err)
	}

	loaded, err := mgr.Load("feat")
	if err != nil || loaded == nil {
		t.Fatalf("load failed: %v", err)
	}

	if err := mgr.Release("feat"); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected lock removed")
	}

	dryOpts := fix.Options(t, false, false, true)
	dryMgr := NewManager(dryOpts)
	if err := dryMgr.Release("feat"); err != nil {
		t.Fatalf("dry release should succeed: %v", err)
	}
	if _, err := dryMgr.Acquire("feat", "", 1, false); err != nil {
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

func TestCreateExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "locks", "test.lock")

	// First create should succeed
	if err := createExclusive(path, []byte("data")); err != nil {
		t.Fatalf("first createExclusive failed: %v", err)
	}

	// Second create should fail (file exists)
	if err := createExclusive(path, []byte("data2")); err == nil {
		t.Fatal("second createExclusive should fail, got nil")
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
