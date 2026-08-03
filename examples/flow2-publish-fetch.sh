#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Flow 2 — Publish → fetch across separate processes, and survive a node death.
# Reproduces docs/local-test-network.md Tier 2.
#
# PASS if: an ephemeral client publishes to a swarm and keeps nothing; a
# different process fetches bit-perfect after the publisher left; and it still
# fetches bit-perfect after one holder daemon is killed.
#
#   ./examples/flow2-publish-fetch.sh          (or SILT_REPO=/path/to/silt …)
# Binds loopback ports 7100-7103; run one example at a time.
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail

REPO="${SILT_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$REPO" || exit 1
[ -d cmd/silt ] || { echo "not a silt repo: $REPO (set SILT_REPO=/path/to/silt)"; exit 1; }
[ -x ./silt ] || { echo "building silt…"; go build -o silt ./cmd/silt || exit 1; }
SILT=./silt
W="$(pwd)/_examples_flow2"; rm -rf "$W"; mkdir -p "$W"

PIDS=()
cleanup(){ for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null; done; }
trap cleanup EXIT
await(){ local f=$1 pat=$2 i; for i in $(seq 1 40); do grep -q "$pat" "$f" 2>/dev/null && return 0; sleep 0.3; done; return 1; }

echo "=== seed daemon (hosts the registry) ==="
$SILT daemon -listen 127.0.0.1:7101 -serve-registry 127.0.0.1:7100 -store "$W/d1" -capacity 2G >"$W/d1.log" 2>&1 &
PIDS+=($!)
await "$W/d1.log" '^registry:'
SEED_PEER=$(awk '/^peer:/{print $2}' "$W/d1.log")
SEED_REG=$(grep '^registry:' "$W/d1.log" | sed -E 's/.*serving ([^ ]+).*/\1/')
echo "SEED_PEER=$SEED_PEER"; echo "SEED_REG=$SEED_REG"
[ -n "$SEED_PEER" ] && [ -n "$SEED_REG" ] || { echo "FATAL: seed did not start"; cat "$W/d1.log"; exit 1; }

echo "=== two more daemons, bootstrapped ==="
$SILT daemon -listen 127.0.0.1:7102 -store "$W/d2" -capacity 2G -bootstrap "$SEED_PEER" -registry "$SEED_REG" >"$W/d2.log" 2>&1 &
PIDS+=($!)
$SILT daemon -listen 127.0.0.1:7103 -store "$W/d3" -capacity 2G -bootstrap "$SEED_PEER" -registry "$SEED_REG" >"$W/d3.log" 2>&1 &
D3=$!; PIDS+=($D3)
await "$W/d2.log" 'bootstrapped'; await "$W/d3.log" 'bootstrapped'

echo "=== publish a 12MB file from an ephemeral client (keeps nothing) ==="
dd if=/dev/urandom of="$W/movie.bin" bs=1m count=12 2>/dev/null
$SILT swarm add "$W/movie.bin" -peers "$SEED_PEER" -registry "$SEED_REG" >"$W/add.out" 2>&1; cat "$W/add.out"
LINK=$(grep -o 'silt:v1:[^ ]*' "$W/add.out" | head -1)
[ -n "$LINK" ] || { echo "FATAL: no link"; exit 1; }

echo "=== fetch from the swarm (publisher gone) ==="
$SILT swarm get "$LINK" -o "$W/got.bin" -peers "$SEED_PEER" -registry "$SEED_REG" >/dev/null 2>&1
diff "$W/movie.bin" "$W/got.bin" >/dev/null && echo "PASS: retrieved-from-swarm bit-perfect" || { echo "FAIL"; exit 1; }

echo "=== kill a holder (d3), fetch again ==="
kill "$D3" 2>/dev/null; sleep 2
$SILT swarm get "$LINK" -o "$W/got2.bin" -peers "$SEED_PEER" -registry "$SEED_REG" >/dev/null 2>&1
diff "$W/movie.bin" "$W/got2.bin" >/dev/null && echo "PASS: survived-node-death bit-perfect" || { echo "FAIL"; exit 1; }
echo ""
echo "FLOW 2: PASS"
