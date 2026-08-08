#!/usr/bin/env bash
# One automated economy + blind-credit field test. Builds the real binary,
# stands up two validator/issuers (one serves the registry) + a holder + a
# freeloader on a flat container net, and asserts against REAL daemon behavior:
#
#   FIELD TEST 5 claims
#   (a) hosts earn per byte served and freeloaders go broke   -> `silt sim run economy`
#   (b) M0 adds publisher-unlinkable prepaid blind-signed publish credits:
#         - a token-less publish is REFUSED when validators -require-tokens   (control)
#         - a Publisher-identity publish is REFUSED (durable linkage rejected)(control)
#         - a tokened publish (blind sigs from -token-quorum validators) COMMITS
#           carrying NO durable Publisher — proven on the committed chain.cbor
#         - a DOUBLE-SPEND of a token is rejected  ... see the FINDING below.
#
# Usage:  ./run.sh          # build, test, tear down; exit 0 = PASS
#         KEEP=1 ./run.sh   # leave the topology up afterward to poke at
set -uo pipefail
cd "$(dirname "$0")"
ROOT=$(cd ../.. && pwd)

dc() { docker compose -p economy "$@"; }
cleanup() { [ "${KEEP:-0}" = 1 ] || dc down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

VAL1_ID=171e68f02e6f66bf9ff65c13c75d9b2b492c2f40ed61e06507cb8b227c3970d5
VAL2_ID=49a396496b0973f889ae30bf6f546ef366c535c2b13f6be4f91791cf42375fbd
PEERS="$VAL1_ID@10.80.0.11:4001,$VAL2_ID@10.80.0.12:4001"
REG="$VAL1_ID@https://10.80.0.11:4003"

pass=1
fail() { echo "FAIL: $*"; pass=0; }

echo "== build the silt binary on the host (linux/$(go env GOARCH)) and the image =="
( cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$(go env GOARCH)" go build -trimpath -o integration/economy/silt ./cmd/silt ) \
  || { echo "FAIL: host (linux) build"; exit 1; }
docker build -q -t silt-economy . >/dev/null || { echo "FAIL: image build"; exit 1; }
# The linux binary above runs in the container; the sim (CLAIM a) runs on the
# HOST, so also build a host-native binary for it (not committed; scratch temp).
SIM_BIN=$(mktemp -t silt-economy-sim.XXXXXX)
( cd "$ROOT" && go build -trimpath -o "$SIM_BIN" ./cmd/silt ) || { echo "FAIL: host-native build"; exit 1; }
trap 'cleanup; rm -f "$SIM_BIN"' EXIT

# ─────────────────────────────────────────────────────────────────────────────
# CLAIM (a): the economy observatory — per-byte earning, freeloaders go broke.
# This runs IN-PROCESS (simnet) against the real core/credit ledger; there is no
# wire seam that exposes per-byte credit accounting on the daemon (see FINDING 2
# in README). We still assert against its REAL stdout so a regression is caught.
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== CLAIM (a): silt sim run economy (per-byte earning; freeloaders broke) =="
"$SIM_BIN" sim run economy >/tmp/econ_sim.out 2>&1 || fail "sim run economy errored"
sed 's/^/  sim: /' /tmp/econ_sim.out
# The top earner served bytes and CAN publish again; a freeloader served 0 and CANNOT.
grep -qE 'top earner .* served +[1-9][0-9]* B.*can publish: true'   /tmp/econ_sim.out || fail "no top earner that served bytes and can re-publish"
grep -qE 'freeloader .* served +0 B.*can publish: false'            /tmp/econ_sim.out || fail "no freeloader that served 0 B and is broke"
grep -qE 'all 6 freeloaders among the rejected: true'              /tmp/econ_sim.out || fail "not all freeloaders were gate-rejected"
[ "$pass" = 1 ] && echo "  CLAIM (a): PASS — hosts earn per byte served; freeloaders cannot re-publish"

# ─────────────────────────────────────────────────────────────────────────────
# Bring up the real swarm for CLAIM (b).
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== phase: bring up validators (val2 first, then val1+registry), holder, leech =="
dc up -d val2
# val1 bootstraps to val2; both must reach standing before they can commit.
dc up -d val1 holder leech

echo "== wait for val1 to serve the registry and require tokens =="
ok=0
for _ in $(seq 1 40); do
  if dc logs val1 2>&1 | grep -qE 'registry: chain-backed'; then ok=1; break; fi
  sleep 1
done
[ "$ok" = 1 ] || { fail "val1 never served the registry"; dc logs val1 | tail -20; }
dc logs val1 2>&1 | grep -E 'publish tokens: required|registry: chain-backed' | head -2 | sed 's/^/  val1: /'
dc logs val2 2>&1 | grep -E 'publish tokens: required' | head -1 | sed 's/^/  val2: /'

echo "== let both validators accrue consensus standing (bond audits) =="
sleep 16

# ─────────────────────────────────────────────────────────────────────────────
# CLAIM (b) CONTROL 1: a token-less publish is REFUSED (-require-tokens is on).
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== CLAIM (b) control 1: a token-less publish is REFUSED =="
dc exec -T holder sh -c "head -c 65536 /dev/urandom > /tmp/f0.bin; silt swarm add /tmp/f0.bin -peers '$PEERS' -registry '$REG'" >/tmp/econ_add0.out 2>&1 || true
tail -1 /tmp/econ_add0.out | sed 's/^/  /'
if grep -qiE 'entry has no publish token|token.*required' /tmp/econ_add0.out; then
  echo "  control 1: PASS — token-less publish refused ('entry has no publish token (required)')"
else
  fail "token-less publish was NOT refused"; cat /tmp/econ_add0.out
fi

# ─────────────────────────────────────────────────────────────────────────────
# CLAIM (b) CONTROL 2: a durable-Publisher publish is REFUSED (unlinkability is
# ENFORCED — the chain rejects any entry that would record a permanent
# Publisher->root link, so a committed entry provably carries no Publisher).
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== CLAIM (b) control 2: an -allow-publisher (durable identity) publish is REFUSED =="
dc exec -T holder sh -c "silt swarm add /tmp/f0.bin -allow-publisher -peers '$PEERS' -registry '$REG'" >/tmp/econ_addp.out 2>&1 || true
tail -1 /tmp/econ_addp.out | sed 's/^/  /'
if grep -qiE 'carries a durable Publisher|permanent linkage' /tmp/econ_addp.out; then
  echo "  control 2: PASS — durable-Publisher publish refused (permanent linkage rejected)"
else
  fail "durable-Publisher publish was NOT refused"; cat /tmp/econ_addp.out
fi

# ─────────────────────────────────────────────────────────────────────────────
# CLAIM (b) POSITIVE: a tokened publish (blind sigs from -token-quorum=2 distinct
# validators) COMMITS and carries NO durable Publisher. Proven two ways:
#   (i)  val1 prints "chain: committed block N";
#   (ii) the committed entry in val1's chain.cbor carries a Token+Serial and a
#        ZERO NodeID Publisher (32 zero bytes) — the unlinkability wire proof.
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== CLAIM (b) positive: a tokened publish (quorum 2) COMMITS with no Publisher =="
H_BEFORE=$(dc exec -T val1 sh -c 'silt chain-status -store /data' 2>/dev/null | awk '/head height/{print $3}' | tr -d '\r')
dc exec -T holder sh -c "silt swarm add /tmp/f0.bin -token-quorum 2 -peers '$PEERS' -registry '$REG'" >/tmp/econ_add1.out 2>&1 || true
LINK=$(grep -oE 'silt:v1:[A-Za-z0-9_:-]+' /tmp/econ_add1.out | head -1)
echo "  link: ${LINK:-<none>}"
sleep 4
H_AFTER=$(dc exec -T val1 sh -c 'silt chain-status -store /data' 2>/dev/null | awk '/head height/{print $3}' | tr -d '\r')
echo "  chain head height: ${H_BEFORE:-?} -> ${H_AFTER:-?}"
[ -n "$LINK" ] || fail "tokened publish returned no link"
dc logs val1 2>&1 | grep -E 'chain: committed block' | tail -1 | sed 's/^/  val1: /'
if [ "${H_AFTER:-0}" -gt "${H_BEFORE:-0}" ]; then
  echo "  positive (i): PASS — tokened publish committed (head $H_BEFORE -> $H_AFTER)"
else
  fail "tokened publish did not advance the chain head"; cat /tmp/econ_add1.out
fi

# (ii) Unlinkability on the wire: the committed user entry has a Token/Serial and
# a zero-NodeID Publisher. We copy val1's chain.cbor out and inspect it.
echo "== unlinkability proof: inspect the committed entry in val1's chain.cbor =="
dc cp val1:/data/chain.cbor /tmp/econ_chain.cbor >/dev/null 2>&1 || true
if [ -s /tmp/econ_chain.cbor ]; then
  UNLINK=$(python3 - /tmp/econ_chain.cbor <<'PY'
import sys
d=open(sys.argv[1],'rb').read()
has_token  = d.find(b'Token')>=0 and d.find(b'Serial')>=0
# A user (tokened) entry stamps a zero NodeID: the CBOR marker "PublisherX "
# (X=0x58 byte-string, 0x20=32 len) followed by 32 zero bytes.
zero_pub = b'PublisherX '+b'\x00'*32
has_zero_pub = d.find(zero_pub)>=0
print("token=%s zero_publisher=%s"%(has_token, has_zero_pub))
sys.exit(0 if (has_token and has_zero_pub) else 1)
PY
) && ZOK=1 || ZOK=0
  echo "  chain.cbor: $UNLINK"
  if [ "$ZOK" = 1 ]; then
    echo "  positive (ii): PASS — committed entry carries a Token+Serial and a ZERO Publisher (unlinkable)"
  else
    fail "committed entry did not show a token + zero-NodeID Publisher"
  fi
else
  fail "could not read val1's chain.cbor for the unlinkability proof"
fi

# ─────────────────────────────────────────────────────────────────────────────
# CLAIM (b) DOUBLE-SPEND — FINDING. The chain rejects a reused token serial
# (core/chain: ErrTokenSpent "publish token serial already spent (double-spend)")
# and the online issuer rejects a reused credit serial (core/node/tokenrole.go).
# BUT there is NO CLI/wire seam to REPLAY a serial: `silt swarm add -token-quorum`
# mints a FRESH random serial every invocation (blindtoken.NewSerial), and no flag
# lets you present a saved token/serial. So the double-spend rejection is real in
# code but UNREACHABLE from the product's CLI. We assert the guard exists at the
# unit level (ground truth) and report the wire gap loudly.
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "== CLAIM (b) double-spend: FINDING — no CLI seam; asserting the unit-level guard =="
if ( cd "$ROOT" && go test ./core/blindtoken/ ./core/chain/ -run 'DoubleSpend|Spent|Token' -count=1 >/tmp/econ_ds.out 2>&1 ); then
  grep -E '^(ok|PASS|---)' /tmp/econ_ds.out | head -6 | sed 's/^/  /'
  echo "  double-spend: guard PRESENT at unit level (ErrTokenSpent / issuer spent-set) — see FINDING 1"
else
  echo "  (double-spend unit tests: see /tmp/econ_ds.out)"; sed 's/^/  /' /tmp/econ_ds.out | tail -8
  fail "double-spend guard unit tests did not pass"
fi

echo ""
if [ "$pass" = 1 ]; then
  echo "RESULT: PASS ✅  economy observatory + blind-credit publisher-unlinkability validated"
  echo "  (double-spend rejection is real in core but has NO CLI/wire seam — see README FINDING 1)"
else
  echo "RESULT: FAIL ❌"
fi
exit $((1-pass))
