package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
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
			Timestamp: e.Timestamp,
			Action:    e.Action,
			Actor:     e.Actor,
			FeatureID: e.FeatureID,
			Details:   e.Details,
			PrevHash:  e.PrevHash,
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
				_ = l.Log("test", "goroutine", "", "concurrent write")
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

	// Corrupt last line
	corruptPath := filepath.Join(dir, "corrupt.jsonl")
	if err := os.WriteFile(corruptPath, []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash, err = lastHash(corruptPath)
	if err != nil {
		t.Fatalf("lastHash on corrupt file should not error: %v", err)
	}
	if hash != "" {
		t.Fatalf("expected empty hash for corrupt file")
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
