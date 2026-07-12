package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func decodeEntry(data []byte) (Entry, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return Entry{}, errors.New("audit entry must be exactly one JSON object")
	}
	var entry Entry
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return Entry{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Entry{}, errors.New("trailing JSON value")
		}
		return Entry{}, fmt.Errorf("trailing data: %w", err)
	}
	return entry, nil
}

// maxLineBytes caps a single audit line at 1 MiB. The default bufio.Scanner
// buffer of 64 KiB would refuse longer lines, and details strings can grow.
const maxLineBytes = 1 << 20

const (
	maxAuditBytes       = 256 << 20
	maxAuditLines       = 1_000_000
	maxAuditMatches     = 100_000
	maxAuditParseErrors = 1_024
)

// MaxQueryEntries is the largest caller-requested retained audit result.
const MaxQueryEntries = maxAuditMatches

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
	result, err := Query(path, Filter{}, false)
	if err != nil {
		return nil, nil, err
	}
	return result.Entries, result.ParseErrors, nil
}

// Query streams an audit log, applies filters before retaining entries, and
// optionally verifies the hash chain without first materializing the whole
// file. Hard aggregate bounds prevent an attacker-controlled or accidentally
// enormous log from exhausting memory. Tail queries use a fixed-size ring.
func Query(path string, filter Filter, verify bool) (QueryResult, error) {
	return queryWithHooks(path, filter, verify, nil)
}

type queryTestHooks struct {
	afterOpen         func()
	beforeFinalVerify func()
}

func queryWithHooks(path string, filter Filter, verify bool, hooks *queryTestHooks) (result QueryResult, retErr error) {
	if filter.Limit < 0 || filter.Limit > maxAuditMatches {
		return result, fmt.Errorf("audit limit must be between 0 and %d", maxAuditMatches)
	}
	scope, err := openAuditScope(path, false)
	if err != nil {
		return result, fmt.Errorf("open audit storage: %w", err)
	}
	if scope == nil {
		return result, nil
	}
	defer scope.close()
	opened, err := scope.openRegular(scope.base, os.O_RDONLY, false)
	if err != nil {
		return result, fmt.Errorf("open audit log: %w", err)
	}
	if opened == nil {
		return result, nil
	}
	defer func() {
		if err := opened.close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close audit log: %w", err))
		}
	}()
	if err := scope.verifyOpenFile(opened); err != nil {
		return result, fmt.Errorf("verify audit log before scan: %w", err)
	}
	initialInfo, err := opened.file.Stat()
	if err != nil {
		return result, err
	}
	if initialInfo.Size() > maxAuditBytes {
		return result, fmt.Errorf("audit log exceeds safe scan bound (%d bytes)", maxAuditBytes)
	}
	if initialInfo.Size() > 0 {
		var terminator [1]byte
		if _, err := opened.file.ReadAt(terminator[:], initialInfo.Size()-1); err != nil {
			return result, fmt.Errorf("inspect audit log terminator: %w", err)
		}
		if terminator[0] != '\n' {
			return result, errors.New("audit log has a truncated final line")
		}
	}
	if hooks != nil && hooks.afterOpen != nil {
		hooks.afterOpen()
	}
	if err := scope.verifyOpenFile(opened); err != nil {
		return result, fmt.Errorf("verify audit log after opening: %w", err)
	}

	// Scan only the size observed above. A concurrent well-formed append may
	// grow the same inode, but it belongs to the next query snapshot.
	scanner := bufio.NewScanner(io.LimitReader(opened.file, initialInfo.Size()))
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	lineNum := 0
	bytesRead := int64(0)
	previousHash := ""
	verifiedIndex := 0
	tailStart := 0
	for scanner.Scan() {
		lineNum++
		raw := scanner.Bytes()
		bytesRead += int64(len(raw)) + 1
		if lineNum > maxAuditLines || bytesRead > maxAuditBytes {
			return result, fmt.Errorf("audit log exceeds safe scan bounds (%d lines or %d bytes)", maxAuditLines, maxAuditBytes)
		}
		if len(raw) == 0 {
			continue
		}
		entry, jerr := decodeEntry(raw)
		if jerr != nil {
			if len(result.ParseErrors) >= maxAuditParseErrors {
				return result, fmt.Errorf("audit log contains more than %d malformed entries", maxAuditParseErrors)
			}
			result.ParseErrors = append(result.ParseErrors, ParseError{Line: lineNum, Err: jerr})
			continue
		}
		result.Total++
		if verify && result.VerifyError == nil {
			if entry.PrevHash != previousHash {
				result.VerifyError = &VerifyError{Index: verifiedIndex, Kind: "prev_hash", Expected: previousHash, Got: entry.PrevHash}
			} else {
				want, hashErr := hashEntry(entry)
				switch {
				case hashErr != nil:
					result.VerifyError = &VerifyError{Index: verifiedIndex, Kind: "hash_version", Expected: legacyHashVersion + " or " + currentHashVersion, Got: entry.HashVersion}
				case entry.EntryHash != want:
					result.VerifyError = &VerifyError{Index: verifiedIndex, Kind: "entry_hash", Expected: want, Got: entry.EntryHash}
				}
			}
			previousHash = entry.EntryHash
			verifiedIndex++
		}
		if !filter.matches(entry) {
			continue
		}
		if filter.Limit > 0 && !filter.Tail && len(result.Entries) >= filter.Limit {
			continue
		}
		if filter.Limit > 0 && filter.Tail {
			if len(result.Entries) < filter.Limit {
				result.Entries = append(result.Entries, entry)
				continue
			}
			result.Entries[tailStart] = entry
			tailStart = (tailStart + 1) % filter.Limit
			continue
		}
		if len(result.Entries) >= maxAuditMatches {
			return result, fmt.Errorf("audit query matched more than %d entries; add filters or --limit", maxAuditMatches)
		}
		result.Entries = append(result.Entries, entry)
	}
	if serr := scanner.Err(); serr != nil {
		return result, fmt.Errorf("scan audit log: %w", serr)
	}
	if hooks != nil && hooks.beforeFinalVerify != nil {
		hooks.beforeFinalVerify()
	}
	if err := scope.verifyOpenFile(opened); err != nil {
		return result, fmt.Errorf("verify audit log after scan: %w", err)
	}
	finalInfo, err := opened.file.Stat()
	if err != nil {
		return result, err
	}
	if finalInfo.Size() < initialInfo.Size() || (finalInfo.Size() == initialInfo.Size() && !finalInfo.ModTime().Equal(initialInfo.ModTime())) {
		return result, errors.New("audit log changed while scanning")
	}
	if filter.Limit > 0 && filter.Tail && len(result.Entries) == filter.Limit && tailStart != 0 {
		ordered := make([]Entry, 0, filter.Limit)
		ordered = append(ordered, result.Entries[tailStart:]...)
		ordered = append(ordered, result.Entries[:tailStart]...)
		result.Entries = ordered
	}
	return result, nil
}

// QueryResult is the bounded result of a streaming audit query.
type QueryResult struct {
	Entries     []Entry
	ParseErrors []ParseError
	Total       int
	VerifyError error
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
	Kind     string // "entry_hash", "prev_hash", or "hash_version"
	Expected string
	Got      string
}

// Error implements the error interface.
func (e *VerifyError) Error() string {
	return fmt.Sprintf("audit entry %d: %s mismatch (expected %s, got %s)", e.Index, e.Kind, e.Expected, e.Got)
}

// Verify walks the hash chain of the given entries and returns nil if the
// chain is intact. It checks two things per entry:
//
//  1. entry_hash matches the recomputed SHA-256 of the entry contents + prev_hash.
//  2. prev_hash matches the previous entry's entry_hash (or "" for the first entry).
//
// The first mismatch wins.
func Verify(entries []Entry) error {
	prev := ""
	for i, e := range entries {
		if e.PrevHash != prev {
			return &VerifyError{Index: i, Kind: "prev_hash", Expected: prev, Got: e.PrevHash}
		}
		want, err := hashEntry(e)
		if err != nil {
			return &VerifyError{Index: i, Kind: "hash_version", Expected: legacyHashVersion + " or " + currentHashVersion, Got: e.HashVersion}
		}
		if e.EntryHash != want {
			return &VerifyError{Index: i, Kind: "entry_hash", Expected: want, Got: e.EntryHash}
		}
		prev = e.EntryHash
	}
	return nil
}
