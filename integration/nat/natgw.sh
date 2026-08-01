#!/bin/sh
# NAT gateway role: forward + masquerade a LAN out to the public segment, and
# never forward unsolicited inbound — so a daemon on the LAN behind us is
# genuinely un-dialable from outside (real NAT, real conntrack). Exactly the
# condition that forces two daemons to meet through the relay, or to
# hole-punch, instead of dialing each other directly.
set -e
: "${LAN_SUBNET:?set LAN_SUBNET, e.g. 10.20.0.0/24}"

# ip_forward is usually set via compose `sysctls:`; set it here too so the
# gateway works even when run by hand.
sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true

# Masquerade anything sourced from our LAN — robust to interface naming.
# Only connections the LAN *initiates* get a conntrack entry, so return
# traffic flows but unsolicited inbound to the LAN has nowhere to go: NAT.
#
# NAT_MODE selects how the external port is chosen — the property that decides
# whether hole-punching (#27) can work:
#   cone (default): preserve/reuse the source port → the same internal (ip,port)
#     maps to a stable external port across destinations (endpoint-independent),
#     so a peer can reuse the mapping the relay observed → PUNCHABLE.
#   symmetric: --random-fully picks a fresh external port per connection, so the
#     mapping is per-destination → the observed endpoint is useless for a punch
#     → the peers must FALL BACK to the relay.
MODE="${NAT_MODE:-cone}"
OPTS=""
[ "$MODE" = "symmetric" ] && OPTS="--random-fully"
iptables -t nat -A POSTROUTING -s "$LAN_SUBNET" -j MASQUERADE $OPTS

echo "[natgw] up — masquerading $LAN_SUBNET (mode=$MODE; inbound to the LAN is blocked)"
exec tail -f /dev/null
