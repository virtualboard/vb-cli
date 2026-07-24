# Release verification

Pin the version in every download URL. A checksum fetched through a moving
`latest` URL does not prove which release was selected.

## macOS and Linux

```bash
VERSION=v0.10.0
ASSET=vb-linux-amd64 # select the exact asset for the host
BASE="https://github.com/virtualboard/vb-cli/releases/download/$VERSION"

curl --fail --location --proto '=https' --proto-redir '=https' \
  --output "$ASSET" "$BASE/$ASSET"
curl --fail --location --proto '=https' --proto-redir '=https' \
  --output checksums.txt "$BASE/checksums.txt"

EXPECTED=$(awk -v asset="$ASSET" \
  '$2 == "./" asset || $2 == asset || $2 == "*" asset {print $1}' \
  checksums.txt)
test "$(printf '%s\n' "$EXPECTED" | sed '/^$/d' | wc -l | tr -d ' ')" = 1
[[ "$EXPECTED" =~ ^[0-9a-f]{64}$ ]]
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$ASSET" | awk '{print $1}')
else
  ACTUAL=$(shasum -a 256 "$ASSET" | awk '{print $1}')
fi
test "$ACTUAL" = "$EXPECTED"
install -m 0755 "$ASSET" "$HOME/.local/bin/vb"
test "$("$HOME/.local/bin/vb" version)" = "$VERSION"
```

Use `vb-macos-arm64`, `vb-macos-amd64`, `vb-linux-amd64`, or
`vb-linux-arm64` as appropriate.

## Windows PowerShell

```powershell
$Version = "v0.10.0"
$Architecture = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
$Asset = "vb-windows-$Architecture.exe"
$Base = "https://github.com/virtualboard/vb-cli/releases/download/$Version"
Invoke-WebRequest -Uri "$Base/$Asset" -OutFile $Asset
Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile "checksums.txt"

$Entries = Get-Content .\checksums.txt | Where-Object {
  $_ -match "^[0-9a-f]{64} [ *](\./)?$([regex]::Escape($Asset))$"
}
if ($Entries.Count -ne 1) { throw "Expected exactly one checksum entry" }
$Expected = ($Entries[0] -split " ")[0].ToLowerInvariant()
$Actual = (Get-FileHash ".\$Asset" -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw "SHA-256 checksum mismatch" }
if ((& ".\$Asset" version) -ne $Version) { throw "Version mismatch" }
```

Windows AMD64 and ARM64 releases support verified manual replacement; `vb
upgrade` does not replace a running Windows executable.

## Build provenance

Release assets and `checksums.txt` receive GitHub artifact provenance
attestations. With the GitHub CLI installed, verify the downloaded asset against
this repository:

```bash
gh attestation verify "$ASSET" --repo virtualboard/vb-cli
```

The attestation identifies the workflow and source commit that produced the
artifact. It complements the pinned checksum check; it does not replace it.
