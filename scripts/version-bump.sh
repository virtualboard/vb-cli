#!/usr/bin/env bash

# Version bump script for vb-cli
# This script updates the version in internal/version/version.go

set -Eeuo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$ROOT"

VERSION_FILE="internal/version/version.go"
CURRENT_VERSION=$(sed -n 's/^const Current = "\([^"]*\)"$/\1/p' "$VERSION_FILE")

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <new-version>"
    echo "Current version: $CURRENT_VERSION"
    exit 1
fi

NEW_VERSION="$1"

# The release workflow intentionally accepts stable tags only.
if [[ ! "$NEW_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: Version must be a stable release in format vX.Y.Z"
    echo "Example: v1.0.0"
    exit 1
fi

echo "Updating version from $CURRENT_VERSION to $NEW_VERSION"

# Update the version file
sed -i.bak "s/const Current = \"$CURRENT_VERSION\"/const Current = \"$NEW_VERSION\"/" "$VERSION_FILE"
rm -- "$VERSION_FILE.bak"

echo "Version updated successfully to $NEW_VERSION"

# Verify the change
UPDATED_VERSION=$(sed -n 's/^const Current = "\([^"]*\)"$/\1/p' "$VERSION_FILE")
if [ "$UPDATED_VERSION" = "$NEW_VERSION" ]; then
    echo "✓ Version file updated correctly"
else
    echo "✗ Error: Version file update failed"
    exit 1
fi
