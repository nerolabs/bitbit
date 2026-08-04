#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Flow 7 sub-claim — "the tokens it issued stay valid across a restart."
# Publisher-privacy path end to end: validators require blind publish tokens
# (-require-tokens), a client acquires one (-token-quorum) so the committed entry
# carries a TOKEN and no Publisher, then the issuing validator is RESTARTED and
# must still issue tokens its peers accept — proving the issuer key persisted
# (#126) and peers' cached copies of it didn't go stale.
#
# PASS if:
#   (negative) with -require-tokens on, a publish WITHOUT a token is REFUSED;
#   (positive) a tokened publish commits; after restarting the issuer, its
#              issuer.key is byte-identical (no new key) and a SECOND tokened
#              publish still commits (the reloaded key's tokens still verify).
#
#   ./examples/flow-tokens-issuer-restart.sh     (or SILT_REPO=/path/to/silt …)
# Binds loopback ports 7100-7102; run one example at a time.
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail

REPO="${SILT_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$REPO" || exit 1
[ -d cmd/silt ] || { echo "not a silt repo: $REPO (set SILT_REPO=/path/to/silt)"; exit 1; }
[ -x ./silt ] || { echo "building silt…"; go build -o silt ./cmd/silt || exit 1; }
SILT=./silt
W="$(pwd)/_examples_tokens"; rm -rf "$W"; mkdir -p "$W"

PIDS=()
cleanup(){ for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null; done; }
trap cleanup EXIT
await(){ local f=$1 pat=$2 i; for i in $(seq 1 40); do grep -q "$pat" "$f" 2>/dev/null && return 0; sleep 0.3; done; return 1; }

IDA=$($SILT id -id-seed 1); IDB=$($SILT id -id-seed 2)
echo "IDA=$IDA"; echo "IDB=$IDB"

LAST_PID=""
start_val(){ # seed port store attesters [extra args...]
  local seed=$1 port=$2 store=$3 att=$4; shift 4
  $SILT daemon -id-seed "$seed" -listen "127.0.0.1:$port" -store "$W/$store" \
    -validator -objective=false -min-rep 100 -quorum 1 -attesters "$att" -require-tokens 1 \
    -bond 8M -bond-audit 1s -capacity 1G -log info "$@" >"$W/$store.stdout" 2>&1 &
  LAST_PID=$!; PIDS+=("$LAST_PID")
}

echo "=== start B, then A (A serves the registry + issues tokens); both require tokens ==="
start_val 2 7102 dB "$IDA";                                    DB=$LAST_PID
await "$W/dB.stdout" '^peer:'
PEERB=$(awk '/^peer:/{print $2}' "$W/dB.stdout")
start_val 1 7101 dA "$IDB" -serve-registry 127.0.0.1:7100 -bootstrap "$PEERB"
await "$W/dA.stdout" '^registry:'
REGA=$(grep '^registry:' "$W/dA.stdout" | sed -E 's/.*serving ([^ ]+).*/\1/')
PEERA=$(awk '/^peer:/{print $2}' "$W/dA.stdout")
echo "standing accrual (14s)…"; sleep 14

echo ""
echo "################ NEGATIVE: a token-less publish is REFUSED ################"
dd if=/dev/urandom of="$W/f0.bin" bs=1m count=1 2>/dev/null
$SILT swarm add "$W/f0.bin" -peers "$PEERA" -registry "$REGA" >"$W/add0.out" 2>&1 || true
echo "publish output: $(tail -1 "$W/add0.out")"
if grep -qiE 'no publish token|token.*required' "$W/add0.out"; then
  echo "NEGATIVE: PASS (token-less publish refused — 'entry has no publish token')"
else echo "NEGATIVE: FAIL (token-less publish was not refused)"; exit 1; fi

echo ""
echo "################ POSITIVE: a tokened publish commits ################"
dd if=/dev/urandom of="$W/f1.bin" bs=1m count=1 2>/dev/null
$SILT swarm add "$W/f1.bin" -token-quorum 1 -peers "$PEERA" -registry "$REGA" >"$W/add1.out" 2>&1
echo "publish output: $(tail -1 "$W/add1.out")"; sleep 3
H1=$($SILT chain-status -store "$W/dA" | awk '/head height/{print $3}')
grep -qi 'committed block' "$W/dA.stdout" && [ "${H1:-0}" -ge 1 ] || { echo "POSITIVE: FAIL (tokened publish did not commit)"; exit 1; }
echo "committed at head height $H1 (tokened, no Publisher)"

echo ""
echo "################ RESTART THE ISSUER, then issue again ################"
KEYHASH_BEFORE=$(shasum -a 256 "$W/dA/issuer/issuer.key" | awk '{print $1}')
echo "kill A (the issuer)"; kill "${PIDS[1]}" 2>/dev/null; sleep 2   # PIDS[1] = A (B started first)
mv "$W/dA.stdout" "$W/dA.boot1.stdout"
start_val 1 7101 dA "$IDB" -serve-registry 127.0.0.1:7100 -bootstrap "$PEERB"
await "$W/dA.stdout" '^registry:'
REGA=$(grep '^registry:' "$W/dA.stdout" | sed -E 's/.*serving ([^ ]+).*/\1/')
PEERA=$(awk '/^peer:/{print $2}' "$W/dA.stdout")
KEYHASH_AFTER=$(shasum -a 256 "$W/dA/issuer/issuer.key" | awk '{print $1}')
echo "A: $(grep -iE 'reloaded.*bond|no re-plot' "$W/dA.stdout" | tail -1)"
echo "issuer key unchanged: $([ "$KEYHASH_BEFORE" = "$KEYHASH_AFTER" ] && echo yes || echo NO)"
echo "standing re-accrual (14s)…"; sleep 14

dd if=/dev/urandom of="$W/f2.bin" bs=1m count=1 2>/dev/null
$SILT swarm add "$W/f2.bin" -token-quorum 1 -peers "$PEERA" -registry "$REGA" >"$W/add2.out" 2>&1
echo "publish output: $(tail -1 "$W/add2.out")"; sleep 3
H2=$($SILT chain-status -store "$W/dA" | awk '/head height/{print $3}')

if [ "$KEYHASH_BEFORE" = "$KEYHASH_AFTER" ] && [ "${H2:-0}" -gt "${H1:-0}" ]; then
  echo ""
  echo "FLOW (tokens survive restart): PASS"
  echo "  issuer key reloaded byte-identical (no re-mint), and a token issued by the"
  echo "  RESTARTED issuer still committed (head height $H1 → $H2) — peers accept it."
else
  echo "FLOW: FAIL (keyBefore=$KEYHASH_BEFORE keyAfter=$KEYHASH_AFTER  h1=$H1 h2=$H2)"; exit 1
fi
