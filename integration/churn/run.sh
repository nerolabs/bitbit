#!/usr/bin/env bash
# One automated repair-under-churn integration test: stand up a real swarm
# (registry seed + a pool of holder daemons + a caretaker), publish an
# erasure-coded file, then KILL holders in waves until enough erasure columns
# are stranded that the caretaker must reconstruct the lost shards from parity
# and re-scatter them to fresh survivors — asserting the file stays bit-perfect
# through it all. This is the real-daemon analog of `silt sim run churn`.
#
# Why it is patient by design: repair is not instant. A caretaker sweeps every
# RepairInterval (60s, compiled in — not a flag), reconstructs from k=10 of a
# stripe's n=16 shards, and re-seeds. Content is also 3x-replicated, so a stripe
# only needs rebuilding once node deaths drop *every* replica of more than
# RepairSlack(=2) of its columns. So we kill progressively — one holder at a
# time, waiting a full sweep window after each — until the caretaker is forced
# to actually repair, then prove retrieval survived. A field test should run
# close to real time; we do not rush the sweeps.
#
# Usage:  ./run.sh              # build, churn, assert, tear down; exit 0 = PASS
#         KEEP=1 ./run.sh       # leave the swarm up afterward to poke at
#         HOLDERS=20 WAVES=3 FILE_BYTES=50000000 ./run.sh   # crank it up
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

# ---- knobs (env-overridable; defaults chosen for signal, not for speed) ----
HOLDERS=${HOLDERS:-16}            # size of the storage pool (more = finer kill granularity,
                                  #   so kills land cleanly inside the repairable window)
PROTECTED=${PROTECTED:-3}         # holders never killed (a permanent survivor set)
WAVES=${WAVES:-2}                 # how many kill→repair→refetch cycles to run
FILE_BYTES=${FILE_BYTES:-20000000}
STEP_WAIT=${STEP_WAIT:-120}       # per-kill wait for a repair sweep (2 * 60s cadence + margin)
STEADY_WAIT=${STEADY_WAIT:-75}    # let the caretaker's first sweep pass before baseline
SETTLE=${SETTLE:-25}              # after 'stripe repaired', let the re-seed land

dc() { docker compose "$@"; }
objcount() { docker exec "$1" sh -c 'find /data/objects -type f 2>/dev/null | wc -l' | tr -d ' \r\n'; }
# Repair events are logged at -log info to <store>/debug.log (NOT stdout), same
# as the NAT harness reads relay splices from there.
care_count() { docker exec "$CARE_CID" sh -c "grep -c \"$1\" /data/debug.log 2>/dev/null || true" | tr -d ' \r\n'; }
care_last()  { docker exec "$CARE_CID" sh -c "grep \"$1\" /data/debug.log 2>/dev/null | tail -1" | tr -d '\r'; }
cleanup() { [ "${KEEP:-0}" = 1 ] || dc down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

pass=1

echo "== build the silt binary on the host (linux/$(go env GOARCH)) and the image =="
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/churn/silt ./cmd/silt ) \
  || { echo "FAIL: host build"; exit 1; }
docker build -q -t silt-churn . >/dev/null || { echo "FAIL: image build"; exit 1; }

echo "== phase 1: seed (registry + bootstrap; holds no content) =="
dc up -d seed
SEED_CID=$(dc ps -q seed)
SEEDIP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$SEED_CID")
SEED_ID=""
for _ in $(seq 1 30); do
  SEED_ID=$(dc logs seed 2>&1 | grep -oE 'peer: [a-f0-9]{64}' | head -1 | awk '{print $2}')
  [ -n "$SEED_ID" ] && break
  sleep 1
done
[ -n "$SEED_ID" ] && [ -n "$SEEDIP" ] || { echo "FAIL: seed never reported a NodeID/IP"; dc logs seed | tail -20; exit 1; }
export SEED_PEER="$SEED_ID@$SEEDIP:4001"
export SEED_REG="$SEED_ID@https://$SEEDIP:4003"
echo "  seed peer: $SEED_PEER"
echo "  seed reg : $SEED_REG"

echo "== phase 2: $HOLDERS holder daemons =="
dc up -d --scale holder="$HOLDERS" holder
HOLDER_IDS=()
while IFS= read -r _id; do [ -n "$_id" ] && HOLDER_IDS+=("$_id"); done < <(dc ps -q holder)
[ "${#HOLDER_IDS[@]}" -eq "$HOLDERS" ] || echo "  note: expected $HOLDERS holders, compose reports ${#HOLDER_IDS[@]}"
echo "== wait for every holder to bootstrap into the swarm =="
for id in "${HOLDER_IDS[@]}"; do
  ok=0
  for _ in $(seq 1 60); do
    docker logs "$id" 2>&1 | grep -q 'bootstrapped' && { ok=1; break; }
    sleep 1
  done
  [ "$ok" = 1 ] || { echo "FAIL: holder ${id:0:12} never bootstrapped"; docker logs "$id" | tail -15; exit 1; }
done
echo "  all ${#HOLDER_IDS[@]} holders bootstrapped"

echo "== publish a ${FILE_BYTES}-byte file from an ephemeral client (keeps nothing) =="
dc exec -T seed sh -c "head -c $FILE_BYTES /dev/urandom > /tmp/f.bin; sha256sum /tmp/f.bin | cut -d' ' -f1 > /tmp/f.sha; silt swarm add /tmp/f.bin -peers '$SEED_PEER' -registry '$SEED_REG'" >/tmp/churn_add.txt 2>&1
LINK=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+' /tmp/churn_add.txt | head -1)
CARE=$(grep -oE 'siltcare:[A-Za-z0-9_:-]+' /tmp/churn_add.txt | head -1)
WANT=$(dc exec -T seed cat /tmp/f.sha | tr -d '\r\n ')
echo "  link: ${LINK:-<none>}"
echo "  care: ${CARE:-<none>}"
echo "  want: ${WANT:-<none>}"
[ -n "$LINK" ] && [ -n "$CARE" ] && [ -n "$WANT" ] || { echo "FAIL: publish did not yield link+care+hash"; cat /tmp/churn_add.txt; exit 1; }

echo "== phase 3: caretaker (holds the care link, runs the repair loop) =="
export CARE_LINK="$CARE"
dc up -d caretaker
CARE_CID=$(dc ps -q caretaker)
ok=0
for _ in $(seq 1 40); do
  docker logs "$CARE_CID" 2>&1 | grep -q 'caretaking' && { ok=1; break; }
  sleep 1
done
[ "$ok" = 1 ] || { echo "FAIL: caretaker never logged 'caretaking'"; docker logs "$CARE_CID" | tail -20; exit 1; }
echo "  caretaker up: $(docker logs "$CARE_CID" 2>&1 | grep 'caretaking' | tail -1)"
echo "  letting the caretaker's first sweep pass (${STEADY_WAIT}s)…"
sleep "$STEADY_WAIT"

# A field test must never hang: cap the fetch. A timed-out get yields no hash,
# which the caller treats as a (loud) failure — never a stall.
FETCH_TIMEOUT=${FETCH_TIMEOUT:-120}
fetch_sha() {
  dc exec -T seed sh -c "timeout ${FETCH_TIMEOUT} silt swarm get '$LINK' -o /tmp/out.bin -peers '$SEED_PEER' -registry '$SEED_REG' && sha256sum /tmp/out.bin | cut -d' ' -f1" 2>/tmp/churn_get.txt | grep -oE '^[a-f0-9]{64}' | tail -1
}

# Dump a builder-ready diagnostic when a churn assertion fails — this harness
# exists to surface repair-under-churn defects, so a failure should be a report,
# not a mystery. Prints the caretaker's repair-decision tally, the fetch error,
# and the surviving swarm's coverage.
finding_dump() {
  echo ""
  echo "───────────────────── FIELD-TEST FINDING (repair-under-churn) ─────────────────────"
  echo "holders killed: $killed of $HOLDERS   survivors alive: ${#ALIVE[@]} holder(s) + caretaker"
  echo "caretaker repair-decision tally (its /data/debug.log):"
  echo "    stripe repaired      : $(care_count 'stripe repaired')"
  echo "    within repair slack  : $(care_count 'within repair slack')"
  echo "    repair below k       : $(care_count 'repair below k')"
  echo "    dial failed (dead)   : $(care_count 'dial failed')   <- stale provider records being re-dialed"
  echo "last non-dial caretaker lines:"
  docker exec "$CARE_CID" sh -c 'grep -vE "dial failed" /data/debug.log 2>/dev/null | tail -6' | sed 's/^/    /'
  echo "fetch error (silt swarm get):"
  grep -iE 'stripe|unrecoverable|shards|error|timeout' /tmp/churn_get.txt 2>/dev/null | tail -4 | sed 's/^/    /'
  echo "surviving-holder object coverage:"
  for _id in "${ALIVE[@]}"; do echo "    holder ${_id:0:12}: $(objcount "$_id") objects"; done
  echo "    caretaker   : $(objcount "$CARE_CID") objects"
  echo "───────────────────────────────────────────────────────────────────────────────────"
}

echo "== baseline: fetch from the swarm (publisher long gone) =="
GOT=$(fetch_sha)
echo "  got : ${GOT:-<none>}"
[ "$GOT" = "$WANT" ] || { echo "FAIL: baseline fetch not bit-perfect"; cat /tmp/churn_get.txt; exit 1; }
echo "  ✅ baseline bit-perfect"

# The live holder set. We kill HEAVIEST-COVERAGE-FIRST: with 3x replication the
# few nodes whose IDs sit closest to the most column keys hold almost everything,
# so random-order kills barely strand a column until near-total wipeout (killing
# 90% of a small swarm can strand a single column). Killing the heavy coverers is
# what actually leaves a low-coverage survivor set and forces erasure
# reconstruction — and it mirrors a realistic worst case: your biggest holders
# are the ones that die.
ALIVE=("${HOLDER_IDS[@]}")
MIN_SURVIVORS=$PROTECTED   # stop killing once this many holders remain (re-seed targets)
killed=0
remove_alive() { local out=() id; for id in "${ALIVE[@]}"; do [ "$id" = "$1" ] || out+=("$id"); done; ALIVE=("${out[@]}"); }
alive_total() { local sum=0 c id; for id in "${ALIVE[@]}"; do c=$(objcount "$id"); sum=$((sum + ${c:-0})); done; c=$(objcount "$CARE_CID"); echo $((sum + ${c:-0})); }
heaviest_alive() { local best="" bestc=-1 id c; for id in "${ALIVE[@]}"; do c=$(objcount "$id"); c=${c:-0}; [ "$c" -gt "$bestc" ] && { bestc=$c; best=$id; }; done; echo "$best $bestc"; }
echo "  objects across the live swarm at baseline: $(alive_total)  (holders alive: ${#ALIVE[@]})"

# ---- the churn: kill heaviest-first until repair is forced, prove it heals ----
for w in $(seq 1 "$WAVES"); do
  echo ""
  echo "== wave $w: kill holders (heaviest coverage first) until repair is FORCED =="
  before=$(care_count 'stripe repaired')
  bk_before=$(care_count 'repair below k')
  forced=0
  overkilled=0
  prerepair=""
  while [ "${#ALIVE[@]}" -gt "$MIN_SURVIVORS" ]; do
    hv=$(heaviest_alive); victim="${hv% *}"; vobj="${hv##* }"
    docker kill "$victim" >/dev/null 2>&1 || true
    remove_alive "$victim"
    killed=$((killed + 1))
    prerepair=$(alive_total)
    echo "  killed holder ${victim:0:12} (held $vobj objects; #$killed killed, ${#ALIVE[@]} left); live-swarm objects now $prerepair"
    echo "    waiting up to ${STEP_WAIT}s for a repair sweep…"
    waited=0
    while [ "$waited" -lt "$STEP_WAIT" ]; do
      now=$(care_count 'stripe repaired')
      if [ "${now:-0}" -gt "${before:-0}" ]; then forced=1; break; fi
      sleep 5; waited=$((waited + 5))
    done
    [ "$forced" = 1 ] && break
    watching=$(care_count 'within repair slack')
    belowk=$(care_count 'repair below k')
    # If we killed so hard that a stripe dropped below k reachable shards before a
    # sweep could catch the repairable window, killing more only digs deeper. Stop.
    if [ "${belowk:-0}" -gt "${bk_before:-0}" ]; then
      echo "    ⚠ 'repair below k' — killed past the k-of-n floor before a repair sweep landed (over-killed)."
      overkilled=1; break
    fi
    echo "    still covered (watching=${watching:-0} belowk=${belowk:-0}) — killing another"
  done

  if [ "$forced" != 1 ]; then
    if [ "$overkilled" = 1 ]; then
      echo "FAIL: over-killed into 'repair below k' without a clean repair."
    else
      echo "FAIL: reached the survivor floor ($MIN_SURVIVORS) without the caretaker ever completing a repair — under heavy churn it never logged a single repair/watch/below-k decision."
    fi
    finding_dump
    pass=0; break
  fi
  after=$(care_count 'stripe repaired')
  echo "  ✅ REPAIR: 'stripe repaired' $before -> $after"
  echo "     $(care_last 'stripe repaired')"

  echo "  letting the reconstructed shards re-seed onto survivors (${SETTLE}s)…"
  sleep "$SETTLE"
  postrepair=$(alive_total)
  echo "  live-swarm objects: pre-repair=$prerepair post-repair=$postrepair"
  if [ "${postrepair:-0}" -gt "${prerepair:-0}" ]; then
    echo "  ✅ re-seed observed: survivors gained $((postrepair - prerepair)) reconstructed shard-objects"
  else
    echo "  ⚠ note: no net object growth measured this window (re-seed may have landed between samples)"
  fi

  echo "  == fetch again after wave $w — must be bit-perfect from the healed swarm =="
  GOT=$(fetch_sha)
  echo "    got : ${GOT:-<none>}"
  if [ "$GOT" = "$WANT" ]; then
    echo "  ✅ wave $w: bit-perfect after killing $killed holder(s) and repairing"
  else
    echo "FAIL: caretaker logged 'stripe repaired' but a fresh client still could not re-fetch bit-perfect after wave $w."
    finding_dump
    pass=0; break
  fi
done

echo ""
TOTAL_REPAIRED=$(care_count 'stripe repaired')
if [ "$pass" = 1 ]; then
  echo "RESULT: PASS ✅  survived $WAVES churn wave(s): $killed/$HOLDERS holders killed, $TOTAL_REPAIRED stripe-repair sweep(s), every fetch bit-perfect"
else
  echo "RESULT: FAIL ❌"
fi
exit $((1 - pass))
