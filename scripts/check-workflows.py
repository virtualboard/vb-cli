#!/usr/bin/env python3
"""Fail closed when CI or release workflow safety invariants drift."""

from __future__ import annotations

import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW_ROOT = ROOT / ".github" / "workflows"


def require(text: str, fragment: str, label: str, failures: list[str]) -> None:
    if fragment not in text:
        failures.append(f"{label}: missing {fragment!r}")


def main() -> int:
    failures: list[str] = []
    workflow_paths = sorted(WORKFLOW_ROOT.glob("*.yml"))
    if [path.name for path in workflow_paths] != ["ci.yml", "release.yml"]:
        failures.append("workflow inventory must contain only ci.yml and release.yml")

    workflows = {
        path.name: path.read_text(encoding="utf-8") for path in workflow_paths
    }
    for name, text in workflows.items():
        if not re.search(r"(?m)^permissions:\s*\n\s+contents: read$", text):
            failures.append(f"{name}: missing least-privilege default permissions")
        references = re.findall(
            r"(?m)^\s*-?\s*uses:\s*([^\s#]+)", text
        )
        for reference in references:
            if reference == "./.github/workflows/ci.yml":
                continue
            if not re.fullmatch(r"[^@\s]+@[0-9a-f]{40}", reference):
                failures.append(f"{name}: action is not commit pinned: {reference}")

    ci = workflows.get("ci.yml", "")
    for fragment in (
        "go mod verify",
        "go vet ./...",
        "go test -race ./...",
        "make test",
        "make scan",
        "go build -trimpath -buildvcs=false ./...",
        'GO_VERSION: "1.25.0"',
        "workflow_call:",
        "macos-15",
        "windows-2025",
        "Run core, lifecycle, and integration-install tests",
    ):
        require(ci, fragment, "ci.yml", failures)

    release = workflows.get("release.yml", "")
    for fragment in (
        '[[ "$RELEASE_TAG" =~ ^v[0-9]+\\.[0-9]+\\.[0-9]+$ ]]',
        'test "$SOURCE_VERSION" = "$RELEASE_TAG"',
        'GO_VERSION: "1.25.0"',
        "scripts/template-release-metadata.py --github-output",
        "uses: ./.github/workflows/ci.yml",
        "releases/download/${TEMPLATE_RELEASE}",
        "template-checksums.txt",
        "templateArchiveSHA256=${TEMPLATE_SHA256}",
        "scripts/release-smoke.sh",
        "coordinated-template-${{ github.sha }}",
        "vb-linux-amd64",
        "vb-linux-arm64",
        "vb-macos-amd64",
        "vb-macos-arm64",
        "vb-windows-amd64.exe",
        "vb-windows-arm64.exe",
        "test \"$(find . -maxdepth 1 -type f -name 'vb-*' | wc -l | tr -d ' ')\" = 6",
        "sha256sum ./vb-* > checksums.txt",
        "test \"$(wc -l < checksums.txt | tr -d ' ')\" = 6",
        "actions/attest-build-provenance@",
        "attestations: write",
        "id-token: write",
        "publisher-tool:",
        "python3 scripts/test_publish_github_release.py -v",
        "release-publisher-${{ github.sha }}-${{ github.run_attempt }}",
        "python3 release-publisher/publish_github_release.py",
        '--source-sha "$GITHUB_SHA"',
        "--asset release-assets/vb-windows-arm64.exe",
        "needs: [attest, publisher-tool]",
    ):
        require(release, fragment, "release.yml", failures)
    if "template-base/archive/" in release:
        failures.append("release.yml: generated GitHub tag archives are not stable assets")
    if 'TEMPLATE_RELEASE: "v' in release:
        failures.append("release.yml: template release must be derived from CLI source")
    if "softprops/action-gh-release" in release:
        failures.append("release.yml: publication must use the source-bound draft helper")
    if release.count("releases/download/${TEMPLATE_RELEASE}") != 1:
        failures.append("release.yml: coordinated template must be downloaded exactly once")
    if release.count("--asset release-assets/") != 7:
        failures.append(
            "release.yml: publication must verify six binaries and publish seven exact assets"
        )
    try:
        before_publish, publish = release.split("\n  publish:\n", 1)
    except ValueError:
        failures.append("release.yml: publish job is missing")
    else:
        if "contents: write" in before_publish:
            failures.append("release.yml: write credentials are available before publication")
        if "actions/checkout@" in publish:
            failures.append("release.yml: publish job must not check out executable source")
        if "environment: release" not in publish:
            failures.append("release.yml: publish job must use the protected release environment")

    publisher_path = ROOT / "scripts" / "publish_github_release.py"
    publisher = publisher_path.read_text(encoding="utf-8") if publisher_path.is_file() else ""
    for fragment in (
        "virtualboard-release-draft:v1",
        '"draft": True',
        '{"draft": False}',
        "published release {self.tag} already exists",
        "draft asset count does not match",
        "publication outcome is unknown; no release cleanup was attempted",
    ):
        require(publisher, fragment, "publish_github_release.py", failures)
    if "delete_release" in publisher:
        failures.append("publish_github_release.py: release deletion is forbidden")

    smoke_path = ROOT / "scripts" / "release-smoke.sh"
    smoke = smoke_path.read_text(encoding="utf-8") if smoke_path.is_file() else ""
    for fragment in ("init", "validate", "index --check", "install cursor", "install opencode"):
        require(smoke, fragment, "release-smoke.sh", failures)

    verification_docs = (
        ROOT / "README.md",
        ROOT / "docs" / "RELEASE_VERIFICATION.md",
        ROOT / "docs" / "index.html",
        ROOT / "docs" / "install.html",
    )
    for path in verification_docs:
        text = path.read_text(encoding="utf-8") if path.is_file() else ""
        if "releases/latest/download" in text:
            failures.append(f"{path.relative_to(ROOT)}: moving latest asset URL is forbidden")
    release_verification = (ROOT / "docs" / "RELEASE_VERIFICATION.md").read_text(
        encoding="utf-8"
    )
    for fragment in ("checksums.txt", "Get-FileHash", "sha256sum", "gh attestation verify"):
        require(release_verification, fragment, "RELEASE_VERIFICATION.md", failures)

    if failures:
        raise SystemExit("\n".join(failures))
    print(f"Verified {len(workflows)} workflow contracts")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
