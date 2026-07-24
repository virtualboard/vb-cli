# Claude Code Guidance

Follow [AGENTS.md](AGENTS.md) as the authoritative repository workflow. This
file adds no separate lifecycle or release rules.

Before editing, inspect the relevant command, internal package, and focused
tests. Preserve these invariants:

- explicit `--actor`/`VIRTUALBOARD_ACTOR`/`AGENT_ID` for mutations;
- contract-driven paths, statuses, transitions, IDs, owners, and filenames;
- ownership and active-lock enforcement;
- immutable lifecycle-managed fields and feature basenames;
- deterministic indexes and truthful dry-run/JSON results;
- bounded, checksummed HTTPS release downloads and atomic replacement; and
- nonzero process status for every structured failure.

Use exactly Go 1.25.0 locally and in CI; do not rely on a floating 1.25 toolchain.

Run the complete gate before handoff:

```bash
gofmt -w <changed-go-files>
go vet ./...
go test -race ./...
make test
make scan
python3 scripts/check-workflows.py
go build -trimpath -buildvcs=false ./...
```

Coverage must come from the unmodified Go coverage profile and meet the
configured threshold. Never normalize, rewrite, or fabricate counters.

Update `README.md`, `docs/CLI.md`, `docs/DEVELOPMENT.md`, and `CHANGELOG.md` for
public behavior. Keep `internal/version.Current` synchronized with the intended
semantic release tag. Do not push, tag, publish, replace a release, install
globally, or perform another external/destructive action without explicit user
authorization.
