package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	if l.prevHash != "" {
		t.Fatalf("expected empty prevHash for new file, got %q", l.prevHash)
	}
}

func TestLogEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	// Write 3 entries
	for i := range 3 {
		err := l.Log("create", "tester", "FTR-0001", "entry "+string(rune('A'+i)))
		if err != nil {
			t.Fatalf("Log failed on entry %d: %v", i, err)
		}
	}

	// Read back and verify chain
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open audit file: %v", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("failed to parse entry: %v", err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify hash chain
	if entries[0].PrevHash != "" {
		t.Fatalf("first entry should have empty PrevHash")
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].PrevHash != entries[i-1].EntryHash {
			t.Fatalf("entry %d PrevHash %q != entry %d EntryHash %q", i, entries[i].PrevHash, i-1, entries[i-1].EntryHash)
		}
	}

	// Verify hashes are deterministic
	for _, e := range entries {
		expected := computeHash(Entry{
			HashVersion: e.HashVersion,
			Timestamp:   e.Timestamp,
			Action:      e.Action,
			Actor:       e.Actor,
			FeatureID:   e.FeatureID,
			Details:     e.Details,
			PrevHash:    e.PrevHash,
		})
		if e.EntryHash != expected {
			t.Fatalf("entry hash mismatch: got %q, computed %q", e.EntryHash, expected)
		}
	}
}

func TestLogConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	const goroutines = 10
	const entriesPerGoroutine = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			for j := range entriesPerGoroutine {
				if err := l.Log("test", "goroutine", "", "concurrent write"); err != nil {
					t.Errorf("concurrent Log failed: %v", err)
					return
				}
				_ = j
			}
		}(i)
	}
	wg.Wait()

	// Verify we have the right number of entries and chain is intact
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open: %v", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("parse failed: %v", err)
		}
		entries = append(entries, e)
	}

	expected := goroutines * entriesPerGoroutine
	if len(entries) != expected {
		t.Fatalf("expected %d entries, got %d", expected, len(entries))
	}

	// Verify chain integrity
	for i := 1; i < len(entries); i++ {
		if entries[i].PrevHash != entries[i-1].EntryHash {
			t.Fatalf("chain broken at entry %d", i)
		}
	}
}

func TestAppendLockCannotBeStolenByAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	first := &Logger{path: path, lockTimeout: 100 * time.Millisecond, lockPoll: time.Millisecond}
	lock, err := first.acquireAppendLock()
	if err != nil {
		t.Fatalf("acquire first append lock: %v", err)
	}
	defer lock.release()

	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(path+appendLockSuffix, old, old); err != nil {
		t.Skipf("cannot age locked coordination file on this platform: %v", err)
	}
	second := &Logger{path: path, lockTimeout: 20 * time.Millisecond, lockPoll: time.Millisecond}
	if _, err := second.acquireAppendLock(); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("live append lock was stolen after its mtime aged: %v", err)
	}
}

func TestAppendLockRejectsSymlinkPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	target := filepath.Join(dir, "attacker")
	if err := os.WriteFile(target, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+appendLockSuffix); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewLogger(path); err == nil {
		t.Fatal("audit logger accepted a symlinked append-lock path")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "do not touch" {
		t.Fatalf("symlink target changed: %q, %v", content, err)
	}
}

func TestLastHash(t *testing.T) {
	dir := t.TempDir()

	// Non-existent file
	hash, err := lastHash(filepath.Join(dir, "nonexistent.jsonl"))
	if err != nil {
		t.Fatalf("lastHash on missing file should not error: %v", err)
	}
	if hash != "" {
		t.Fatalf("expected empty hash for missing file")
	}

	// Empty file
	emptyPath := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(emptyPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err = lastHash(emptyPath)
	if err != nil {
		t.Fatalf("lastHash on empty file should not error: %v", err)
	}
	if hash != "" {
		t.Fatalf("expected empty hash for empty file")
	}

	// Corrupt last lines fail closed rather than resetting the chain.
	corruptPath := filepath.Join(dir, "corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = lastHash(corruptPath); err == nil {
		t.Fatal("lastHash accepted a corrupt final line")
	}
	if _, err = NewLogger(corruptPath); err == nil {
		t.Fatal("NewLogger accepted a corrupt final line")
	}
}

func TestNewLoggerResumesChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	// Write some entries with first logger
	l1, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	if err := l1.Log("create", "user1", "FTR-0001", "first"); err != nil {
		t.Fatal(err)
	}
	if err := l1.Log("move", "user1", "FTR-0001", "second"); err != nil {
		t.Fatal(err)
	}

	// Create a new logger on the same file — should resume chain
	l2, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger resume failed: %v", err)
	}
	if err := l2.Log("delete", "user2", "FTR-0001", "third"); err != nil {
		t.Fatal(err)
	}

	// Read all entries and verify chain
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify chain continuity across loggers
	if entries[2].PrevHash != entries[1].EntryHash {
		t.Fatalf("chain broken across logger instances: entry 2 PrevHash %q != entry 1 EntryHash %q", entries[2].PrevHash, entries[1].EntryHash)
	}
}

func TestComputeHash(t *testing.T) {
	e := Entry{
		Timestamp: "2024-01-01T00:00:00Z",
		Action:    "create",
		Actor:     "tester",
		FeatureID: "FTR-0001",
		Details:   "test",
		PrevHash:  "",
	}

	hash1 := computeHash(e)
	hash2 := computeHash(e)
	if hash1 != hash2 {
		t.Fatalf("computeHash is not deterministic")
	}

	// Different input should produce different hash
	e.Details = "different"
	hash3 := computeHash(e)
	if hash1 == hash3 {
		t.Fatalf("different inputs should produce different hashes")
	}
}

func TestHashV2FramesAmbiguousV1Values(t *testing.T) {
	left := Entry{Timestamp: "2024-01-01T00:00:00Z", Action: "ab", Actor: "c"}
	right := Entry{Timestamp: "2024-01-01T00:00:00Z", Action: "a", Actor: "bc"}
	if hashEntryV1(left) != hashEntryV1(right) {
		t.Fatal("test setup does not demonstrate the v1 concatenation ambiguity")
	}
	left.HashVersion = currentHashVersion
	right.HashVersion = currentHashVersion
	if hashEntryV2(left) == hashEntryV2(right) {
		t.Fatal("v2 length framing did not disambiguate adjacent fields")
	}
}

func TestLastHashRejectsTruncationAndTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Log("create", "tester", "FTR-0001", "valid"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lastHash(path); err == nil {
		t.Fatal("lastHash accepted a final line without its newline terminator")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	tampered := []byte(string(data[:len(data)-2]) + "0\n")
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lastHash(path); err == nil {
		t.Fatal("lastHash accepted a tampered final hash")
	}
}

func TestAppendLockWaitIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	held, err := l.acquireAppendLock()
	if err != nil {
		t.Fatalf("acquire held append lock: %v", err)
	}
	l.lockTimeout = 40 * time.Millisecond
	l.lockPoll = 5 * time.Millisecond
	if err := l.Log("create", "tester", "", ""); err == nil {
		t.Fatal("Log did not time out behind an active process lock")
	}
	if err := held.release(); err != nil {
		t.Fatalf("release held append lock: %v", err)
	}
	if err := l.Log("create", "tester", "", "after release"); err != nil {
		t.Fatalf("Log did not continue after live lock release: %v", err)
	}
}

func TestAuditProcessHelper(t *testing.T) {
	if os.Getenv("VB_AUDIT_PROCESS_HELPER") != "1" {
		return
	}
	path := os.Getenv("VB_AUDIT_PROCESS_PATH")
	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := l.Log("process", os.Getenv("VB_AUDIT_PROCESS_ACTOR"), "", "append"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLogConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	const processCount = 6
	type runningCommand struct {
		cmd    *exec.Cmd
		output bytes.Buffer
	}
	commands := make([]*runningCommand, 0, processCount)
	for i := 0; i < processCount; i++ {
		running := &runningCommand{cmd: exec.Command(os.Args[0], "-test.run=^TestAuditProcessHelper$")}
		running.cmd.Env = append(os.Environ(),
			"VB_AUDIT_PROCESS_HELPER=1",
			"VB_AUDIT_PROCESS_PATH="+path,
			fmt.Sprintf("VB_AUDIT_PROCESS_ACTOR=process-%d", i),
		)
		running.cmd.Stdout = &running.output
		running.cmd.Stderr = &running.output
		if err := running.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, running)
	}
	for _, running := range commands {
		if err := running.cmd.Wait(); err != nil {
			t.Fatalf("audit helper failed: %v\n%s", err, running.output.String())
		}
	}
	entries, parseErrors, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parseErrors) != 0 {
		t.Fatalf("multiprocess append produced parse errors: %v", parseErrors)
	}
	if len(entries) != processCount*12 {
		t.Fatalf("expected %d process entries, got %d", processCount*12, len(entries))
	}
	if err := Verify(entries); err != nil {
		t.Fatalf("multiprocess append forked the hash chain: %v", err)
	}
}

func TestLogOptionalFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	l, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}

	// Log with empty optional fields
	if err := l.Log("lock", "system", "", ""); err != nil {
		t.Fatalf("Log with empty optionals failed: %v", err)
	}

	// Verify the entry was written
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal(data[:len(data)-1], &e); err != nil {
		t.Fatal(err)
	}
	if e.Action != "lock" {
		t.Fatalf("expected action 'lock', got %q", e.Action)
	}
}

func TestAuditStorageRejectsSymlinkedParentComponent(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(base, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(linked, "nested", "audit.jsonl")
	if _, err := NewLogger(path); err == nil {
		t.Fatal("NewLogger accepted a symlinked audit directory component")
	}
	if _, err := Query(path, Filter{}, false); err == nil {
		t.Fatal("Query accepted a symlinked audit directory component")
	}
	if _, err := os.Stat(filepath.Join(outside, "nested", "audit.jsonl"+appendLockSuffix)); !os.IsNotExist(err) {
		t.Fatalf("outside append lock was created: %v", err)
	}
}

func TestAuditLogLeafSymlinkFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	target := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := NewLogger(path); err == nil {
		t.Fatal("NewLogger accepted a symlinked audit log")
	}
	if _, err := Query(path, Filter{}, false); err == nil {
		t.Fatal("Query accepted a symlinked audit log")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "outside\n" {
		t.Fatalf("outside symlink target changed: %q, %v", data, err)
	}
}

func TestAuditLogAndAppendLockHardlinksFailClosed(t *testing.T) {
	t.Run("log", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		logger, err := NewLogger(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := logger.Log("create", "actor", "", "seed"); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, path+".alias"); err != nil {
			t.Skipf("hardlinks unavailable: %v", err)
		}
		if _, err := Query(path, Filter{}, false); err == nil {
			t.Fatal("Query accepted a multi-hardlink audit log")
		}
		if err := logger.Log("create", "actor", "", "blocked"); err == nil {
			t.Fatal("Log accepted a multi-hardlink audit log")
		}
	})

	t.Run("append-lock", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		logger, err := NewLogger(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path+appendLockSuffix, path+".lock-alias"); err != nil {
			t.Skipf("hardlinks unavailable: %v", err)
		}
		if err := logger.Log("create", "actor", "", "blocked"); err == nil {
			t.Fatal("Log accepted a multi-hardlink append lock")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("audit entry was written through unsafe append lock: %v", err)
		}
	})
}

func TestLogDetectsAppendLockReplacementBeforeOpeningLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	logger.testHooks = &loggerTestHooks{
		afterAppendLock: func() {
			replaceAuditTestFile(t, path+appendLockSuffix, nil)
		},
	}
	if err := logger.Log("create", "actor", "", "must not persist"); err == nil {
		t.Fatal("Log accepted an append-lock inode replacement")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("audit log was created after append-lock replacement: %v", err)
	}
}

func TestLogDetectsAuditReplacementBeforePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Log("create", "actor", "", "seed"); err != nil {
		t.Fatal(err)
	}
	detached := path + ".detached"
	replacement := []byte("replacement must remain untouched\n")
	logger.testHooks = &loggerTestHooks{
		afterLogOpen: func() {
			if err := os.Rename(path, detached); err != nil {
				t.Skipf("cannot replace an open audit path on this platform: %v", err)
			}
			if err := os.WriteFile(path, replacement, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	if err := logger.Log("create", "actor", "", "must not persist"); err == nil {
		t.Fatal("Log accepted audit replacement before persistence")
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, replacement) {
		t.Fatalf("replacement audit file changed: %q, %v", data, err)
	}
	entries, parseErrors, err := Read(detached)
	if err != nil || len(parseErrors) != 0 || len(entries) != 1 {
		t.Fatalf("detached original was unexpectedly appended: entries=%d parse=%v err=%v", len(entries), parseErrors, err)
	}
}

func TestLogReportsAuditReplacementAfterPersistenceHonestly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := logger.Log("create", "actor", "", "seed"); err != nil {
		t.Fatal(err)
	}
	detached := path + ".persisted-detached"
	replacement := []byte("new path remains untouched\n")
	logger.testHooks = &loggerTestHooks{
		afterPersist: func() {
			if err := os.Rename(path, detached); err != nil {
				t.Skipf("cannot replace an open audit path on this platform: %v", err)
			}
			if err := os.WriteFile(path, replacement, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	if err := logger.Log("create", "actor", "", "persisted before swap"); err == nil || !strings.Contains(err.Error(), "persisted") {
		t.Fatalf("post-persistence replacement error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, replacement) {
		t.Fatalf("replacement audit file changed: %q, %v", data, err)
	}
	entries, parseErrors, err := Read(detached)
	if err != nil || len(parseErrors) != 0 || len(entries) != 2 {
		t.Fatalf("detached persisted log did not retain the append: entries=%d parse=%v err=%v", len(entries), parseErrors, err)
	}
}

func TestLogDetectsAuditDirectorySwapWhileLockHeld(t *testing.T) {
	base := t.TempDir()
	directory := filepath.Join(base, "workspace")
	path := filepath.Join(directory, "audit.jsonl")
	logger, err := NewLogger(path)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "workspace-moved")
	logger.testHooks = &loggerTestHooks{
		afterAppendLock: func() {
			if err := os.Rename(directory, moved); err != nil {
				t.Skipf("cannot replace an open audit directory on this platform: %v", err)
			}
			if err := os.Mkdir(directory, 0o750); err != nil {
				t.Fatal(err)
			}
		},
	}
	if err := logger.Log("create", "actor", "", "must not persist"); err == nil {
		t.Fatal("Log accepted an audit directory identity swap")
	}
	for _, candidate := range []string{path, filepath.Join(moved, "audit.jsonl")} {
		if _, err := os.Stat(candidate); !os.IsNotExist(err) {
			t.Fatalf("audit entry appeared after directory swap at %s: %v", candidate, err)
		}
	}
}

func TestLastHashRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	for _, payload := range []string{
		`{"timestamp":"2026-01-01T00:00:00Z","action":"x","actor":"a","prev_hash":"","entry_hash":"` + strings.Repeat("0", 64) + `","unknown":true}` + "\n",
		`{"timestamp":"2026-01-01T00:00:00Z","action":"x","actor":"a","prev_hash":"","entry_hash":"` + strings.Repeat("0", 64) + `"}{}` + "\n",
	} {
		path := filepath.Join(t.TempDir(), "audit.jsonl")
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := lastHash(path); err == nil {
			t.Fatal("lastHash accepted non-strict audit JSON")
		}
		if _, err := NewLogger(path); err == nil {
			t.Fatal("NewLogger accepted non-strict audit JSON")
		}
	}
}

func replaceAuditTestFile(t *testing.T, path string, payload []byte) {
	t.Helper()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".audit-test-replace-*")
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
	if err := replaceAuditTestPath(tmpPath, path); err != nil {
		t.Fatal(err)
	}
}
