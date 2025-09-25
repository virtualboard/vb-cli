# Development

- `make build` – compile all packages.
- `make scan` – run gosec security checks.
- `make test` – run unit tests with coverage (requires 100% coverage).
- `make package` – build the CLI binary into `dist/`.

## GitHub Actions Workflows

This project uses GitHub Actions for automated testing, building, and releasing. The workflow is designed around a `dev` → `main` branching strategy with automatic release creation.

### Workflow Overview
- **Feature branches** → **dev branch**: Creates RC releases for testing
- **dev branch** → **main branch**: Creates final releases for production
- **All changes** go through CI/CD pipeline with 100% test coverage requirement

### CI Workflow (`.github/workflows/ci.yml`)
Runs on every push and pull request to `main` and `dev` branches:
- **Testing**: Runs full test suite with 100% coverage requirement
- **Security**: Executes gosec security scanning
- **Build Verification**: Tests that the project builds successfully
- **Coverage Reporting**: Uploads coverage reports to Codecov

### Release Workflow (`.github/workflows/release.yml`)
Triggers when version tags are pushed (e.g., `v1.0.0`):
- **Multi-platform Builds**: Creates binaries for:
  - Linux AMD64 and ARM64
  - macOS AMD64 and ARM64
  - Windows AMD64
- **Automated Releases**: Creates GitHub releases with:
  - Changelog generation from git commits
  - Asset verification and checksums
  - Platform-specific archives (tar.gz for Unix, zip for Windows)
- **Homebrew Integration**: Updates Homebrew formula for macOS users

### Automatic Release Workflow (`.github/workflows/auto-release.yml`)
Automatically creates releases based on branch merges:
- **Feature branch → dev**: Creates release candidate (RC) releases (e.g., `v1.0.1-rc`)
- **Dev → main**: Creates final releases (e.g., `v1.0.1`)
- **Direct push to dev**: Creates RC releases
- **Multi-platform Builds**: Same as release workflow
- **Version Management**: Automatically increments version and updates `internal/version/version.go`
- **Homebrew Integration**: Updates Homebrew formula for final releases only

### Additional Workflows

#### Validate Workflows (`.github/workflows/validate-workflows.yml`)
Runs when workflow files are modified:
- **Syntax Validation**: Validates GitHub Actions workflow syntax
- **Security Checks**: Ensures workflows follow security best practices
- **YAML Validation**: Verifies YAML syntax and formatting

#### Update Dependencies (`.github/workflows/update-dependencies.yml`)
Runs weekly (Mondays at 9 AM UTC) or manually:
- **Dependency Updates**: Automatically updates Go dependencies
- **Pull Request Creation**: Creates PRs with updated dependencies
- **Testing Checklist**: Includes testing requirements in PR description

#### Test Release Process (`.github/workflows/test-release.yml`)
Manual workflow for testing the release process:
- **Multi-platform Testing**: Tests builds on all supported platforms
- **Package Creation**: Tests release package creation
- **Binary Verification**: Ensures binaries work correctly

#### Workflow Status (`.github/workflows/status.yml`)
Monitors workflow failures:
- **Failure Notification**: Alerts when critical workflows fail
- **Status Monitoring**: Tracks CI and pre-commit workflow status

## Versioning

`vb-cli` follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html). The current release is tracked in `internal/version` and published via the `vb version` command. Every change is recorded in [CHANGELOG.md](../CHANGELOG.md).

## Pre-commit Hooks

Install pre-commit and enable hooks:

```bash
pip install pre-commit
pre-commit install
```

### Pre-commit Checks

The pre-commit configuration includes comprehensive quality checks:

#### **Go-specific Checks:**
- **Security Scanning**: `make scan` with gosec
- **Test Coverage**: `make test` with 100% coverage requirement
- **Code Formatting**: `gofmt` verification
- **Dependency Management**: `go mod tidy` validation

#### **GitHub Actions Validation:**
- **Workflow Syntax**: `actionlint` validation for `.github/workflows/*.yml`
- **YAML Formatting**: Prettier formatting for workflow files

#### **Markdown Quality:**
- **Linting**: `markdownlint` with custom rules (`.markdownlint.yaml`)
- **Link Checking**: Validates markdown links and references
- **Spell Checking**: `typos` spell checker with technical dictionary (`.typos.toml`)

#### **General File Checks:**
- **Trailing Whitespace**: Automatic trimming
- **End of File**: Ensures proper file endings
- **YAML/JSON Syntax**: Validates configuration files
- **Merge Conflicts**: Detects unresolved conflicts
- **Large Files**: Prevents committing files >1MB
- **Private Keys**: Detects accidentally committed secrets
- **Line Endings**: Enforces LF line endings

#### **Security Checks:**
- **Private Key Detection**: Prevents committing sensitive data
- **Merge Conflict Detection**: Ensures clean merges
- **Executable Permissions**: Validates script permissions

### Configuration Files

- **`.markdownlint.yaml`**: Markdown linting rules
- **`.typos.toml`**: Spell checking dictionary and rules
- **`.pre-commit-config.yaml`**: Complete pre-commit configuration

### Running Pre-commit Manually

```bash
# Run all hooks on all files
pre-commit run --all-files

# Run specific hook
pre-commit run actionlint
pre-commit run markdownlint
pre-commit run typos

# Update hook versions
pre-commit autoupdate
```

## Release Process

### Automatic Releases (Recommended)
The project uses automated releases based on branch merges:

1. **Feature Development**: Work on feature branches
2. **Merge to Dev**: When ready, merge feature branch to `dev`
   - **Automatic**: Creates RC release (e.g., `v1.0.1-rc`)
   - **Testing**: Use RC releases for testing and validation
3. **Merge to Main**: When RC is stable, merge `dev` to `main`
   - **Automatic**: Creates final release (e.g., `v1.0.1`)
   - **Distribution**: Final releases are ready for production use

### Manual Releases (Alternative)
For manual control over releases:

1. **Update Version**: Modify `internal/version/version.go` with new version
2. **Update Changelog**: Add entries to `CHANGELOG.md`
3. **Create Tag**: `git tag vX.Y.Z && git push origin vX.Y.Z`
4. **Automated Release**: GitHub Actions will automatically:
   - Build for all platforms
   - Create GitHub release
   - Update Homebrew formula
   - Generate checksums and changelog

### Release Types
- **RC Releases**: Created when merging to `dev` branch
  - Used for testing and validation
  - Marked as prerelease on GitHub
  - Version format: `v1.0.1-rc`
- **Final Releases**: Created when merging `dev` to `main`
  - Ready for production use
  - Not marked as prerelease
  - Version format: `v1.0.1`
