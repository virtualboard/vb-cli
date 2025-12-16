package spec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
)

func setupTestEnv(t *testing.T) (*Manager, string) {
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
    "spec_type": {"type": "string"},
    "title": {"type": "string"},
    "status": {"type": "string"},
    "last_updated": {"type": "string"},
    "applicability": {"type": "array", "items": {"type": "string"}},
    "owner": {"type": "string"},
    "related_initiatives": {"type": "array", "items": {"type": "string"}}
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

	return NewManager(opts), vbDir
}

func TestManagerSpecsDir(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	expected := filepath.Join(vbDir, "specs")
	if mgr.SpecsDir() != expected {
		t.Errorf("expected %s, got %s", expected, mgr.SpecsDir())
	}
}

func TestManagerSchemaPath(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	expected := filepath.Join(vbDir, "schemas", "system-spec.schema.json")
	if mgr.SchemaPath() != expected {
		t.Errorf("expected %s, got %s", expected, mgr.SchemaPath())
	}
}

func TestLoadByName(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	specsDir := filepath.Join(vbDir, "specs")

	content := `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
---

## Stack
Details here.`

	if err := os.WriteFile(filepath.Join(specsDir, "tech-stack.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "with extension",
			input:   "tech-stack.md",
			wantErr: false,
		},
		{
			name:    "without extension",
			input:   "tech-stack",
			wantErr: false,
		},
		{
			name:    "not found",
			input:   "nonexistent.md",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := mgr.LoadByName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), "not found") {
					t.Errorf("expected not found error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec == nil {
				t.Fatal("expected spec, got nil")
			}
			if spec.FrontMatter.SpecType != "tech-stack" {
				t.Errorf("expected spec_type tech-stack, got %s", spec.FrontMatter.SpecType)
			}
		})
	}
}

func TestSave(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	specsDir := filepath.Join(vbDir, "specs")

	spec := &Spec{
		Path: filepath.Join(specsDir, "test-spec.md"),
		FrontMatter: FrontMatter{
			SpecType:      "database-schema",
			Title:         "Test Spec",
			Status:        "draft",
			LastUpdated:   "2024-01-15",
			Applicability: []string{"backend"},
		},
		Body: "## Test\nContent here.",
	}

	if err := mgr.Save(spec); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Verify file exists and can be read back
	loaded, err := mgr.LoadByName("test-spec.md")
	if err != nil {
		t.Fatalf("load after save failed: %v", err)
	}
	if loaded.FrontMatter.Title != "Test Spec" {
		t.Errorf("expected title Test Spec, got %s", loaded.FrontMatter.Title)
	}
}

func TestSaveDryRun(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	specsDir := filepath.Join(vbDir, "specs")

	// Enable dry-run mode
	opts := config.New()
	if err := opts.Init(filepath.Dir(vbDir), false, false, true, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}
	mgr = NewManager(opts)

	spec := &Spec{
		Path: filepath.Join(specsDir, "dry-run-spec.md"),
		FrontMatter: FrontMatter{
			SpecType:      "tech-stack",
			Title:         "Dry Run",
			Status:        "draft",
			LastUpdated:   "2024-01-15",
			Applicability: []string{"backend"},
		},
		Body: "## Test\nContent.",
	}

	if err := mgr.Save(spec); err != nil {
		t.Fatalf("save in dry-run failed: %v", err)
	}

	// File should not exist
	if _, err := os.Stat(spec.Path); !os.IsNotExist(err) {
		t.Error("file should not exist in dry-run mode")
	}
}

func TestList(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	specsDir := filepath.Join(vbDir, "specs")

	// Create test specs
	specs := []struct {
		filename string
		content  string
	}{
		{
			filename: "tech-stack.md",
			content: `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
---

## Stack
Details.`,
		},
		{
			filename: "database-schema.md",
			content: `---
spec_type: database-schema
title: Database Schema
status: draft
last_updated: 2024-01-15
applicability:
  - backend
---

## Schema
Schema details.`,
		},
		{
			filename: "README.md",
			content:  "# This should be skipped",
		},
	}

	for _, spec := range specs {
		if err := os.WriteFile(filepath.Join(specsDir, spec.filename), []byte(spec.content), 0o600); err != nil {
			t.Fatalf("failed to write spec %s: %v", spec.filename, err)
		}
	}

	list, err := mgr.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	// Should have 2 specs (README.md is skipped)
	if len(list) != 2 {
		t.Errorf("expected 2 specs, got %d", len(list))
	}

	// Verify sorting by filename
	if len(list) == 2 {
		if filepath.Base(list[0].Path) != "database-schema.md" {
			t.Errorf("expected first spec to be database-schema.md, got %s", filepath.Base(list[0].Path))
		}
		if filepath.Base(list[1].Path) != "tech-stack.md" {
			t.Errorf("expected second spec to be tech-stack.md, got %s", filepath.Base(list[1].Path))
		}
	}
}

func TestListEmpty(t *testing.T) {
	mgr, _ := setupTestEnv(t)

	list, err := mgr.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	if len(list) != 0 {
		t.Errorf("expected empty list, got %d specs", len(list))
	}
}

func TestListNoSpecsDir(t *testing.T) {
	tmpDir := t.TempDir()
	opts := config.New()
	if err := opts.Init(tmpDir, false, false, false, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}
	mgr := NewManager(opts)

	list, err := mgr.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	if len(list) != 0 {
		t.Errorf("expected empty list when specs dir missing, got %d", len(list))
	}
}

func TestListInvalidFiles(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	specsDir := filepath.Join(vbDir, "specs")

	// Create an invalid spec
	invalid := `---
invalid yaml [
---
Body`
	if err := os.WriteFile(filepath.Join(specsDir, "invalid.md"), []byte(invalid), 0o600); err != nil {
		t.Fatalf("failed to write invalid spec: %v", err)
	}

	_, err := mgr.List()
	if err == nil {
		t.Fatal("expected error for invalid files")
	}

	var invalidErr *InvalidFileError
	if !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("expected InvalidFileError, got %v", err)
	}
	// Try type assertion
	if invalidFileErr, ok := err.(*InvalidFileError); ok {
		invalidErr = invalidFileErr
		if len(invalidErr.Files) != 1 {
			t.Errorf("expected 1 invalid file, got %d", len(invalidErr.Files))
		}
	}
}

func TestLoadByNameWithSubdir(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	specsDir := filepath.Join(vbDir, "specs")

	// Create a spec
	content := `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
---

## Stack
Details.`

	if err := os.WriteFile(filepath.Join(specsDir, "tech-stack.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	// Create a subdirectory (should be skipped)
	subdir := filepath.Join(specsDir, "subdir")
	if err := os.MkdirAll(subdir, 0o750); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "ignored.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write spec in subdir: %v", err)
	}

	// List should only find the top-level spec
	list, err := mgr.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 spec (subdirs should be skipped), got %d", len(list))
	}
}

func TestLoadByNameReadError(t *testing.T) {
	tmpDir := t.TempDir()
	vbDir := filepath.Join(tmpDir, ".virtualboard")
	specsDir := filepath.Join(vbDir, "specs")

	if err := os.MkdirAll(specsDir, 0o750); err != nil {
		t.Fatalf("failed to create specs dir: %v", err)
	}

	opts := config.New()
	if err := opts.Init(tmpDir, false, false, false, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}
	mgr := NewManager(opts)

	// Try to load non-existent file
	_, err := mgr.LoadByName("nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}

func TestListWithNonMarkdownFiles(t *testing.T) {
	mgr, vbDir := setupTestEnv(t)
	specsDir := filepath.Join(vbDir, "specs")

	// Create a valid spec
	content := `---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability:
  - backend
---

## Stack
Details.`

	if err := os.WriteFile(filepath.Join(specsDir, "tech-stack.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write spec: %v", err)
	}

	// Create non-markdown file (should be skipped)
	if err := os.WriteFile(filepath.Join(specsDir, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatalf("failed to write txt file: %v", err)
	}

	list, err := mgr.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 spec (non-md files should be skipped), got %d", len(list))
	}
}

func TestInvalidFileErrorMessage(t *testing.T) {
	err := &InvalidFileError{
		Files: []InvalidFile{
			{Path: "/path/to/file1.md", Reason: "parse error 1"},
			{Path: "/path/to/file2.md", Reason: "parse error 2"},
		},
	}

	msg := err.Error()
	if !strings.Contains(msg, "failed to parse 2 spec file(s)") {
		t.Errorf("expected error message to mention 2 files, got: %s", msg)
	}
	if !strings.Contains(msg, "/path/to/file1.md") {
		t.Errorf("expected error message to contain file1 path, got: %s", msg)
	}
	if !strings.Contains(msg, "/path/to/file2.md") {
		t.Errorf("expected error message to contain file2 path, got: %s", msg)
	}
}
