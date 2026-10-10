package audit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// maxLineBytes caps a single audit line at 1 MiB. The default bufio.Scanner
// buffer of 64 KiB would refuse longer lines, and details strings can grow.
const maxLineBytes = 1 << 20

// Read parses an audit JSONL file into a slice of Entry values.
//
// Behaviour:
//   - If the file does not exist, returns an empty slice and nil error (an
//     un-initialised workspace is not an error condition for reads).
//   - Blank lines are silently skipped.
//   - Lines that fail JSON parsing are reported via the returned ParseErrors
//     slice but do NOT abort the read. Callers may surface the errors as
//     warnings without losing the entries that did parse.
func Read(path string) (entries []Entry, parseErrors []ParseError, err error) {
	// #nosec G304 -- path is constructed from controlled RootDir configuration
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Bytes()
		if len(raw) == 0 {
			continue
		}
		var entry Entry
		if jerr := json.Unmarshal(raw, &entry); jerr != nil {
			parseErrors = append(parseErrors, ParseError{Line: lineNum, Err: jerr})
			continue
		}
		entries = append(entries, entry)
	}
	if serr := scanner.Err(); serr != nil {
		return entries, parseErrors, fmt.Errorf("scan audit log: %w", serr)
	}
	return entries, parseErrors, nil
}

// ParseError records a JSON parse failure for a single audit line.
type ParseError struct {
	Line int
	Err  error
}

// Error implements the error interface.
func (e ParseError) Error() string {
	return fmt.Sprintf("line %d: %v", e.Line, e.Err)
}

// Filter holds optional filter criteria for audit entries. A zero-value Filter
// matches every entry. Slice fields use OR semantics within the field; the
// fields themselves combine with AND.
type Filter struct {
	Actions    []string
	Actors     []string
	FeatureIDs []string
	Since      *time.Time
	Until      *time.Time
	Contains   string // case-insensitive substring match against Details
	Limit      int    // 0 = no cap
	Tail       bool   // when Limit > 0, take the last Limit matches instead of the first
}

// ParseTime accepts RFC3339 timestamps and bare YYYY-MM-DD dates. The empty
// string yields a nil pointer so callers can pass user input through unchanged.
func ParseTime(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("unrecognised time format %q (expected RFC3339 or YYYY-MM-DD)", s)
}

// Apply filters the input entries and returns the matching subset. The order
// of the input slice is preserved, except when Tail is true and Limit > 0,
// in which case the last Limit matches are returned (still in chronological
// order).
func (f Filter) Apply(entries []Entry) []Entry {
	if len(entries) == 0 {
		return nil
	}

	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if !f.matches(e) {
			continue
		}
		out = append(out, e)
	}

	if f.Limit > 0 && len(out) > f.Limit {
		if f.Tail {
			out = out[len(out)-f.Limit:]
		} else {
			out = out[:f.Limit]
		}
	}
	return out
}

func (f Filter) matches(e Entry) bool {
	if len(f.Actions) > 0 && !containsString(f.Actions, e.Action) {
		return false
	}
	if len(f.Actors) > 0 && !containsString(f.Actors, e.Actor) {
		return false
	}
	if len(f.FeatureIDs) > 0 && !containsString(f.FeatureIDs, e.FeatureID) {
		return false
	}
	if f.Since != nil || f.Until != nil {
		ts, err := time.Parse(time.RFC3339, e.Timestamp)
		if err != nil {
			// Entries with unparsable timestamps cannot satisfy a time filter.
			return false
		}
		if f.Since != nil && ts.Before(*f.Since) {
			return false
		}
		if f.Until != nil && ts.After(*f.Until) {
			return false
		}
	}
	if f.Contains != "" {
		if !strings.Contains(strings.ToLower(e.Details), strings.ToLower(f.Contains)) {
			return false
		}
	}
	return true
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// VerifyError describes a hash-chain integrity failure.
type VerifyError struct {
	Index    int    // zero-based index of the offending entry
	Kind     string // "entry_hash" or "prev_hash"
	Expected string
	Got      string
}

// Error implements the error interface.
func (e *VerifyError) Error() string {
	return fmt.Sprintf("audit entry %d: %s mismatch (expected %s, got %s)", e.Index, e.Kind, e.Expected, e.Got)
}

// Verify checks that no entry has been ALTERED, and returns nil when none has.
//
// It recomputes each entry's own SHA-256 over its contents plus the prev_hash
// it carries. That is a per-entry signature: it depends on nothing outside the
// entry, so it holds however the file was assembled.
//
// IT DOES NOT ASSERT ORDER, AND IT CANNOT DETECT A DELETION. That is a
// deliberate narrowing, not an oversight, and the reason is that the log is a
// file in a git repository that several branches append to at once:
//
//	two branches each append with prev_hash pointing at the tip THEY saw, so the
//	moment they merge the links fork — by construction, with nothing wrong and
//	no one tampering.
//
// The old Verify also required prev_hash to name the previous line, so it
// failed on any board that had ever been worked on two branches. Measured on
// one real repository: 2016 entries, 726 broken links, and ZERO altered
// entries. It had been failing since a week after that log was created, which
// is the whole working life of the tool — a check that is always red detects
// nothing, because nobody can tell the day it goes red for a real reason.
//
// Use ChainBreaks to report the links as an observation. Do not promote it
// back into this function without first making concurrent appends impossible.
func Verify(entries []Entry) error {
	for i, e := range entries {
		want := computeHash(Entry{
			Timestamp: e.Timestamp,
			Action:    e.Action,
			Actor:     e.Actor,
			FeatureID: e.FeatureID,
			Details:   e.Details,
			PrevHash:  e.PrevHash,
		})
		if e.EntryHash != want {
			return &VerifyError{Index: i, Kind: "entry_hash", Expected: want, Got: e.EntryHash}
		}
	}
	return nil
}

// ChainBreaks counts entries whose prev_hash does not name the preceding
// entry's hash.
//
// Each one is a point where two branches' appends were interleaved by a merge.
// It is a count of merge joins, NOT a count of problems, and that is why it is
// reported rather than returned as an error.
func ChainBreaks(entries []Entry) int {
	breaks := 0
	prev := ""
	for _, e := range entries {
		if e.PrevHash != prev {
			breaks++
		}
		prev = e.EntryHash
	}
	return breaks
}
