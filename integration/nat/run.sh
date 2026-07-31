#!/usr/bin/env bash
# One automated cross-NAT integration test: build the real binary, stand up
# two NATed daemons + a public relay in real container networks, publish a
# file from behind NAT A, fetch it from behind NAT B, and assert it comes
# back bit-perfect having crossed the relay. No second machine, no manual
# steps — this is the automatable replacement for the Mac-A↔Mac-B rig.
#
# Usage:  ./run.sh            # build, test, tear down; exit 0 = PASS
#         KEEP=1 ./run.sh     # leave the topology up afterward to poke at
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)
FILE_BYTES=${FILE_BYTES:-5000000}

dc() { docker compose "$@"; }
# Per-event logs (placements, relay splices) go to <store>/debug.log inside the
# container, NOT to stdout — so count splices there, not in `docker compose logs`.
relay_splices() { dc exec -T relay sh -c 'grep -c "relay splice" /data/debug.log 2>/dev/null || true' | tr -d "\r\n"; }
cleanup() { [ "${KEEP:-0}" = 1 ] || dc down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "== build the silt binary on the host (linux/$(go env GOARCH)) and the image =="
# Compile on the host — CGO off, so it cross-compiles trivially — and copy the
# binary into a slim image. Keeps the image tiny and avoids a Go-build memory
# spike inside Docker (this harness must run on small hosts too).
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/nat/silt ./cmd/silt )
docker build -q -t silt-nat . >/dev/null

echo "== phase 1: relay + NAT gateways =="
dc up -d relay natA natB

echo "== discover the relay's NodeID from its log =="
RELAY_ID=""
for _ in $(seq 1 30); do
  RELAY_ID=$(dc logs relay 2>&1 | grep -oE 'peer: [a-f0-9]{64}' | head -1 | awk '{print $2}')
  [ -n "$RELAY_ID" ] && break
  sleep 1
done
[ -n "$RELAY_ID" ] || { echo "FAIL: relay never reported a NodeID"; dc logs relay | tail -20; exit 1; }
export RELAY_ID
echo "relay id: $RELAY_ID"

echo "== phase 2: NATed nodes A and B =="
dc up -d nodeA nodeB

echo "== wait for both nodes to detect NAT and register via the relay =="
for n in nodeA nodeB; do
  ok=0
  for _ in $(seq 1 40); do
    if dc logs "$n" 2>&1 | grep -q "relay-via: registered"; then ok=1; break; fi
    sleep 1
  done
  [ "$ok" = 1 ] || { echo "FAIL: $n did not register via the relay"; dc logs "$n" | tail -20; exit 1; }
  dc logs "$n" 2>&1 | grep -E "NATed|relay-via: registered" | tail -1 | sed "s/^/  $n: /"
done

PEERS="$RELAY_ID@10.10.0.10:4001"
REG="$RELAY_ID@https://10.10.0.10:4003"

# #27 Phase 1: a NATed node learns its PUBLIC endpoint from the relay
# (STUN-style). The proof that real NAT is in play: nodeA's LAN address is
# 10.20.0.100, but the relay sees it as natA's masqueraded public IP 10.10.0.11
# — that mapped address is what a hole-punch will aim at.
echo "== #27 P1: nodes learn their NAT-mapped public endpoint via the relay =="
obsA=$(dc exec -T nodeA sh -c 'grep -oE "observed public endpoint.*" /data/debug.log 2>/dev/null | tail -1' | tr -d '\r')
echo "  nodeA (LAN 10.20.0.100) observed as: ${obsA:-<none>}"
echo "$obsA" | grep -q "10.10.0.11" || { echo "FAIL: nodeA should learn its NAT-mapped public IP 10.10.0.11, not its LAN address (#27 P1)"; exit 1; }

echo "== publish a ${FILE_BYTES}-byte file from nodeA (behind NAT A) =="
dc exec -T nodeA sh -c "head -c $FILE_BYTES /dev/urandom > /tmp/f.bin; sha256sum /tmp/f.bin | cut -d' ' -f1 > /tmp/f.sha; silt swarm add /tmp/f.bin -peers '$PEERS' -registry '$REG'" >/tmp/nat_add.txt 2>&1
LINK=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+' /tmp/nat_add.txt | head -1)
WANT=$(dc exec -T nodeA cat /tmp/f.sha | tr -d '\r\n ')
echo "  link: ${LINK:-<none>}"
echo "  want: $WANT"
[ -n "$LINK" ] || { echo "FAIL: publish returned no link"; cat /tmp/nat_add.txt; exit 1; }

splices_before=$(relay_splices)

echo "== fetch it from nodeB (behind NAT B) — must cross the relay =="
dc exec -T nodeB sh -c "silt swarm get '$LINK' -o /tmp/out.bin -peers '$PEERS' -registry '$REG' && sha256sum /tmp/out.bin | cut -d' ' -f1" >/tmp/nat_get.txt 2>&1
GOT=$(grep -oE '^[a-f0-9]{64}' /tmp/nat_get.txt | tail -1)
splices_after=$(relay_splices)

echo "  got : ${GOT:-<none>}"
echo "  content lives only on nodeA: relay=$(dc exec -T relay sh -c 'ls /data/objects 2>/dev/null | wc -l' | tr -d ' \r\n') nodeA=$(dc exec -T nodeA sh -c 'ls /data/objects 2>/dev/null | wc -l' | tr -d ' \r\n') nodeB=$(dc exec -T nodeB sh -c 'ls /data/objects 2>/dev/null | wc -l' | tr -d ' \r\n') objects"
echo "  relay splices: ${splices_before:-0} -> ${splices_after:-0}  (bytes crossed the relay NATed→NATed)"

pass=1
[ "$WANT" = "$GOT" ] || { echo "FAIL: checksum mismatch"; cat /tmp/nat_get.txt; pass=0; }
if [ "${splices_after:-0}" -le "${splices_before:-0}" ]; then
  echo "FAIL: no relay splice observed — the fetch did not cross NAT (topology is not exercising the relay path)"; pass=0
fi

# #69: prove content survives a restart. Restart the WHOLE swarm (stores
# persist across `restart`, but every in-memory provider record is lost), then
# re-fetch. This only works if each holder reloads its persisted proofs and
# re-announces its coded shards under the right column key — otherwise the
# disk-full-of-content is invisible.
if [ "${RESTART:-0}" = 1 ] && [ "$pass" = 1 ]; then
  echo "== #69: restart the whole swarm (stores persist, provider records don't), then re-fetch =="
  dc restart >/dev/null 2>&1
  for _ in $(seq 1 60); do
    dc logs --since 2m nodeA 2>&1 | grep -q "relay-via: registered" && break
    sleep 1
  done
  sleep 6   # let AnnounceHeld (after re-bootstrap) re-plant provider records
  reloaded=$(dc exec -T nodeA sh -c 'grep -oE "reloaded storage proofs.*" /data/debug.log 2>/dev/null | tail -1' | tr -d '\r')
  echo "  nodeA on restart: ${reloaded:-<no reload line>}"
  dc exec -T nodeB sh -c "silt swarm get '$LINK' -o /tmp/out2.bin -peers '$PEERS' -registry '$REG' && sha256sum /tmp/out2.bin | cut -d' ' -f1" >/tmp/nat_get2.txt 2>&1
  GOT2=$(grep -oE '^[a-f0-9]{64}' /tmp/nat_get2.txt | tail -1)
  echo "  re-fetch after restart got: ${GOT2:-<none>}"
  [ "$WANT" = "$GOT2" ] || { echo "FAIL: content undiscoverable after restart — reprovide broken (#69)"; cat /tmp/nat_get2.txt; pass=0; }
fi

if [ "$pass" = 1 ]; then
  echo "RESULT: PASS ✅  cross-NAT publish→fetch bit-perfect, via the relay${RESTART:+ (survived a full-swarm restart)}"
else
  echo "RESULT: FAIL ❌"
fi
exit $((1-pass))
