package audit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeEntries(t *testing.T, path string, actions ...string) []Entry {
	t.Helper()
	l, err := NewLogger(path)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	for _, a := range actions {
		if err := l.Log(a, "tester", "FTR-0001", "details for "+a); err != nil {
			t.Fatalf("Log failed: %v", err)
		}
	}
	entries, _, err := Read(path)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	return entries
}

func TestReadMissingFile(t *testing.T) {
	entries, parseErrs, err := Read(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("Read on missing file should not error: %v", err)
	}
	if len(entries) != 0 || len(parseErrs) != 0 {
		t.Fatalf("expected zero entries and zero parse errors, got %d/%d", len(entries), len(parseErrs))
	}
}

func TestReadValidAndCorruptLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	// Two valid entries, one corrupt line, one blank line.
	content := `{"timestamp":"2026-04-16T21:06:59Z","action":"lock","actor":"netors","feature_id":"FTR-1","details":"","prev_hash":"","entry_hash":"h1"}
not json
{"timestamp":"2026-04-16T21:07:00Z","action":"unlock","actor":"netors","feature_id":"FTR-1","details":"","prev_hash":"h1","entry_hash":"h2"}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, parseErrs, err := Read(path)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if len(parseErrs) != 1 {
		t.Fatalf("expected 1 parse error, got %d", len(parseErrs))
	}
	if parseErrs[0].Line != 2 {
		t.Fatalf("expected parse error on line 2, got line %d", parseErrs[0].Line)
	}
	if !strings.Contains(parseErrs[0].Error(), "line 2") {
		t.Fatalf("expected ParseError.Error() to mention line, got %q", parseErrs[0].Error())
	}
}

func TestReadOpenError(t *testing.T) {
	// Pointing Read at a directory triggers a non-IsNotExist open error.
	dir := t.TempDir()
	_, _, err := Read(dir)
	if err == nil {
		t.Fatalf("expected open error when path is a directory")
	}
}

func TestReadScanError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	// A line longer than maxLineBytes forces the scanner to error.
	long := strings.Repeat("a", maxLineBytes+10)
	if err := os.WriteFile(path, []byte(long+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Read(path)
	if err == nil {
		t.Fatalf("expected scanner error on overlong line")
	}
}

func TestParseTime(t *testing.T) {
	if tp, err := ParseTime(""); err != nil || tp != nil {
		t.Fatalf("empty string should yield nil/nil, got %v/%v", tp, err)
	}
	if tp, err := ParseTime("2026-04-16"); err != nil || tp == nil {
		t.Fatalf("YYYY-MM-DD should parse, got err=%v tp=%v", err, tp)
	}
	if tp, err := ParseTime("2026-04-16T12:00:00Z"); err != nil || tp == nil {
		t.Fatalf("RFC3339 should parse, got err=%v tp=%v", err, tp)
	}
	if _, err := ParseTime("not a date"); err == nil {
		t.Fatalf("garbage should fail to parse")
	}
}

func TestFilterEmptyInput(t *testing.T) {
	if got := (Filter{}).Apply(nil); got != nil {
		t.Fatalf("filtering nil should yield nil, got %v", got)
	}
}

func TestFilterByEveryDimension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	entries := writeEntries(t, path, "lock", "unlock", "move", "delete", "create")
	// Re-hydrate with different actors/feature_ids/details by appending raw entries.
	// Easier: just craft entries directly.
	entries = []Entry{
		{Timestamp: "2026-04-16T10:00:00Z", Action: "lock", Actor: "alice", FeatureID: "FTR-1", Details: "owner=alice ttl=1"},
		{Timestamp: "2026-04-17T10:00:00Z", Action: "unlock", Actor: "bob", FeatureID: "FTR-1", Details: "owner=bob"},
		{Timestamp: "2026-04-18T10:00:00Z", Action: "move", Actor: "alice", FeatureID: "FTR-2", Details: "from=backlog to=in-progress"},
		{Timestamp: "2026-04-19T10:00:00Z", Action: "create", Actor: "carol", FeatureID: "FTR-3", Details: ""},
		{Timestamp: "bad-timestamp", Action: "delete", Actor: "alice", FeatureID: "FTR-4", Details: ""},
	}

	tests := []struct {
		name   string
		filter Filter
		want   int
	}{
		{"all", Filter{}, 5},
		{"action=lock", Filter{Actions: []string{"lock"}}, 1},
		{"action=lock|unlock", Filter{Actions: []string{"lock", "unlock"}}, 2},
		{"actor=alice", Filter{Actors: []string{"alice"}}, 3},
		{"feature_id=FTR-1", Filter{FeatureIDs: []string{"FTR-1"}}, 2},
		{"contains owner", Filter{Contains: "OWNER"}, 2},
		{"contains miss", Filter{Contains: "nonexistent"}, 0},
		{"since 2026-04-18", Filter{Since: mustTime(t, "2026-04-18")}, 2}, // entries on/after 04-18 with valid TS
		{"until 2026-04-16", Filter{Until: mustTime(t, "2026-04-16T23:59:59Z")}, 1},
		{"since+until window", Filter{Since: mustTime(t, "2026-04-17"), Until: mustTime(t, "2026-04-18T23:59:59Z")}, 2},
		{"limit head", Filter{Limit: 2}, 2},
		{"limit tail", Filter{Limit: 2, Tail: true}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.filter.Apply(entries)
			if len(got) != tc.want {
				t.Fatalf("want %d, got %d (%v)", tc.want, len(got), got)
			}
		})
	}

	// Tail returns the LAST N, head returns the FIRST N.
	head := Filter{Limit: 2}.Apply(entries)
	tail := Filter{Limit: 2, Tail: true}.Apply(entries)
	if head[0].Action != "lock" || head[1].Action != "unlock" {
		t.Fatalf("head wrong: %v", head)
	}
	if tail[0].Action != "create" || tail[1].Action != "delete" {
		t.Fatalf("tail wrong: %v", tail)
	}
}

func mustTime(t *testing.T, s string) *time.Time {
	t.Helper()
	tp, err := ParseTime(s)
	if err != nil {
		t.Fatalf("ParseTime(%q): %v", s, err)
	}
	return tp
}

func TestVerifyClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	entries := writeEntries(t, path, "create", "move", "delete")
	if err := Verify(entries); err != nil {
		t.Fatalf("Verify on intact chain failed: %v", err)
	}
}

func TestVerifyEntryHashMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	entries := writeEntries(t, path, "create", "move")
	entries[1].EntryHash = "tampered"
	err := Verify(entries)
	if err == nil {
		t.Fatalf("expected error on tampered entry_hash")
	}
	var ve *VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VerifyError, got %T", err)
	}
	if ve.Index != 1 || ve.Kind != "entry_hash" {
		t.Fatalf("unexpected VerifyError: %+v", ve)
	}
	if !strings.Contains(ve.Error(), "entry_hash mismatch") {
		t.Fatalf("Error() should describe the mismatch, got %q", ve.Error())
	}
}

// Editing prev_hash is still caught — it is part of the entry's own hash, so
// the alteration surfaces as entry_hash rather than going unnoticed. Verify
// stopped asserting ORDER; it did not stop noticing an edit to this field.
func TestVerifyPrevHashEditIsStillAnAlteration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	entries := writeEntries(t, path, "create", "move")
	entries[1].PrevHash = "wrong"
	err := Verify(entries)
	var ve *VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VerifyError, got %v", err)
	}
	if ve.Kind != "entry_hash" {
		t.Fatalf("expected entry_hash mismatch, got %s", ve.Kind)
	}
}

// THE CASE THE OLD Verify FAILED ON, and the reason this card exists: two
// branches each appended, a merge interleaved them, so entry 1's prev_hash
// names a tip that is no longer the line above it. Nothing was altered — every
// entry still hashes to what it carries — and Verify must say so.
func TestVerifyPassesAcrossAMergeJoin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	entries := writeEntries(t, path, "create", "move")

	// Re-seat the second entry on a DIFFERENT branch's tip and re-sign it, which
	// is exactly what the other branch's own append looked like before the merge.
	entries[1].PrevHash = "0000000000000000000000000000000000000000000000000000000000000000"
	entries[1].EntryHash = computeHash(Entry{
		Timestamp: entries[1].Timestamp, Action: entries[1].Action, Actor: entries[1].Actor,
		FeatureID: entries[1].FeatureID, Details: entries[1].Details, PrevHash: entries[1].PrevHash,
	})

	if err := Verify(entries); err != nil {
		t.Fatalf("a merged log must verify, got %v", err)
	}
	if got := ChainBreaks(entries); got != 1 {
		t.Fatalf("expected 1 merge join, got %d", got)
	}
}

func TestChainBreaksCountsNothingOnAnUnmergedLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	entries := writeEntries(t, path, "create", "move", "update")
	if got := ChainBreaks(entries); got != 0 {
		t.Fatalf("expected 0 joins on a linear log, got %d", got)
	}
	if got := ChainBreaks(nil); got != 0 {
		t.Fatalf("expected 0 joins on an empty log, got %d", got)
	}
}
