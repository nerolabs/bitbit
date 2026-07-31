#!/bin/sh
# NATed-daemon role: re-point our default route through the NAT gateway on our
# LAN, then exec the real daemon. Because the gateway masquerades us and never
# forwards inbound, this daemon can reach the public relay but cannot be dialed
# back — so silt's own reachability probe concludes "NATed" and leans on the
# relay, with zero test-only flags. Real behavior, not a simulated flag.
set -e
: "${GATEWAY:?set GATEWAY, the NAT gateway's LAN IP}"

ip route replace default via "$GATEWAY"
echo "[node] default route via $GATEWAY; exec: $*"
exec "$@"
