#!/bin/bash

# Version bump script for vb-cli
# This script updates the version in internal/version/version.go

set -e

VERSION_FILE="internal/version/version.go"
CURRENT_VERSION=$(grep 'const Current = ' "$VERSION_FILE" | sed 's/.*"\(.*\)".*/\1/')

if [ -z "$1" ]; then
    echo "Usage: $0 <new-version>"
    echo "Current version: $CURRENT_VERSION"
    exit 1
fi

NEW_VERSION="$1"

# Validate version format (should start with 'v')
if [[ ! "$NEW_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc)?$ ]]; then
    echo "Error: Version must be in format vX.Y.Z or vX.Y.Z-rc"
    echo "Example: v1.0.0 or v1.0.0-rc"
    exit 1
fi

echo "Updating version from $CURRENT_VERSION to $NEW_VERSION"

# Update the version file
sed -i.bak "s/const Current = \"$CURRENT_VERSION\"/const Current = \"$NEW_VERSION\"/" "$VERSION_FILE"
rm "$VERSION_FILE.bak"

echo "Version updated successfully to $NEW_VERSION"

# Verify the change
UPDATED_VERSION=$(grep 'const Current = ' "$VERSION_FILE" | sed 's/.*"\(.*\)".*/\1/')
if [ "$UPDATED_VERSION" = "$NEW_VERSION" ]; then
    echo "✓ Version file updated correctly"
else
    echo "✗ Error: Version file update failed"
    exit 1
fi
