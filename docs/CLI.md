# vb CLI Reference

## Overview

`vb` is a command-line interface for managing VirtualBoard feature specifications. It streamlines feature lifecycle tasks such as creation, updates, validation, indexing, and lock management within a repository structured around the VirtualBoard spec format.

## Global Flags

- `--json` – Output results as structured JSON.
- `--verbose` – Enable informative logging.
- `--dry-run` – Simulate actions without modifying files.
- `--root` – Set the repository root. An explicit flag wins; otherwise vb uses
  `VIRTUALBOARD_ROOT`, then the current directory.
- `--log-file` – Write verbose logs to a file.
- `--actor <id>` – Stable mutation identity. Mutating commands require this flag, `VIRTUALBOARD_ACTOR`, or `AGENT_ID`; the OS username is never used as an agent identity.

Actor values are self-asserted coordination identifiers, not authentication.
The CLI prevents accidental overlap among cooperative callers sharing protected
storage; it does not replace OS access control or an authenticated distributed
orchestrator.

## Commands

### `vb init`
Initialise the current directory from the exact pinned `virtualboard/template-base` release. Release binaries embed the archive SHA-256 and refuse an unverified or incomplete scaffold.

**Flags:**
- `--force` – Refresh framework files while preserving features, specs, reports, archives, locks, and runtime state
- `--update` – Update an existing workspace to the pinned template version (interactive file-by-file diff and apply)
- `--files <file1,file2,...>` – When using `--update`, only update specific files (comma-separated list of relative paths)
- `--yes` – Automatically apply all changes without prompting (only valid with `--update`)

`--json` never grants mutation consent: combine it with `--yes` to apply changes, or with `--dry-run` to inspect them. `VB_TEMPLATE_ARCHIVE_FILE` may point at a local copy of the pinned archive for offline/release rehearsal; the compiled SHA-256 is still mandatory and verified.

**Update Workflow:**
When using `--update`, vb will:
1. Fetch and verify the exact pinned template archive
2. Compare it with your local `.virtualboard/` directory
3. Show an enhanced summary with line counts (added, modified, removed files with statistics)
4. For each change, display a color-coded unified diff and prompt for confirmation
5. Apply selected changes with a final content-and-mode compare-and-swap; a file created, edited, removed, or chmodded after comparison is reported as a conflict instead of being overwritten
6. Preserve unmanaged and customized retired files; removal atomically moves the current path into unique recovery storage under `.state/template-retired/` and verifies its bytes and mode there before completing, while replacements retain the prior file under `.state/template-replaced/`
7. Repair mode-only drift and advance `.template-version` and `.template-manifest.json` only after every installed manifest entry matches the authenticated bytes and mode

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

# Inspect in JSON mode without writing
vb init --update --json --dry-run

# Apply in JSON mode with explicit consent
vb init --update --json --yes
```

### `vb install <ide>`
Install VirtualBoard integration for a supported IDE.

**Supported IDEs:**
- `claude` – Claude Code (requires the `claude` CLI to be installed)
- `cursor` – Cursor IDE (copies the scaffolded rule from `docs/.cursor`)
- `opencode` – OpenCode (copies the scaffolded integration, including the VirtualBoard skill)

**Flags:**
- `--force` – Replace differing Cursor or OpenCode files without confirmation

**Installation Details:**

*Claude Code:*
- Requires the `claude` CLI to be installed and in PATH
- Registers the exact framework release with `claude plugin marketplace add virtualboard/template-base#v0.8.0`
- Installs the marketplace-qualified plugin with `claude plugin install virtualboard@virtualboard-marketplace`
- Provides access to 10+ specialized agent roles and 30+ commands

*Cursor:*
- Accepts the fixed `docs/.cursor/rules/virtualboard.mdc` payload only when its
  SHA-256 matches the inventory compiled into this CLI release
- Rejects a symlink at the project root, `.cursor`, `.cursor/rules`, or the rule
  leaf before reading or writing the destination
- Creates missing directories with non-writable-by-group/world permissions and
  writes the rule as non-executable mode `0644`
- If the file already exists and differs, prompts for confirmation (unless `--force`)
- Atomically moves an existing rule into root-scoped recovery storage, verifies
  the captured bytes and mode against the approved snapshot, and publishes the
  authenticated rule only while the target is still absent
- Never replaces a file recreated after capture; the captured rule remains at
  the reported recovery path and blocks later installs until reconciled
- If the file is identical, reports "already up to date"

*OpenCode:*
- Requires exact set equality with the sorted path/SHA-256 inventory compiled
  into this CLI release; modified, extra, missing, symlinked, and non-regular
  source files fail before destination mutation
- Installs the VirtualBoard skill under `.opencode/skill/virtualboard`
- Installs only manifest-authorized paths, retaining their authenticated bytes
  in memory through staging so new local paths are never copied implicitly
- Normalizes installed payload files to non-executable mode `0644` and removes
  group/world write bits from integration directories
- Refuses differing files in JSON/non-interactive mode unless `--force` is explicit
- Holds an OS-backed replacement lease from the initial snapshot through
  activation, atomically moves the live tree to its journaled backup, verifies
  the complete bounded snapshot there, and publishes the stage with a
  platform no-replace rename
- Never replaces a tree recreated after capture. On ambiguity it retains the
  stage, captured tree, journal, and matching lease nonce so recovery fails
  closed and preserves both versions for reconciliation

**Examples:**

```bash
# Install Claude Code plugin
vb install claude

# Install Cursor rules
vb install cursor

# Force replace existing Cursor rules
vb install cursor --force

# Install OpenCode agents
vb install opencode

# Preview installation without changes
vb install cursor --dry-run

# JSON output for automation
vb install claude --json
```

### `vb new <title> [labels...]`
Create a new feature spec in the backlog using the canonical template.

Initialises `implementation_owner: unassigned` and `status_changed` to today. New feature slugs obey `feature.slugMaxWords` from `virtualboard.json`; validation still accepts longer immutable slugs created by v0.9. An explicit actor is required even though backlog ownership remains unassigned.

### `vb move <id> <status> [owner]`
Move a feature between workflow statuses and optionally assign an owner.

**Valid Statuses:** `backlog`, `in-progress`, `blocked`, `review`, `done`

**Allowed Transitions:**
- `backlog` → `in-progress`
- `in-progress` → `blocked`, `review`
- `blocked` → `in-progress`
- `review` → `in-progress`, `done`
- `done` → (terminal, no transitions)

**Flags:**
- `--owner <name>` – Set the owner while moving

`--owner` does not establish actor identity. Initial claims must assign the actor.
An `in-progress → review` handoff requires an explicit reviewer distinct from
`implementation_owner`; review/done validation enforces that separation.
`implementation_owner` is retained when ownership passes to a reviewer, and
review handback can only restore that preserved implementation owner. Every
successful transition updates `status_changed`; ordinary content updates do not.

### `vb update <id>`
Modify front-matter fields or body sections.

**Flags:**
- `--field key=value` – Update front-matter field (can be used multiple times)
- `--body-section section=content` – Update body section (can be used multiple times)

Lifecycle-managed fields (`id`, `status`, `owner`, `created`, `updated`, `implementation_owner`, and `status_changed`) cannot be changed with `update`. Use lifecycle commands or the explicit migration command instead.

### `vb delete <id>`
Delete an unreferenced backlog feature spec. Features in another lifecycle
state, or referenced by any other feature, cannot be deleted. Confirmation is
required unless `--force` is provided, and that confirmation is bound to the
exact authorized source path, identity, bytes, and mode observed before the
prompt; any intervening edit requires a new confirmation.

**Flags:**
- `--force` – Delete without confirmation

Feature-spec mutations first hold a global board-graph guard and then the same
per-feature guard used by user-lock operations. Existing files are journaled
and atomically captured into unique recovery storage before replacement,
movement, or deletion. The CLI verifies the captured path, identity, mode, and
SHA-256 digest, publishes new paths exclusively, and restores without
overwriting concurrent content. Ambiguous crash or external-edit states retain
the journal and captured bytes for manual reconciliation.

Modern initialized workspaces require `virtualboard.json`. The built-in
lifecycle is used only when a regular, parseable pre-v0.8 `.template-version`
or `version.txt` marker explicitly identifies a legacy workspace. For modern
workspaces, `virtualboard.json` must be a bounded regular non-symlink file and
its complete semantic JSON must match the canonical contract compiled into this
CLI release; changing authorization, role, command, plugin, path, or lifecycle
fields fails closed.

### `vb index`
Generate indexes in Markdown, JSON, or HTML. For markdown format, the command automatically detects changes by comparing the new index with the existing INDEX.md file and provides informative feedback.

Index build, comparison, and publication hold the same board-graph snapshot
guard as feature mutations, preventing a paused older generator from publishing
after a newer feature snapshot.

**Flags:**
- `--format <format>` – Index format: md, json, html (default: md)
- `--output <path>` – Output destination (default: features/INDEX.md for md format)
- `-v, --verbose` – Show detailed list of features that changed (can be used twice: `-vv` for very verbose output)
- `-q, --quiet` – Only output if there are changes detected
- `--check` – Compare the deterministic target without writing and exit nonzero on missing/stale output

Output paths must remain inside the workspace, including after symlink resolution.

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

# CI drift gate (never writes)
vb index --check
```

### `vb validate [id|name|all]`
Validate feature specs and system specs against their respective schemas and rules.

By default, validates both features and specs. Use flags to validate specific types.

**Arguments:**
- `id` – Feature ID (e.g., `FTR-001`) to validate a specific feature
- `name` – Spec filename (e.g., `tech-stack.md`) to validate a specific spec
- `all` – Validate all features and specs (default)

**Flags:**
- `--fix` – Apply safe template repairs and update `updated`; immutable feature filenames are never renamed
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
vb validate FTR-0001

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
- Required lifecycle provenance (`implementation_owner`, `status_changed`)
- Broken internal Markdown links
- `updated` must be today when git reports an uncommitted feature-file change

Every feature body must contain exactly one ordered copy of all 14 canonical
H2 sections from `Summary` through `Links`, all inside one
`<untrusted-content>` boundary. Fenced example headings do not count. Moving to
`review` requires at least one meaningful acceptance item and every acceptance
item checked; moving to `done` additionally requires meaningful implementation
evidence and a concrete artifact, work item, commit, URL, or local-path
reference in `Links`.

*Specs:*
- Schema validation against `schemas/system-spec.schema.json`
- Required fields (spec_type, title, status, last_updated, applicability)
- Valid spec types (tech-stack, database-schema, etc.)
- Valid statuses (draft, approved, deprecated)
- Date format (YYYY-MM-DD)

### `vb template apply <id>`
Reapply the canonical template to ensure required sections and defaults exist.
It also performs the one unambiguous legacy body migration where the historical
closing trust marker appears immediately before the final `Links` section; all
other duplicate, reordered, or misplaced structures fail closed.

### `vb lock <id>`
Acquire, check, or release feature locks.

The ID must use canonical `FTR-####` form and name an existing, unique feature. In v0.10, `virtualboard.json` is a validated machine-readable mirror—not a supported way to replace IDs, paths, statuses, transitions, or ownership semantics. Lock acquisition/release requires an explicit actor; `--owner` cannot impersonate one. `--status` remains read-only and does not require an actor.

Each v0.10 acquisition is published atomically with a unique 256-bit token,
which is returned only by the acquisition command. Keep that token with the
session that acquired the lock and pass it back to `--release`; status output
deliberately does not disclose it.
Per-feature filesystem guards serialize acquisition, expiry cleanup, and release
across CLI processes; every destructive step rechecks the exact token and bytes
before it removes or replaces a lock. Tokenless v0.9 lock records remain
readable and may be released through the ordinary owner-checked CLI path. A
token-bearing lock cannot be released by owner name alone, even when a stale
session and a newer session use the same owner. `--force --release` is the
explicit administrative override.

**Flags:**
- `--ttl <minutes>` – Lock TTL in minutes (default: 30; maximum: 10,080 / 7 days)
- `--owner <name>` – Owner acquiring the lock
- `--release` – Release the lock (requires `--token` for v0.10 acquisitions)
- `--token <token>` – Exact opaque token returned by the acquisition command
- `--token-only` – On a real plain-text acquisition, print only the token so a
  shell workflow can capture it without a JSON parser
- `--status` – Show lock status
- `--force` – Override an active lock

### `vb audit`

Inspect the SHA-256 hash-chained audit log at `workspace.paths.auditLog`.
Lock, unlock, and feature-mutation actions attempt a best-effort append; append
failure does not roll back the primary operation, so the log is not a
completeness proof. This command lets you query recorded entries without
resorting to `jq`/`grep` and optionally verify the integrity of their chain.

**Filter Flags (combine with AND across, OR within a repeated flag):**

- `--action <name>` – Filter by action value (e.g. `lock`, `unlock`, `move`, `create`, `delete`). Repeatable.
- `--actor <name>` – Filter by actor. Repeatable.
- `--feature-id <id>` – Filter by feature_id. Repeatable.
- `--since <time>` – Inclusive lower bound. Accepts `2026-04-16` or `2026-04-16T21:06:59Z`.
- `--until <time>` – Inclusive upper bound. Same formats.
- `--contains <substr>` – Case-insensitive substring match against the `details` column.
- `--limit <n>` – Cap retained entries (0 = no caller cap; the fixed safety
  ceiling is 100,000 entries).
- `--tail` – When `--limit` is set, return the LAST n entries instead of the first n.

**Output Flags:**

- `--format <fmt>` – One of: `human` (default), `table`, `jsonl`, `json`, `xml`, `agent`. Ignored when `--json` is set.
- `--verify` – Walk the hash chain and fail with exit code `1` (Validation) on any tampering. Prints `Audit chain verified: OK` when the chain is intact.

Queries stream filtering and verification, keep only the requested head or
bounded tail, and fail closed when fixed line, byte, malformed-line, or retained
result limits are exceeded.

**Formats:**

| Format | Description |
|--------|-------------|
| `human` | One line per entry with reformatted timestamps. Hides hashes unless `--verbose`. The default. |
| `table` | Fixed-width text table via `tabwriter`. With `--verbose`, includes truncated `prev`/`hash` columns. |
| `jsonl` | Round-trippable: one JSON object per line, identical to the on-disk format. |
| `json` | Single document `{"count": N, "entries": [...]}` for scripting. |
| `xml` | `<audit count="N"><entry>...</entry></audit>` for legacy tooling. |
| `agent` | Compact blocks separated by `--- entry N ---` headers. Every untrusted value is JSON-quoted so embedded newlines or header-like text cannot alter the structure. Empty optional fields are omitted. |

**Interaction with `--json`:**

When the global `--json` flag is set, the response is always wrapped as
`{success, message, data:{path, total, count, entries, verified, parse_errors}}`
and `--format` is ignored. This matches the convention used by other `vb`
commands: `--json` is for programmatic consumers, `--format` for presentation.

**Examples:**

```bash
# Default human-readable output
vb audit

# All lock/unlock activity as a table
vb audit --format table --action lock --action unlock

# Everything alice did to FTR-0019 last week, JSON-Lines
vb audit --format jsonl --actor alice --feature-id FTR-0019 --since 2026-04-10

# Last 5 entries whose details mention "ttl"
vb audit --contains ttl --limit 5 --tail

# Confirm the audit chain has not been tampered with
vb audit --verify

# Machine-readable summary for an agent or script
vb audit --json --since 2026-04-01
```

**Exit Codes:**

- `0` – Audit log read and rendered successfully (or empty).
- `1` – Validation error: bad `--format`, unparsable `--since`/`--until`, or `--verify` failure.
- `6` – Filesystem error reading the audit log.

### `vb migrate lifecycle-metadata`

Repair legacy `implementation_owner` and `status_changed` fields with an all-workspace preflight before any feature write.

**Flags:**

- `--implementation-owner FTR-####=actor` – Required explicit implementation provenance for every non-backlog legacy feature (repeatable); current `owner` is never treated as proof of the original implementer
- `--status-changed FTR-####=YYYY-MM-DD` – Explicit state-entry date (repeatable)
- `--force` – Administrative owner override for multi-owner legacy boards; explicit actor and all active-lock checks still apply, and the override is recorded in the canonical audit log

Backlog records can safely derive `implementation_owner=unassigned` and `status_changed=created`. In-progress/blocked records can derive implementation ownership only from a concrete current owner. Review/done implementation ownership and every non-backlog missing state-entry date require explicit mappings. Ambiguity, unknown mappings, ownership conflicts, or locks fail the complete preflight with zero feature writes. The command is idempotent and supports global `--dry-run`/`--json`.

```bash
vb --actor migration-admin migrate lifecycle-metadata \
  --implementation-owner FTR-0042=backend-agent \
  --status-changed FTR-0042=2026-07-01
```

### `vb version`
Print the CLI semantic version (supports JSON output).

### `vb upgrade`
Check for a newer stable GitHub release and atomically replace the current
binary on a supported Linux or macOS target.

**Behavior:**
- Uses a bounded GitHub API client and accepts only a valid, non-draft,
  non-prerelease semantic version
- Compares with the current version
- Requires unique HTTPS binary/checksum assets with bounded declared and actual
  sizes, then verifies the strict SHA-256 manifest
- Retains the checksum-verified download through an open file handle (unlinking
  its temporary name on Unix), copies and re-hashes that exact handle into a
  private same-filesystem stage, and verifies the exact embedded version marker
  against the release tag without executing downloaded bytes
- Serializes activation, rechecks the staged inode and digest immediately before
  the atomic rename, preserves executable mode, syncs the containing directory,
  and leaves the current binary intact on pre-activation failure
- Emits structured JSON and a nonzero status on failure; it never suggests or
  invokes implicit sudo
- Global `--dry-run` performs only the bounded release check and reports
  `update_available`; it never downloads or replaces a binary

**Platform Support:**
- Linux (amd64, arm64)
- macOS (amd64, arm64)

Windows AMD64 and ARM64 release assets are supported for manual verified
installation, but `vb upgrade` fails before download on Windows because in-place atomic
replacement of the running executable is not portable. Install to a
user-writable directory; if the current location is not writable, choose a new
location rather than rerunning the CLI with elevated privileges.

## Exit Codes

`vb` surfaces rich exit codes to indicate validation errors, not found resources, lock conflicts, and more.

| Code | Name | Description |
|------|------|-------------|
| 0 | Success | Command completed successfully |
| 1 | Validation | Validation error (schema, workflow rules) |
| 2 | NotFound | Resource not found |
| 3 | InvalidTransition | Invalid workflow transition |
| 4 | Dependency | Dependency error (cycles, missing) |
| 5 | LockConflict | Lock conflict |
| 6 | Filesystem | Filesystem error |
| 7 | Schema | Schema error |
| 8 | ExternalCommand | External command failed (e.g., `claude` CLI) |
| 10 | Unknown | Unknown error |

Refer to `cmd/exit.go` for implementation details.

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
