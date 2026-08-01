#!/usr/bin/env bash
# End-to-end proof that two NATed *daemons* upgrade a relay path to a DIRECT
# connection (#27). nodeA holds a file; nodeB is a caretaker of it, so its
# repair loop probes/fetches the shards from nodeA through the relay — real
# daemon-to-daemon traffic — which triggers the relay-coordinated hole-punch.
# On cone NAT the punch lands and BOTH daemons log a direct connection; on
# symmetric NAT it can't and they stay on the relay.
#
#   ./holepunch.sh                   # cone — expect the punch to land
#   NAT_MODE=symmetric ./holepunch.sh  # expect NO punch (relay stays)
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)
MODE="${NAT_MODE:-cone}"
export NAT_MODE="$MODE"

dc() { docker compose -f docker-compose.yml -f docker-compose.holepunch.yml "$@"; }
trap 'dc down -v >/dev/null 2>&1 || true' EXIT

echo "== build (NAT_MODE=$MODE) =="
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/nat/silt ./cmd/silt )
docker build -q -t silt-nat . >/dev/null

echo "== relay + gateways + nodeA (nodeB comes up as caretaker after publish) =="
export CARE_LINK=none
dc up -d relay natA natB
RELAY_ID=""
for _ in $(seq 1 30); do
  RELAY_ID=$(dc logs relay 2>&1 | grep -oE 'peer: [a-f0-9]{64}' | head -1 | awk '{print $2}')
  [ -n "$RELAY_ID" ] && break; sleep 1
done
[ -n "$RELAY_ID" ] || { echo "FAIL: relay id"; exit 1; }
export RELAY_ID
dc up -d nodeA
sleep 5

PEERS="$RELAY_ID@10.10.0.10:4001"; REG="$RELAY_ID@https://10.10.0.10:4003"
echo "== publish a file from nodeA (content lands on nodeA; capture its care link) =="
dc exec -T nodeA sh -c "head -c 3000000 /dev/urandom > /tmp/f.bin; silt swarm add /tmp/f.bin -peers '$PEERS' -registry '$REG'" >/tmp/hp_add.txt 2>&1
LINK=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+' /tmp/hp_add.txt | head -1)
CARE=$(grep -oE 'siltcare:[A-Za-z0-9_:-]+' /tmp/hp_add.txt | head -1)
echo "  link: ${LINK:-<none>}"
echo "  care: ${CARE:-<none>}"
[ -n "$CARE" ] || { echo "FAIL: no care link captured"; cat /tmp/hp_add.txt; exit 1; }

echo "== bring up nodeB as a caretaker of that root =="
export CARE_LINK="$CARE"
dc up -d nodeB
echo "  waiting for nodeB's repair loop to probe nodeA through the relay (up to 90s)..."
punched=0
for _ in $(seq 1 90); do
  if dc exec -T nodeA sh -c 'grep -q "hole-punch: direct connection established" /data/debug.log 2>/dev/null'; then punched=1; break; fi
  sleep 1
done

echo "== results =="
a=$(dc exec -T nodeA sh -c 'grep "hole-punch: direct connection established" /data/debug.log 2>/dev/null | tail -1' | tr -d '\r')
b=$(dc exec -T nodeB sh -c 'grep "hole-punch: direct connection established" /data/debug.log 2>/dev/null | tail -1' | tr -d '\r')
coord=$(dc exec -T relay sh -c 'grep -c "relay punch coordinated" /data/debug.log 2>/dev/null || true' | tr -d '\r\n ')
echo "  relay punch-coordinations: ${coord:-0}"
echo "  nodeA: ${a:-<no direct connection>}"
echo "  nodeB: ${b:-<no direct connection>}"

if [ "$MODE" = symmetric ]; then
  if [ "$punched" = 0 ] && [ -z "$a" ]; then
    echo "RESULT: PASS ✅ symmetric NAT — daemons correctly stayed on the relay (no punch)"; exit 0
  fi
  echo "RESULT: FAIL ❌ symmetric NAT unexpectedly punched"; exit 1
else
  if [ -n "$a" ] && [ -n "$b" ]; then
    echo "RESULT: PASS ✅ two NATed daemons upgraded the relay path to a DIRECT connection"; exit 0
  fi
  echo "RESULT: FAIL ❌ daemons did not establish a direct connection"
  echo "--- nodeB repair/punch log ---"; dc exec -T nodeB sh -c 'grep -iE "repair|care|punch|relay dial|fetch" /data/debug.log 2>/dev/null | tail -15'
  exit 1
fi
