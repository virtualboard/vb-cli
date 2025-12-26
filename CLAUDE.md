# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**vb-cli** is a Go-based CLI tool for managing VirtualBoard feature specifications. It provides commands to scaffold, validate, move, and index feature specs that live under `.virtualboard/` directories in repositories.

- **Language**: Go 1.25+
- **CLI Framework**: [spf13/cobra](https://github.com/spf13/cobra)
- **Key Dependencies**:
  - `gojsonschema` - JSON schema validation
  - `sirupsen/logrus` - structured logging
  - `gopkg.in/yaml.v3` - YAML parsing for frontmatter
  - `google/go-github` - GitHub API for upgrades

## Development Commands

### Building & Testing

```bash
# Compile all packages
make build

# Run tests with enforced 100% coverage (REQUIRED)
make test

# Generate HTML coverage report
make coverage

# Run security scanning with gosec (REQUIRED)
make scan

# Build binary to dist/vb
make package

# Run all pre-commit checks
make pre-commit
```

### Test Execution

```bash
# Run all tests with coverage
go test ./... -coverprofile=coverage.out

# Run specific package tests
go test ./cmd/...
go test ./internal/feature/...

# Run specific test function
go test ./cmd -run TestInitCommand

# Run tests with verbose output
go test -v ./...

# Run tests without cache (useful after file changes)
go test ./... -count=1
```

### Version Management

```bash
# Bump version (updates internal/version/version.go)
make version-bump VERSION=v1.2.3
# OR
./scripts/version-bump.sh v1.2.3
```

## Architecture

### Package Structure

**`cmd/`** - Cobra command implementations
- Each command in a separate file (`init.go`, `new.go`, `move.go`, etc.)
- Shared helpers in `helpers.go`
- Exit code definitions in `exit.go`
- All commands support `--json`, `--verbose`, `--dry-run`, `--root` flags

**`internal/feature/`** - Core feature spec logic
- `model.go` - Feature struct with YAML frontmatter parsing
- `manager.go` - CRUD operations for feature files
- `status.go` - Workflow status directory mappings (backlog → in-progress → done)
- `sections.go` - Body section parsing/manipulation (H2 headings)
- `errors.go` - Custom error types (`InvalidFileError` for batch parse failures)

**`internal/spec/`** - System specification management
- `model.go` - System spec struct with YAML frontmatter (spec_type, title, status, applicability)
- `manager.go` - CRUD operations for system spec files
- `validator.go` - Validation against `schemas/system-spec.schema.json`
- Supports 8 spec types: tech-stack, local-development, hosting-and-infrastructure, ci-cd-pipeline, database-schema, caching-and-performance, security-and-compliance, observability-and-incident-response

**`internal/validator/`** - Feature validation engine
- JSON schema validation via `gojsonschema`
- Workflow rules (status → directory consistency)
- Dependency cycle detection
- Filename format validation (`{id}-{slug}.md`)

**`internal/indexer/`** - Index generation
- Produces Markdown, JSON, and HTML indexes
- Uses Go templates for HTML output
- Thread-safe, deterministic sorting

**`internal/lock/`** - Collaborative locking
- TTL-based locks for concurrent editing
- Lock status checking and force-override

**`internal/template/`** - Template application
- Ensures required sections exist in feature specs
- Non-destructive fixes

**`internal/templatediff/`** - Template update diffing
- Compares local `.virtualboard/` with latest template
- Powers `vb init --update` workflow
- Generates unified diffs with line statistics
- Excludes user feature files from updates

**`internal/util/`** - Shared utilities
- `fs.go` - Atomic file writes with secure permissions
- `slug.go` - URL-safe slug generation
- `output.go` - JSON/plain text response formatting
- `interactive/` - User prompts and confirmations for interactive workflows

**`internal/config/`** - CLI options management
- `options.go` - Global flags and configuration

**`internal/version/`** - Version tracking
- `version.go` - Semantic version constant

**`internal/upgrade/`** - Self-update logic
- GitHub release checking
- Platform-specific binary downloads

### Feature Spec Format

Features are markdown files with YAML frontmatter:

```markdown
---
id: FEAT-001
title: Example Feature
status: backlog
owner: ""
priority: medium
complexity: medium
created: 2024-01-01
updated: 2024-01-01
labels: [tag1, tag2]
dependencies: []
# Optional fields (omitted when empty):
# epic: "EPIC-001"
# risk_notes: "High risk due to external dependencies"
---

## Overview
Feature description here...

## Acceptance Criteria
- Criterion 1
- Criterion 2
```

**Status Workflow**: `backlog` → `in-progress` → `done`
- Each status maps to a directory: `.virtualboard/features/{status}/`
- Files must be in the correct directory for their status
- Dependencies must be `done` before moving to `in-progress`

### System Spec Format

System specifications are markdown files with YAML frontmatter stored in `.virtualboard/specs/`:

```markdown
---
spec_type: tech-stack
title: Technology Stack
status: approved
last_updated: 2024-01-15
applicability: [backend, frontend, infrastructure]
owner: platform-team
related_initiatives: []
---

## Overview
System specification content here...
```

**Valid Spec Types**:
- `tech-stack` - Technology stack decisions
- `local-development` - Local development environment
- `hosting-and-infrastructure` - Deployment infrastructure
- `ci-cd-pipeline` - CI/CD configuration
- `database-schema` - Database design
- `caching-and-performance` - Performance optimization
- `security-and-compliance` - Security policies
- `observability-and-incident-response` - Monitoring and alerting

**Valid Statuses**: `draft`, `approved`, `deprecated`

### Testing Architecture

- **100% coverage is mandatory** - `make test` enforces this
- Test files co-located with implementation (`*_test.go`)
- `internal/testutil/` provides fixture utilities for isolated test environments
- Most command tests use temporary directories via `testutil.Fixture`

**Test Isolation Pattern**:

```go
fix := testutil.NewFixture(t)           // Creates temp workspace
opts := fix.Options(t, false, false, false)  // json, verbose, dryRun
mgr := feature.NewManager(opts)
// Test operations in isolated environment
// Fixture automatically cleans up after test
```

### CLI Response Pattern

All commands use a consistent response pattern via `cmd/helpers.go:respond()`:
- Plain text output by default
- JSON output when `--json` flag is used
- Structured data alongside human-readable messages

## Key Workflows

### 1. Init Workflow
- Downloads template from GitHub (no git dependency)
- Extracts to `.virtualboard/`
- Validates zip archive security (path traversal, size limits)
- Tracks template version in `.template-version`

### 2. Template Update Workflow (`vb init --update`)
- Fetches latest template from GitHub
- Compares with local `.virtualboard/` using `internal/templatediff`
- Shows enhanced diff summary with line counts
- Interactive prompts for each change (y/n/a/q/d/e/h)
- Excludes user feature files from updates
- Applies selected changes and updates `.template-version`

### 3. Feature Creation
- Generates unique ID (FEAT-XXX)
- Creates file in backlog with template
- Sets timestamps, slugified filename

### 4. Feature Movement
- Validates workflow transitions
- Moves file to status-appropriate directory
- Updates timestamps and owner

### 5. Validation
**Features:**
- Schema validation via `schemas/frontmatter.schema.json`
- Workflow rules (status/directory consistency)
- Dependency resolution and cycle detection
- Filename format checks (`{id}-{slug}.md`)

**System Specs:**
- Schema validation via `schemas/system-spec.schema.json`
- Spec type validation (8 valid types)
- Status validation (draft/approved/deprecated)
- Date format validation (YYYY-MM-DD)

### 6. Index Generation
- Collects all features
- Sorts by status/priority
- Outputs to Markdown/JSON/HTML
- Change detection (added, transitioned, removed)

## Critical Requirements

### 100% Test Coverage
- Every function must have tests
- `make test` fails if coverage < 100%
- Coverage report normalized to handle Go counter edge cases

### Security Scanning
- All code must pass `make scan` (gosec)
- File permissions: `0o600` for files, `0o750` for directories
- Use `#nosec` only with justification comments

### Documentation Sync
When changing behavior, update:
- `README.md` - High-level overview
- `docs/CLI.md` - Command reference
- `docs/DEVELOPMENT.md` - Development guide
- `docs/index.html` - HTML documentation index
- `CHANGELOG.md` - Semantic versioning changelog

### Pre-commit Hooks
Install with `pre-commit install` (requires Python's pre-commit package)
- Runs `make test` and `make scan`
- Validates GitHub Actions workflows with `actionlint`
- Lints markdown, checks spelling, validates YAML

## CI/CD Pipeline

### Branch Strategy
- `dev` branch - RC releases (`vX.Y.Z-rc`)
- `main` branch - Production releases (`vX.Y.Z`)

### Workflows
- **CI** (`.github/workflows/ci.yml`) - Tests, security, build verification on every push
- **Auto-release** (`.github/workflows/auto-release.yml`) - Creates releases on branch merges
- **Release** (`.github/workflows/release.yml`) - Multi-platform builds triggered by tags

### Release Process
1. Update `internal/version/version.go`
2. Update `CHANGELOG.md`
3. Push to `dev` → creates RC release
4. Merge `dev` to `main` → creates final release

**IMPORTANT**: Never create git tags manually - use the automated workflow

## Common Patterns

### Reading Feature Specs

```go
mgr := feature.NewManager(opts)
feat, err := mgr.LoadByID("FEAT-001")
```

### Validating Features

```go
validator, _ := validator.New(opts, mgr)
summary, _ := validator.ValidateAll()
```

### Working with System Specs

```go
// Load a system spec
specMgr := spec.NewManager(opts)
systemSpec, err := specMgr.LoadByName("tech-stack.md")

// Validate system specs
specValidator, _ := spec.NewValidator(opts, specMgr)
summary, _ := specValidator.ValidateAll()
```

### Atomic File Writes

```go
// Always use util.WriteFileAtomic for atomic, secure writes
util.WriteFileAtomic(path, data, 0o600)
// Writes to temp file first, then atomic rename
// Prevents partial writes or corruption
```

### Command Response

```go
// Use respond() for consistent output
return respond(cmd, opts, success, message, dataMap)
```

### Logging

```go
// Get logger from options
log := opts.Logger().WithField("component", "mycomponent")
log.Info("message")
```

### Error Handling

**Custom Error Types**:
- Use `errors.New()` for sentinel errors (`ErrNotFound`, `ErrInvalidTransition`)
- Create custom error types for complex failures that need context
- Example: `InvalidFileError` collects multiple parse failures with paths and reasons

**Error Pattern**:

```go
// For batch operations that shouldn't fail-fast
var invalidFiles []InvalidFile
// ... collect errors ...
if len(invalidFiles) > 0 {
    return &InvalidFileError{Files: invalidFiles}
}
```

## File Permissions

- **Files**: `0o600` (read/write owner only)
- **Directories**: `0o750` (rwx owner, rx group)
- Required for gosec compliance

## Versioning

- Follows [Semantic Versioning 2.0.0](https://semver.org/)
- Version tracked in `internal/version/version.go`
- Breaking CLI changes → major bump
- New features → minor bump
- Bug fixes → patch bump

## Additional Notes

- Feature specs use `---` delimited YAML frontmatter
- All file I/O uses atomic writes for safety
- Validation uses JSON schema from `.virtualboard/schema.json`
- Index generation is deterministic (sorted output)
- Logging goes to stderr or optional `--log-file`
- Commands are idempotent where possible
- Dry-run mode (`--dry-run`) for safe preview of changes
