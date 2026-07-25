#!/usr/bin/env bash
# Cross-compile the BitBit desktop client for Mac, Windows, and Linux
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

echo "Building bitbit $VERSION for ${#targets[@]} targets…"
for t in "${targets[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  ext=""; [ "$os" = "windows" ] && ext=".exe"
  name="$OUT/bitbit-$VERSION-$os-$arch$ext"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w" -o "$name" ./cmd/bitbit
  printf "  %-28s %s\n" "$os/$arch" "$(du -h "$name" | cut -f1)"
done

echo
echo "Done. Each is a single self-contained binary; run:"
echo "  ./$OUT/bitbit-$VERSION-<os>-<arch>  client   # desktop app (serves + consumes, opens UI)"
echo "  ./$OUT/bitbit-$VERSION-<os>-<arch>  daemon    # headless swarm node"
