package lock

import (
	"errors"
	"os"
	"path/filepath"
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
