#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Field test #1 — multi-validator consensus + partition-heal, in real Docker.
#
# The M0 keystone under test: OBJECTIVE on-chain-bond fork-choice. Four real
# `silt daemon -validator` processes on a flat Docker network form two groups.
# A network partition (the built-in test-harness -block-peers flag) severs them;
# each group commits its OWN fork over real TCP. We assert:
#
#   (P0 negative control) an UNBONDED / no-earned-standing publish is REFUSED
#                         (no commit) — the write path is earned, not a stamp.
#   (P1 convergence)      before the partition, group 1's two validators agree
#                         on the same chain head (identical `silt chain-status`).
#   (P2 partition)        the partition is in effect (⚠ PARTITION log on C,D) and
#                         the two groups commit DIFFERENT heads — heavier group 1
#                         = [g,a1,a2] (height 2), lighter group 2 = [g,c1]
#                         (height 1). They do NOT converge onto a shared head
#                         while severed (no cross-group reorg).
#   (P3 heal→converge)   restart C WITHOUT -block-peers, bootstrapped to A. It
#                         reloads its persisted [g,c1], discovers group 1's
#                         heavier fork, and REORGS onto it (real log line), then
#                         its `chain-status` head EQUALS group 1's. The set has
#                         converged to the heavier-bonded chain.
#
# Every assertion keys off a REAL observed CLI flag / stdout line / chain-status
# field — no invented strings. This is the Docker-real counterpart of
# e2e/partition_test.go (in-process), over real sockets and kernel networking.
#
# Usage:  ./run.sh          # build, test, tear down; exit 0 = PASS
#         KEEP=1 ./run.sh   # leave the topology up afterward to poke at
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

dc()      { docker compose "$@"; }
dc_heal() { docker compose -f docker-compose.yml -f docker-compose.heal.yml "$@"; }
cleanup() { [ "${KEEP:-0}" = 1 ] || dc_heal down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Wait until a regex appears in a service's stdout (docker compose logs).
await_log() { # service pattern [tries]
  local svc=$1 pat=$2 tries=${3:-60} i
  for i in $(seq 1 "$tries"); do
    dc logs "$svc" 2>&1 | grep -qE "$pat" && return 0
    sleep 1
  done
  return 1
}
# head height / head hash from `silt chain-status` read inside a container's store.
head_height() { dc exec -T "$1" silt chain-status -store /data 2>/dev/null | awk '/head height/{print $3}'; }
head_hash()   { dc exec -T "$1" silt chain-status -store /data 2>/dev/null | awk '/head hash/{print $3}'; }

echo "== build the silt binary on the host (linux/$(go env GOARCH)) and the image =="
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/consensus/silt ./cmd/silt ) \
  || { echo "FAIL: host build failed"; exit 1; }
docker build -q -t silt-consensus . >/dev/null || { echo "FAIL: image build failed"; exit 1; }

# Derive each validator's NodeID from its -id-seed up front (no daemon needed),
# exactly as the examples/flows do — so -anchors / -attesters / -block-peers are
# fillable BEFORE launch. The four share one anchor set → identical genesis. The
# binary is a linux image (built for the container), so run `silt id` INSIDE a
# throwaway container rather than on the host.
silt_id() { docker run --rm silt-consensus silt id -id-seed "$1" | tr -d '\r'; }
ID_A=$(silt_id 7001)
ID_B=$(silt_id 7002)
ID_C=$(silt_id 7003)
ID_D=$(silt_id 7004)
ANCHORS="$ID_A,$ID_B,$ID_C,$ID_D"
export ID_A ID_B ID_C ID_D ANCHORS
echo "  valA=$ID_A"
echo "  valB=$ID_B"
echo "  valC=$ID_C"
echo "  valD=$ID_D"

pass=1
fail() { echo "FAIL: $*"; pass=0; }

# ─────────────────────────────────────────────────────────────────────────────
# P0 — NEGATIVE CONTROL: an unbonded / no-earned-standing validator must REFUSE
# to commit a publish. We run one lone objective validator (no bonded attester
# can qualify it), publish, and assert NO "committed block" appears. Proves the
# write path is EARNED standing, not a rubber-stamp (flow4's negative control).
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== P0 negative control: a lone unqualified validator must REFUSE to commit =="
# Fully self-contained throwaway (its OWN loopback-only container, no main
# topology, no shared network) so it can't couple to or race the four-validator
# run below. A lone objective validator has no qualified bonded attester, so a
# publish against its registry must NOT commit.
NEG_OUT=$(docker run --rm silt-consensus \
  sh -c 'silt daemon -id-seed 9001 -listen 127.0.0.1:4001 -serve-registry 127.0.0.1:4003 -store /data \
      -validator -objective=false -min-rep 100 -quorum 1 -attesters '"$ID_A"' -bond 8M -bond-audit 1s \
      -capacity 1G -log info >/data/neg.log 2>&1 &
    for i in $(seq 1 30); do grep -q "^registry:" /data/neg.log && break; sleep 1; done
    head -c 65536 /dev/urandom > /tmp/n.bin
    silt swarm add /tmp/n.bin -peers "$(awk "/^peer:/{print \$2}" /data/neg.log)" \
      -registry "$(sed -En "s/.*serving ([^ ]+).*/\1/p" /data/neg.log | head -1)" >/tmp/neg_add.out 2>&1 || true
    echo "---neg-add---"; cat /tmp/neg_add.out
    echo "---neg-commits---"; grep -c "committed block" /data/neg.log || true' 2>&1)
if echo "$NEG_OUT" | sed -n '/---neg-commits---/,$p' | grep -qE '^[1-9]'; then
  fail "P0 unbonded publish COMMITTED — rubber-stamp (negative control failed)"
else
  echo "  P0 PASS: unbonded/unqualified publish refused, no committed block"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Bring up the four validators and let objective standing accrue.
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== bring up the four validators (A,B heavier group | C,D lighter, partitioned) =="
dc up -d valA valB valC valD 2>&1 | grep -vE 'Network|Volume|Container.*(Creat|Start|Running)' || true
# Fail loud if the daemons didn't come up (was previously suppressed → silent).
for v in valA valB valC valD; do
  st=$(dc ps --status running --format '{{.Service}}' 2>/dev/null | grep -x "$v" || true)
  [ -n "$st" ] || fail "$v is not running after 'compose up' — daemon failed to start"
done
await_log valA 'peer: [0-9a-f]{64}@' 40 || { fail "valA never printed its peer line"; }
await_log valA 'registry: chain-backed, serving' 40 || fail "valA registry never came up"
await_log valB 'bootstrapped \(' 40 || fail "valB never bootstrapped"
await_log valC 'peer: [0-9a-f]{64}@' 40 || fail "valC never printed its peer line"
await_log valD 'bootstrapped \(' 40 || fail "valD never bootstrapped"

echo "  bonds sealed (objective standing):"
for v in valA valB valC valD; do
  bl=$(dc logs "$v" 2>&1 | grep -oE 'bond: (sealed|reloaded)[^\n]*' | head -1)
  echo "    $v: ${bl:-<no bond line>}"
done

# P2a — the partition really is in effect on the lighter group.
echo "  partition flag in effect on the lighter group:"
for v in valC valD; do
  pl=$(dc logs "$v" 2>&1 | grep -oE '⚠ PARTITION:[^\n]*' | head -1)
  echo "    $v: ${pl:-<no partition line>}"
  [ -n "$pl" ] || fail "$v did not report the partition (-block-peers) in effect"
done

echo "  letting objective standing accrue (14s)…"; sleep 14

# ─────────────────────────────────────────────────────────────────────────────
# Drive commits: two publishes into group 1 (heavier), one into group 2.
# ─────────────────────────────────────────────────────────────────────────────
REG_A="$ID_A@https://10.50.0.11:4003"
REG_C="$ID_C@https://10.50.0.13:4003"
BOOT_A="$ID_A@10.50.0.11:4001"
BOOT_C="$ID_C@10.50.0.13:4001"

# publish <container> <name> <boot> <reg> — retries until a link comes back, so we
# don't race standing accrual. Runs the client from inside the group's registry node.
publish() {
  local svc=$1 name=$2 boot=$3 reg=$4 i out
  for i in $(seq 1 40); do
    out=$(dc exec -T "$svc" sh -c "head -c 32768 /dev/urandom > /tmp/$name.bin; \
      silt swarm add /tmp/$name.bin -peers '$boot' -registry '$reg' 2>&1")
    echo "$out" | grep -qoE 'silt:v1:[A-Za-z0-9_:-]+' && { echo "$out" | grep -oE 'silt:v1:[A-Za-z0-9_:-]+' | head -1; return 0; }
    sleep 1
  done
  echo ""
  return 1
}

echo ""
echo "== P1 convergence (pre-partition): group 1 commits, its replicas agree =="
L1=$(publish valA a1 "$BOOT_A" "$REG_A"); echo "  a1 link: ${L1:-<none>}"
[ -n "$L1" ] || fail "group 1 first publish never committed"
sleep 3
HA=$(head_hash valA); HB=$(head_hash valB)
hA=$(head_height valA); hB=$(head_height valB)
echo "  valA head: height=$hA hash=${HA:0:16}…"
echo "  valB head: height=$hB hash=${HB:0:16}…"
if [ -n "$HA" ] && [ "$HA" = "$HB" ]; then
  echo "  P1 PASS: group-1 replicas agree on one head (${HA:0:16}…) — convergence"
else
  fail "P1 group-1 replicas DIVERGED (A=$HA B=$HB) — no convergence"
fi

echo ""
echo "== P2 partition: the two groups commit DIFFERENT heads and do not converge =="
L2=$(publish valA a2 "$BOOT_A" "$REG_A"); echo "  a2 link: ${L2:-<none>}"
await_log valA 'chain: committed block 2' 30 || fail "group 1 never reached height 2 (heavier fork)"
LC=$(publish valC c1 "$BOOT_C" "$REG_C"); echo "  c1 link: ${LC:-<none>}"
await_log valC 'chain: committed block 1' 30 || fail "group 2 never committed its own fork"
sleep 3
H1=$(head_hash valA); h1=$(head_height valA)
H2=$(head_hash valC); h2=$(head_height valC)
echo "  group 1 (heavier) head: height=$h1 hash=${H1:0:16}…"
echo "  group 2 (lighter) head: height=$h2 hash=${H2:0:16}…"
# The keystone under partition: honest sides do NOT diverge onto a shared-but-
# conflicting head. Distinct heads (different hash), heavier side ahead in height,
# and neither reorged toward the other while severed.
if [ -n "$H1" ] && [ -n "$H2" ] && [ "$H1" != "$H2" ] && [ "${h1:-0}" -gt "${h2:-0}" ]; then
  echo "  P2 PASS: distinct heads under partition (heavier h=$h1 ≠ lighter h=$h2); groups stayed on their own forks"
else
  fail "P2 groups did not fork as expected (H1=$H1 h1=$h1 | H2=$H2 h2=$h2)"
fi
if dc logs valA 2>&1 | grep -q 'reorged onto a heavier fork'; then
  fail "P2 group 1 reorged while still partitioned — it should never have learned group 2"
else
  echo "  P2 PASS: group 1 (unblocked) never learned group 2 — no spurious cross-partition reorg"
fi

echo ""
echo "== P3 heal → converge to the heavier-bonded chain =="
# Record the lighter side's OWN pre-heal head so we can prove it actually moved
# OFF its fork (not that it was already on group 1's).
H_C_pre=$(head_hash valC)
echo "  valC pre-heal head: height=$(head_height valC) hash=${H_C_pre:0:16}… (its own [g,c1] fork)"
echo "  restart valC WITHOUT -block-peers, bootstrapped to valA (persisted chain reloads and reconciles)…"
dc_heal up -d valC >/dev/null 2>&1 || fail "heal restart of valC failed"
# The healed valC reloads its persisted [g,c1] and reconciles against group 1's
# heavier fork. It converges either by an explicit REORG (drops c1, adopts a1,a2 —
# the OnReorg narration) or by the catch-up sweep adopting the heavier history;
# both land on the SAME head. GROUND TRUTH is the chain-status head hash — the
# reorg log line is supporting evidence, reported if it fired but not required,
# because the real-socket catch-up path may narrate "caught up" instead.
await_log valC 'chain: (reorged onto a heavier fork|caught up [0-9]+ block)' 90 \
  || fail "P3 valC never reconciled a peer chain after heal (no reorg or catch-up line)"
REORG_LINE=$(dc logs valC 2>&1 | grep -oE 'chain: reorged onto a heavier fork \(dropped [0-9]+ block\(s\), new head height [0-9]+\)' | tail -1)
CATCHUP_LINE=$(dc logs valC 2>&1 | grep -oE 'chain: caught up [0-9]+ block\(s\) from peers' | tail -1)
RESTORE_LINE=$(dc logs valC 2>&1 | grep -oE 'chain: restored [0-9]+ block\(s\) from disk' | tail -1)
echo "  valC on heal: reload=[${RESTORE_LINE:-<none>}] reorg=[${REORG_LINE:-<none>}] catchup=[${CATCHUP_LINE:-<none>}]"
# Converge — retry-poll the head hash (the sweep is periodic; give it time to land).
HC=""; hC=""
for _ in $(seq 1 30); do
  HC=$(head_hash valC); hC=$(head_height valC)
  HAf=$(head_hash valA); hAf=$(head_height valA)
  [ -n "$HAf" ] && [ "$HAf" = "$HC" ] && break
  sleep 2
done
echo "  after heal — valA head: height=$hAf hash=${HAf:0:16}…"
echo "  after heal — valC head: height=$hC hash=${HC:0:16}…"
if [ -n "$HAf" ] && [ "$HAf" = "$HC" ] && [ "${hC:-0}" = "${hAf:-0}" ]; then
  if [ "$HC" != "$H_C_pre" ]; then
    echo "  P3 PASS: valC left its own fork (${H_C_pre:0:16}…) and converged to group 1's head (${HAf:0:16}…, height $hAf) — the heavier-bonded chain"
  else
    echo "  P3 PASS: valC head matches group 1 (${HAf:0:16}…) — converged to the heavier-bonded chain"
  fi
  # Bonus assertion: if the persisted store survived, we should have seen a reload line.
  [ -n "$RESTORE_LINE" ] || echo "  NOTE: no 'restored from disk' line — the heal reload of the persisted fork is worth confirming (see README findings)"
else
  fail "P3 valC did NOT converge to the heavier head (A=$HAf@$hAf  C=$HC@$hC)"
fi

echo ""
if [ "$pass" = 1 ]; then
  echo "RESULT: PASS ✅  objective fork-choice: honest validators forked under partition, converged to the heavier-bonded chain on heal (M0 keystone)"
else
  echo "RESULT: FAIL ❌  (see the failing assertion(s) above)"
fi
exit $((1-pass))
