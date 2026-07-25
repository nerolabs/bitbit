#!/usr/bin/env bash
# Cross-compile the Silt desktop client for Mac, Windows, and Linux
# from one source tree — no cgo, no native toolkit, so this is just
# `go build` with GOOS/GOARCH set. The embedded web UI (go:embed) is
# baked into each binary; there is nothing else to ship.
set -euo pipefail
cd "$(dirname "$0")"

VERSION="${1:-dev}"
OUT="dist"
mkdir -p "$OUT"

targets=(
  "darwin/amd64"   "darwin/arm64"     # Intel + Apple Silicon Macs
  "windows/amd64"                     # Windows
  "linux/amd64"    "linux/arm64"      # Linux (servers, Raspberry Pi)
)

echo "Building silt $VERSION for ${#targets[@]} targets…"
for t in "${targets[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  ext=""; [ "$os" = "windows" ] && ext=".exe"
  name="$OUT/silt-$VERSION-$os-$arch$ext"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w" -o "$name" ./cmd/silt
  printf "  %-28s %s\n" "$os/$arch" "$(du -h "$name" | cut -f1)"
done

# Checksums so anyone can verify a download — the binaries aren't
# code-signed yet, so SHA256SUMS is how you know you got what we built.
( cd "$OUT" && { sha256sum silt-* 2>/dev/null || shasum -a 256 silt-*; } > SHA256SUMS )
echo "  wrote SHA256SUMS ($(wc -l < "$OUT/SHA256SUMS" | tr -d ' ') files)"

echo
echo "Done. Each is a single self-contained binary; run:"
echo "  ./$OUT/silt-$VERSION-<os>-<arch>  client   # desktop app (serves + consumes, opens UI)"
echo "  ./$OUT/silt-$VERSION-<os>-<arch>  daemon    # headless swarm node"
