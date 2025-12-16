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
- `--update` – Update an existing workspace to the latest template version (interactive file-by-file diff and apply)
- `--files <file1,file2,...>` – When using `--update`, only update specific files (comma-separated list of relative paths)
- `--yes` – Automatically apply all changes without prompting (only valid with `--update`)

**Update Workflow:**
When using `--update`, vb will:
1. Fetch the latest template from GitHub
2. Compare it with your local `.virtualboard/` directory
3. Show an enhanced summary with line counts (added, modified, removed files with statistics)
4. For each change, display a color-coded unified diff and prompt for confirmation
5. Apply selected changes and track the template version in `.template-version`

**Interactive Prompts (Yeoman-style):**
During `--update`, you'll be prompted with these options for each file:
- **y** – Apply this change
- **n** – Skip this change
- **a** – Apply this change and all remaining changes automatically
- **q** – Quit update process (no more changes will be applied)
- **d** – Show the diff/content again (up to 5 times per file)
- **e** – Open file in $EDITOR for manual merging
- **h** – Show help text with all options

**Enhanced Features:**
- **Color-coded diffs**: Green for additions (+), red for deletions (-), cyan for headers
- **Line count statistics**: See how many lines are added/removed in each file and overall
- **Diff pagination**: Long diffs automatically open in your $PAGER (less/more)
- **Manual editing**: Use 'e' to open files in your editor for manual conflict resolution
- **Repeatable viewing**: Press 'd' to review diffs multiple times before deciding

**Examples:**

```bash
# Initialize a new workspace
vb init

# Update all template files interactively (with enhanced prompts)
vb init --update

# Update automatically without prompts
vb init --update --yes

# Update only specific files
vb init --update --files README.md,schema.json

# Preview changes without applying (dry-run)
vb init --update --dry-run

# Update automatically in JSON mode (no prompts, machine-readable output)
vb init --update --json
```

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

### `vb validate [id|name|all]`
Validate feature specs and system specs against their respective schemas and rules.

By default, validates both features and specs. Use flags to validate specific types.

**Arguments:**
- `id` – Feature ID (e.g., `FTR-001`) to validate a specific feature
- `name` – Spec filename (e.g., `tech-stack.md`) to validate a specific spec
- `all` – Validate all features and specs (default)

**Flags:**
- `--fix` – Apply safe fixes before validating (features only: reapply templates and sync filenames with titles)
- `--only-features` – Validate only feature specs
- `--only-specs` – Validate only system specs

**Examples:**

```bash
# Validate all features and specs
vb validate

# Validate only features
vb validate --only-features

# Validate only specs
vb validate --only-specs

# Validate specific feature by ID
vb validate FTR-001

# Validate specific spec by filename
vb validate tech-stack.md
```

**Validation Rules:**

*Features:*
- Schema validation against `schemas/frontmatter.schema.json`
- Workflow rules (status/directory consistency)
- Dependency validation (cycles, missing dependencies)
- Filename format (`{id}-{slug}.md`)
- Date format (YYYY-MM-DD)

*Specs:*
- Schema validation against `schemas/system-spec.schema.json`
- Required fields (spec_type, title, status, last_updated, applicability)
- Valid spec types (tech-stack, database-schema, etc.)
- Valid statuses (draft, approved, deprecated)
- Date format (YYYY-MM-DD)

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
