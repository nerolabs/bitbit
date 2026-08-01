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

# TCP hole-punching (#27) sends "crossing" SYNs that a stock NAT gateway drops:
# reverse-path filtering sees the asymmetric path, and conntrack treats the
# simultaneous-open SYN as out-of-window/invalid. Loosen both so a real cone
# NAT's punch behaviour shows through (a home router that permits hole-punching
# is doing the equivalent). None of this weakens the NAT itself — inbound with
# no matching mapping is still dropped.
sysctl -w net.ipv4.conf.all.rp_filter=0 2>/dev/null || true
sysctl -w net.ipv4.conf.default.rp_filter=0 2>/dev/null || true
sysctl -w net.netfilter.nf_conntrack_tcp_be_liberal=1 2>/dev/null || true
sysctl -w net.netfilter.nf_conntrack_tcp_loose=1 2>/dev/null || true

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

# A real NAT firewall DROPS unsolicited inbound (stealth); it does not RST. That
# distinction is what makes hole-punching possible: the first crossing SYN (no
# mapping yet) must be silently dropped and RETRANSMITTED, so that by the retry
# the peer's own outbound SYN has created the reverse mapping and the SYN is
# DNAT'd through. A stock container RSTs the closed port ("connection refused")
# and kills the punch. An already-mapped SYN is DNAT'd in PREROUTING before it
# reaches here, so this only silences the truly-unsolicited ones.
iptables -A INPUT -p tcp --syn -m conntrack --ctstate NEW -j DROP

echo "[natgw] up — masquerading $LAN_SUBNET (mode=$MODE; inbound to the LAN is blocked)"
exec tail -f /dev/null
