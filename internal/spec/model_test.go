package spec

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		errType error
	}{
		{
			name: "valid spec",
			input: `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
  - frontend
owner: platform-team
related_initiatives:
  - INIT-001
---

## Overview
This is the tech stack specification.`,
			wantErr: false,
		},
		{
			name: "minimal valid spec",
			input: `---
spec_type: database-schema
title: Database Schema
status: draft
last_updated: 2024-01-15
applicability:
  - backend
---

## Schema
Database schema details.`,
			wantErr: false,
		},
		{
			name:    "no frontmatter",
			input:   "Just some content without frontmatter",
			wantErr: true,
			errType: ErrNoFrontmatter,
		},
		{
			name: "unclosed frontmatter",
			input: `---
spec_type: tech-stack
title: Test
`,
			wantErr: true,
			errType: ErrNoFrontmatter,
		},
		{
			name: "invalid yaml",
			input: `---
spec_type: tech-stack
  invalid: [yaml
---
Body`,
			wantErr: true,
			errType: ErrInvalidFrontmatter,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := Parse("test.md", []byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errType != nil && !strings.Contains(err.Error(), tt.errType.Error()) {
					t.Errorf("expected error containing %v, got %v", tt.errType, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec == nil {
				t.Fatal("expected spec, got nil")
			}
			if spec.Path != "test.md" {
				t.Errorf("expected path test.md, got %s", spec.Path)
			}
		})
	}
}

func TestEncode(t *testing.T) {
	original := `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
  - frontend
owner: platform-team
related_initiatives:
  - INIT-001
---

## Overview
This is the tech stack specification.`

	spec, err := Parse("test.md", []byte(original))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	encoded, err := spec.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	// Parse the encoded version to ensure it's valid
	reparsed, err := Parse("test.md", encoded)
	if err != nil {
		t.Fatalf("reparse failed: %v", err)
	}

	// Verify key fields
	if reparsed.FrontMatter.SpecType != "tech-stack" {
		t.Errorf("expected spec_type tech-stack, got %s", reparsed.FrontMatter.SpecType)
	}
	if reparsed.FrontMatter.Title != "Technology Stack" {
		t.Errorf("expected title Technology Stack, got %s", reparsed.FrontMatter.Title)
	}
	if reparsed.FrontMatter.Status != "approved" {
		t.Errorf("expected status approved, got %s", reparsed.FrontMatter.Status)
	}
	if len(reparsed.FrontMatter.Applicability) != 2 {
		t.Errorf("expected 2 applicability entries, got %d", len(reparsed.FrontMatter.Applicability))
	}
}

func TestParseExtractsBody(t *testing.T) {
	input := `---
spec_type: database-schema
title: Database Schema
status: draft
last_updated: 2024-01-15
applicability:
  - backend
---

## Schema
Database schema details.

## Migrations
Migration details.`

	spec, err := Parse("test.md", []byte(input))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if !strings.Contains(spec.Body, "## Schema") {
		t.Error("body missing Schema section")
	}
	if !strings.Contains(spec.Body, "## Migrations") {
		t.Error("body missing Migrations section")
	}
	// Body should not contain frontmatter delimiters
	if strings.Contains(spec.Body, "---") {
		t.Error("body should not contain frontmatter delimiters")
	}
}

func TestUpdateTimestamp(t *testing.T) {
	spec := &Spec{
		FrontMatter: FrontMatter{
			LastUpdated: "2024-01-01",
		},
	}

	spec.UpdateTimestamp()

	// UpdateTimestamp is intentionally a no-op for explicit control
	if spec.FrontMatter.LastUpdated != "2024-01-01" {
		t.Errorf("timestamp should not change, got %s", spec.FrontMatter.LastUpdated)
	}
}

func TestParseTooShort(t *testing.T) {
	input := `---
title: Too Short`

	_, err := Parse("test.md", []byte(input))
	if err == nil || err != ErrNoFrontmatter {
		t.Errorf("expected ErrNoFrontmatter for too short content, got: %v", err)
	}
}

func TestParseNoEndDelimiter(t *testing.T) {
	input := `---
spec_type: tech-stack
title: Test
status: draft
last_updated: 2024-01-15
applicability:
  - backend`

	_, err := Parse("test.md", []byte(input))
	if err == nil || err != ErrNoFrontmatter {
		t.Errorf("expected ErrNoFrontmatter when end delimiter missing, got: %v", err)
	}
}

func TestEncodeError(t *testing.T) {
	// Create a spec with a field that cannot be marshaled
	spec := &Spec{
		Path: "test.md",
		FrontMatter: FrontMatter{
			SpecType:      "tech-stack",
			Title:         "Test",
			Status:        "draft",
			LastUpdated:   "2024-01-15",
			Applicability: []string{"backend"},
		},
		Body: "## Test\nContent.",
	}

	// Normal encode should succeed
	_, err := spec.Encode()
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
}

func TestParsePreservesEmptyBody(t *testing.T) {
	input := `---
spec_type: tech-stack
title: Empty Body
status: draft
last_updated: 2024-01-15
applicability:
  - backend
---`

	spec, err := Parse("test.md", []byte(input))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if spec.Body != "" {
		t.Errorf("expected empty body, got: %q", spec.Body)
	}
}

func TestParseTrimsLeadingNewlines(t *testing.T) {
	input := `---
spec_type: tech-stack
title: Test
status: draft
last_updated: 2024-01-15
applicability:
  - backend
---


## Content
After newlines.`

	spec, err := Parse("test.md", []byte(input))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if strings.HasPrefix(spec.Body, "\n") {
		t.Error("body should not have leading newlines")
	}
	if !strings.Contains(spec.Body, "## Content") {
		t.Error("body missing content")
	}
}
