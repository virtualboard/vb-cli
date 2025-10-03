# vb-cli

[![Tests](https://img.shields.io/badge/tests-passing-brightgreen.svg)](docs/DEVELOPMENT.md#development)
[![Coverage](https://img.shields.io/badge/coverage-100%25-blue.svg)](docs/DEVELOPMENT.md#development)
[![Release](https://img.shields.io/badge/version-v0.1.2-informational.svg)](CHANGELOG.md)
[![Semantic Versioning](https://img.shields.io/badge/semver-2.0.0-blue.svg)](https://semver.org/spec/v2.0.0.html)

VirtualBoard CLI (`vb`) is the workspace companion for authoring, validating, and shipping feature specifications that live under `.virtualboard/`. It keeps teams in sync by scaffolding new specs, guarding workflow transitions, surfacing validation issues early, and generating stakeholder-friendly indexes.

## Key Capabilities

- Initialise a repository with `vb init`, which downloads and expands the VirtualBoard template archive into `.virtualboard/`.
- Create, update, move, delete, and lock features end-to-end via dedicated subcommands (`vb new`, `vb update`, `vb move`, `vb delete`, `vb lock`).
- Validate schema, workflow, and dependency rules with `vb validate`, and regenerate indices in Markdown/JSON/HTML with `vb index`.
- Apply opinionated templates and fixes (`vb template apply`) while maintaining 100% unit-test coverage and gosec-scanned code.
- Display the VirtualBoard logo as beautiful colored ASCII art with `vb art`.
- Self-update to the latest version with `vb upgrade`, which automatically detects your platform and downloads the appropriate binary from GitHub releases.

## Install & First Run

```bash
go install github.com/virtualboard/vb-cli@latest
vb --help
```

From your repository root:

```bash
vb init               # bootstrap .virtualboard/
vb new "Awesome Feature" label1 label2
vb validate all       # ensure specs meet schema + workflow rules
vb index              # emit .virtualboard/features/INDEX.md
vb upgrade            # check for and install the latest version
```

All commands support JSON/plain output, `--dry-run`, verbose logging, and respect the `.virtualboard` workspace root.

## Development Workflow

### Quick Setup

```bash
# Run the setup script to configure your development environment
./scripts/setup-dev.sh
```

### Manual Development Commands
- `make test` – runs the entire suite and enforces 100% coverage.
- `make scan` – executes gosec checks across the codebase.
- `make build` / `make package` – compile the CLI or produce a `dist/vb` binary.
- `make pre-commit` – runs all pre-commit checks manually.
- Pre-commit hooks (see `.pre-commit-config.yaml`) automatically run comprehensive quality checks.

## CI/CD Pipeline

This project uses GitHub Actions for continuous integration and deployment:

- **CI Workflow** (`.github/workflows/ci.yml`): Runs on every push and PR to `main`/`dev` branches
  - Tests with 100% coverage requirement
  - Security scanning with gosec
  - Build verification
  - Code coverage reporting

- **Release Workflow** (`.github/workflows/release.yml`): Triggers on version tags
  - Multi-platform builds (Linux AMD64/ARM64, macOS AMD64/ARM64, Windows AMD64)
  - Automated GitHub releases with changelog generation
  - Homebrew formula updates
  - Asset verification and checksums

- **Pre-commit Workflow** (`.github/workflows/pre-commit.yml`): Code quality checks
  - Format verification
  - Dependency validation
  - Test and security scan enforcement

Additional contributor guidance lives in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) and the automation checklist in [AGENTS.md](AGENTS.md).

## Documentation

- [CLI Reference](docs/CLI.md)
- [Development Guide](docs/DEVELOPMENT.md)
- [Changelog](CHANGELOG.md)

## Maintainers & Contributors

- VirtualBoard Engineering Guild
- Community contributors – see [GitHub contributors](https://github.com/virtualboard/vb-cli/graphs/contributors)

Contributions are welcome—run the required `make` targets above before opening a PR.
