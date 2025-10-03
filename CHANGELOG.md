# Changelog

All notable changes to this project will be documented in this file. The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.6] - 2025-01-27

### Changed

- Enhanced `vb art` command ASCII character set for smoother gradients (now uses `. : - = + * # @ █`)
- Increased ASCII art output width from 80 to 100 characters for better detail
- Improved brightness mapping and alpha transparency handling in ASCII conversion
- Updated logo search to prefer `docs/logo.png` over `docs/avatar.png` for dark background support

### Fixed

- Corrected ASCII art character progression from lightest to darkest for proper image rendering
- Enhanced low-alpha pixel handling to display as spaces for better transparency support

## [v0.1.5] - 2025-01-27

### Fixed

- Fixed ASCII art scaling in `vb art` command to maintain proper aspect ratio for square images
- Corrected terminal character aspect ratio compensation (2:1 height:width) to prevent vertical stretching
- Updated coordinate mapping to use separate scaling factors for width and height dimensions

## [v0.1.4] - 2025-01-27

### Added

- Added `vb art` command to render the VirtualBoard logo as colored ASCII art in the terminal
- Implemented image-to-ASCII conversion with 24-bit ANSI color support and intelligent scaling
- Added comprehensive test coverage for the new art command functionality

### Updated

- Updated CLI documentation to include the new `vb art` command
- Enhanced README.md with ASCII art capability description
- Updated HTML index page to showcase the new art command feature

## [v0.1.3] - 2025-10-02

### Fixed

- Fixed `vb upgrade` command binary naming discrepancy - now correctly matches GitHub Actions release asset names (e.g., `vb-macos-arm64` instead of `vb_darwin_arm64`)

## [v0.1.2] - 2025-01-27

### Added

- Added VirtualBoard logo integration to the landing page (docs/index.html)
- Implemented responsive logo design with hover animations and mobile optimization

### Changed

- Updated landing page color theme to match VirtualBoard brand identity
- Enhanced visual design with cyan-to-lime gradient accents and blue-to-purple logo background
- Improved button styling and code highlighting to use the new brand colors
- Updated badge styling and visual elements to maintain brand consistency

## [v0.1.1] - 2025-01-27

### Fixed

- Fixed GitHub workflow validation issues in auto-release.yml
- Resolved SC2034 warning by removing unused RELEASE_TYPE variable
- Fixed SC1089 shell script syntax error in if-else-fi structure
- All GitHub workflows now pass actionlint validation with 0 errors

## [v0.1.0] - 2025-01-27

### Added

- New `vb upgrade` command that automatically checks for newer versions on GitHub releases and upgrades the binary
- Platform detection logic that automatically selects the correct binary for the current operating system and architecture
- Safe binary replacement with automatic backup creation during the upgrade process
- Support for Linux (amd64, 386, arm64, arm), macOS (amd64, arm64), and Windows (amd64, 386, arm64, arm) platforms
- Comprehensive test coverage for the upgrade functionality including mock tests and integration tests
- Updated CLI documentation with detailed upgrade command reference and platform support information

### Changed

- Updated README.md to include upgrade command in key capabilities and usage examples
- Enhanced CLI reference documentation with upgrade command details and platform support information

### Fixed

- Fixed auto-release workflow to use version from code instead of calculating from git tags
- Corrected version mismatch issues where releases were tagged with incorrect version numbers
- Improved release process robustness by ensuring tag names always match code version

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
