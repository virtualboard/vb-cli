# GitHub Actions

The CLI repository intentionally has two workflow files. Both default to
`contents: read`, pin every third-party action to a full commit SHA, set bounded
job timeouts, and are checked by `scripts/check-workflows.py`.

## Continuous integration

`.github/workflows/ci.yml` runs for pushes and pull requests targeting `main` or
`dev`. Its single verification job performs:

1. exact Go 1.25.0 setup and module download/verification;
2. `gofmt` and workflow-contract checks;
3. `go vet ./...`;
4. `go test -race ./...`;
5. `make test` using the unmodified coverage profile;
6. `make scan` with a pinned gosec version; and
7. a clean `go build`.

The same reusable workflow runs native core, lifecycle, and integration-install
tests on macOS and Windows. A release cannot bypass those platform jobs.

The workflow does not publish artifacts, mutate source, create tags, or open
pull requests.

## Release

`.github/workflows/release.yml` runs only when a tag matching `v*.*.*` is pushed.
The verify job applies a stricter regular expression and requires the tag to
equal `internal/version.Current`; malformed or mismatched tags fail before any
build or publication.

The build matrix produces exactly:

- `vb-linux-amd64`
- `vb-linux-arm64`
- `vb-macos-amd64`
- `vb-macos-arm64`
- `vb-windows-amd64.exe`
- `vb-windows-arm64.exe`

The workflow derives the selected template release from `cmd/init.go`; there is
no second hand-maintained template version in workflow YAML. One job downloads
the stable template ZIP and checksum manifest over bounded HTTPS, verifies its
embedded `version.txt`, and uploads one immutable internal artifact. Every build
job consumes that same artifact and digest. The digest is injected into the
binary as `cmd.templateArchiveSHA256`, allowing `vb init` to reject a substituted
scaffold.

Before the platform matrix starts, a native built binary consumes that exact
local archive and completes `init → validate → index --check → install cursor →
install opencode`. This catches mismatches between the archive digest and the
compiled integration inventories before publication.

The publish job has the only `contents: write` permission. A preceding read-only
assembly job verifies the exact six-binary asset set and creates `checksums.txt`;
a dedicated OIDC job attaches GitHub build-provenance attestations. A separately
tested publisher is passed into the write job as an immutable workflow artifact,
so publication never checks out source while holding write authority.

Publication creates or resumes only a draft whose exact marker, tag, target
commit, title, and prerelease state bind it to the current tag commit. It
revalidates that identity before reconciling partial assets, then requires the
exact seven-file names, nonzero sizes, uploaded states, and available SHA-256
digests before one `draft: false` API transition. Any published same-tag release
is refused. Failed or ambiguous operations retain the validated draft; an
ambiguous publish is re-fetched and no code path deletes a release.

## Dependency updates

`.github/dependabot.yml` proposes weekly, grouped pull requests for Go modules
and GitHub Actions. Updates pass through the same CI checks and have no release
authority.

## Local workflow validation

```bash
python3 scripts/check-workflows.py
python3 scripts/test_publish_github_release.py -v
```

This dependency-free check validates workflow inventory, least-privilege
defaults, action commit pins, required CI gates, strict release/version checks,
stable template assets, digest injection, supported binaries, checksums, and
source-bound draft publication. The state-machine tests cover published-release
refusal, mismatched drafts, partial asset reconciliation, and ambiguous create
and publish responses.

End users should follow [Release verification](RELEASE_VERIFICATION.md): pin the
version in both asset and checksum URLs, require exactly one manifest match,
verify SHA-256 and the reported CLI version, and optionally verify the GitHub
provenance attestation.
