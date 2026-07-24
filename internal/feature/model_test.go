package feature

import (
	"path/filepath"
	"strings"
	"testing"
)

const sampleFeature = `---
id: FTR-0001
title: Sample Feature
status: backlog
owner: tester
implementation_owner: implementer
priority: high
complexity: S
created: 2023-01-01
updated: 2023-01-02
status_changed: 2023-01-01
labels:
  - alpha
dependencies:
  - FTR-0002
---

Intro text.

<untrusted-content>

## Summary

Summary details.

## Details

More details.

</untrusted-content>
`

func TestParseAndEncode(t *testing.T) {
	feat, err := Parse("/tmp/sample.md", []byte(sampleFeature))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if feat.FrontMatter.ID != "FTR-0001" {
		t.Fatalf("unexpected id: %s", feat.FrontMatter.ID)
	}
	if feat.Body == "" {
		t.Fatalf("expected body content")
	}
	if feat.FrontMatter.ImplementationOwner != "implementer" || feat.FrontMatter.StatusChanged != "2023-01-01" {
		t.Fatalf("lifecycle provenance not parsed: %+v", feat.FrontMatter)
	}

	encoded, err := feat.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !strings.Contains(string(encoded), "Sample Feature") {
		t.Fatalf("encoded output missing data: %s", string(encoded))
	}
	if !strings.Contains(string(encoded), "implementation_owner: implementer") || !strings.Contains(string(encoded), "status_changed:") || !strings.Contains(string(encoded), "2023-01-01") {
		t.Fatalf("encoded output dropped lifecycle provenance: %s", string(encoded))
	}

	feat.UpdateTimestamp()
	if feat.FrontMatter.Updated == "" {
		t.Fatalf("update timestamp not applied")
	}

	if feat.StatusDirectory("/root") != filepath.Join("/root", "features", "backlog") {
		t.Fatalf("unexpected directory")
	}
	feat.FrontMatter.Status = "unknown"
	if feat.StatusDirectory("/root") != "" {
		t.Fatalf("expected empty directory for unknown status")
	}
}

func TestParseAndEncodePreserveCRLFBody(t *testing.T) {
	crlf := strings.ReplaceAll(sampleFeature, "\n", "\r\n")
	_, expectedBody, ok := splitFrontmatter([]byte(crlf))
	if !ok {
		t.Fatal("test fixture frontmatter did not split")
	}
	feat, err := Parse("sample.md", []byte(crlf))
	if err != nil {
		t.Fatalf("parse CRLF feature: %v", err)
	}
	if feat.Body != string(expectedBody) {
		t.Fatal("parse changed CRLF body bytes")
	}
	encoded, err := feat.Encode()
	if err != nil {
		t.Fatal(err)
	}
	_, encodedBody, ok := splitFrontmatter(encoded)
	if !ok || string(encodedBody) != string(expectedBody) {
		t.Fatal("encode changed CRLF body bytes")
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse("/tmp/sample.md", []byte("no frontmatter")); err == nil {
		t.Fatalf("expected parse error")
	}

	content := strings.Replace(sampleFeature, "id: FTR-0001", "id: [", 1)
	if _, err := Parse("/tmp/sample.md", []byte(content)); err == nil {
		t.Fatalf("expected yaml parse error")
	}

	unknown := strings.Replace(sampleFeature, "title: Sample Feature", "title: Sample Feature\nunknown_contract_field: must-not-be-dropped", 1)
	if _, err := Parse("/tmp/sample.md", []byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown_contract_field") {
		t.Fatalf("unknown frontmatter key was silently pruned: %v", err)
	}
}

func TestSetSectionAndAddMissingSections(t *testing.T) {
	feat, err := Parse("/tmp/sample.md", []byte(sampleFeature))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if err := feat.SetSection("Summary", "Updated summary"); err != nil {
		t.Fatalf("set section failed: %v", err)
	}
	if !strings.Contains(feat.Body, "Updated summary") {
		t.Fatalf("body not updated")
	}
	if err := feat.SetSection("Missing", "content"); err == nil {
		t.Fatalf("expected error for missing section")
	}

	feat.Body = "Intro\n\n<untrusted-content>\n\n## Summary\nExisting summary.\n\n</untrusted-content>\n"
	defaults := map[string]string{"Problem Statement": "Default problem statement"}
	if err := feat.AddMissingSections([]string{"Summary", "Problem Statement"}, defaults); err != nil {
		t.Fatalf("add missing sections failed: %v", err)
	}
	if !strings.Contains(feat.Body, "Default problem statement") {
		t.Fatalf("expected default content: %s", feat.Body)
	}
	if err := feat.AddMissingSections([]string{"Summary"}, nil); err != nil {
		t.Fatalf("idempotent add failed: %v", err)
	}
}

func TestSetFieldAndHelpers(t *testing.T) {
	feat, err := Parse("/tmp/sample.md", []byte(sampleFeature))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	assignments := map[string]string{
		"title":        "New Title",
		"priority":     "medium",
		"complexity":   "L",
		"epic":         "Epic",
		"risk_notes":   "Risks",
		"labels":       "one, two",
		"dependencies": "FTR-1\nFTR-2",
	}
	for key, value := range assignments {
		if err := feat.SetField(key, value); err != nil {
			t.Fatalf("set field %s failed: %v", key, err)
		}
	}
	for _, key := range []string{"id", "status", "owner", "created", "updated", "implementation_owner", "status_changed"} {
		if err := feat.SetField(key, "managed-value"); err == nil {
			t.Fatalf("managed field %s was mutable", key)
		}
	}
	if err := feat.SetField("unknown", "value"); err == nil {
		t.Fatalf("expected error for unknown field")
	}
	if got := feat.LabelsAsYAML(); !strings.Contains(got, "\"one\"") {
		t.Fatalf("unexpected labels yaml: %s", got)
	}
	feat.FrontMatter.Labels = nil
	if feat.LabelsAsYAML() != "[]" {
		t.Fatalf("expected empty list representation")
	}

	if list := splitList("a, b\nc"); len(list) != 3 {
		t.Fatalf("unexpected split list: %#v", list)
	}
	if list := splitList("   "); len(list) != 0 {
		t.Fatalf("expected empty list for blanks: %#v", list)
	}
}

func TestOptionalFieldsOmitted(t *testing.T) {
	// Feature without epic and risk_notes fields
	minimalFeature := `---
id: FTR-0003
title: Minimal Feature
status: backlog
owner: ""
priority: low
complexity: XS
created: 2023-01-01
updated: 2023-01-01
labels: []
dependencies: []
---

## Summary

Minimal feature without optional fields.
`
	feat, err := Parse("/tmp/minimal.md", []byte(minimalFeature))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	// Verify epic and risk_notes are empty strings (zero values)
	if feat.FrontMatter.Epic != "" {
		t.Fatalf("expected empty epic, got: %s", feat.FrontMatter.Epic)
	}
	if feat.FrontMatter.RiskNotes != "" {
		t.Fatalf("expected empty risk_notes, got: %s", feat.FrontMatter.RiskNotes)
	}

	// Re-encode and verify only truly optional fields are omitted.
	encoded, err := feat.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	encodedStr := string(encoded)
	if strings.Contains(encodedStr, "epic:") {
		t.Fatalf("expected epic field to be omitted from YAML when empty, got: %s", encodedStr)
	}
	for _, required := range []string{"implementation_owner:", "status_changed:", "risk_notes:"} {
		if !strings.Contains(encodedStr, required) {
			t.Fatalf("expected required field %s in YAML, got: %s", required, encodedStr)
		}
	}

	// Verify required fields are still present
	if !strings.Contains(encodedStr, "id: FTR-0003") {
		t.Fatalf("expected id field in encoded output")
	}
	if !strings.Contains(encodedStr, "title: Minimal Feature") {
		t.Fatalf("expected title field in encoded output")
	}
}
