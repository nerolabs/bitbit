#!/usr/bin/env bash
# NAT-gateway startup (rendered by Terraform templatefile). This is the same NAT
# behaviour as integration/nat/natgw.sh, lifted onto a real GCE instance so the
# natted subnet is genuinely un-dialable and its peers must reach the swarm through
# the relay (or hole-punch). $${..} are BASH vars; ${nat_cidr}/${ttl_minutes} are
# Terraform-injected.
set -euo pipefail
exec > >(logger -t natgw-startup) 2>&1

shutdown -h +${ttl_minutes} || true   # cost backstop

sysctl -w net.ipv4.ip_forward=1 || true
# Loosen rp_filter/conntrack so a cone NAT's hole-punch behaviour shows through
# (#27) — this does not weaken the NAT itself; unsolicited inbound is still dropped.
sysctl -w net.ipv4.conf.all.rp_filter=0 2>/dev/null || true
sysctl -w net.ipv4.conf.default.rp_filter=0 2>/dev/null || true
sysctl -w net.netfilter.nf_conntrack_tcp_be_liberal=1 2>/dev/null || true
sysctl -w net.netfilter.nf_conntrack_tcp_loose=1 2>/dev/null || true

# NAT_MODE=cone (default) preserves the source port → punchable; symmetric uses
# --random-fully → per-destination mapping → peers must fall back to the relay.
MODE="$${NAT_MODE:-cone}"
OPTS=""
[ "$${MODE}" = symmetric ] && OPTS="--random-fully"

iptables -t nat -A POSTROUTING -s "${nat_cidr}" -j MASQUERADE $${OPTS}
# A real NAT firewall DROPs unsolicited inbound (stealth, not RST) so a crossing
# SYN is retransmitted until the reverse mapping exists — the condition that makes
# hole-punching possible. Already-mapped SYNs are DNAT'd in PREROUTING first.
iptables -A FORWARD -s "${nat_cidr}" -j ACCEPT
iptables -A FORWARD -d "${nat_cidr}" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -A FORWARD -d "${nat_cidr}" -p tcp --syn -m conntrack --ctstate NEW -j DROP

echo "natgw-startup: up — masquerading ${nat_cidr} (mode=$${MODE})"
