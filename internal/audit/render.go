package audit

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"text/tabwriter"
)

// Format identifies a renderer for audit entries.
type Format string

// Supported render formats.
const (
	FormatHuman Format = "human"
	FormatTable Format = "table"
	FormatJSONL Format = "jsonl"
	FormatJSON  Format = "json"
	FormatXML   Format = "xml"
	FormatAgent Format = "agent"
)

// Formats lists every supported format value, for help text and validation.
var Formats = []Format{FormatHuman, FormatTable, FormatJSONL, FormatJSON, FormatXML, FormatAgent}

// ParseFormat returns the canonical Format for s or an error listing the valid options.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "human":
		return FormatHuman, nil
	case "table":
		return FormatTable, nil
	case "jsonl":
		return FormatJSONL, nil
	case "json":
		return FormatJSON, nil
	case "xml":
		return FormatXML, nil
	case "agent":
		return FormatAgent, nil
	}
	names := make([]string, len(Formats))
	for i, f := range Formats {
		names[i] = string(f)
	}
	return "", fmt.Errorf("unknown format %q (valid: %s)", s, strings.Join(names, ", "))
}

// RenderOptions controls renderer behaviour.
type RenderOptions struct {
	// IncludeHashes, when true, makes the human/table/agent renderers include
	// the truncated prev_hash and entry_hash. JSON/JSONL/XML always include them.
	IncludeHashes bool
}

// Render dispatches to the appropriate per-format renderer.
func Render(entries []Entry, format Format, opts RenderOptions) (string, error) {
	switch format {
	case FormatJSONL:
		return renderJSONL(entries)
	case FormatJSON:
		return renderJSON(entries)
	case FormatXML:
		return renderXML(entries)
	case FormatTable:
		return renderTable(entries, opts), nil
	case FormatAgent:
		return renderAgent(entries, opts), nil
	case FormatHuman, "":
		return renderHuman(entries, opts), nil
	}
	return "", fmt.Errorf("unsupported format %q", format)
}

func renderJSONL(entries []Entry) (string, error) {
	var b strings.Builder
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return "", fmt.Errorf("marshal entry: %w", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func renderJSON(entries []Entry) (string, error) {
	out := map[string]interface{}{
		"count":   len(entries),
		"entries": entries,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal json: %w", err)
	}
	return string(data) + "\n", nil
}

// auditXML is the root element wrapper for XML output.
type auditXML struct {
	XMLName xml.Name `xml:"audit"`
	Count   int      `xml:"count,attr"`
	Entries []Entry  `xml:"entry"`
}

func renderXML(entries []Entry) (string, error) {
	wrap := auditXML{Count: len(entries), Entries: entries}
	data, err := xml.MarshalIndent(wrap, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal xml: %w", err)
	}
	return xml.Header + string(data) + "\n", nil
}

func renderTable(entries []Entry, opts RenderOptions) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	header := "TIMESTAMP\tACTION\tACTOR\tFEATURE_ID\tDETAILS"
	if opts.IncludeHashes {
		header += "\tPREV\tHASH"
	}
	fmt.Fprintln(w, header)
	for _, e := range entries {
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s",
			formatTimestamp(e.Timestamp),
			emptyDash(e.Action),
			emptyDash(e.Actor),
			emptyDash(e.FeatureID),
			emptyDash(e.Details),
		)
		if opts.IncludeHashes {
			row += "\t" + shortHash(e.PrevHash) + "\t" + shortHash(e.EntryHash)
		}
		fmt.Fprintln(w, row)
	}
	_ = w.Flush()
	return b.String()
}

func renderHuman(entries []Entry, opts RenderOptions) string {
	if len(entries) == 0 {
		return "No audit entries match the given filters.\n"
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s  %-10s  %-12s  %-20s  %s",
			formatTimestamp(e.Timestamp),
			emptyDash(e.Action),
			emptyDash(e.Actor),
			emptyDash(e.FeatureID),
			emptyDash(e.Details),
		)
		if opts.IncludeHashes {
			fmt.Fprintf(&b, "  prev=%s hash=%s", shortHash(e.PrevHash), shortHash(e.EntryHash))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func renderAgent(entries []Entry, opts RenderOptions) string {
	if len(entries) == 0 {
		return "count: 0\nentries: []\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "count: %d\n", len(entries))
	for i, e := range entries {
		fmt.Fprintf(&b, "\n--- entry %d ---\n", i+1)
		fmt.Fprintf(&b, "timestamp: %s\n", e.Timestamp)
		fmt.Fprintf(&b, "action: %s\n", e.Action)
		fmt.Fprintf(&b, "actor: %s\n", e.Actor)
		if e.FeatureID != "" {
			fmt.Fprintf(&b, "feature_id: %s\n", e.FeatureID)
		}
		if e.Details != "" {
			fmt.Fprintf(&b, "details: %s\n", e.Details)
		}
		if opts.IncludeHashes {
			fmt.Fprintf(&b, "prev_hash: %s\n", e.PrevHash)
			fmt.Fprintf(&b, "entry_hash: %s\n", e.EntryHash)
		}
	}
	return b.String()
}

// formatTimestamp renders RFC3339 timestamps as "2026-04-16 21:06:59" for
// human-readable formats. Unparsable timestamps pass through unchanged so
// debugging odd entries is still possible.
func formatTimestamp(ts string) string {
	if len(ts) >= 19 && ts[10] == 'T' {
		return ts[:10] + " " + ts[11:19]
	}
	return ts
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func shortHash(h string) string {
	if len(h) == 0 {
		return "-"
	}
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
