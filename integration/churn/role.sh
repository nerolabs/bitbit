#!/bin/sh
# Shared entrypoint for every daemon in the churn harness (seed, holders,
# caretaker). On a flat Docker network a wildcard bind is not self-advertising
# ("a wildcard bind is never advertised on its own" — daemon -advertise help),
# so each daemon must announce the concrete, routable IP its container got from
# Docker's IPAM. We discover it from /etc/hosts via getent (glibc, always
# present — no iproute2/hostname package needed) and append -advertise, then
# exec the daemon. The compose `command:` supplies everything else.
set -e
IP=$(getent hosts "$(cat /etc/hostname)" | awk '{print $1; exit}')
[ -n "$IP" ] || { echo "[role] FATAL: could not determine container IP"; exit 1; }
echo "[role] advertising $IP:4001 — exec: $*"
exec "$@" -advertise="$IP:4001"
