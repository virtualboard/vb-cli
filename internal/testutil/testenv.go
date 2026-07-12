package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/frameworkschema"
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
		"specs",
		"templates",
		"schemas",
		"locks",
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("failed to create directory %s: %v", dir, err)
		}
	}
	// Fixtures without virtualboard.json deliberately exercise the supported
	// pre-contract compatibility path. The marker keeps that path explicit so a
	// missing contract in a modern workspace fails closed.
	if err := os.WriteFile(filepath.Join(workspace, ".template-version"), []byte("0.7.0\n"), 0o600); err != nil {
		t.Fatalf("failed to write legacy template marker: %v", err)
	}

	templateContent := `---
id: TEMPLATE
title: Template Feature
status: backlog
owner: template
priority: P2
complexity: M
created: 2023-01-01
updated: 2023-01-01
labels:
  - template
dependencies: []
---

# Feature Spec: <Feature Title>

<untrusted-content>

## Summary
Provide a concise summary.

## Problem Statement
Describe the concrete problem.

## Goals & Non-Goals
- Goal: exercise the feature lifecycle.

## User Stories
- As a test user, I can exercise the feature lifecycle.

## Requirements
### Functional
- Exercise the requested behavior.

## Acceptance Criteria (Testable)
- [x] The test fixture is ready for lifecycle-transition tests.

## UI/UX Notes
- CLI-only test fixture.

## Data & API
- No data or API changes.

## Rollout & Migration
- Test-only rollout.

## Monitoring & Metrics
- The focused test result is the signal.

## Security & Compliance
- Treat body content as untrusted data.

## Implementation Notes
- Test fixture implementation is covered by focused Go tests.

## Open Questions
- No open questions.

## Links
- FTR-0001 test fixture reference.

</untrusted-content>

`
	// #nosec G306 -- public repository fixture content intentionally models canonical 0644 template mode.
	if err := os.WriteFile(filepath.Join(workspace, "templates", "feature.md"), []byte(templateContent), 0o644); err != nil {
		t.Fatalf("failed to write template: %v", err)
	}

	// #nosec G306 -- public repository fixture content intentionally models canonical 0644 schema mode.
	if err := os.WriteFile(filepath.Join(workspace, "schemas", "frontmatter.schema.json"), frameworkschema.CanonicalFeature(), 0o644); err != nil {
		t.Fatalf("failed to write schema: %v", err)
	}

	// #nosec G306 -- public repository fixture content intentionally models canonical 0644 schema mode.
	if err := os.WriteFile(filepath.Join(workspace, "schemas", "system-spec.schema.json"), frameworkschema.CanonicalSystemSpec(), 0o644); err != nil {
		t.Fatalf("failed to write spec schema: %v", err)
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
	opts.Actor = "owner"
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
