#!/bin/sh
# Holder/operator role: self-advertise this container's own IP so peers can
# dial us back (the daemon's reachability probe otherwise sees only the
# wildcard bind), then exec the real daemon. No test-only flags — the daemon
# concludes "public" because it really is dialable inside the compose network.
#
# ADVERTISE_PORT defaults to the swarm listen port (4001). We resolve our
# container IP from the default route's source address, which is stable inside
# a single-bridge compose network.
set -e
IP="$(ip -o -4 addr show scope global | awk '{print $4}' | cut -d/ -f1 | head -1)"
: "${ADVERTISE_PORT:=4001}"
echo "[node] advertising ${IP}:${ADVERTISE_PORT}; exec: $*"
exec "$@" -advertise "${IP}:${ADVERTISE_PORT}"
