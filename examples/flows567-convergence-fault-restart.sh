#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Flows 5, 6, 7 — the three-validator field test (roadmap #52), as a runnable
# operator playbook.
#   5 Convergence      : every replica agrees on the committed history.
#   6 Fault tolerance  : kill one validator, a survivor quorum still commits.
#   7 Restart survival : a validator reloads its bond with NO re-plot, standing
#                        returns, and its chain CATCHES UP; a storage node
#                        re-announces and still serves its content.
#
# It uses `silt id` to learn each validator's NodeID before launch (so the
# `-attesters <ID>` wiring is fillable up front) and `silt chain-status` to
# confirm convergence by head hash — no throwaway probe daemons, no hashing
# chain.cbor by hand.
#
#   ./examples/flows567-convergence-fault-restart.sh          # from the repo
#   SILT_REPO=/path/to/silt examples/flows567-...sh           # from anywhere
# Binds loopback ports 7100-7103; run one example at a time.
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail

REPO="${SILT_REPO:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$REPO" || exit 1
[ -d cmd/silt ] || { echo "not a silt repo: $REPO (set SILT_REPO=/path/to/silt)"; exit 1; }
[ -x ./silt ] || { echo "building silt…"; go build -o silt ./cmd/silt || exit 1; }
SILT=./silt
W="$(pwd)/_examples_flows567"; rm -rf "$W"; mkdir -p "$W"

PIDS=()
cleanup(){ for p in "${PIDS[@]:-}"; do kill "$p" 2>/dev/null; done; }
trap cleanup EXIT

# Learn each validator's NodeID up front — no daemon launch needed (was F1).
IDA=$($SILT id -id-seed 1); IDB=$($SILT id -id-seed 2); IDC=$($SILT id -id-seed 3)
echo "IDA=$IDA"; echo "IDB=$IDB"; echo "IDC=$IDC"

LAST_PID=""
start_val(){ # seed port store attesters [extra args...]
  local seed=$1 port=$2 store=$3 att=$4; shift 4
  $SILT daemon -id-seed "$seed" -listen "127.0.0.1:$port" -store "$W/$store" \
    -validator -objective=false -min-rep 100 -quorum 1 -attesters "$att" -bond 8M -bond-audit 1s \
    -capacity 1G -log info "$@" >"$W/$store.stdout" 2>&1 &
  LAST_PID=$!; PIDS+=("$LAST_PID")
}
await(){ local f=$1 pat=$2 i; for i in $(seq 1 40); do grep -q "$pat" "$f" 2>/dev/null && return 0; sleep 0.3; done; return 1; }
head_hash(){ $SILT chain-status -store "$W/$1" 2>/dev/null | awk '/head hash:/{print $3}'; }

echo "=== start B, C, then A (A serves the registry); all mutually attest ==="
start_val 2 7102 dB "$IDA,$IDC";                        DB=$LAST_PID
await "$W/dB.stdout" '^peer:'
PEERB=$(awk '/^peer:/{print $2}' "$W/dB.stdout")
start_val 3 7103 dC "$IDA,$IDB" -bootstrap "$PEERB";    DC=$LAST_PID
start_val 1 7101 dA "$IDB,$IDC" -serve-registry 127.0.0.1:7100 -bootstrap "$PEERB"
await "$W/dA.stdout" '^registry:'
REGA=$(grep '^registry:' "$W/dA.stdout" | sed -E 's/.*serving ([^ ]+).*/\1/')
PEERA=$(awk '/^peer:/{print $2}' "$W/dA.stdout")
echo "standing accrual (14s)…"; sleep 14

echo ""
echo "################ FLOW 5: CONVERGENCE ################"
dd if=/dev/urandom of="$W/f1.bin" bs=1m count=2 2>/dev/null
$SILT swarm add "$W/f1.bin" -peers "$PEERA" -registry "$REGA" >"$W/add1.out" 2>&1
grep -o 'silt:v1:[^ ]*' "$W/add1.out" | head -1 > "$W/link1"; sleep 3
for n in dA dB dC; do
  echo "$n: $(grep -i 'committed block' "$W/$n.stdout" | tail -1)"
  $SILT chain-status -store "$W/$n" | sed 's/^/    /'
done
HA=$(head_hash dA); HB=$(head_hash dB); HC=$(head_hash dC)
if [ -n "$HA" ] && [ "$HA" = "$HB" ] && [ "$HB" = "$HC" ]; then
  echo "FLOW 5: PASS (all three replicas report the same head hash: ${HA:0:16}…)"
else echo "FLOW 5: FAIL (head hashes differ: A=$HA B=$HB C=$HC)"; exit 1; fi

echo ""
echo "################ FLOW 6: FAULT TOLERANCE ################"
echo "kill C"; kill "$DC" 2>/dev/null; sleep 2
dd if=/dev/urandom of="$W/f2.bin" bs=1m count=2 2>/dev/null
$SILT swarm add "$W/f2.bin" -peers "$PEERA" -registry "$REGA" >"$W/add2.out" 2>&1
LINK2=$(grep -o 'silt:v1:[^ ]*' "$W/add2.out" | head -1); sleep 3
for n in dA dB; do echo "$n: $(grep -i 'committed block' "$W/$n.stdout" | tail -1)"; done
$SILT swarm get "$LINK2" -o "$W/got2.bin" -peers "$PEERA" -registry "$REGA" >/dev/null 2>&1
if grep -qi 'committed block 2' "$W/dA.stdout" && grep -qi 'committed block 2' "$W/dB.stdout" && diff "$W/f2.bin" "$W/got2.bin" >/dev/null 2>&1; then
  echo "FLOW 6: PASS (survivor quorum committed block 2, fetch bit-perfect)"
else echo "FLOW 6: FAIL"; exit 1; fi

echo ""
echo "################ FLOW 7: RESTART SURVIVAL ################"
echo "--- 7a: restart validator C on the same -store (expect NO re-plot; chain catches up) ---"
HA_NOW=$(head_hash dA)
mv "$W/dC.stdout" "$W/dC.boot1.stdout"
start_val 3 7103 dC "$IDA,$IDB" -bootstrap "$PEERA"
await "$W/dC.stdout" '^peer:'; sleep 8
echo "C: $(grep -iE 'reloaded.*bond|no re-plot' "$W/dC.stdout" | tail -1)"
echo "C standing: $(grep 'reputation=' "$W/dC/debug.log" 2>/dev/null | tail -1)"
HC_NOW=$(head_hash dC)
if grep -qi 'no re-plot' "$W/dC.stdout" && [ -n "$HA_NOW" ] && [ "$HA_NOW" = "$HC_NOW" ]; then
  echo "FLOW 7a: PASS (bond reloaded no re-plot; C caught up to head ${HC_NOW:0:16}…)"
else echo "FLOW 7a: FAIL (A head=$HA_NOW  C head=$HC_NOW)"; exit 1; fi

echo "--- 7b: restart storage node B on the same -store (content still served) ---"
LINK1=$(cat "$W/link1")
echo "kill B"; kill "$DB" 2>/dev/null; sleep 2
mv "$W/dB.stdout" "$W/dB.boot1.stdout"
start_val 2 7102 dB "$IDA,$IDC" -bootstrap "$PEERA"
await "$W/dB.stdout" '^peer:'; sleep 3
echo "B: $(grep -iE 're-announced' "$W/dB.stdout" | tail -1)"
$SILT swarm get "$LINK1" -o "$W/got1r.bin" -peers "$PEERA" -registry "$REGA" >/dev/null 2>&1
if diff "$W/f1.bin" "$W/got1r.bin" >/dev/null 2>&1; then echo "FLOW 7b: PASS (content served after restart)"; else echo "FLOW 7b: FAIL"; exit 1; fi
echo ""
echo "FLOWS 5/6/7: PASS"
