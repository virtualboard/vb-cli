package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
)

// Fixture provides a temporary workspace with the directory structure expected by the CLI.
type Fixture struct {
	Root string
}

// NewFixture initialises a new test workspace with template and schema files.
func NewFixture(t *testing.T) *Fixture {
	t.Helper()
	root := t.TempDir()

	workspace := filepath.Join(root, ".virtualboard")
	dirs := []string{
		"features/backlog",
		"features/in-progress",
		"features/blocked",
		"features/review",
		"features/done",
		"templates",
		"schemas",
		"locks",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
	}

	templateContent := `---
id: TEMPLATE
title: Template Feature
status: backlog
owner: template
priority: medium
complexity: medium
created: 2023-01-01
updated: 2023-01-01
labels:
  - template
dependencies: []
---

## Summary

Provide a concise summary.

## Details

Additional details.

`
	if err := os.WriteFile(filepath.Join(workspace, "templates", "spec.md"), []byte(templateContent), 0o600); err != nil {
		t.Fatalf("failed to write template: %v", err)
	}

	schema := `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "required": ["ID", "Title", "Status", "Created", "Updated"],
  "properties": {
    "ID": {"type": "string"},
    "Title": {"type": "string"},
    "Status": {"type": "string"},
    "Owner": {"type": "string"},
    "Priority": {"type": "string"},
    "Complexity": {"type": "string"},
    "Created": {"type": "string"},
    "Updated": {"type": "string"},
    "Labels": {"type": "array", "items": {"type": "string"}},
    "Dependencies": {"type": "array", "items": {"type": "string"}}
  },
  "additionalProperties": true
}`
	if err := os.WriteFile(filepath.Join(workspace, "schemas", "frontmatter.schema.json"), []byte(schema), 0o600); err != nil {
		t.Fatalf("failed to write schema: %v", err)
	}

	return &Fixture{Root: root}
}

// Options returns cli options initialised for the fixture.
func (f *Fixture) Options(t *testing.T, jsonOut, verbose, dry bool) *config.Options {
	t.Helper()
	opts := config.New()
	if err := opts.Init(f.Root, jsonOut, verbose, dry, ""); err != nil {
		t.Fatalf("failed to init options: %v", err)
	}
	return opts
}

// WriteFile writes a file relative to the fixture root.
func (f *Fixture) WriteFile(t *testing.T, relative string, data []byte) {
	t.Helper()
	workspace := filepath.Join(f.Root, ".virtualboard")
	path := filepath.Join(workspace, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
}

// Path resolves a path relative to the fixture root.
func (f *Fixture) Path(parts ...string) string {
	workspace := filepath.Join(f.Root, ".virtualboard")
	return filepath.Join(append([]string{workspace}, parts...)...)
}
