#!/usr/bin/env python3
"""Read and validate the template release selected by the CLI source."""

from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
INIT_SOURCE = ROOT / "cmd" / "init.go"


def read_constant(source: str, name: str) -> str:
    matches = re.findall(
        rf'^const {re.escape(name)} = "([^"]+)"$', source, re.MULTILINE
    )
    if len(matches) != 1:
        raise SystemExit(f"expected exactly one {name} constant; found {len(matches)}")
    return matches[0]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--github-output",
        type=Path,
        help="append release metadata to this GitHub Actions output file",
    )
    args = parser.parse_args()

    source = INIT_SOURCE.read_text(encoding="utf-8")
    release = read_constant(source, "templateRelease")
    declared_version = read_constant(source, "templateVersion")
    if not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", release):
        raise SystemExit(f"invalid templateRelease: {release!r}")
    version = release[1:]
    if declared_version != version:
        raise SystemExit(
            f"templateRelease {release!r} and templateVersion {declared_version!r} disagree"
        )

    metadata = {
        "release": release,
        "version": version,
        "asset": f"template-base-{release}.zip",
    }
    if args.github_output:
        with args.github_output.open("a", encoding="utf-8") as output:
            for key, value in metadata.items():
                output.write(f"{key}={value}\n")
    print(json.dumps(metadata, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
