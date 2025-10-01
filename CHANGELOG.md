# Changelog

All notable changes to this project will be documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.0.3] - 2025-01-27

### Fixed

- Fixed GitHub Actions release workflow to use version from code instead of tag name
- Corrected release tag mismatch issue where manual tags caused incorrect release names
- Improved release process robustness by extracting version from `internal/version/version.go`

### Changed

- Updated GitHub Actions workflows to release direct binary files instead of compressed archives (tar.gz/zip)
- Simplified release process by removing archive creation and distributing binaries directly
- Updated release notes and documentation to reflect direct binary downloads
- Modified Homebrew formula references to use direct binary downloads

## [v0.0.2] - 2025-01-27
### Removed

- Homebrew tap and rc release from Github Actions

## [v0.0.1] - 2025-09-25
### Added

- Initial VirtualBoard CLI built with Cobra, including commands for `init`, `new`, `move`, `update`, `delete`, `index`, `validate`, `lock`, `template`, and `version`.
- Workspace bootstrap that downloads and extracts the VirtualBoard template into `.virtualboard/` with optional `--force` reinitialisation.
- Feature management tooling covering creation, updates, status transitions, dependency checks, validation, and indexing (Markdown/JSON/HTML).
- Development tooling: Makefile targets (`build`, `test`, `scan`, `package`), enforced 100% test coverage, gosec integration, and pre-commit hooks running security scans plus tests.
- Project documentation set (README, docs/CLI.md, docs/DEVELOPMENT.md, AGENTS.md) outlining usage, workflows, and automation guidance.
- Added Github Actions to build and release the vb cli
