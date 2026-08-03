#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Flow 4 — Become a validator, EARN standing, commit a publish through consensus.
# Reproduces docs/user-seam.md Role 4.
#
# PASS if:
#   (negative) a lone validator with no bonded attester is REFUSED — proving the
#              path is EARNED standing, not a rubber-stamp; then
#   (positive) two mutually-auditing validators accrue standing (rep > min-rep),
#              a publish commits on both, the fetch is bit-perfect, and the bond
#              plot + chain persist.
#
# `silt id` learns each peer's NodeID before launch, so `-attesters <ID>` is
# fillable up front (no throwaway probe daemon needed).
#
#   ./examples/flow4-earned-standing.sh        (or SILT_REPO=/path/to/silt …)
# Binds loopback ports 7100-7102; run one example at a time.
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail

REPO="${SILT_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$REPO" || exit 1
[ -d cmd/silt ] || { echo "not a silt repo: $REPO (set SILT_REPO=/path/to/silt)"; exit 1; }
[ -x ./silt ] || { echo "building silt…"; go build -o silt ./cmd/silt || exit 1; }
SILT=./silt
W="$(pwd)/_examples_flow4"; rm -rf "$W"; mkdir -p "$W"

PIDS=()
cleanup(){ for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null; done; }
trap cleanup EXIT
await(){ local f=$1 pat=$2 i; for i in $(seq 1 40); do grep -q "$pat" "$f" 2>/dev/null && return 0; sleep 0.3; done; return 1; }

# Learn the two validator IDs before launch — no daemon needed (was F1).
IDA=$($SILT id -id-seed 1); IDB=$($SILT id -id-seed 2)
echo "IDA=$IDA"; echo "IDB=$IDB"

echo ""
echo "########## NEGATIVE CONTROL — no earned standing must be REFUSED ##########"
$SILT daemon -id-seed 1 -listen 127.0.0.1:7101 -serve-registry 127.0.0.1:7100 -store "$W/neg" \
  -validator -min-rep 100 -quorum 1 -attesters "$IDB" -bond 8M -bond-audit 1s -capacity 1G -log info >"$W/neg.log" 2>&1 &
PIDS+=($!)
await "$W/neg.log" '^registry:'
REGN=$(grep '^registry:' "$W/neg.log" | sed -E 's/.*serving ([^ ]+).*/\1/'); PEERN=$(awk '/^peer:/{print $2}' "$W/neg.log")
dd if=/dev/urandom of="$W/f_neg.bin" bs=1k count=64 2>/dev/null
$SILT swarm add "$W/f_neg.bin" -peers "$PEERN" -registry "$REGN" >"$W/neg_add.out" 2>&1 || true
echo "publish output: $(cat "$W/neg_add.out")"
if grep -qi 'committed block' "$W/neg.log"; then echo "NEGATIVE: FAIL (unexpected commit — rubber-stamp!)"; exit 1; else echo "NEGATIVE: PASS (unbonded publish refused, no commit)"; fi
kill "${PIDS[0]}" 2>/dev/null; sleep 1

echo ""
echo "########## POSITIVE — two validators earn standing, then commit ##########"
$SILT daemon -id-seed 2 -listen 127.0.0.1:7102 -store "$W/dB" \
  -validator -min-rep 100 -quorum 1 -attesters "$IDA" -bond 8M -bond-audit 1s -capacity 1G -log info >"$W/B.log" 2>&1 &
PIDS+=($!)
await "$W/B.log" '^peer:'
PEERB=$(awk '/^peer:/{print $2}' "$W/B.log")
$SILT daemon -id-seed 1 -listen 127.0.0.1:7101 -serve-registry 127.0.0.1:7100 -store "$W/dA" \
  -validator -min-rep 100 -quorum 1 -attesters "$IDB" -bootstrap "$PEERB" -bond 8M -bond-audit 1s -capacity 1G -log info >"$W/A.log" 2>&1 &
PIDS+=($!)
await "$W/A.log" '^registry:'
REGA=$(grep '^registry:' "$W/A.log" | sed -E 's/.*serving ([^ ]+).*/\1/'); PEERA=$(awk '/^peer:/{print $2}' "$W/A.log")

echo "letting bond audits accrue standing (12s)…"; sleep 12
echo "standing on A: $(grep 'reputation=' "$W/dA/debug.log" 2>/dev/null | tail -1)"

dd if=/dev/urandom of="$W/f_pos.bin" bs=1m count=2 2>/dev/null
$SILT swarm add "$W/f_pos.bin" -peers "$PEERA" -registry "$REGA" >"$W/pos_add.out" 2>&1
POSLINK=$(grep -o 'silt:v1:[^ ]*' "$W/pos_add.out" | head -1); sleep 3
echo "A commit: $(grep -i 'committed block' "$W/A.log" | tail -1)"
echo "B commit: $(grep -i 'committed block' "$W/B.log" | tail -1)"
$SILT swarm get "$POSLINK" -o "$W/pos_got.bin" -peers "$PEERA" -registry "$REGA" >/dev/null 2>&1
echo "chain on A: $($SILT chain-status -store "$W/dA" | awk '/head height/{print}')"
OK=1
grep -qi 'committed block' "$W/A.log" && grep -qi 'committed block' "$W/B.log" || OK=0
diff "$W/f_pos.bin" "$W/pos_got.bin" >/dev/null 2>&1 || OK=0
[ -d "$W/dA/plot" ] && [ -d "$W/dB/plot" ] && [ -f "$W/dA/chain.cbor" ] || OK=0
[ "$OK" = 1 ] && echo "POSITIVE: PASS (earned-standing commit + fetch + persisted bond/chain)" || { echo "POSITIVE: FAIL"; exit 1; }
echo ""
echo "FLOW 4: PASS"
