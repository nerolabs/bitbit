#!/bin/sh
# Flat-net daemon role for the economy field test. On a shared bridge every
# container CAN dial every other — but silt never advertises a wildcard bind on
# its own (a 0.0.0.0 listen is not a dialable endpoint), so a peer that hears
# "I'm at 0.0.0.0:4001" cannot dial back and no token request / attestation
# lands. So we discover our OWN container IP and stamp it via -advertise, the
# same job integration/nat/node.sh does by re-routing: make this daemon
# reachable at a real address with zero test-only daemon flags.
set -e

# Our container IP on the economy bridge (first global v4 address).
SELF_IP=$(ip -4 -o addr show scope global | awk '{print $4}' | cut -d/ -f1 | head -1)
[ -n "$SELF_IP" ] || { echo "[node] FATAL: could not determine container IP"; exit 1; }

PORT="${LISTEN_PORT:-4001}"
echo "[node] self $SELF_IP:$PORT; exec: $* -advertise=$SELF_IP:$PORT"
exec "$@" "-advertise=$SELF_IP:$PORT"
