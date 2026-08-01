#!/usr/bin/env bash
# Empirical NAT hole-punch check, behind the harness's real kernel NAT. Runs a
# standalone probe (integration/nat/probe): a coordinator on the public relay
# pairs two peers on separate NATed LANs, swaps the source addresses it
# observed (their NAT mappings), and both peers simultaneously dial each other
# from a reused local port. Success ("DIRECT-OK") means the harness's NAT is
# punchable; with NAT_MODE=symmetric it should fail, which is what forces the
# relay fallback. This validates the #27 approach against real conntrack before
# it's wired into the transport.
#
#   ./probe.sh                 # cone NAT (default) — expect DIRECT-OK
#   NAT_MODE=symmetric ./probe.sh   # expect DIRECT-FAIL (relay fallback needed)
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)
MODE="${NAT_MODE:-cone}"
export NAT_MODE="$MODE"

dc() { docker compose "$@"; }
trap 'dc down -v >/dev/null 2>&1 || true' EXIT

echo "== build silt image + probe (NAT_MODE=$MODE) =="
ARCH=$(go env GOARCH)
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -o integration/nat/silt ./cmd/silt )
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -o integration/nat/probe.bin ./integration/nat/probe )
docker build -q -t silt-nat . >/dev/null

echo "== bring up topology =="
dc up -d relay natA natB nodeA nodeB
sleep 4

for c in relay nodeA nodeB; do
  dc cp probe.bin "$c":/usr/local/bin/probe >/dev/null
  dc exec -T "$c" chmod +x /usr/local/bin/probe
done

echo "== coordinator on the public relay (10.10.0.10:9000) =="
dc exec -d relay probe coord :9000
sleep 1

echo "== both peers punch (nodeA:45000 ↔ nodeB:45001) =="
dc exec -T nodeA probe peer 10.10.0.10:9000 45000 A >/tmp/probeA.txt 2>&1 &
pa=$!
dc exec -T nodeB probe peer 10.10.0.10:9000 45001 B >/tmp/probeB.txt 2>&1 &
pb=$!
wait $pa; wait $pb

rA=$(grep -oE 'DIRECT-(OK|FAIL)[^ ]*' /tmp/probeA.txt | head -1)
rB=$(grep -oE 'DIRECT-(OK|FAIL)[^ ]*' /tmp/probeB.txt | head -1)
echo "  nodeA: ${rA:-<none>}   $(grep -oE 'punching toward.*' /tmp/probeA.txt | head -1)"
echo "  nodeB: ${rB:-<none>}   $(grep -oE 'punching toward.*' /tmp/probeB.txt | head -1)"

if [ "$MODE" = symmetric ]; then
  # Expected outcome under symmetric NAT is failure (fallback to relay).
  if [ "$rA" = "DIRECT-FAIL" ] && [ "$rB" = "DIRECT-FAIL" ]; then
    echo "RESULT: PASS ✅ symmetric NAT correctly defeats the punch (relay fallback needed)"; exit 0
  fi
  echo "RESULT: unexpected — symmetric NAT was punched (rA=$rA rB=$rB)"; exit 1
else
  if [ "${rA%% *}" = "DIRECT-OK" ] && [ "${rB%% *}" = "DIRECT-OK" ]; then
    echo "RESULT: PASS ✅ cone NAT is punchable — direct connection formed through both NATs"; exit 0
  fi
  echo "RESULT: FAIL ❌ cone NAT did not punch (rA=$rA rB=$rB)"
  echo "--- nodeA log ---"; cat /tmp/probeA.txt; echo "--- nodeB log ---"; cat /tmp/probeB.txt
  exit 1
fi
