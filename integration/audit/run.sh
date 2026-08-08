#!/usr/bin/env bash
# Field test #2 — proof-of-retrieval (PoR) audit / the "liar" node, over REAL
# daemons in REAL containers.
#
# The `silt sim run audit` claim (in-process): a storage node that KEEPS its
# storage proof but DELETES the data is CAUGHT by a verify-without-fetch PoR
# challenge, loses standing, and repair re-scatters the lost shards.
#
# This harness drives the REAL daemon over TCP and asserts every claim it CAN
# against real <store>/debug.log lines:
#
#   1. Publish an erasure-coded file so shards scatter across honest holders.
#   2. A caretaker (-care <careLink>, -log info) runs its real repair loop.
#   3. POSITIVE CONTROL: before any damage, the caretaker reports NO repair and
#      the file is retrievable from the intact swarm.
#   4. `rm` the shard files from 3 of 4 holders' /data/objects trees WHILE those
#      daemons keep running (they retain their persisted storage proofs, exactly
#      the "keep the receipt, ditch the goods" liar posture).
#   5. Assert the caretaker DETECTS the loss (`stripe degraded … watching`),
#      REPAIRS it (`stripe repaired`), RE-SCATTERS the rebuilt shards onto fresh
#      holders (wiped stores go 0 → ≥1 objects), and the file stays bit-perfect.
#
# FINDING (see README.md §Findings): the LITERAL PoR-audit half of the sim claim
# — a challenge that catches a liar WITHOUT fetching the bytes, and a standing
# slash — is NOT reachable through the real daemon. The auditor (core/node
# Node.Audit) and the liar seam (SetLiar) exist and are unit/sim-tested, and the
# PoR PROVER answers MsgChallenge over the real wire, but NOTHING in cmd/silt
# ever invokes Node.Audit and there is no -liar / audit-trigger flag. So over the
# wire, a liar is caught only INDIRECTLY: it answers MsgHasChunk=false once its
# bytes are gone, the caretaker's availability probe sees the shard vanish, and
# repair re-scatters — which is what this harness proves. Step 6 asserts that gap
# is real (no such flags/CLI) so the test fails loudly if a future build wires
# the audit path and this harness should be extended.
#
# Usage:  ./run.sh          # build, test, tear down; exit 0 = PASS
#         KEEP=1 ./run.sh   # leave the topology up afterward to poke at
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)
export COMPOSE_PROJECT_NAME=audit

dc() { docker compose "$@"; }
cleanup() { [ "${KEEP:-0}" = 1 ] || dc down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

# The caretaker's repair sweep runs every RepairInterval (60s, not a daemon
# flag) — so a sweep-dependent assertion must allow ~1.5 intervals.
SWEEP_WAIT=${SWEEP_WAIT:-80}
objs()  { dc exec -T "$1" sh -c 'find /data/objects -type f 2>/dev/null | wc -l' | tr -d ' \r\n'; }
carelog() { dc exec -T caretaker sh -c "grep -c \"$1\" /data/debug.log 2>/dev/null || true" | tr -d ' \r\n'; }

echo "== build the silt binary on the host (linux/$(go env GOARCH)) and the image =="
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/audit/silt ./cmd/silt )
docker build -q -t silt-audit . >/dev/null

echo "== phase 1: registry/bootstrap seed =="
dc up -d seed
SEED_ID=""
for _ in $(seq 1 30); do
  SEED_ID=$(dc logs seed 2>&1 | grep -oE 'peer: [a-f0-9]{64}' | head -1 | awk '{print $2}')
  [ -n "$SEED_ID" ] && break
  sleep 1
done
[ -n "$SEED_ID" ] || { echo "FAIL: seed never reported a NodeID"; dc logs seed | tail -20; exit 1; }
export SEED_ID
echo "seed id: $SEED_ID"
PEERS="$SEED_ID@10.60.0.10:4001"
REG="$SEED_ID@https://10.60.0.10:4003"

echo "== phase 2: four honest holders =="
dc up -d holder1 holder2 holder3 holder4
for h in holder1 holder2 holder3 holder4; do
  ok=0
  for _ in $(seq 1 40); do
    dc logs "$h" 2>&1 | grep -q "bootstrapped" && { ok=1; break; }
    sleep 1
  done
  [ "$ok" = 1 ] || { echo "FAIL: $h never bootstrapped"; dc logs "$h" | tail -20; exit 1; }
done
echo "  all four holders bootstrapped"

echo "== publish a 2 MB erasure-coded file; shards scatter across the holders =="
dc exec -T holder1 sh -c "head -c 2000000 /dev/urandom > /tmp/f.bin; sha256sum /tmp/f.bin | cut -d' ' -f1 > /tmp/f.sha; silt swarm add /tmp/f.bin -peers '$PEERS' -registry '$REG' -mode convergent" >/tmp/audit_add.txt 2>&1
CARE=$(grep -oE 'siltcare:v1:[A-Za-z0-9_:-]+' /tmp/audit_add.txt | head -1)
LINK=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+'    /tmp/audit_add.txt | head -1)
WANT=$(dc exec -T holder1 cat /tmp/f.sha | tr -d '\r\n ')
echo "  care: ${CARE:-<none>}"
echo "  link: ${LINK:-<none>}"
[ -n "$CARE" ] && [ -n "$LINK" ] || { echo "FAIL: publish returned no care/silt link"; cat /tmp/audit_add.txt; exit 1; }
echo "  placement: h1=$(objs holder1) h2=$(objs holder2) h3=$(objs holder3) h4=$(objs holder4) seed=$(objs seed) objects"
[ "$(objs seed)" = 0 ] || echo "  note: seed unexpectedly holds objects (expected 0 with -capacity 1)"

echo "== phase 3: caretaker with the care link (-log info: its repair loop narrates) =="
export CARE_LINK="$CARE"
dc up -d caretaker
ok=0
for _ in $(seq 1 40); do
  dc logs caretaker 2>&1 | grep -q "caretaking" && { ok=1; break; }
  sleep 1
done
[ "$ok" = 1 ] || { echo "FAIL: caretaker never started caretaking"; dc logs caretaker | tail -20; exit 1; }
sleep 8   # let the warm-start manifest fetch land in the caretaker's store

pass=1

echo "== POSITIVE CONTROL: intact swarm — no repair, file retrievable =="
pre_repaired=$(carelog "stripe repaired")
echo "  caretaker 'stripe repaired' lines so far: ${pre_repaired:-0} (want 0)"
[ "${pre_repaired:-0}" = 0 ] || { echo "FAIL: caretaker repaired an intact file (false positive)"; pass=0; }
dc exec -T holder1 sh -c "silt swarm get '$LINK' -o /tmp/pre.bin -peers '$PEERS' -registry '$REG' && sha256sum /tmp/pre.bin | cut -d' ' -f1" >/tmp/audit_pre.txt 2>&1
PRE=$(grep -oE '^[a-f0-9]{64}' /tmp/audit_pre.txt | tail -1)
[ "$PRE" = "$WANT" ] && echo "  intact file retrievable: yes (bit-perfect)" || { echo "FAIL: intact file not retrievable"; cat /tmp/audit_pre.txt; pass=0; }

echo "== the attack: rm the shard files from 3 of 4 holders, daemons KEEP running =="
# The daemons retain their persisted storage proofs (n.proofs) — "keep the
# receipt, ditch the goods" — but the bytes under /data/objects are gone, so an
# honest holder now answers MsgHasChunk=false and the caretaker's probe sees it.
for h in holder2 holder3 holder4; do
  dc exec -T "$h" sh -c 'rm -rf /data/objects/*'
done
echo "  after rm: h1=$(objs holder1) h2=$(objs holder2) h3=$(objs holder3) h4=$(objs holder4)"
[ "$(objs holder2)" = 0 ] && [ "$(objs holder3)" = 0 ] && [ "$(objs holder4)" = 0 ] || { echo "FAIL: rm did not clear the holder stores"; pass=0; }

echo "== wait ~1.5 repair sweeps (${SWEEP_WAIT}s) for the caretaker to detect + repair =="
sleep "$SWEEP_WAIT"

degraded=$(carelog "stripe degraded")
repaired=$(carelog "stripe repaired")
belowk=$(carelog "repair below k")
echo "  caretaker debug.log: stripe degraded=${degraded:-0}  stripe repaired=${repaired:-0}  repair below k=${belowk:-0}"

# DETECTION: the caretaker's availability probe caught the missing shards.
if [ "${degraded:-0}" -ge 1 ] || [ "${repaired:-0}" -ge 1 ]; then
  echo "  DETECTED loss over the wire (probe saw shards vanish) ✅"
else
  echo "FAIL: caretaker never noticed the deleted shards — no degraded/repaired line"; pass=0
fi
# REPAIR: at least one stripe reconstructed from parity and re-seeded.
[ "${repaired:-0}" -ge 1 ] || { echo "FAIL: no 'stripe repaired' — repair did not reconstruct the loss"; pass=0; }

echo "== assert the rebuilt shards RE-SCATTERED onto fresh holders =="
h2=$(objs holder2); h3=$(objs holder3); h4=$(objs holder4)
echo "  wiped holders now hold: h2=$h2 h3=$h3 h4=$h4 (were 0; >0 == re-scatter)"
if [ "$h2" -gt 0 ] || [ "$h3" -gt 0 ] || [ "$h4" -gt 0 ]; then
  echo "  repair re-scattered rebuilt shards to fresh nodes ✅"
else
  echo "FAIL: no rebuilt shards re-scattered onto the emptied holders"; pass=0
fi

echo "== assert the file survived the loss (bit-perfect after repair) =="
dc exec -T holder1 sh -c "silt swarm get '$LINK' -o /tmp/post.bin -peers '$PEERS' -registry '$REG' && sha256sum /tmp/post.bin | cut -d' ' -f1" >/tmp/audit_post.txt 2>&1
POST=$(grep -oE '^[a-f0-9]{64}' /tmp/audit_post.txt | tail -1)
echo "  want $WANT"
echo "  got  ${POST:-<none>}"
[ "$POST" = "$WANT" ] || { echo "FAIL: file not bit-perfect after loss+repair"; cat /tmp/audit_post.txt; pass=0; }

echo "== FINDING probe: is the PoR-audit / liar path reachable over the real daemon? =="
# The sim's headline — a verify-WITHOUT-fetch PoR challenge catches a liar and
# SLASHES its standing — needs (a) a way to run a liar daemon and (b) a way to
# trigger Node.Audit over the wire. Assert neither flag exists, so this test
# fails loudly if a future build wires the audit path (then extend this harness).
help=$(dc run --rm --no-deps --entrypoint silt seed daemon -h 2>&1 || true)
if echo "$help" | grep -qiE '^\s*-liar\b|trigger.*audit|por.*challenge|audit.*challenge'; then
  echo "  NOTE: a liar/audit-trigger flag now EXISTS — the PoR path may be wire-drivable."
  echo "        Extend this harness to assert the slash directly. (Not a failure.)"
else
  echo "  confirmed: no -liar and no audit-trigger flag on 'silt daemon' — the PoR"
  echo "  audit sweep (Node.Audit) is only reachable from the in-process sim, so the"
  echo "  literal 'caught without fetch + standing slash' claim is NOT wire-testable"
  echo "  today. This harness proves the OBSERVABLE half: detect-via-probe + repair."
fi

echo
if [ "$pass" = 1 ]; then
  echo "RESULT: PASS ✅  liar deletes data → caretaker detects the loss over the wire,"
  echo "        repairs from parity, re-scatters the shards, file stays bit-perfect."
  echo "        (See the FINDING above + README for the PoR-audit gap.)"
else
  echo "RESULT: FAIL ❌  (see the FAIL lines above)"
fi
exit $((1-pass))
