package feature

import (
	"strings"
	"testing"
)

const sampleFeature = `---
id: FTR-0001
title: Sample Feature
status: backlog
owner: tester
priority: high
complexity: low
created: 2023-01-01
updated: 2023-01-02
labels:
  - alpha
dependencies:
  - FTR-0002
---

Intro text.

## Summary

Summary details.

## Details

More details.
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

	encoded, err := feat.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if !strings.Contains(string(encoded), "Sample Feature") {
		t.Fatalf("encoded output missing data: %s", string(encoded))
	}

	feat.UpdateTimestamp()
	if feat.FrontMatter.Updated == "" {
		t.Fatalf("update timestamp not applied")
	}

	if feat.StatusDirectory("/root") != "/root/features/backlog" {
		t.Fatalf("unexpected directory")
	}
	feat.FrontMatter.Status = "unknown"
	if feat.StatusDirectory("/root") != "" {
		t.Fatalf("expected empty directory for unknown status")
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

	feat.Body = "Intro\n\n"
	defaults := map[string]string{"Summary": "Default summary"}
	feat.AddMissingSections([]string{"Summary", "Details"}, defaults)
	if !strings.Contains(feat.Body, "Default summary") {
		t.Fatalf("expected default content: %s", feat.Body)
	}
	feat.AddMissingSections([]string{"Summary"}, nil)
}

func TestSetFieldAndHelpers(t *testing.T) {
	feat, err := Parse("/tmp/sample.md", []byte(sampleFeature))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	assignments := map[string]string{
		"id":           "FTR-9999",
		"title":        "New Title",
		"status":       "review",
		"owner":        "owner",
		"priority":     "medium",
		"complexity":   "high",
		"created":      "2024-01-01",
		"updated":      "2024-01-02",
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
