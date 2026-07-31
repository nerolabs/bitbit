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
iptables -t nat -C POSTROUTING -s "$LAN_SUBNET" -j MASQUERADE 2>/dev/null \
  || iptables -t nat -A POSTROUTING -s "$LAN_SUBNET" -j MASQUERADE

echo "[natgw] up — masquerading $LAN_SUBNET (inbound to the LAN is blocked)"
exec tail -f /dev/null
