# vb-cli

[![Tests](https://img.shields.io/badge/tests-passing-brightgreen.svg)](docs/DEVELOPMENT.md#development)
[![Coverage](https://img.shields.io/badge/coverage-measured-blue.svg)](docs/DEVELOPMENT.md#development)
[![Release](https://img.shields.io/badge/version-v0.10.0-informational.svg)](CHANGELOG.md)
[![Semantic Versioning](https://img.shields.io/badge/semver-2.0.0-blue.svg)](https://semver.org/spec/v2.0.0.html)

VirtualBoard CLI (`vb`) is the workspace companion for authoring, validating, and shipping feature specifications that live under `.virtualboard/`. It keeps teams in sync by scaffolding new specs, guarding workflow transitions, surfacing validation issues early, and generating stakeholder-friendly indexes.

## Key Capabilities

- Initialise a repository with `vb init`, which verifies and expands an exact pinned VirtualBoard template release into `.virtualboard/`. Keep your workspace up-to-date with `vb init --update` for reviewed template updates that preserve user state.
- Install release-pinned IDE integrations with `vb install <ide>` for Claude
  Code, Cursor, and OpenCode. Cursor and OpenCode payloads are accepted only
  when their compiled SHA-256 inventory matches the coordinated template
  release.
- Create, update, move, and lock features end-to-end via dedicated subcommands;
  `vb delete` is deliberately limited to unreferenced backlog specs.
- Serialize the complete board graph and per-feature/user-lock changes behind
  cross-process guards. Existing specs are captured into journaled recovery
  storage before replacement, move, or deletion, and concurrent cycle commits
  are rejected. Modern workspaces also refuse to run without `virtualboard.json`;
  the contract must be a bounded regular non-symlink file whose complete
  semantic JSON matches the canonical contract compiled into the CLI. Built-in
  lifecycle fallback is reserved for explicitly versioned pre-v0.8 layouts.
- Validate both feature specs and system specs with `vb validate`, supporting schema validation for features (workflow, dependencies) and specs (architectural blueprints). Use `--only-features` or `--only-specs` to validate specific types.
- Inspect the tamper-evident audit log with `vb audit`, supporting filters on every column (action, actor, feature_id, time range, details substring), six output formats (human, table, jsonl, json, xml, agent), and `--verify` to walk the SHA-256 hash chain.
- Regenerate deterministic indices in Markdown/JSON/HTML with `vb index`, or use `vb index --check` as a no-write CI drift gate.
- Repair legacy lifecycle provenance with the preflighted, auditable `vb migrate lifecycle-metadata` command.
- Apply opinionated templates and fixes (`vb template apply`) behind a measured coverage gate and gosec-scanned code.
- Self-update on supported Linux/macOS platforms with `vb upgrade`; Windows
  binaries remain available for verified manual replacement because an active
  executable cannot be atomically replaced portably there.

## Download & Install

### Download and verify a pinned release

Choose an exact version and platform asset. Do not automate installation through
the moving `/latest/download/` URLs: the release tag and checksum manifest must
be pinned together.

```bash
VERSION=v0.10.0
ASSET=vb-macos-arm64 # vb-macos-amd64, vb-linux-amd64, or vb-linux-arm64
BASE="https://github.com/virtualboard/vb-cli/releases/download/$VERSION"
curl --fail --location --proto '=https' --proto-redir '=https' \
  --output "$ASSET" "$BASE/$ASSET"
curl --fail --location --proto '=https' --proto-redir '=https' \
  --output checksums.txt "$BASE/checksums.txt"
EXPECTED=$(awk -v asset="$ASSET" '$2 == "./" asset || $2 == asset || $2 == "*" asset {print $1}' checksums.txt)
test "$(printf '%s\n' "$EXPECTED" | sed '/^$/d' | wc -l | tr -d ' ')" = 1
test "$(shasum -a 256 "$ASSET" | awk '{print $1}')" = "$EXPECTED"
```

Windows users should download the exact `v0.10.0/vb-windows-amd64.exe` or
`v0.10.0/vb-windows-arm64.exe` asset for their native architecture plus
`v0.10.0/checksums.txt`, then compare `Get-FileHash` output with the single
matching manifest entry. Full commands and provenance verification are in
[Release verification](docs/RELEASE_VERIFICATION.md).

### Platform-Specific Installation

#### macOS

1. **Download the appropriate binary** for your architecture (ARM64 for Apple Silicon, AMD64 for Intel)
2. **Install to a user-writable path:**

   ```bash
   mkdir -p "$HOME/.local/bin"
   install -m 0755 vb-macos-[architecture] "$HOME/.local/bin/vb"
   ```

   Replace `[architecture]` with `arm64` or `amd64`, and add
   `$HOME/.local/bin` to `PATH` if needed.

#### Linux

1. **Download the appropriate binary** for your architecture (AMD64 or ARM64)
2. **Install to a user-writable path:**

   ```bash
   mkdir -p "$HOME/.local/bin"
   install -m 0755 vb-linux-[architecture] "$HOME/.local/bin/vb"
   ```

   Replace `[architecture]` with `amd64` or `arm64`, and add
   `$HOME/.local/bin` to `PATH` if needed.

#### Windows

1. **Download the matching Windows binary** (`vb-windows-amd64.exe` or `vb-windows-arm64.exe`)
2. **Move it to a user-writable directory in your PATH** (for example, `%LOCALAPPDATA%\Programs\VirtualBoard`); do not elevate merely to install it
3. **Optionally rename** to `vb.exe` for convenience
4. **Add to PATH** if the directory isn't already included

### Verify Installation

```bash
vb version
```

### First Run

From your repository root:

```bash
vb init                      # bootstrap .virtualboard/
vb init --update             # reapply the exact template release pinned in this binary
vb install claude            # install Claude Code plugin
vb install cursor            # install Cursor IDE rules
vb install opencode          # install the OpenCode skill
vb --actor dev-alex new "Awesome Feature" label1 label2
vb validate                  # validate both features and system specs
vb validate --only-features  # validate only feature specs
vb validate --only-specs     # validate only system specs
vb index                     # emit .virtualboard/features/INDEX.md
vb index --check             # fail without writing when the index is stale
vb audit                     # browse the hash-chained audit log
vb audit --format table --actor netors --since 2026-04-01
vb audit --verify            # check the audit chain for tampering
vb upgrade                   # atomic self-update on supported Linux/macOS targets
```

`vb install claude` registers the exact `template-base#v0.8.0` marketplace and
installs `virtualboard@virtualboard-marketplace`. Cursor installation rejects
symlinked destinations and modified or missing fixed payloads. OpenCode also
requires exact inventory equality, rejecting extra local payload files. Both
installers normalize installed files to non-executable `0644` mode. Existing
destinations are moved into recovery storage and verified against the approved
snapshot before no-replace activation; a concurrently recreated path is never
overwritten, and ambiguous recovery state is retained for reconciliation.

All commands support JSON/plain output, `--dry-run`, verbose logging, and respect the `.virtualboard` workspace root. Mutating commands require a stable `--actor`, `VIRTUALBOARD_ACTOR`, or `AGENT_ID`; OS usernames are never treated as agent identity.

Actor and owner values are self-asserted coordination labels, not authenticated
principals. Filesystem permissions and the calling host remain the security
boundary; hostile or compliance-grade multi-tenant use requires an authenticated
orchestrator rather than relying on local labels and Markdown policy.

## Development Workflow

### Quick Setup

```bash
# Run the setup script to configure your development environment
./scripts/setup-dev.sh
```

### Manual Development Commands
- `make test` – runs the entire suite, records real coverage, and enforces the configured minimum without rewriting counters.
- `make scan` – executes gosec checks across the codebase.
- `make build` / `make package` – compile the CLI or produce a `dist/vb` binary.
- `make pre-commit` – runs all pre-commit checks manually.
- Pre-commit hooks (see `.pre-commit-config.yaml`) automatically run comprehensive quality checks.

## CI/CD Pipeline

This project uses GitHub Actions for continuous integration and deployment:

- **CI Workflow** (`.github/workflows/ci.yml`): Runs on every push and PR to `main`/`dev` branches
  - Race tests plus an honest measured coverage threshold
  - Security scanning with gosec
  - Build verification
  - Workflow-contract and build verification

- **Release Workflow** (`.github/workflows/release.yml`): Triggers on version tags
  - Multi-platform builds (Linux AMD64/ARM64, macOS AMD64/ARM64, Windows AMD64/ARM64)
  - One verified template input and a built-binary init/install smoke test
  - Immutable GitHub releases with generated notes, checksums, and provenance attestations

- **Workflow contract checker** (`scripts/check-workflows.py`): commit pins,
  permissions, release assets, and required quality gates

Additional contributor guidance lives in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) and the automation checklist in [AGENTS.md](AGENTS.md).

## Documentation

- [CLI Reference](docs/CLI.md)
- [Development Guide](docs/DEVELOPMENT.md)
- [Release verification](docs/RELEASE_VERIFICATION.md)
- [Changelog](CHANGELOG.md)

## Maintainers & Contributors

- VirtualBoard Engineering Guild
- Community contributors – see [GitHub contributors](https://github.com/virtualboard/vb-cli/graphs/contributors)

Contributions are welcome—run the required `make` targets above before opening a PR.
