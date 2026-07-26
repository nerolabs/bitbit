#!/bin/sh
# dev-relay.sh — run a throwaway public Silt dev node: ordinary daemon,
# public IP, relay capability on. Development/test infrastructure — NOT
# operated by the Silt project; see deploy/README.md for the neutrality
# rules this must stay inside.
set -eu

SWARM_PORT="${SWARM_PORT:-4001}"
RELAY_PORT="${RELAY_PORT:-4002}"
STORE="${STORE:-$HOME/.silt-dev-relay}"
CAPACITY="${CAPACITY:-2G}"

cd "$(dirname "$0")/.."
echo "building silt..."
go build -o "$STORE/silt" ./cmd/silt 2>/dev/null || {
    mkdir -p "$STORE"
    go build -o "$STORE/silt" ./cmd/silt
}

# The box's public IP, for the copy-paste lines below. Overridable
# because some providers NAT their VMs (in which case this box is the
# wrong box for the job).
PUBLIC_IP="${PUBLIC_IP:-$(curl -fsS https://api.ipify.org 2>/dev/null || echo '<public-ip>')}"

echo "starting dev relay daemon (store: $STORE)"
echo "  swarm: 0.0.0.0:$SWARM_PORT   relay: 0.0.0.0:$RELAY_PORT"
echo
echo "when the daemon prints its 'peer:' line (<ID>@...), hand out:"
echo "  bootstrap: <ID>@$PUBLIC_IP:$SWARM_PORT"
echo "  relay-via: <ID>@$PUBLIC_IP:$RELAY_PORT"
echo

# A wildcard bind is never advertised on its own (nobody can dial
# "0.0.0.0"), so a public box must say what address peers should learn.
ADVERTISE=""
case "$PUBLIC_IP" in
    *'<'*) echo "warning: could not determine public IP; gossip will not carry this box's address" ;;
    *) ADVERTISE="-advertise $PUBLIC_IP:$SWARM_PORT" ;;
esac

exec "$STORE/silt" daemon \
    -listen "0.0.0.0:$SWARM_PORT" \
    -relay "0.0.0.0:$RELAY_PORT" \
    $ADVERTISE \
    -store "$STORE" \
    -capacity "$CAPACITY" \
    -mdns=false \
    -debug
