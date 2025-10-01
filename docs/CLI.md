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

## Exit Codes

`vb` surfaces rich exit codes to indicate validation errors, not found resources, lock conflicts, and more. Refer to `cmd/exit.go` for the complete list.
