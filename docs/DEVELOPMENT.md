# Development

VirtualBoard CLI requires exactly Go 1.25.0 and uses the repository Makefile as the local
quality interface.

```bash
make build       # compile all packages
make test        # run tests and enforce real measured coverage
make scan        # run the pinned gosec release
make package     # build dist/vb
python3 scripts/check-workflows.py
```

`make test` writes `coverage.out` from the actual `go test` counters and compares
the reported total with `COVERAGE_MIN` (75.0 by default). It never edits the
profile to turn missed statements into covered statements. A temporary local
experiment may set a higher threshold, for example `COVERAGE_MIN=90 make test`;
the committed CI minimum must be changed through review.

## Internal packages

- `internal/contract` reads the workspace contract as bounded,
  identity-stable, regular non-symlink data and rejects semantic drift anywhere
  in the complete canonical v0.10 contract, including authorization effects,
  roles, commands, plugin expectations, paths, and lifecycle semantics.
- `internal/feature` implements feature discovery, ownership, provenance, and
  lifecycle mutations.
- `internal/validator` enforces schemas, dependencies, paths, links, and dates.
- `internal/indexer` produces deterministic Markdown, JSON, and HTML indexes.
- `internal/lock` manages actor-attributed TTL locks with per-acquisition
  256-bit tokens, cross-process per-ID guards, conditional removal/replacement,
  and atomic publication. Internal feature mutations hold a callback-scoped OS
  guard for their full duration rather than relying on an expiring TTL lease.
- `internal/audit` appends and verifies the versioned hash-chained audit log.
- `internal/migration` preflights and applies legacy lifecycle provenance.
- `internal/upgrade` retains a checksum-bound source handle, re-hashes its
  private same-filesystem stage, verifies the handle-bound embedded CLI version
  marker against the release tag without executing it, and atomically installs
  supported release assets.

IDE installation trust is release-coupled in `cmd/install.go`: the Claude
marketplace source includes `templateRelease`, and the Cursor digest plus sorted
OpenCode path/SHA-256 inventory must match the exact payloads in that template
release. Update those compiled values in the same reviewed change as a template
payload change. Tests must cover modified, missing, extra, symlinked, and
non-regular payloads as well as destination-parent symlinks and safe modes.

Audit-chain verification detects modification or truncation of entries that are
present. The feature and lock managers currently treat append failures as
best-effort, so the log is not a completeness proof for every mutation. Do not
use it as the only authorization or accounting control.

## CI and release

Only two GitHub Actions workflows are supported:

- `.github/workflows/ci.yml` runs on pushes and pull requests to `main` and
  `dev`, and is reused by the release workflow. Ubuntu verifies dependencies,
  formatting, workflow contracts, vet, race tests, measured coverage, gosec,
  and a clean build. Native macOS and Windows jobs run the core, lifecycle, and
  integration-install tests before a release can proceed.
- `.github/workflows/release.yml` runs only for strict `vMAJOR.MINOR.PATCH` tags
  whose value matches `internal/version/version.go` and whose commit is on
  `main`. It derives the template release from `cmd/init.go`, downloads that
  template once, shares its immutable artifact and digest with every build,
  exercises a built binary through init/validate/index/Cursor/OpenCode, builds
  six platform binaries, creates checksums and provenance attestations, and
  publishes only through a source-bound draft after exact asset verification.

Every third-party action is pinned to a commit SHA and default workflow
permissions are read-only. `scripts/check-workflows.py` prevents those release
invariants from silently drifting. Dependabot proposes reviewed Go-module and
GitHub Actions updates; it does not publish releases.

## Version and coordinated release process

1. Update `internal/version/version.go`, the template constants in `cmd/init.go`,
   and `CHANGELOG.md` in a reviewed change. The release workflow derives the
   template release from those source constants; it has no duplicate workflow
   version setting.
2. Ensure that exact template release exists with both the stable ZIP asset and
   `template-checksums.txt`, and verify the compiled Cursor digest and OpenCode
   file inventory match that exact release.
3. Run the full local verification commands above.
4. After merge, create and push the exact matching semantic-version tag.
5. The release workflow verifies tag/source agreement and refuses replacement
   of an existing GitHub release.

The template asset must be released first because the CLI release embeds its
SHA-256 digest. Template v0.8 uses a reviewed CLI source commit during its own
bootstrap verification, so it does not require a pre-existing v0.10 binary.
Tagging, pushing, and publishing are external writes and require the repository
owner's explicit authorization. Verify downloaded releases with the
pinned-version procedure in [RELEASE_VERIFICATION.md](RELEASE_VERIFICATION.md).

## Pre-commit

```bash
python3 -m pip install pre-commit
pre-commit install
pre-commit run --all-files
```

The configured hooks run Go formatting, dependency, test, coverage, security,
workflow, prose, JSON/YAML, secret, and file-integrity checks. Hook downloads are
developer tooling; CI remains the authoritative release gate.

## Contribution expectations

- Add focused tests for behavior changes and failure paths.
- Preserve stable JSON output and nonzero exit codes for failures.
- Run `gofmt`, `go vet`, race tests, measured coverage, and gosec.
- Update CLI/help documentation and the changelog when behavior changes.
- Do not weaken actor, ownership, lock, path, checksum, or release controls to
  make a test pass.
