package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
)

func setupValidatorTest(t *testing.T) (*Manager, *Validator, string) {
	t.Helper()
	tmpDir := t.TempDir()
	vbDir := filepath.Join(tmpDir, ".virtualboard")
	specsDir := filepath.Join(vbDir, "specs")
	schemasDir := filepath.Join(vbDir, "schemas")

	if err := os.MkdirAll(specsDir, 0o750); err != nil {
		t.Fatalf("failed to create specs dir: %v", err)
	}
	if err := os.MkdirAll(schemasDir, 0o750); err != nil {
		t.Fatalf("failed to create schemas dir: %v", err)
	}

	schema := `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["spec_type", "title", "status", "last_updated", "applicability"],
  "properties": {
    "spec_type": {
      "type": "string",
      "enum": [
        "tech-stack",
        "local-development",
        "hosting-and-infrastructure",
        "ci-cd-pipeline",
        "database-schema",
        "caching-and-performance",
        "security-and-compliance",
        "observability-and-incident-response"
      ]
    },
    "title": {
      "type": "string",
      "minLength": 3
    },
    "status": {
      "type": "string",
      "enum": ["draft", "approved", "deprecated"]
    },
    "last_updated": {
      "type": "string",
      "pattern": "^[0-9]{4}-[0-9]{2}-[0-9]{2}$"
    },
    "applicability": {
      "type": "array",
      "items": {
        "type": "string",
        "pattern": "^[a-z0-9-]+$"
      },
      "minItems": 1,
      "uniqueItems": true
    },
    "owner": {
      "type": "string"
    },
    "related_initiatives": {
      "type": "array",
      "items": {
        "type": "string",
        "pattern": "^[A-Za-z0-9_-]+$"
      }
    }
  },
  "additionalProperties": false
}`
	if err := os.WriteFile(filepath.Join(schemasDir, "system-spec.schema.json"), []byte(schema), 0o600); err != nil {
		t.Fatalf("failed to write schema: %v", err)
	}

	opts := config.New()
	if err := opts.Init(tmpDir, false, false, false, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}

	mgr := NewManager(opts)
	validator, err := NewValidator(opts, mgr)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	return mgr, validator, vbDir
}

func TestValidateSingleValid(t *testing.T) {
	mgr, validator, vbDir := setupValidatorTest(t)
	specsDir := filepath.Join(vbDir, "specs")

	content := `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
  - frontend
owner: platform-team
---

## Stack
Details.`

	if err := os.WriteFile(filepath.Join(specsDir, "tech-stack.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	spec, err := mgr.LoadByName("tech-stack.md")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	result := validator.validateSingle(spec)
	if len(result.Errors) > 0 {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateSingleInvalidStatus(t *testing.T) {
	_, validator, _ := setupValidatorTest(t)

	spec := &Spec{
		Path: "test.md",
		FrontMatter: FrontMatter{
			SpecType:      "tech-stack",
			Title:         "Test",
			Status:        "invalid-status",
			LastUpdated:   "2024-01-15",
			Applicability: []string{"backend"},
		},
	}

	result := validator.validateSingle(spec)
	if len(result.Errors) == 0 {
		t.Error("expected errors for invalid status")
	}

	found := false
	for _, err := range result.Errors {
		if strings.Contains(err, "status") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected status validation error")
	}
}

func TestValidateSingleInvalidDate(t *testing.T) {
	_, validator, _ := setupValidatorTest(t)

	spec := &Spec{
		Path: "test.md",
		FrontMatter: FrontMatter{
			SpecType:      "tech-stack",
			Title:         "Test",
			Status:        "draft",
			LastUpdated:   "invalid-date",
			Applicability: []string{"backend"},
		},
	}

	result := validator.validateSingle(spec)
	if len(result.Errors) == 0 {
		t.Error("expected errors for invalid date")
	}

	found := false
	for _, err := range result.Errors {
		if strings.Contains(err, "last_updated") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected date validation error")
	}
}

func TestValidateSingleInvalidSpecType(t *testing.T) {
	_, validator, _ := setupValidatorTest(t)

	spec := &Spec{
		Path: "test.md",
		FrontMatter: FrontMatter{
			SpecType:      "unknown-type",
			Title:         "Test",
			Status:        "draft",
			LastUpdated:   "2024-01-15",
			Applicability: []string{"backend"},
		},
	}

	result := validator.validateSingle(spec)
	if len(result.Errors) == 0 {
		t.Error("expected errors for invalid spec type")
	}

	found := false
	for _, err := range result.Errors {
		if strings.Contains(err, "spec_type") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected spec_type validation error")
	}
}

func TestValidateSingleMissingApplicability(t *testing.T) {
	_, validator, _ := setupValidatorTest(t)

	spec := &Spec{
		Path: "test.md",
		FrontMatter: FrontMatter{
			SpecType:      "tech-stack",
			Title:         "Test",
			Status:        "draft",
			LastUpdated:   "2024-01-15",
			Applicability: []string{},
		},
	}

	result := validator.validateSingle(spec)
	if len(result.Errors) == 0 {
		t.Error("expected errors for missing applicability")
	}

	found := false
	for _, err := range result.Errors {
		if strings.Contains(err, "applicability") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected applicability validation error")
	}
}

func TestValidateAll(t *testing.T) {
	_, validator, vbDir := setupValidatorTest(t)
	specsDir := filepath.Join(vbDir, "specs")

	validSpec := `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
---

## Stack
Details.`

	invalidSpec := `---
spec_type: tech-stack
title: Invalid Spec
status: invalid-status
last_updated: 2024-01-15
applicability:
  - backend
---

## Content
Details.`

	if err := os.WriteFile(filepath.Join(specsDir, "valid.md"), []byte(validSpec), 0o600); err != nil {
		t.Fatalf("failed to write valid spec: %v", err)
	}

	if err := os.WriteFile(filepath.Join(specsDir, "invalid.md"), []byte(invalidSpec), 0o600); err != nil {
		t.Fatalf("failed to write invalid spec: %v", err)
	}

	summary, err := validator.ValidateAll()
	if err != nil {
		t.Fatalf("validate all failed: %v", err)
	}

	if summary.Total != 2 {
		t.Errorf("expected 2 total, got %d", summary.Total)
	}
	if summary.Valid != 1 {
		t.Errorf("expected 1 valid, got %d", summary.Valid)
	}
	if summary.Invalid != 1 {
		t.Errorf("expected 1 invalid, got %d", summary.Invalid)
	}
	if !summary.HasErrors() {
		t.Error("expected summary to have errors")
	}

	if len(summary.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(summary.Results))
	}
}

func TestValidateName(t *testing.T) {
	_, validator, vbDir := setupValidatorTest(t)
	specsDir := filepath.Join(vbDir, "specs")

	content := `---
spec_type: database-schema
title: Database Schema
status: draft
last_updated: 2024-01-15
applicability:
  - backend
---

## Schema
Details.`

	if err := os.WriteFile(filepath.Join(specsDir, "database-schema.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	result, err := validator.ValidateName("database-schema.md")
	if err != nil {
		t.Fatalf("validate name failed: %v", err)
	}

	if len(result.Errors) > 0 {
		t.Errorf("expected no errors, got: %v", result.Errors)
	}
}

func TestValidateNameNotFound(t *testing.T) {
	_, validator, _ := setupValidatorTest(t)

	_, err := validator.ValidateName("nonexistent.md")
	if err == nil {
		t.Fatal("expected error for nonexistent spec")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got %v", err)
	}
}

func TestSummaryHasErrors(t *testing.T) {
	tests := []struct {
		name     string
		summary  *Summary
		expected bool
	}{
		{
			name: "no errors",
			summary: &Summary{
				Total:   2,
				Valid:   2,
				Invalid: 0,
			},
			expected: false,
		},
		{
			name: "has errors",
			summary: &Summary{
				Total:   2,
				Valid:   1,
				Invalid: 1,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.summary.HasErrors()
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestSummaryError(t *testing.T) {
	tests := []struct {
		name        string
		summary     *Summary
		expectError bool
	}{
		{
			name: "no errors",
			summary: &Summary{
				Total:      2,
				Valid:      2,
				Invalid:    0,
				ErrorCount: map[string]int{},
			},
			expectError: false,
		},
		{
			name: "has errors",
			summary: &Summary{
				Total:   2,
				Valid:   1,
				Invalid: 1,
				ErrorCount: map[string]int{
					"invalid.md": 2,
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.summary.Error()
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestValidateAllEmpty(t *testing.T) {
	_, validator, _ := setupValidatorTest(t)

	summary, err := validator.ValidateAll()
	if err != nil {
		t.Fatalf("validate all failed: %v", err)
	}

	if summary.Total != 0 {
		t.Errorf("expected 0 total, got %d", summary.Total)
	}
	if summary.HasErrors() {
		t.Error("empty validation should have no errors")
	}
}

func TestValidateSingleAllErrors(t *testing.T) {
	_, validator, _ := setupValidatorTest(t)

	// Create spec with all possible errors
	spec := &Spec{
		Path: "test.md",
		FrontMatter: FrontMatter{
			SpecType:      "unknown-type",
			Title:         "Te", // Too short
			Status:        "invalid-status",
			LastUpdated:   "bad-date",
			Applicability: []string{}, // Empty
		},
	}

	result := validator.validateSingle(spec)
	if len(result.Errors) == 0 {
		t.Error("expected multiple errors")
	}

	// Should have errors for: title length, spec_type, status, date, applicability
	if len(result.Errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}
