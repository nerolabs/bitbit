#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Field test #6 — takedown, in real Docker containers.
#
# Proves the safety model behind `silt sim run takedown` + core/denylist over
# the real wire: a takedown is PER-OPERATOR, EXISTENCE-CHECKED, and REVERSIBLE.
#
#   1. Two independent operators (opA, opB) each publish the SAME convergent
#      file → the SAME root. Both serve it (baseline). A control file is
#      published too, to distinguish a real PASS from a global wipe.
#   2. opA loads an operator-local `-denylist` naming that root → it PURGES the
#      held chunks and REFUSES to serve it, while opB (never denies) still
#      serves the identical root, and the control file survives everywhere.
#      Per-operator, per-hash — no global switch.
#   3. Existence check (on-chain): a trusted validator revokes the REAL,
#      committed root → a takedown block commits; revoking a BOGUS root → no
#      block ever commits (the chain refuses to revoke what it never published).
#   4. Reversal: restart opA WITHOUT the denylist and re-publish the same
#      convergent file → opA serves the once-denied root again. The denial was
#      conditional on the list, not a permanent ban.
#
# Usage:  ./run.sh          # build, test, tear down; exit 0 = PASS
#         KEEP=1 ./run.sh   # leave the topology up afterward to poke at
# ─────────────────────────────────────────────────────────────────────────────
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

dc()  { docker compose "$@"; }
WORK=""
cleanup() {
  [ -n "$WORK" ] && rm -rf "$WORK"
  [ "${KEEP:-0}" = 1 ] || dc -f docker-compose.yml down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

# base64url (link field) → hex (what -denylist files and -revoke want). The
# image carries coreutils, so base64/od are available inside the container; we
# do the conversion on the host with the same tools.
b64url_to_hex() {
  local s="$1"
  while [ $(( ${#s} % 4 )) -ne 0 ]; do s="${s}="; done
  printf '%s' "$s" | tr '_-' '/+' | base64 -d 2>/dev/null | od -An -v -tx1 | tr -d ' \r\n'
}
# wait for a log line inside a container's store debug/stdout. Daemons here log
# to stdout (captured by `docker compose logs`), so grep there.
await_log() {
  local svc="$1" pat="$2" i
  for i in $(seq 1 60); do
    dc logs "$svc" 2>&1 | grep -qE "$pat" && return 0
    sleep 1
  done
  return 1
}
objects() { dc exec -T "$1" sh -c 'find /data/objects -type f 2>/dev/null | wc -l' | tr -d ' \r\n'; }

pass=1
fail() { echo "FAIL: $*"; pass=0; }

echo "== build the silt binary on the host (linux/$(go env GOARCH)) and the image =="
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/takedown/silt ./cmd/silt ) \
  || { echo "FAIL: go build"; exit 1; }
docker build -q -t silt-takedown . >/dev/null || { echo "FAIL: docker build"; exit 1; }

echo "== phase 0: bring up validator + two independent operators =="
dc up -d val opA opB
await_log val '^registry:|serving' || { echo "FAIL: val registry never came up"; dc logs val | tail -20; exit 1; }
await_log opA '^registry:|serving' || { echo "FAIL: opA registry never came up"; dc logs opA | tail -20; exit 1; }
await_log opB '^registry:|serving' || { echo "FAIL: opB registry never came up"; dc logs opB | tail -20; exit 1; }

# Each operator's own peer/registry refs. The daemon binds a wildcard address,
# so its printed `peer:`/`registry:` lines carry `[::]` — useless to dial. But
# the NodeID (a stable function of -id-seed) is right there, and each service
# sits at a fixed compose IP, so build the dialable refs from ID@STATIC_IP.
id_of()   { dc logs "$1" 2>&1 | grep -oE 'peer: [a-f0-9]{64}' | head -1 | awk '{print $2}'; }
IDA=$(id_of opA); IDB=$(id_of opB); IDV=$(id_of val)
PEERA="$IDA@10.90.0.11:4001"; REGA="$IDA@https://10.90.0.11:4003"
PEERB="$IDB@10.90.0.12:4001"; REGB="$IDB@https://10.90.0.12:4003"
PEERV="$IDV@10.90.0.10:4001"; REGV="$IDV@https://10.90.0.10:4003"
echo "  opA: peer=$PEERA reg=$REGA"
echo "  opB: peer=$PEERB reg=$REGB"
echo "  val: peer=$PEERV reg=$REGV"
[ -n "$IDA" ] && [ -n "$IDB" ] && [ -n "$IDV" ] \
  || { echo "FAIL: could not read a NodeID from the logs"; dc logs opA | tail -20; exit 1; }

# Test fixtures live on the HOST (WORK dir) and are piped into containers on
# demand. A `docker compose --force-recreate` wipes a container's ephemeral
# layer (/tmp), so the reference bytes and the reference for `cmp` must NOT live
# inside the container. We fetch results back out to the host and compare here.
WORK=$(mktemp -d)
head -c 400000 /dev/urandom > "$WORK/target.bin"
head -c 400000 /dev/urandom > "$WORK/control.bin"
# push identical bytes so the convergent root matches on every operator
push() { dc exec -T "$1" sh -c "cat > '$2'"; }   # push <svc> <path-in-container> < hostfile
add() {  # add <svc> <peer> <reg> <hostfile>  -> stdout is the daemon output
  push "$1" /data/in.bin < "$4"
  dc exec -T "$1" sh -c "silt swarm add /data/in.bin -mode convergent -peers '$2' -registry '$3'"
}
get() {  # get <svc> <link> <peer> <reg> <hostfile-ref> -> echoes SERVED|REFUSED
  dc exec -T "$1" sh -c "rm -f /data/out.bin; silt swarm get '$2' -o /data/out.bin -peers '$3' -registry '$4' >/dev/null 2>&1; cat /data/out.bin 2>/dev/null" > "$WORK/out.bin" 2>/dev/null
  if [ -s "$WORK/out.bin" ] && cmp -s "$5" "$WORK/out.bin"; then echo SERVED; else echo REFUSED; fi
}

echo "== phase 1: publish the SAME convergent file to opA and opB, plus a control =="
add opA "$PEERA" "$REGA" "$WORK/target.bin"  > "$WORK/add_targetA.out" 2>&1
add opB "$PEERB" "$REGB" "$WORK/target.bin"  > "$WORK/add_targetB.out" 2>&1
add opA "$PEERA" "$REGA" "$WORK/control.bin" > "$WORK/add_control.out" 2>&1
# publish the target to the validator too, so its chain COMMITS the root (needed
# for the on-chain existence check in phase 3).
add val "$PEERV" "$REGV" "$WORK/target.bin"  > "$WORK/add_targetV.out" 2>&1

TLINKA=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+' "$WORK/add_targetA.out" | head -1)
TLINKB=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+' "$WORK/add_targetB.out" | head -1)
CLINK=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+' "$WORK/add_control.out" | head -1)
TROOT=$(b64url_to_hex "$(printf '%s' "$TLINKA" | cut -d: -f3)")
echo "  target link (opA): $TLINKA"
echo "  target link (opB): $TLINKB"
echo "  target root (hex): $TROOT"
echo "  control link:      $CLINK"
[ -n "$TLINKA" ] && [ "$TLINKA" = "$TLINKB" ] \
  || fail "convergent publish did not yield an identical root on both operators (A=$TLINKA B=$TLINKB)"
[ "${#TROOT}" = 64 ] || fail "could not derive a 64-hex target root (got '${TROOT}')"

echo "== baseline: both operators serve the target; control serves on A =="
BASE_A=$(get opA "$TLINKA" "$PEERA" "$REGA" "$WORK/target.bin")
BASE_B=$(get opB "$TLINKB" "$PEERB" "$REGB" "$WORK/target.bin")
BASE_C=$(get opA "$CLINK"  "$PEERA" "$REGA" "$WORK/control.bin")
echo "  opA target baseline:  $BASE_A"
echo "  opB target baseline:  $BASE_B"
echo "  opA control baseline: $BASE_C"
[ "$BASE_A" = SERVED ] || fail "opA did not serve the target at baseline"
[ "$BASE_B" = SERVED ] || fail "opB did not serve the target at baseline"
[ "$BASE_C" = SERVED ] || fail "control did not serve at baseline"
OBJ_A_BEFORE=$(objects opA); OBJ_B_BEFORE=$(objects opB)
echo "  objects before: opA=$OBJ_A_BEFORE opB=$OBJ_B_BEFORE"

echo "== phase 2: apply an operator-local -denylist to opA ONLY =="
printf '%s\n' "$TROOT" > "$WORK/deny.txt"
dc cp "$WORK/deny.txt" opA:/data/deny.txt
dc -f docker-compose.yml -f docker-compose.deny.yml up -d --force-recreate opA
# match the daemon's OUTPUT line ("denylist: N root(s) denied; purged M ...") —
# not the entrypoint's echo of the "-denylist=" flag.
await_log opA 'denylist: [0-9]+ root' || { echo "FAIL: opA never logged a denylist enforcement line"; dc logs opA | tail -20; pass=0; }
DENYLINE=$(dc logs opA 2>&1 | grep -iE 'denylist: [0-9]+ root' | tail -1)
echo "  opA: $DENYLINE"
echo "$DENYLINE" | grep -qiE 'purged [1-9]' || fail "opA denylist did not report purging any held chunks"
OBJ_A_AFTER=$(objects opA); OBJ_B_AFTER=$(objects opB)
echo "  objects after deny: opA=$OBJ_A_AFTER (was $OBJ_A_BEFORE)  opB=$OBJ_B_AFTER (was $OBJ_B_BEFORE)"
[ "${OBJ_A_AFTER:-0}" -lt "${OBJ_A_BEFORE:-0}" ] || fail "opA object count did not drop after takedown (no purge)"
[ "${OBJ_B_AFTER:-0}" = "${OBJ_B_BEFORE:-0}" ] || fail "opB object count changed — takedown leaked across operators"

# The recreate lost opA's in-memory provider records; it re-announces the
# surviving (control) chunks after bootstrap (AnnounceHeld). Wait for that so
# the control fetch below resolves against real provider records, not a race.
await_log opA 're-announced [0-9]+ held chunks' || sleep 6

echo "== assert: opA REFUSES the target, opB STILL SERVES it, control SURVIVES =="
DENY_A=$(get opA "$TLINKA" "$PEERA" "$REGA" "$WORK/target.bin")
DENY_B=$(get opB "$TLINKB" "$PEERB" "$REGB" "$WORK/target.bin")
DENY_C=$(get opA "$CLINK"  "$PEERA" "$REGA" "$WORK/control.bin")
echo "  opA target after takedown:  $DENY_A   (want REFUSED)"
echo "  opB target after takedown:  $DENY_B   (want SERVED — per-operator scope)"
echo "  opA control after takedown: $DENY_C   (want SERVED — survives)"
[ "$DENY_A" = REFUSED ] || fail "opA still served the denied target — takedown not enforced"
[ "$DENY_B" = SERVED ]  || fail "opB stopped serving — takedown leaked across operators (not per-operator)"
[ "$DENY_C" = SERVED ]  || fail "control file died on opA — takedown was not per-hash"

echo "== phase 3: on-chain existence check — validator revokes REAL vs BOGUS root =="
# Each --force-recreate makes a FRESH val container whose logs start empty; it
# replays the persisted chain ("chain: restored N block(s)"), so we assert
# directly on the fresh container's log rather than on a cross-recreate delta.
#
# Real root: the validator's chain committed it in phase 1, so LookupRoot
# resolves, the revocation is proposed, and a takedown block commits.
REVOKE_ROOT="$TROOT" dc -f docker-compose.yml -f docker-compose.revoke.yml up -d --force-recreate val >/dev/null 2>&1
await_log val 'takedown: proposed on-chain revocation' || true
sleep 3
REALREV=$(dc logs val 2>&1 | grep -E "takedown: proposed on-chain revocation of $TROOT" | tail -1)
REAL_BLOCK=$(dc logs val 2>&1 | grep -E 'chain: committed block' | tail -1)
echo "  real-root revoke: ${REALREV:-<none>}"
echo "  takedown block:   ${REAL_BLOCK:-<none>}"
[ -n "$REALREV" ]   || fail "validator did not propose an on-chain revocation of the real committed root"
[ -n "$REAL_BLOCK" ] || fail "no takedown block committed for the real root"

# Bogus root: never published, so the -revoke loop spins on LookupRoot and NO
# revocation is ever proposed or committed. (Had it reached a proposal, the
# chain's validateTakedowns would reject it with ErrRevokeUnknownRoot — "a
# root never published on this chain"; the daemon's existence gate refuses to
# even propose it.) The fresh container restores the chain but commits nothing.
BOGUS=$(printf 'ab%.0s' {1..32})   # 64 hex chars, deterministic, never on-chain
REVOKE_ROOT="$BOGUS" dc -f docker-compose.yml -f docker-compose.revoke.yml up -d --force-recreate val >/dev/null 2>&1
await_log val 'chain: restored' || true
sleep 8   # give the -revoke spin loop ample time to (not) act
BOGUSREV=$(dc logs val 2>&1 | grep -E "takedown: proposed on-chain revocation" | tail -1)
BOGUS_BLOCK=$(dc logs val 2>&1 | grep -E 'chain: committed block' | tail -1)
echo "  bogus-root revoke proposed: ${BOGUSREV:-<none — correctly refused>}"
echo "  bogus-root takedown block:  ${BOGUS_BLOCK:-<none — correctly none>}"
[ -z "$BOGUSREV" ]   || fail "validator proposed a revocation of a NEVER-PUBLISHED root (existence check bypassed)"
[ -z "$BOGUS_BLOCK" ] || fail "a takedown block committed for a bogus root (existence check bypassed)"

echo "== phase 4: reversibility — restart opA WITHOUT the denylist, re-publish =="
dc -f docker-compose.yml up -d --force-recreate opA
await_log opA 'bootstrapped' || fail "opA did not come back up after removing the denylist"
# wait for the re-announce of the surviving chunks so provider records are live
await_log opA 're-announced [0-9]+ held chunks' || sleep 6
# opA's NodeID is a function of -id-seed, so its refs are unchanged across the
# restart; rebuild them from the stable ID and static IP.
IDA=$(id_of opA); PEERA="$IDA@10.90.0.11:4001"; REGA="$IDA@https://10.90.0.11:4003"
# with the denial gone, opA accepts and serves the once-denied root again
# (re-push the fixture from the host — the recreate wiped the container's /tmp)
add opA "$PEERA" "$REGA" "$WORK/target.bin" > "$WORK/readd.out" 2>&1
echo "  re-publish: $(grep -oE 'scattered .*' "$WORK/readd.out" | head -1)"
sleep 2
REV=$(get opA "$TLINKA" "$PEERA" "$REGA" "$WORK/target.bin")
REV_DENY_LINES=$(dc logs opA 2>&1 | grep -ciE 'denylist:')
echo "  opA denylist lines after restart-without-list: $REV_DENY_LINES (want 0)"
echo "  opA re-serves the once-denied root: $REV   (want SERVED)"
[ "$REV" = SERVED ] || fail "opA did not serve the root after the denylist was removed (not reversible)"

echo
if [ "$pass" = 1 ]; then
  echo "RESULT: PASS ✅  takedown is per-operator (opA refuses, opB serves the identical root),"
  echo "        existence-checked (real root revokes on-chain; bogus root refused), and reversible."
else
  echo "RESULT: FAIL ❌  (see FAIL lines above)"
fi
exit $((1-pass))
