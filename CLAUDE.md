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

**`internal/validator/`** - Validation engine
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

**`internal/util/`** - Shared utilities
- `fs.go` - Atomic file writes with secure permissions
- `slug.go` - URL-safe slug generation
- `output.go` - JSON/plain text response formatting

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
epic: ""
risk_notes: ""
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

### Testing Architecture

- **100% coverage is mandatory** - `make test` enforces this
- Test files co-located with implementation (`*_test.go`)
- `internal/testutil/` provides fixture utilities for isolated test environments
- Most command tests use temporary directories via `testutil.Fixture`

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

### 2. Feature Creation
- Generates unique ID (FEAT-XXX)
- Creates file in backlog with template
- Sets timestamps, slugified filename

### 3. Feature Movement
- Validates workflow transitions
- Moves file to status-appropriate directory
- Updates timestamps and owner

### 4. Validation
- Schema validation via JSON schema
- Workflow rules (status/directory consistency)
- Dependency resolution and cycle detection
- Filename format checks

### 5. Index Generation
- Collects all features
- Sorts by status/priority
- Outputs to Markdown/JSON/HTML

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

### Atomic File Writes

```go
// Always use util.WriteFile for atomic, secure writes
util.WriteFile(path, data, 0o600)
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
