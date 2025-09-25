# Agent Operations Guide

Welcome! This document orients AI coding agents working on `vb-cli`. Follow these instructions to keep the project consistent and healthy.

## Project Overview

- **Language**: Go (1.25+). CLI built atop [spf13/cobra](https://github.com/spf13/cobra).
- **Layout**: Root commands under `cmd/`, domain logic under `internal/<package>/`, shared helpers in `internal/util/` and `internal/testutil/`.
- **Key Packages**:
  - `internal/feature`: feature parsing, CRUD, workflow transitions.
  - `internal/indexer`: renders indexes (Markdown, JSON, HTML).
  - `internal/validator`: schema + workflow validation with dependency checks.
  - `internal/lock`: locking semantics for collaborative edits.
  - `internal/template`: template application helpers.
  - `internal/util`: I/O primitives, JSON helpers, slugging.
  - `internal/version`: semantic version string.
- **CLI**: Exposed via cobra commands (`vb init`, `vb new`, `vb move`, `vb validate`, etc.). `vb init` downloads and extracts the template archive (no git dependency). Full reference lives in `docs/CLI.md`.

## Development Workflow
- Use `make` targets:
  - `make build` – compile modules.
  - `make test` – run full test suite with enforced 100% coverage. Fails if any package reports <100%.
  - `make scan` – run gosec security scan (excludes `.gomodcache` and `dist`).
  - `make package` – build `dist/vb` binary.
- Pre-commit hooks (`pre-commit install`) run `make test` and `make scan` prior to commit.
- All PRs must run both `make test` and `make scan` (either manually or via hooks) before completion.

## Testing & Coverage
- 100% coverage is mandatory. The `make test` target normalises counters and verifies total coverage equals `100.0%`.
- Add or adjust tests whenever code changes. Most tests live beside their packages (`*_test.go`).

## Security Practices
- gosec integration is required. Pay attention to file permission warnings (use `0o600/0o750` as needed) and annotate deliberate `#nosec` cases with justification.

## Documentation Expectations
- Primary docs:
  - `README.md` – high-level overview, quick start, badges, links.
  - `docs/CLI.md` – command reference.
  - `docs/DEVELOPMENT.md` – build/test/gosec/versioning instructions.
  - `CHANGELOG.md` – semantic release notes.
- **Always** update relevant documentation (`docs/` and `README.md`) when behaviour changes. Keep examples and command descriptions in sync.

## Versioning & Releases
- Semantic Versioning (`internal/version/Current`).
- Every change must assess whether to bump **major**, **minor**, or **patch**:
  - Breaking CLI/API changes → major.
  - Backwards-compatible features → minor.
  - Bug fixes / internal changes → patch.
- Update `internal/version/version.go` with the new tag (use `vX.Y.Z`).
- Document the release in `CHANGELOG.md` with date and categorized entries.
- Mention the version bump in README badges or documentation if relevant.

## Agent Checklist Before Completion
1. Review requirements and determine if external docs need updates (`docs/`, `README.md`). Apply updates.
2. Decide on SemVer bump; update `internal/version/Current` and `CHANGELOG.md`.
3. Ensure new functionality/tests reflect in docs.
4. Run `make scan` and `make test` (or allow pre-commit to run them). Confirm both pass.
5. Verify coverage remains at 100%.
6. Summarize changes, include verification steps in final output.

## Additional Context
- Tests rely on `internal/testutil.Fixture` for isolated environments.
- File I/O uses atomic writes (`internal/util/fs.go`) and secure permissions.
- Validation uses JSON schema via `gojsonschema` (documented in `internal/validator`).
- CLI responses support plain text and JSON; persist this dual behaviour on new commands.
- Logging uses `sirupsen/logrus`; verbose mode writes to stderr or optional log file.
- Index generation uses Go templates for HTML; keep additions thread-safe and deterministic.

Stay consistent, keep docs current, and run the verification suite before finishing. EOF
