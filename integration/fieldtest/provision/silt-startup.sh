#!/usr/bin/env bash
# silt node startup (rendered by Terraform templatefile; $${..} are BASH vars,
# $${ttl_minutes} below with no $ is the Terraform-injected value).
#
# Fully self-configuring: the node's entire `silt` argv — with every peer ID,
# anchor, and relay reference — was computed deterministically before this VM
# existed (topology.py) and handed in as the `silt-argv` metadata attribute. So
# there is nothing to discover and no reconfiguration round-trip.
set -euo pipefail
exec > >(logger -t silt-startup) 2>&1

# ── Cost backstop: hard self-destruct after the TTL, no matter what ─────────────
shutdown -h +${ttl_minutes} || true

meta() { curl -s -H "Metadata-Flavor: Google" "http://metadata.google.internal/computeMetadata/v1/instance/$1"; }

ARGV="$(meta attributes/silt-argv)"
BIN_URL="$(meta attributes/silt-binary-url)"     # gs://bucket/object
NODE="$(meta attributes/node-name)"

# ── Download the silt binary from GCS using the VM's own access token ───────────
# (avoids depending on gsutil/gcloud being preinstalled on the image)
TOKEN="$(meta service-accounts/default/token | python3 -c 'import sys,json;print(json.load(sys.stdin)["access_token"])')"
BUCKET="$${BIN_URL#gs://}"; OBJECT="$${BUCKET#*/}"; BUCKET="$${BUCKET%%/*}"
mkdir -p /var/lib/silt
curl -sf -H "Authorization: Bearer $${TOKEN}" \
  "https://storage.googleapis.com/storage/v1/b/$${BUCKET}/o/$${OBJECT}?alt=media" \
  -o /usr/local/bin/silt
chmod +x /usr/local/bin/silt

# ── Run silt under systemd so it restarts on failure and logs to journald ───────
# (the orchestrator reads progress with `journalctl -u silt` over SSH)
cat >/etc/systemd/system/silt.service <<UNIT
[Unit]
Description=silt field-test node ($${NODE})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/var/lib/silt
ExecStart=/usr/local/bin/silt $${ARGV}
Restart=on-failure
RestartSec=2
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now silt.service
echo "silt-startup: $${NODE} up — argv: $${ARGV}"
