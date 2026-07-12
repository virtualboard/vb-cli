package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/frameworkschema"
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
	writeSpecTestContract(t, vbDir)

	if err := os.WriteFile(filepath.Join(schemasDir, "system-spec.schema.json"), frameworkschema.CanonicalSystemSpec(), 0o600); err != nil {
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
` + approvedSpecBody("tech-stack")

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
` + approvedSpecBody("tech-stack")

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

func TestApprovedBodiesRequireTypeAppropriateContent(t *testing.T) {
	_, validator, vbDir := setupValidatorTest(t)
	for specType := range approvedBodySections {
		t.Run(specType, func(t *testing.T) {
			spec := validSpecForValidation(filepath.Join(vbDir, "specs", specType+".md"), specType, "approved", approvedSpecBody(specType))
			result := validator.validateSingle(spec)
			if len(result.Errors) != 0 {
				t.Fatalf("meaningful approved %s body rejected: %v", specType, result.Errors)
			}
		})
	}

	t.Run("missing required section", func(t *testing.T) {
		body := strings.Replace(approvedSpecBody("tech-stack"), "## Architecture Overview\n", "## Other Architecture\n", 1)
		spec := validSpecForValidation(filepath.Join(vbDir, "specs", "missing.md"), "tech-stack", "approved", body)
		result := validator.validateSingle(spec)
		if !specErrorsContain(result.Errors, `requires section "Architecture Overview"`) {
			t.Fatalf("missing type-specific section accepted: %v", result.Errors)
		}
	})

	t.Run("placeholder section", func(t *testing.T) {
		body := approvedSpecBody("database-schema")
		body = replaceSpecSectionContent(body, "Schema Summary", "TODO: describe the schema later.")
		spec := validSpecForValidation(filepath.Join(vbDir, "specs", "placeholder.md"), "database-schema", "approved", body)
		result := validator.validateSingle(spec)
		if !specErrorsContain(result.Errors, `section "Schema Summary" must contain a meaningful decision or instruction`) {
			t.Fatalf("placeholder approved section accepted: %v", result.Errors)
		}
	})

	t.Run("operationally sparse", func(t *testing.T) {
		var body strings.Builder
		for _, heading := range approvedBodySections["observability-and-incident-response"] {
			fmt.Fprintf(&body, "## %s\nDecision is concrete.\n\n", heading)
		}
		spec := validSpecForValidation(filepath.Join(vbDir, "specs", "sparse.md"), "observability-and-incident-response", "approved", body.String())
		result := validator.validateSingle(spec)
		if !specErrorsContain(result.Errors, "body is too sparse to establish an operational decision") {
			t.Fatalf("operationally sparse approved body accepted: %v", result.Errors)
		}
	})

	t.Run("heading inside code fence", func(t *testing.T) {
		body := strings.Replace(approvedSpecBody("security-and-compliance"), "## Threat Modeling\n", "```markdown\n## Threat Modeling\n```\n", 1)
		spec := validSpecForValidation(filepath.Join(vbDir, "specs", "fenced.md"), "security-and-compliance", "approved", body)
		result := validator.validateSingle(spec)
		if !specErrorsContain(result.Errors, `requires section "Threat Modeling"`) {
			t.Fatalf("fenced pseudo-heading satisfied approved body contract: %v", result.Errors)
		}
	})

	t.Run("duplicate and out of order sections", func(t *testing.T) {
		body := approvedSpecBody("ci-cd-pipeline")
		body += "\n## Purpose\nA duplicate purpose decision is deliberately present.\n"
		body = strings.Replace(body,
			"## Pipeline Overview\nThe approved Pipeline Overview decision is concrete, owned, testable, and operationally verified.\n\n## Triggers & Branching Strategy",
			"## Triggers & Branching Strategy\nThe approved Triggers & Branching Strategy decision is concrete, owned, testable, and operationally verified.\n\n## Pipeline Overview",
			1)
		spec := validSpecForValidation(filepath.Join(vbDir, "specs", "ordering.md"), "ci-cd-pipeline", "approved", body)
		result := validator.validateSingle(spec)
		if !specErrorsContain(result.Errors, `section "Purpose" appears 2 times`) || !specErrorsContain(result.Errors, "out of canonical order") {
			t.Fatalf("duplicate/out-of-order approved sections accepted: %v", result.Errors)
		}
	})

	t.Run("draft is permissive", func(t *testing.T) {
		spec := validSpecForValidation(filepath.Join(vbDir, "specs", "draft.md"), "tech-stack", "draft", "TODO")
		result := validator.validateSingle(spec)
		if len(result.Errors) != 0 {
			t.Fatalf("draft body should remain permissive: %v", result.Errors)
		}
	})
}

func TestSpecValidationRejectsFutureDates(t *testing.T) {
	_, validator, vbDir := setupValidatorTest(t)
	spec := validSpecForValidation(filepath.Join(vbDir, "specs", "future.md"), "tech-stack", "draft", "TODO")
	spec.FrontMatter.LastUpdated = "2999-01-01"
	result := validator.validateSingle(spec)
	if !specErrorsContain(result.Errors, "last_updated cannot be in the future") {
		t.Fatalf("future last_updated accepted: %v", result.Errors)
	}
}

func TestSpecInternalLinksRequireWorkspaceContainment(t *testing.T) {
	_, validator, vbDir := setupValidatorTest(t)
	specsDir := filepath.Join(vbDir, "specs")
	contained := filepath.Join(vbDir, "docs", "contained.md")
	if err := os.MkdirAll(filepath.Dir(contained), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(contained, []byte("# Contained\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("# Outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(specsDir, "outside-link.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(contained, filepath.Join(specsDir, "contained-link.md")); err != nil {
		t.Fatal(err)
	}
	spec := validSpecForValidation(filepath.Join(specsDir, "links.md"), "tech-stack", "draft", strings.Join([]string{
		"[contained](../docs/contained.md)",
		"[lexical escape](../../outside.md)",
		"[symlink escape](outside-link.md)",
		"[file scheme](file:///etc/passwd)",
		"[drive escape](C:/outside.md)",
		`[UNC escape](\\server\share\outside.md)`,
		`[rooted backslash escape](\outside.md)`,
		"[outer [nested label]](../../outside-nested-label.md)",
		"[balanced parentheses](folder(name)/../../../outside-balanced-parentheses.md)",
		`[escaped separators](..\/..\/outside-escaped-separators.md)`,
		"[entity separators](..&#47;..&#47;outside-entity-separators.md)",
		"[contained symlink](contained-link.md)",
		"[anchor](#section)",
		"[mail](mailto:owner@example.invalid)",
		"[external](https://example.com)",
	}, " ")+"\n[reference escape]: ../../outside-reference.md\n[outer [nested reference]]: ../../outside-nested-reference.md\n[reference anchor]: #section\n[reference external]: https://example.com/reference\n`[inline code](../../outside-inline-code.md)`\n```markdown\n[fenced code](../../outside-fenced-code.md)\n[fenced reference]: ../../outside-fenced-reference.md\n```\n\\`[escaped backticks do not hide a link](../../outside-escaped-backtick.md)\\`\n    ```\n[indented pseudo-fence does not hide a link](../../outside-indented-fence.md)\n```bad`info\n[invalid pseudo-fence does not hide a link](../../outside-invalid-fence.md)\n")
	result := validator.validateSingle(spec)
	for _, want := range []string{
		"internal link escapes workspace: ../../outside.md",
		"internal link resolves outside workspace: outside-link.md",
		"local file link scheme is not allowed: file:///etc/passwd",
		"internal link escapes workspace: C:/outside.md",
		`internal link escapes workspace: \server\share\outside.md`,
		`internal link escapes workspace: \outside.md`,
		"internal link escapes workspace: ../../outside-nested-label.md",
		"internal link escapes workspace: folder(name)/../../../outside-balanced-parentheses.md",
		"internal link escapes workspace: ../../outside-escaped-separators.md",
		"internal link escapes workspace: ../../outside-entity-separators.md",
		"internal link escapes workspace: ../../outside-reference.md",
		"internal link escapes workspace: ../../outside-nested-reference.md",
		"internal link escapes workspace: ../../outside-escaped-backtick.md",
		"internal link escapes workspace: ../../outside-indented-fence.md",
		"internal link escapes workspace: ../../outside-invalid-fence.md",
	} {
		if !specErrorsContain(result.Errors, want) {
			t.Fatalf("spec link errors = %v, want %q", result.Errors, want)
		}
	}
	for _, allowed := range []string{"contained.md", "contained-link.md", "#section", "mailto:", "https://", "outside-inline-code.md", "outside-fenced-code.md", "outside-fenced-reference.md"} {
		if specErrorsContain(result.Errors, allowed) {
			t.Fatalf("allowed spec link %q was rejected: %v", allowed, result.Errors)
		}
	}
}

func TestSpecInternalLinkValidationIsBounded(t *testing.T) {
	_, validator, vbDir := setupValidatorTest(t)
	spec := validSpecForValidation(filepath.Join(vbDir, "specs", "links.md"), "tech-stack", "draft", strings.Repeat("[anchor](#section)\n", maxSpecMarkdownLinks+1))
	result := validator.validateSingle(spec)
	if !specErrorsContain(result.Errors, "more than 1024 Markdown links") {
		t.Fatalf("unbounded spec link inventory was accepted: %v", result.Errors)
	}
}

func approvedSpecBody(specType string) string {
	var body strings.Builder
	for _, heading := range approvedBodySections[specType] {
		fmt.Fprintf(&body, "\n## %s\nThe approved %s decision is concrete, owned, testable, and operationally verified.\n", heading, heading)
	}
	return body.String()
}

func replaceSpecSectionContent(body, heading, replacement string) string {
	start := strings.Index(body, "## "+heading+"\n")
	if start < 0 {
		return body
	}
	contentStart := start + len("## "+heading+"\n")
	next := strings.Index(body[contentStart:], "\n## ")
	if next < 0 {
		return body[:contentStart] + replacement + "\n"
	}
	return body[:contentStart] + replacement + body[contentStart+next:]
}

func validSpecForValidation(path, specType, status, body string) *Spec {
	return &Spec{
		Path: path,
		FrontMatter: FrontMatter{
			SpecType:      specType,
			Title:         "Validated System Specification",
			Status:        status,
			LastUpdated:   "2024-01-15",
			Applicability: []string{"backend"},
			Owner:         "platform-team",
		},
		Body: body,
	}
}

func specErrorsContain(errors []string, substring string) bool {
	for _, validationError := range errors {
		if strings.Contains(validationError, substring) {
			return true
		}
	}
	return false
}
