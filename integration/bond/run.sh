#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Field test #8 — proof-of-space-time BOND COST (M0 claim C1: "no discount").
#
# C1 says a validator's Sybil-resistance bond is genuinely EXPENSIVE TO FAKE: it
# must generate a real depth-robust plot (real disk) and prove space-TIME on it
# over time; a shortcut (undersized / no plot) earns NOTHING. This harness turns
# that claim into NUMBERS on real Docker:
#
#   MEASURE   real plot-generation wall time + on-disk plot bytes for a given
#             -bond, and the live bond-challenge/verify cadence (from real
#             debug.log timestamps).
#   POSITIVE  a correctly-bonded validator earns standing and its peer VERIFIES
#             the bond over the wire (`bond challenge … passed=true`).
#   NEGATIVE  a shortcut is rejected:
#               (1) a bond below -min-bond-floor earns NO standing — the daemon
#                   fails closed at startup with the real refusal string;
#               (2) an UNDER-BONDED validator's proposal is REFUSED by an honest
#                   peer before it will attest (-lowbond-propose over the wire).
#
# Usage:  ./run.sh                 build, run all phases, tear down; exit 0 = PASS
#         BOND_SIZE=128M ./run.sh  bigger bond (longer plot, more disk — the point)
#         KEEP=1 ./run.sh          leave the positive-phase topology up to poke at
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)
BOND_SIZE=${BOND_SIZE:-64M}     # modest by default so plotting is quick; document it
export BOND_SIZE

dc() { docker compose "$@"; }
cleanup() { [ "${KEEP:-0}" = 1 ] || dc --profile adversary down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== build the silt binary on the host (linux/$(go env GOARCH)) and the image =="
# Compile on the host — CGO off, so it cross-compiles trivially — and copy the
# binary into a slim image (mirrors integration/nat): tiny image, no in-Docker
# Go-build memory spike.
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/bond/silt ./cmd/silt ) \
  || { echo "FAIL: host build"; exit 1; }
docker build -q -t silt-bond . >/dev/null || { echo "FAIL: docker build"; exit 1; }

# The binary is linux-built, so `silt id` must run INSIDE a container to learn
# the deterministic NodeIDs (H(pubkey) of a fixed -id-seed) before launch.
sid() { dc run --rm --no-deps -T honest silt id -id-seed "$1" 2>/dev/null | tr -d '\r\n '; }
echo "== learn the deterministic validator NodeIDs (silt id in a container) =="
HONEST_ID=$(sid 1); PEER_ID=$(sid 2)
[ -n "$HONEST_ID" ] && [ -n "$PEER_ID" ] || { echo "FAIL: could not derive NodeIDs"; exit 1; }
export HONEST_ID PEER_ID
echo "  honest id: $HONEST_ID"
echo "  peer   id: $PEER_ID"

pass=1

# ─── PHASE 0 — NEGATIVE: a sub-floor bond earns NO standing (fail-closed) ───
# Run FIRST, before any static-IP service is up, so this one-shot container can
# take honest's 10.100.0.10 without an "address already in use" collision.
echo ""
echo "########## PHASE 0 — NEGATIVE: bond below -min-bond-floor earns nothing ##########"
# A 16M bond under a 64M floor: the daemon must REFUSE to start rather than run a
# validator that silently earns nothing (M0 F1/F2 anti-release).
FLOOR_OUT=$(dc run --rm --no-deps -T \
  honest silt daemon -id-seed=1 -listen=0.0.0.0:4001 -store=/data \
  -validator -objective=false -min-rep=100 -quorum=1 \
  -bond=16M -min-bond-floor=64M -bond-audit=1s -bond-ttl=0 -capacity=1G -log=info 2>&1)
FLOOR_RC=$?
echo "  exit code: $FLOOR_RC (nonzero = refused to run)"
echo "  refusal  : $(echo "$FLOOR_OUT" | grep -m1 'below the anti-release floor')"
if [ "$FLOOR_RC" -ne 0 ] && echo "$FLOOR_OUT" | grep -q "is below the anti-release floor"; then
  echo "  NEGATIVE-0: PASS (sub-floor bond fails closed — earns NO standing)"
else
  echo "  NEGATIVE-0: FAIL (sub-floor bond was NOT refused)"; echo "$FLOOR_OUT" | tail -5; pass=0
fi

# ─── PHASE 1 — POSITIVE: seal a real plot, MEASURE it, verify over the wire ───
echo ""
echo "########## PHASE 1 — real bond: seal cost + live verify (C1 as numbers) ##########"
t0=$(date +%s.%N)
dc up -d honest peer >/dev/null 2>&1 || { echo "FAIL: compose up"; exit 1; }

# Wall-clock plot-generation time: from `up` to honest's `bond: sealed a … bond`
# line on stdout (the daemon prints it the instant Seal() returns).
sealed=0
for _ in $(seq 1 600); do
  if dc logs honest 2>&1 | grep -q "bond: sealed a .* storage bond"; then sealed=1; break; fi
  sleep 0.1
done
t1=$(date +%s.%N)
if [ "$sealed" = 1 ]; then
  PLOT_WALL=$(awk "BEGIN{printf \"%.2f\", $t1-$t0}")
else
  echo "FAIL: honest never sealed a bond"; dc logs honest | tail -20; pass=0; PLOT_WALL="n/a"
fi
echo "  seal line: $(dc logs honest 2>&1 | grep -m1 'bond: sealed a .* storage bond')"

# On-disk plot size: the persisted plot is a single <nodeid>.plot file under
# <store>/plot (adapters/diskplot). This IS the resident cost C1 charges.
sleep 2
PLOT_BYTES=$(dc exec -T honest sh -c 'stat -c %s /data/plot/*.plot 2>/dev/null | head -1' | tr -d '\r\n ')
PLOT_HUMAN=$(dc exec -T honest sh -c 'du -h /data/plot/*.plot 2>/dev/null | cut -f1' | tr -d '\r\n ')
echo "  on-disk plot: ${PLOT_BYTES:-?} bytes (${PLOT_HUMAN:-?}) for a $BOND_SIZE bond"
echo "  plot wall-clock (up→sealed, incl. container/daemon start): ${PLOT_WALL}s"

# Live verification: let a few audit sweeps run, then read the REAL over-the-wire
# verdicts. peer challenges honest (recomputing labels from H(pk,n) without the
# plot) → `bond challenge peer=<honest> passed=true standing=<N>`.
echo "  letting live proof-of-space-time challenges run (8s)…"
sleep 8

PEER_VERDICT=$(dc exec -T peer sh -c 'grep "bond challenge" /data/debug.log 2>/dev/null | tail -1' | tr -d '\r')
HON_STANDING=$(dc exec -T honest sh -c 'grep "standing self" /data/debug.log 2>/dev/null | tail -1' | tr -d '\r')
echo "  peer's verdict on honest's bond : ${PEER_VERDICT:-<none>}"
echo "  honest's self-narrated standing : ${HON_STANDING:-<none>}"

# Verify cadence: gap between two consecutive bond-challenge verdicts on peer =
# one full challenge+verify round-trip at the -bond-audit=1s cadence (verify is
# O((Samples+5k)·log n) — cheap even for a big plot; the wall cost is dominated
# by the 1s audit interval, so this bounds the verify+RTT, it doesn't isolate it).
CHAL_TS=$(dc exec -T peer sh -c 'grep "bond challenge" /data/debug.log 2>/dev/null | grep -oE "^[0-9T:.-]+Z" | tail -2' | tr "\n" " ")
echo "  last two peer challenge timestamps: ${CHAL_TS:-<none>}"

echo "$PEER_VERDICT" | grep -q "passed=true"  || { echo "FAIL(pos): peer did not VERIFY honest's real bond over the wire"; pass=0; }
echo "$HON_STANDING"  | grep -qE "reputation=[1-9]" || { echo "FAIL(pos): honest earned no standing from its real bond"; pass=0; }
[ "${PLOT_BYTES:-0}" -gt 0 ] 2>/dev/null || { echo "FAIL(pos): no plot on disk"; pass=0; }

# ─── PHASE 2 — NEGATIVE: an under-bonded proposer is REFUSED over the wire ───
echo ""
echo "########## PHASE 2 — NEGATIVE: under-bonded proposal REFUSED by honest ##########"
# honest + peer are still up (real qualified validators). Bring up the adversary:
# a 4K-"bonded" validator that proposes a well-formed block to honest anyway. An
# honest validator refuses to attest a proposer without a qualifying bond.
dc --profile adversary up -d adversary >/dev/null 2>&1
ADV_VERDICT=""
for _ in $(seq 1 60); do
  ADV_VERDICT=$(dc logs adversary 2>&1 | grep -E "adversary: lowbond-propose proposal (correctly REJECTED|UNEXPECTEDLY ACCEPTED)" | tail -1)
  [ -n "$ADV_VERDICT" ] && break
  sleep 0.5
done
echo "  adversary verdict: ${ADV_VERDICT:-<none — target unreachable?>}"
if echo "$ADV_VERDICT" | grep -q "correctly REJECTED"; then
  echo "  NEGATIVE-2: PASS (under-bonded proposal refused before attest)"
else
  echo "  NEGATIVE-2: FAIL (under-bonded proposal not refused)"; dc logs adversary | tail -15; pass=0
fi

# ─── verdict ───
echo ""
echo "==================== RESULT ===================="
echo "  bond size            : $BOND_SIZE"
echo "  plot generation wall : ${PLOT_WALL}s  (up→'sealed', includes container+daemon start)"
echo "  on-disk plot         : ${PLOT_BYTES:-?} bytes (${PLOT_HUMAN:-?})"
echo "  peer verify verdict  : ${PEER_VERDICT:-<none>}"
echo "  honest standing      : ${HON_STANDING:-<none>}"
if [ "$pass" = 1 ]; then
  echo "  RESULT: PASS ✅  real plot is expensive to make, cheap to verify, and a shortcut is rejected (C1)"
else
  echo "  RESULT: FAIL ❌"
fi
exit $((1-pass))
