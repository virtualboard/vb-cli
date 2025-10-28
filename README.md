# vb-cli

[![Tests](https://img.shields.io/badge/tests-passing-brightgreen.svg)](docs/DEVELOPMENT.md#development)
[![Coverage](https://img.shields.io/badge/coverage-100%25-blue.svg)](docs/DEVELOPMENT.md#development)
[![Release](https://img.shields.io/badge/version-v0.1.2-informational.svg)](CHANGELOG.md)
[![Semantic Versioning](https://img.shields.io/badge/semver-2.0.0-blue.svg)](https://semver.org/spec/v2.0.0.html)

VirtualBoard CLI (`vb`) is the workspace companion for authoring, validating, and shipping feature specifications that live under `.virtualboard/`. It keeps teams in sync by scaffolding new specs, guarding workflow transitions, surfacing validation issues early, and generating stakeholder-friendly indexes.

## Key Capabilities

- Initialise a repository with `vb init`, which downloads and expands the VirtualBoard template archive into `.virtualboard/`. Keep your workspace up-to-date with `vb init --update` for interactive template updates.
- Create, update, move, delete, and lock features end-to-end via dedicated subcommands (`vb new`, `vb update`, `vb move`, `vb delete`, `vb lock`).
- Validate schema, workflow, and dependency rules with `vb validate`, and regenerate indices in Markdown/JSON/HTML with `vb index`.
- Apply opinionated templates and fixes (`vb template apply`) while maintaining 100% unit-test coverage and gosec-scanned code.
- Self-update to the latest version with `vb upgrade`, which automatically detects your platform and downloads the appropriate binary from GitHub releases.

## Download & Install

### Download Platform-Specific Binaries

Choose your platform and download the appropriate binary from our latest release:

- **macOS ARM64 (Apple Silicon):** [Download `vb-macos-arm64`](https://github.com/virtualboard/vb-cli/releases/latest/download/vb-macos-arm64)
- **macOS AMD64 (Intel):** [Download `vb-macos-amd64`](https://github.com/virtualboard/vb-cli/releases/latest/download/vb-macos-amd64)
- **Linux AMD64:** [Download `vb-linux-amd64`](https://github.com/virtualboard/vb-cli/releases/latest/download/vb-linux-amd64)
- **Linux ARM64:** [Download `vb-linux-arm64`](https://github.com/virtualboard/vb-cli/releases/latest/download/vb-linux-arm64)
- **Windows AMD64:** [Download `vb-windows-amd64.exe`](https://github.com/virtualboard/vb-cli/releases/latest/download/vb-windows-amd64.exe)

### Platform-Specific Installation

#### macOS

1. **Download the appropriate binary** for your architecture (ARM64 for Apple Silicon, AMD64 for Intel)
2. **Move to system path:**

   ```bash
   sudo mv vb-macos-[architecture] /usr/local/bin/vb
   sudo chmod +x /usr/local/bin/vb
   ```

   Replace `[architecture]` with `arm64` or `amd64` as appropriate.

#### Linux

1. **Download the appropriate binary** for your architecture (AMD64 or ARM64)
2. **Move to system path:**

   ```bash
   sudo mv vb-linux-[architecture] /usr/local/bin/vb
   sudo chmod +x /usr/local/bin/vb
   ```

   Replace `[architecture]` with `amd64` or `arm64` as appropriate.

#### Windows

1. **Download the Windows binary** (`vb-windows-amd64.exe`)
2. **Move to a directory in your PATH** (e.g., `C:\Program Files\vb-cli\`)
3. **Optionally rename** to `vb.exe` for convenience
4. **Add to PATH** if the directory isn't already included

### Verify Installation

```bash
vb version
```

### First Run

From your repository root:

```bash
vb init               # bootstrap .virtualboard/
vb init --update      # update template to latest version
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
