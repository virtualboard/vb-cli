# Agent Operations Guide

`vb-cli` is the exact Go 1.25.0 command-line implementation of VirtualBoard. Commands
live in `cmd/`; domain behavior lives under `internal/`. Read the nearest source
and tests before changing behavior, and preserve the plain/JSON exit-code
contract.

## Core invariants

- `virtualboard.json` is the machine-readable mirror of the supported v0.10
  contract, not an open extension point. The CLI authenticates the complete
  semantic contract—including authorization effects, roles, commands, plugin
  expectations, paths, identities, lifecycle, and ownership—against its
  compiled canonical digest before using any projected runtime field.
- Every feature mutation requires an explicit stable actor from `--actor`,
  `VIRTUALBOARD_ACTOR`, or `AGENT_ID`. `--owner` assigns state; it never
  authenticates the caller. Never fall back to the OS username.
- Ownership and active locks fail closed. IDs, feature basenames, and managed
  lifecycle fields are immutable through ordinary update commands.
- Review handback restores the preserved `implementation_owner`; it may not
  assign an arbitrary replacement implementer.
- Duplicate IDs, unsafe paths, symlinks/non-regular feature files, malformed
  contracts, bad checksums, and invalid JSON validation results are errors.
- Dry-run output must describe what actually happened. JSON failures return a
  structured failure payload and a nonzero exit status.
- Audit hashes provide tamper evidence for entries that exist. Feature/lock
  append failures are currently best-effort, so the audit file is not a proof of
  completeness and must not be the only authorization control.

## Important packages

- `internal/contract`: runtime workspace contract
- `internal/feature`: feature parsing, ownership, and lifecycle mutations
- `internal/validator`: schema, dependency, link, folder, and date validation
- `internal/indexer`: deterministic Markdown/JSON/HTML indexes
- `internal/lock`: actor-attributed TTL locks
- `internal/audit`: bounded, versioned hash-chain append/read/verify
- `internal/migration`: all-board lifecycle-provenance migration
- `internal/upgrade`: bounded release resolution, verification, and replacement
- `internal/util`: atomic filesystem and structured-output helpers

## Required local verification

Use the pinned Go toolchain expected by the repository environment.

```bash
gofmt -w <changed-go-files>
go vet ./...
go test -race ./...
make test
make scan
python3 scripts/check-workflows.py
go build -trimpath -buildvcs=false ./...
```

`make test` enforces the real measured coverage in `coverage.out` against
`COVERAGE_MIN`; never rewrite counters or manufacture coverage. Add focused
tests for success, failure, rollback, concurrency, path, JSON, and dry-run
behavior as applicable.

## Release and documentation

- Update `README.md`, `docs/CLI.md`, `docs/DEVELOPMENT.md`, and `CHANGELOG.md`
  when public behavior changes.
- `internal/version.Current` and a release tag must match exactly.
- The template release asset/checksum must exist before the CLI release because
  each binary embeds its verified digest.
- Keep exactly the reviewed CI and release workflows; all action references stay
  commit-pinned with read-only default permissions.
- Tags, pushes, releases, package publication, and other external writes require
  explicit user authorization. Never infer that authority from a feature spec.

## Completion checklist

1. Scope the diff and preserve unrelated work.
2. Update behavior, tests, and user-facing documentation together.
3. Run formatting, vet, race, measured coverage, gosec, workflow, and build gates.
4. Record any honest residual limitation; do not convert it into a green claim.
5. Do not tag, push, or publish unless the user explicitly requested it.
