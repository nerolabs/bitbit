#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Flow 8 — Per-hash takedown on ONE operator; nothing is removed everywhere.
# Reproduces docs/safety-denylist.md + user-seam.md Role 3 (-denylist).
#
# PASS if: the same file (convergent → identical root) is served by two
# independent operators; after operator A loads a -denylist with that root, A
# purges it and refuses to serve, while operator B still serves it. No global
# switch — takedown is per-hash and per-operator.
#
#   ./examples/flow8-takedown.sh        (or SILT_REPO=/path/to/silt …)
# Binds loopback ports 7100/7101 (op A) and 7200/7201 (op B); one at a time.
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail

REPO="${SILT_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$REPO" || exit 1
[ -d cmd/silt ] || { echo "not a silt repo: $REPO (set SILT_REPO=/path/to/silt)"; exit 1; }
[ -x ./silt ] || { echo "building silt…"; go build -o silt ./cmd/silt || exit 1; }
SILT=./silt
W="$(pwd)/_examples_flow8"; rm -rf "$W"; mkdir -p "$W"

PIDS=()
cleanup(){ for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null; done; }
trap cleanup EXIT
await(){ local f=$1 pat=$2 i; for i in $(seq 1 40); do grep -q "$pat" "$f" 2>/dev/null && return 0; sleep 0.3; done; return 1; }

dd if=/dev/urandom of="$W/doc.bin" bs=1m count=2 2>/dev/null
LAST_PID=""
start_seed(){ # seed listen regport store [extra args...]
  local seed=$1 listen=$2 regport=$3 store=$4; shift 4
  $SILT daemon -id-seed "$seed" -listen "127.0.0.1:$listen" -serve-registry "127.0.0.1:$regport" \
    -store "$W/$store" -capacity 2G -log info "$@" >"$W/$store.stdout" 2>&1 &
  LAST_PID=$!; PIDS+=("$LAST_PID")
}

echo "=== operator A (will deny) + operator B (never denies), independent ==="
start_seed 1 7101 7100 opA; DA=$LAST_PID
start_seed 2 7201 7200 opB; DB=$LAST_PID
await "$W/opA.stdout" '^registry:'; await "$W/opB.stdout" '^registry:'
PEERA=$(awk '/^peer:/{print $2}' "$W/opA.stdout"); REGA=$(grep '^registry:' "$W/opA.stdout" | sed -E 's/.*serving ([^ ]+).*/\1/')
PEERB=$(awk '/^peer:/{print $2}' "$W/opB.stdout"); REGB=$(grep '^registry:' "$W/opB.stdout" | sed -E 's/.*serving ([^ ]+).*/\1/')

echo "=== publish the same file to both (convergent → same root) ==="
# -mode convergent is REQUIRED for the identical-root premise: swarm add defaults to
# -mode private (random per-file key, H6), which would give two DIFFERENT roots and
# make this test deny one root while confirming an unrelated one still serves. With
# convergent, both operators hold the SAME root, so the takedown is tested on it.
$SILT swarm add "$W/doc.bin" -mode convergent -peers "$PEERA" -registry "$REGA" >"$W/addA.out" 2>&1
$SILT swarm add "$W/doc.bin" -mode convergent -peers "$PEERB" -registry "$REGB" >"$W/addB.out" 2>&1
LINK=$(grep -o 'silt:v1:[^ ]*' "$W/addA.out" | head -1); LINKB=$(grep -o 'silt:v1:[^ ]*' "$W/addB.out" | head -1)
ROOTHEX=$($SILT info "$LINK" -store "$W/opA" 2>/dev/null | awk '/^root:/{print $2}')
echo "link A: $LINK"; echo "link B: $LINKB"; echo "ROOTHEX=$ROOTHEX"

echo "=== baseline: both serve ==="
$SILT swarm get "$LINK"  -o "$W/beforeA.bin" -peers "$PEERA" -registry "$REGA" >/dev/null 2>&1; diff "$W/doc.bin" "$W/beforeA.bin" >/dev/null && echo "A serves: YES" || echo "A serves: NO"
$SILT swarm get "$LINKB" -o "$W/beforeB.bin" -peers "$PEERB" -registry "$REGB" >/dev/null 2>&1; diff "$W/doc.bin" "$W/beforeB.bin" >/dev/null && echo "B serves: YES" || echo "B serves: NO"

echo "=== deny the root on A only; restart A with -denylist ==="
echo "$ROOTHEX" > "$W/deny.txt"
kill "$DA" 2>/dev/null; sleep 2
mv "$W/opA.stdout" "$W/opA.boot1.stdout"
start_seed 1 7101 7100 opA -denylist "$W/deny.txt"
await "$W/opA.stdout" '^registry:'
echo "A: $(grep -iE 'denylist|purged' "$W/opA.stdout" | head -1)"

echo "=== after takedown: A refuses, B still serves ==="
$SILT swarm get "$LINK"  -o "$W/afterA.bin" -peers "$PEERA" -registry "$REGA" >"$W/getA.out" 2>&1 || true
$SILT swarm get "$LINKB" -o "$W/afterB.bin" -peers "$PEERB" -registry "$REGB" >/dev/null 2>&1
A_REFUSED=1; [ -f "$W/afterA.bin" ] && diff "$W/doc.bin" "$W/afterA.bin" >/dev/null 2>&1 && A_REFUSED=0
B_SERVES=0;  diff "$W/doc.bin" "$W/afterB.bin" >/dev/null 2>&1 && B_SERVES=1
echo "A objects now: $(find "$W/opA/objects" -type f 2>/dev/null | wc -l | tr -d ' ')  (purged)"
echo "B objects now: $(find "$W/opB/objects" -type f 2>/dev/null | wc -l | tr -d ' ')  (intact)"
if [ "$A_REFUSED" = 1 ] && [ "$B_SERVES" = 1 ]; then echo "FLOW 8: PASS (A refuses here, B unaffected — per-hash, per-operator, no global switch)"; else echo "FLOW 8: FAIL (A_REFUSED=$A_REFUSED B_SERVES=$B_SERVES)"; exit 1; fi
