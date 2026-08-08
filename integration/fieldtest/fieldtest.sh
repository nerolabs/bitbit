#!/usr/bin/env bash
# fieldtest.sh — one-command GCP field test for silt (roadmap #52).
#
#   ./fieldtest.sh            build → topology → apply → run flows → report → DESTROY
#   ./fieldtest.sh up         bring the network up and leave it (implies KEEP_UP)
#   ./fieldtest.sh run        run the scenarios against an already-up network
#   ./fieldtest.sh report     regenerate the report from results.jsonl
#   ./fieldtest.sh down       terraform destroy
#   ./fieldtest.sh nuke       last-resort: delete every resource labelled fieldtest=<run>
#
# Teardown is guaranteed: the default lifecycle destroys on EXIT (even on error),
# every VM self-destructs after TTL_MINUTES, and `nuke` cleans up by label if
# Terraform state is ever lost. See README for the cost model.
set -euo pipefail
FT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$FT_DIR/../.." && pwd)"
cd "$FT_DIR"

[ -f config.env ] || { echo "no config.env — copy config.env.example and fill it in"; exit 1; }
# shellcheck disable=SC1091
. ./config.env
: "${PROJECT_ID:?set PROJECT_ID in config.env}"
: "${REGION:=us-central1}"
export PROJECT_ID REGION

# A stable run id for this invocation (no Date.now flakiness — derived from git + pid).
: "${RUN_ID:=$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo dev)-$$}"
RUN_ID="$(printf '%s' "$RUN_ID" | tr -c 'a-z0-9-' '-' | cut -c1-24)"
export RUN_ID
STATE_TFVARS="terraform/topology.auto.tfvars.json"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing prerequisite: $1"; exit 1; }; }
check_prereqs() { need terraform; need gcloud; need go; need python3; need curl; }

build_binary() {
  echo "==> building silt (linux/amd64) @ $RUN_ID"
  ( cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
      go build -trimpath -ldflags '-s -w' -o "$FT_DIR/silt-linux-amd64" ./cmd/silt )
  ( cd "$REPO_ROOT" && go build -o "$FT_DIR/.silt-local" ./cmd/silt )   # for topology id-gen
}

gen_topology() {
  echo "==> generating deterministic topology${SMOKE:+ (SMOKE — trimmed 4-node set)}"
  SILT_BIN="$FT_DIR/.silt-local" BOND_MODE="${BOND_MODE:-fast}" SMOKE="${SMOKE:-0}" python3 topology.py
}

tf() { terraform -chdir="$FT_DIR/terraform" "$@"; }

apply() {
  echo "==> terraform apply (run=$RUN_ID)"
  tf init -input=false >/dev/null
  tf apply -input=false -auto-approve \
    -var "project_id=$PROJECT_ID" \
    -var "default_region=$REGION" \
    -var "run_id=$RUN_ID" \
    -var "machine_type=${MACHINE_TYPE:-e2-small}" \
    -var "boot_disk_gb=${BOOT_DISK_GB:-20}" \
    -var "ttl_minutes=${TTL_MINUTES:-180}" \
    -var "silt_binary_path=$FT_DIR/silt-linux-amd64" \
    -var "budget_amount_usd=${BUDGET_AMOUNT_USD:-0}" \
    -var "billing_account=${BILLING_ACCOUNT:-}"
  tf output -json nodes > "$FT_DIR/nodes.json"
  echo "    nodes.json written ($(python3 -c "import json;print(len(json.load(open('$FT_DIR/nodes.json'))))") instances)"
}

wait_ready() {
  echo "==> waiting for silt.service on every node (startup pulls the binary + boots)"
  # shellcheck disable=SC1091
  . ./lib.sh
  local n deadline=$(( $(date +%s) + 600 )) pending
  while :; do
    pending=""
    for n in $(node_names); do
      [ "$(node_field "$n" role)" = natgw ] && continue
      ssh_node "$n" "systemctl is-active --quiet silt.service" 2>/dev/null || pending="$pending $n"
    done
    [ -z "$pending" ] && { echo "    all nodes ready"; return 0; }
    [ "$(date +%s)" -ge "$deadline" ] && { echo "    timed out waiting for:$pending"; return 1; }
    echo "    still starting:$pending"; sleep 15
  done
}

run_scenarios() {
  echo "==> running scenarios"
  : > "$FT_DIR/results.jsonl"
  # shellcheck disable=SC1091
  . ./lib.sh
  # shellcheck disable=SC1091
  . ./scenarios.sh
  run_all_scenarios || true    # a failing check is recorded, never aborts the run
}

report() { echo "==> report"; RUN_ID="$RUN_ID" ./gen_report.sh; }

teardown() {
  [ "${KEEP_UP:-0}" = 1 ] && { echo "==> KEEP_UP=1 — leaving the network up. './fieldtest.sh down' when done."; return; }
  echo "==> DESTROY (run=$RUN_ID)"
  tf destroy -input=false -auto-approve \
    -var "project_id=$PROJECT_ID" -var "default_region=$REGION" -var "run_id=$RUN_ID" \
    -var "silt_binary_path=$FT_DIR/silt-linux-amd64" 2>/dev/null \
    || { echo "    terraform destroy failed — falling back to nuke-by-label"; nuke; }
}

nuke() {
  echo "==> nuke: deleting every resource labelled fieldtest=$RUN_ID in $PROJECT_ID"
  gcloud compute instances list --project "$PROJECT_ID" \
    --filter "labels.fieldtest=$RUN_ID" --format 'value(name,zone)' | while read -r name zone; do
    [ -n "$name" ] && gcloud compute instances delete "$name" --zone "$zone" --project "$PROJECT_ID" --quiet || true
  done
  echo "    (subnets/network/bucket: 'terraform destroy' is preferred; check the console if state was lost)"
}

case "${1:-all}" in
  all)
    check_prereqs; build_binary; gen_topology
    trap teardown EXIT
    apply; wait_ready; run_scenarios; report
    ;;
  up)     check_prereqs; build_binary; gen_topology; KEEP_UP=1 apply; wait_ready; echo "network up (run=$RUN_ID)"; ;;
  run)    run_scenarios; report; ;;
  report) report; ;;
  down)   KEEP_UP=0 teardown; ;;
  nuke)   nuke; ;;
  *) echo "usage: ./fieldtest.sh [all|up|run|report|down|nuke]"; exit 1; ;;
esac
