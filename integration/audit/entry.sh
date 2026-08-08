#!/bin/sh
# Daemon entrypoint for the audit harness. On a flat Docker bridge network a
# wildcard bind (0.0.0.0) is NOT self-advertising — cmd/silt/daemon.go says a
# wildcard bind is never advertised on its own, so peers would learn no
# dialable address for us. Discover our own container IP and stamp it onto
# -advertise so every daemon announces a real, dialable HOST:PORT.
#
# Usage: entry.sh <port> <silt daemon args...>   (no -advertise; we inject it)
set -e
PORT="$1"; shift
IP=$(getent hosts "$(cat /etc/hostname)" | awk '{print $1}' | head -1)
[ -n "$IP" ] || IP=$(hostname -i 2>/dev/null | awk '{print $1}')
echo "[entry] container IP=$IP advertise=$IP:$PORT ; exec: $*"
exec silt daemon -advertise="$IP:$PORT" "$@"
