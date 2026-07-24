package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildTemplateManifestRejectsManagedSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(external, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "README.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := buildTemplateManifest(root, "1.0.0"); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("managed symlink was accepted: %v", err)
	}
}

func TestCloneTemplateManifestDeepCopiesFileInventory(t *testing.T) {
	digest := sha256.Sum256([]byte("original\n"))
	original := &templateManifest{
		ManifestVersion: templateManifestVersion,
		TemplateVersion: "1.0.0",
		Files: map[string]templateFileState{
			"README.md": {SHA256: hex.EncodeToString(digest[:]), Mode: 0o644},
		},
	}
	clone := cloneTemplateManifest(original)
	clone.Files["README.md"] = templateFileState{SHA256: strings.Repeat("0", sha256.Size*2), Mode: 0o600}

	if original.Files["README.md"] == clone.Files["README.md"] {
		t.Fatal("clone mutation aliased the original manifest map")
	}
}

func TestRecoverDirectoryReplacementRejectsSymlinkJournal(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "workspace")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(parent, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, directoryReplaceJournalPath(target)); err != nil {
		t.Fatal(err)
	}

	if err := recoverDirectoryReplacement(target); err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("symlink journal was not rejected: %v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "keep\n" {
		t.Fatalf("journal recovery changed symlink target: %q, %v", data, err)
	}
}

func TestRecoverDirectoryReplacementRejectsSymlinkStatePaths(t *testing.T) {
	for _, pathKind := range []string{"target", "stage", "backup"} {
		t.Run(pathKind, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "workspace")
			stage := filepath.Join(parent, ".vb-template-stage-test")
			backup := filepath.Join(parent, ".vb-template-backup-test")
			external := filepath.Join(parent, "external")
			if err := os.Mkdir(external, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, path := range map[string]string{"target": target, "stage": stage, "backup": backup} {
				if name == pathKind {
					if err := os.Symlink(external, path); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			journal := directoryReplaceJournal{
				Version: directoryReplaceJournalVersion, Target: filepath.Base(target),
				Stage: filepath.Base(stage), Backup: filepath.Base(backup),
				StartedAt: time.Now().Add(-2 * time.Hour),
			}
			writeRecoverableDirectoryReplaceJournal(t, target, &journal)

			if err := recoverDirectoryReplacement(target); err == nil || !strings.Contains(err.Error(), "not a real directory") {
				t.Fatalf("symlink %s was not rejected: %v", pathKind, err)
			}
			if info, err := os.Stat(external); err != nil || !info.IsDir() {
				t.Fatalf("external directory changed: %v, %v", info, err)
			}
		})
	}
}

func TestRenameReplacementDirectoryExclusiveNeverReplacesDestination(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "marker"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "marker"), []byte("destination"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := renameReplacementDirectoryExclusive(source, destination); err == nil {
		t.Fatal("exclusive directory rename replaced an existing destination")
	}
	for path, want := range map[string]string{source: "source", destination: "destination"} {
		content, err := os.ReadFile(filepath.Join(path, "marker"))
		if err != nil || string(content) != want {
			t.Fatalf("exclusive rename changed %s: %q, %v", path, content, err)
		}
	}

	if err := os.RemoveAll(destination); err != nil {
		t.Fatal(err)
	}
	if err := renameReplacementDirectoryExclusive(source, destination); err != nil {
		t.Fatalf("exclusive directory rename into absent destination: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil || string(content) != "source" {
		t.Fatalf("exclusive rename did not activate source: %q, %v", content, err)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatalf("exclusive rename left source name: %v", err)
	}
}

func TestRecoverDirectoryReplacementCrashStates(t *testing.T) {
	tests := []struct {
		name                    string
		target, stage, backup   bool
		wantTarget, wantStage   bool
		wantBackup, wantJournal bool
		wantTargetMarker        string
	}{
		{name: "before first rename", target: true, stage: true, wantTarget: true, wantTargetMarker: "target"},
		{name: "after first rename", stage: true, backup: true, wantTarget: true, wantTargetMarker: "stage"},
		{name: "after activation", target: true, backup: true, wantTarget: true, wantTargetMarker: "target"},
		{name: "restore backup", backup: true, wantTarget: true, wantTargetMarker: "backup"},
		{name: "stale journal", target: true, wantTarget: true, wantTargetMarker: "target"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "workspace")
			stage := filepath.Join(parent, ".vb-template-stage-test")
			backup := filepath.Join(parent, ".vb-template-backup-test")
			for _, item := range []struct {
				path, marker string
				exists       bool
			}{{target, "target", tt.target}, {stage, "stage", tt.stage}, {backup, "backup", tt.backup}} {
				if !item.exists {
					continue
				}
				if err := os.Mkdir(item.path, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(item.path, "marker"), []byte(item.marker), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			journal := directoryReplaceJournal{
				Version: directoryReplaceJournalVersion, Target: filepath.Base(target),
				Stage: filepath.Base(stage), Backup: filepath.Base(backup),
				StartedAt: time.Now().Add(-2 * time.Hour),
			}
			journalPath := directoryReplaceJournalPath(target)
			writeRecoverableDirectoryReplaceJournal(t, target, &journal)
			if err := recoverDirectoryReplacement(target); err != nil {
				t.Fatalf("recover: %v", err)
			}

			assertPathExistence(t, target, tt.wantTarget)
			assertPathExistence(t, stage, tt.wantStage)
			assertPathExistence(t, backup, tt.wantBackup)
			assertPathExistence(t, journalPath, tt.wantJournal)
			if tt.wantTargetMarker != "" {
				data, err := os.ReadFile(filepath.Join(target, "marker"))
				if err != nil || string(data) != tt.wantTargetMarker {
					t.Fatalf("target marker = %q, %v; want %q", data, err, tt.wantTargetMarker)
				}
			}
		})
	}
}

func TestRecoverDirectoryReplacementRejectsAmbiguousState(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "workspace")
	stage := filepath.Join(parent, ".vb-integration-stage-test")
	backup := filepath.Join(parent, ".vb-template-backup-test")
	for _, path := range []string{target, stage, backup} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	journal := directoryReplaceJournal{
		Version: directoryReplaceJournalVersion, Target: filepath.Base(target),
		Stage: filepath.Base(stage), Backup: filepath.Base(backup),
		StartedAt: time.Now().Add(-2 * time.Hour),
	}
	journalPath := directoryReplaceJournalPath(target)
	writeRecoverableDirectoryReplaceJournal(t, target, &journal)
	if err := recoverDirectoryReplacement(target); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous state was not rejected: %v", err)
	}
	for _, path := range []string{target, stage, backup, journalPath} {
		assertPathExistence(t, path, true)
	}
}

func TestRecoverDirectoryReplacementRejectsLiveOwnerRegardlessOfAge(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "workspace")
	stage := filepath.Join(parent, ".vb-template-stage-live")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	lease, _, err := acquireDirectoryReplaceLease(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.release() })
	journal := directoryReplaceJournal{
		Version: directoryReplaceJournalVersion, Target: filepath.Base(target),
		Stage: filepath.Base(stage), Backup: ".vb-template-backup-live",
		Nonce: lease.record.Nonce, PID: lease.record.PID, Host: lease.record.Host, StartedAt: time.Now().Add(-24 * time.Hour),
	}
	if err := writeDirectoryReplaceJournal(directoryReplaceJournalPath(target), journal); err != nil {
		t.Fatal(err)
	}

	if err := recoverDirectoryReplacement(target); err == nil || !strings.Contains(err.Error(), "still in progress") {
		t.Fatalf("live but old operation was recovered: %v", err)
	}
	for _, path := range []string{target, stage, directoryReplaceJournalPath(target), directoryReplaceLeasePath(target)} {
		assertPathExistence(t, path, true)
	}
}

func TestRemoveDirectoryReplaceJournalComparesNonce(t *testing.T) {
	target := filepath.Join(t.TempDir(), "workspace")
	journal := directoryReplaceJournal{
		Version: directoryReplaceJournalVersion, Target: filepath.Base(target),
		Stage: ".vb-template-stage-test", Backup: ".vb-template-backup-test",
		Nonce: strings.Repeat("b", 48), PID: os.Getpid(), Host: "host", StartedAt: time.Now(),
	}
	path := directoryReplaceJournalPath(target)
	if err := writeDirectoryReplaceJournal(path, journal); err != nil {
		t.Fatal(err)
	}
	if err := removeDirectoryReplaceJournal(path, strings.Repeat("c", 48)); err == nil || !strings.Contains(err.Error(), "ownership changed") {
		t.Fatalf("mismatched journal nonce was removed: %v", err)
	}
	assertPathExistence(t, path, true)
}

func writeRecoverableDirectoryReplaceJournal(t *testing.T, target string, journal *directoryReplaceJournal) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("d", 48)
	journal.Nonce = nonce
	journal.PID = 1 << 30
	journal.Host = host
	record := directoryReplaceLeaseRecord{
		Version: directoryReplaceLeaseVersion, Nonce: nonce, PID: journal.PID, Host: host,
		StartedAt: journal.StartedAt,
	}
	if err := writeDirectoryReplaceLease(directoryReplaceLeasePath(target), record); err != nil {
		t.Fatal(err)
	}
	if err := writeDirectoryReplaceJournal(directoryReplaceJournalPath(target), *journal); err != nil {
		t.Fatal(err)
	}
}

func assertPathExistence(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if want && err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !want && !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent: %v", path, err)
	}
}
