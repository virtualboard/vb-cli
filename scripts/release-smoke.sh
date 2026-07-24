#!/usr/bin/env bash

# Exercise a built CLI against the exact local template archive that will be
# released. The binary still verifies its compiled archive digest; the local
# path only removes publication/network ordering from the rehearsal.

set -Eeuo pipefail

if [[ $# -ne 3 ]]; then
    echo "usage: $0 <vb-binary> <template-archive> <template-release>" >&2
    exit 2
fi

VB=$(cd -- "$(dirname -- "$1")" && pwd -P)/$(basename -- "$1")
ARCHIVE=$(cd -- "$(dirname -- "$2")" && pwd -P)/$(basename -- "$2")
TEMPLATE_RELEASE=$3

[[ -x "$VB" ]] || { echo "release-smoke binary is not executable: $VB" >&2; exit 1; }
[[ -f "$ARCHIVE" && ! -L "$ARCHIVE" ]] || { echo "template archive is not a regular file: $ARCHIVE" >&2; exit 1; }
[[ "$TEMPLATE_RELEASE" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "invalid template release: $TEMPLATE_RELEASE" >&2; exit 1; }

WORK=$(mktemp -d "${TMPDIR:-/tmp}/vb-release-smoke.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
APP_ROOT="$WORK/app"
mkdir -p "$APP_ROOT"

export VB_TEMPLATE_ARCHIVE_FILE="$ARCHIVE"
"$VB" --root "$APP_ROOT" init
VB_ROOT="$APP_ROOT/.virtualboard"

test "$(tr -d '[:space:]' < "$VB_ROOT/.template-version")" = "${TEMPLATE_RELEASE#v}"
"$VB" --root "$VB_ROOT" validate
"$VB" --root "$VB_ROOT" index --check
"$VB" --root "$VB_ROOT" install cursor
"$VB" --root "$VB_ROOT" install opencode

cmp "$VB_ROOT/docs/.cursor/rules/virtualboard.mdc" \
    "$APP_ROOT/.cursor/rules/virtualboard.mdc"
cmp "$VB_ROOT/docs/.opencode/skill/virtualboard/SKILL.md" \
    "$APP_ROOT/.opencode/skill/virtualboard/SKILL.md"

echo "Release smoke passed for $($VB version) with $TEMPLATE_RELEASE"
