package audit

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
)

func sampleEntries() []Entry {
	return []Entry{
		{
			Timestamp: "2026-04-16T21:06:59Z",
			Action:    "lock",
			Actor:     "netors",
			FeatureID: "op-move-FTR-0019",
			Details:   "owner=vb-cli-op ttl=1",
			PrevHash:  "",
			EntryHash: "96bd69a73fdc5a51d2758e869b6ea7c60c03f32ef274f4008eb8a60e3aaafc97",
		},
		{
			Timestamp: "2026-04-16T21:07:05Z",
			Action:    "unlock",
			Actor:     "netors",
			FeatureID: "op-move-FTR-0019",
			Details:   "",
			PrevHash:  "96bd69a73fdc5a51d2758e869b6ea7c60c03f32ef274f4008eb8a60e3aaafc97",
			EntryHash: "deadbeefcafefeed1234567890abcdefdeadbeefcafefeed1234567890abcdef",
		},
	}
}

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{
		"":       FormatHuman,
		"human":  FormatHuman,
		"HUMAN":  FormatHuman,
		" table": FormatTable,
		"jsonl":  FormatJSONL,
		"json":   FormatJSON,
		"xml":    FormatXML,
		"agent":  FormatAgent,
	}
	for input, want := range cases {
		got, err := ParseFormat(input)
		if err != nil {
			t.Fatalf("ParseFormat(%q) errored: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseFormat(%q) = %q, want %q", input, got, want)
		}
	}

	if _, err := ParseFormat("yaml"); err == nil {
		t.Fatalf("expected error for unknown format")
	}
}

func TestRenderJSONL(t *testing.T) {
	out, err := Render(sampleEntries(), FormatJSONL, RenderOptions{})
	if err != nil {
		t.Fatalf("Render jsonl failed: %v", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 jsonl lines, got %d", len(lines))
	}
	var e Entry
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("first line not valid JSON: %v", err)
	}
	if e.Action != "lock" {
		t.Fatalf("expected action=lock, got %q", e.Action)
	}
}

func TestRenderJSON(t *testing.T) {
	out, err := Render(sampleEntries(), FormatJSON, RenderOptions{})
	if err != nil {
		t.Fatalf("Render json failed: %v", err)
	}
	var payload struct {
		Count   int     `json:"count"`
		Entries []Entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output not valid JSON: %v\noutput:\n%s", err, out)
	}
	if payload.Count != 2 || len(payload.Entries) != 2 {
		t.Fatalf("expected 2 entries, got count=%d entries=%d", payload.Count, len(payload.Entries))
	}
}

func TestRenderXML(t *testing.T) {
	out, err := Render(sampleEntries(), FormatXML, RenderOptions{})
	if err != nil {
		t.Fatalf("Render xml failed: %v", err)
	}
	if !strings.HasPrefix(out, xml.Header) {
		t.Fatalf("XML output missing header")
	}
	var wrap auditXML
	body := strings.TrimPrefix(out, xml.Header)
	if err := xml.Unmarshal([]byte(body), &wrap); err != nil {
		t.Fatalf("XML output failed to unmarshal: %v\n%s", err, out)
	}
	if wrap.Count != 2 || len(wrap.Entries) != 2 {
		t.Fatalf("wrong XML count: %+v", wrap)
	}
}

func TestRenderTable(t *testing.T) {
	out, err := Render(sampleEntries(), FormatTable, RenderOptions{})
	if err != nil {
		t.Fatalf("Render table failed: %v", err)
	}
	if !strings.Contains(out, "TIMESTAMP") {
		t.Fatalf("table missing header: %s", out)
	}
	if !strings.Contains(out, "lock") || !strings.Contains(out, "unlock") {
		t.Fatalf("table missing rows: %s", out)
	}
	if strings.Contains(out, "PREV") {
		t.Fatalf("table should not include hash columns by default")
	}

	// With hashes.
	out, err = Render(sampleEntries(), FormatTable, RenderOptions{IncludeHashes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "PREV") || !strings.Contains(out, "HASH") {
		t.Fatalf("table with hashes missing columns: %s", out)
	}
	if !strings.Contains(out, "96bd69a7") {
		t.Fatalf("table should include truncated hash: %s", out)
	}
}

func TestRenderHuman(t *testing.T) {
	out, err := Render(sampleEntries(), FormatHuman, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2026-04-16 21:06:59") {
		t.Fatalf("expected reformatted timestamp, got: %s", out)
	}
	if !strings.Contains(out, "owner=vb-cli-op") {
		t.Fatalf("human output missing details: %s", out)
	}
	if strings.Contains(out, "prev=") {
		t.Fatalf("default human output should hide hashes")
	}

	// With hashes.
	out, err = Render(sampleEntries(), FormatHuman, RenderOptions{IncludeHashes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "prev=-") || !strings.Contains(out, "hash=96bd69a7") {
		t.Fatalf("verbose human missing hashes: %s", out)
	}

	// Empty.
	out, err = Render(nil, FormatHuman, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No audit entries") {
		t.Fatalf("expected empty-state message, got %q", out)
	}
}

func TestRenderAgent(t *testing.T) {
	// Empty
	out, err := Render(nil, FormatAgent, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "count: 0") {
		t.Fatalf("empty agent output wrong: %s", out)
	}

	// Default (no hashes)
	out, err = Render(sampleEntries(), FormatAgent, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "count: 2") || !strings.Contains(out, "--- entry 1 ---") {
		t.Fatalf("agent output missing structure: %s", out)
	}
	if strings.Contains(out, "prev_hash:") {
		t.Fatalf("default agent should hide hashes: %s", out)
	}

	// With hashes
	out, err = Render(sampleEntries(), FormatAgent, RenderOptions{IncludeHashes: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "prev_hash:") || !strings.Contains(out, "entry_hash:") {
		t.Fatalf("verbose agent missing hashes: %s", out)
	}

	// Entry with empty optional fields should omit those keys.
	minimal := []Entry{{Timestamp: "2026-04-16T21:06:59Z", Action: "lock", Actor: "system"}}
	out, err = Render(minimal, FormatAgent, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "feature_id:") || strings.Contains(out, "details:") {
		t.Fatalf("agent should omit empty optionals: %s", out)
	}
}

func TestRenderUnknownFormat(t *testing.T) {
	if _, err := Render(sampleEntries(), Format("nope"), RenderOptions{}); err == nil {
		t.Fatalf("expected error for unsupported format")
	}
}

func TestFormatTimestampPassThrough(t *testing.T) {
	if got := formatTimestamp("garbage"); got != "garbage" {
		t.Fatalf("expected pass-through for non-RFC3339 timestamp, got %q", got)
	}
}

func TestShortHashEdgeCases(t *testing.T) {
	if got := shortHash(""); got != "-" {
		t.Fatalf("empty hash should render as dash, got %q", got)
	}
	if got := shortHash("abc"); got != "abc" {
		t.Fatalf("short hash should pass through, got %q", got)
	}
}
