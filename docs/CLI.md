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
Initialise the current directory with the VirtualBoard template. Downloads the latest `virtualboard/template-base` archive, extracts it into `.virtualboard`, and recommends git version control. Use `--force` to re-create an existing workspace (previous contents are removed).

### `vb new <title> [labels...]`
Create a new feature spec in the backlog using the canonical template.

### `vb move <id> <status> [owner]`
Move a feature between workflow statuses and optionally assign an owner.

### `vb update <id>`
Modify front-matter fields or body sections using `--field key=value` and `--body-section section=content` flags.

### `vb delete <id>`
Delete a feature spec. Confirmation is required unless `--force` is provided.

### `vb index`
Generate indexes in Markdown, JSON, or HTML via `--format`. Use `--output` to specify a destination.

### `vb validate [id|all]`
Validate feature specs against schema, workflow rules, and dependency checks. Use `--fix` to reapply templates.

### `vb template apply <id>`
Reapply the canonical template to ensure required sections and defaults exist.

### `vb lock <id>`
Acquire, check, or release feature locks. Flags include `--ttl`, `--owner`, `--release`, `--status`, and `--force`.

### `vb version`
Print the CLI semantic version (supports JSON output).

## Exit Codes

`vb` surfaces rich exit codes to indicate validation errors, not found resources, lock conflicts, and more. Refer to `cmd/exit.go` for the complete list.
