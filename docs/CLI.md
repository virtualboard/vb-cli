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
Generate indexes in Markdown, JSON, or HTML. For markdown format, the command automatically detects changes by comparing the new index with the existing INDEX.md file and provides informative feedback.

**Flags:**
- `--format <format>` – Index format: md, json, html (default: md)
- `--output <path>` – Output destination (default: features/INDEX.md for md format)
- `-v, --verbose` – Show detailed list of features that changed (can be used twice: `-vv` for very verbose output)
- `-q, --quiet` – Only output if there are changes detected

**Change Detection (Markdown format only):**
- Default: Shows summary of changes ("No changes", "3 added, 2 transitioned, 1 removed")
- `-v`: Shows detailed list with feature IDs grouped by change type (Added, Status Changes, Metadata Changes, Removed)
- `-vv`: Shows very verbose output with symbols and full change details
- `-q`: Suppresses all output when no changes are detected (useful for CI/automation)

**Examples:**

```bash
# Generate index with default output
vb index

# Show detailed changes
vb index -v

# Very verbose with full details
vb index -vv

# Quiet mode for automation (only output if changes)
vb index -q

# Generate HTML index
vb index --format html --output docs/features.html
```

### `vb validate [id|all]`
Validate feature specs against schema, workflow rules, and dependency checks.

**Flags:**
- `--fix` – Apply safe fixes before validating (reapply templates and sync filenames with titles)

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

## Common Issues and Troubleshooting

### Invalid Feature Files

When running `vb validate` or `vb index`, you may encounter an error about invalid feature files:

```
found 2 invalid feature files:
  - .virtualboard/features/backlog/notes.md: invalid feature spec: missing frontmatter
  - .virtualboard/features/backlog/draft.md: failed to parse frontmatter: yaml: line 2: mapping values are not allowed

These files do not follow the feature spec format. Please review and move them to another directory if they are not feature specs.
```

**Cause:** The `.virtualboard/features/` directory contains markdown files that don't follow the required feature spec format (YAML frontmatter delimited by `---`).

**Solution:**
1. Review the listed files
2. Move non-feature files (notes, documentation, drafts) to a different directory
3. Fix any malformed frontmatter in actual feature specs
4. Ensure all feature files start with properly formatted YAML frontmatter:

```markdown
---
id: FTR-0001
title: Feature Title
status: backlog
owner: ""
priority: medium
complexity: medium
created: 2024-01-01
updated: 2024-01-01
labels: []
dependencies: []
---

## Overview
...
```
