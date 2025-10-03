# vb CLI Reference

## Overview

`vb` is a command-line interface for managing VirtualBoard feature specifications. It streamlines feature lifecycle tasks such as creation, updates, validation, indexing, and lock management within a repository structured around the VirtualBoard spec format.

## Global Flags

- `--json` – Output results as structured JSON.
- `--verbose` – Enable informative logging.
- `--dry-run` – Simulate actions without modifying files.
- `--root` – Set the repository root (defaults to current directory).
- `--log-file` – Write verbose logs to a file.

## Commands

### `vb init`
Initialise the current directory with the VirtualBoard template. Downloads the latest `virtualboard/template-base` archive, extracts it into `.virtualboard`, and recommends git version control.

**Flags:**
- `--force` – Re-create an existing workspace (previous contents are removed)

### `vb new <title> [labels...]`
Create a new feature spec in the backlog using the canonical template.

### `vb move <id> <status> [owner]`
Move a feature between workflow statuses and optionally assign an owner.

**Flags:**
- `--owner <name>` – Set the owner while moving

### `vb update <id>`
Modify front-matter fields or body sections.

**Flags:**
- `--field key=value` – Update front-matter field (can be used multiple times)
- `--body-section section=content` – Update body section (can be used multiple times)

### `vb delete <id>`
Delete a feature spec. Confirmation is required unless `--force` is provided.

**Flags:**
- `--force` – Delete without confirmation

### `vb index`
Generate indexes in Markdown, JSON, or HTML.

**Flags:**
- `--format <format>` – Index format: md, json, html (default: md)
- `--output <path>` – Output destination (default: features/INDEX.md for md format)

### `vb validate [id|all]`
Validate feature specs against schema, workflow rules, and dependency checks.

**Flags:**
- `--fix` – Apply safe fixes before validating (reapply templates)

### `vb template apply <id>`
Reapply the canonical template to ensure required sections and defaults exist.

### `vb lock <id>`
Acquire, check, or release feature locks.

**Flags:**
- `--ttl <minutes>` – Lock TTL in minutes (default: 30)
- `--owner <name>` – Owner acquiring the lock
- `--release` – Release the lock
- `--status` – Show lock status
- `--force` – Override an active lock

### `vb version`
Print the CLI semantic version (supports JSON output).

### `vb art`
Render the VirtualBoard logo as colored ASCII art in the terminal.

**Behavior:**
- Locates the avatar.png file in the docs/ directory
- Converts the image to colored ASCII art using ANSI color codes
- Scales the image to fit within 80 characters width with proper aspect ratio
- Compensates for terminal character aspect ratio (2:1 height:width) to prevent stretching
- Uses a range of ASCII characters from light to dark based on pixel brightness
- Preserves original colors using 24-bit ANSI color codes

### `vb upgrade`
Check for a newer version of vb on GitHub releases and upgrade the binary if available. The command automatically detects the current platform and downloads the appropriate binary for your system.

**Behavior:**
- Checks the latest release on GitHub
- Compares with the current version
- If no update is available, shows current version and confirms you're up to date
- If an update is available, downloads the platform-specific binary
- Safely replaces the current binary with the new one
- Creates a backup during the replacement process
- Provides clear feedback about the upgrade status
- Supports JSON output format

**Output Messages:**
- When up to date: "You are already running the latest version (vX.Y.Z)"
- When upgraded: "Successfully upgraded from vX.Y.Z to vA.B.C"
- When permission denied: "upgrade failed: permission denied. Please run with sudo to upgrade the binary"

**Platform Support:**
- Linux (amd64, 386, arm64, arm)
- macOS (amd64, arm64)
- Windows (amd64, 386, arm64, arm)

**Note:** On Unix-like systems, you may need to run with `sudo` to upgrade the binary if it's installed in a system directory like `/usr/local/bin`.

## Exit Codes

`vb` surfaces rich exit codes to indicate validation errors, not found resources, lock conflicts, and more. Refer to `cmd/exit.go` for the complete list.
