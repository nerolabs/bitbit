#!/usr/bin/env bash
# lib.sh — shared helpers for the field-test orchestrator + scenarios.
# Sourced, not executed. Targets bash 3.2+ (macOS default) — no associative arrays.

FT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${NODES_JSON:=$FT_DIR/nodes.json}"        # terraform output -json nodes, written by fieldtest.sh
: "${RESULTS_JSONL:=$FT_DIR/results.jsonl}"  # one line per SLO check
: "${PROJECT_ID:?PROJECT_ID must be set (config.env)}"

# ── node metadata (from terraform output) ──────────────────────────────────────
node_field() { # node_field NAME FIELD
  python3 -c "import json,sys;print(json.load(open('$NODES_JSON'))['$1']['$2'])"
}
node_names() { python3 -c "import json;print(' '.join(json.load(open('$NODES_JSON')).keys()))"; }
node_exists() { python3 -c "import json,sys;sys.exit(0 if '$1' in json.load(open('$NODES_JSON')) else 1)" 2>/dev/null; }

# ── remote exec over IAP (reaches natted nodes too; no key management) ──────────
ssh_node() { # ssh_node NAME "remote command"
  local name="$1"; shift
  local inst zone
  inst="$(node_field "$name" instance_name)"; zone="$(node_field "$name" zone)"
  gcloud compute ssh "$inst" --zone "$zone" --project "$PROJECT_ID" \
    --tunnel-through-iap --quiet --command "$*" 2>/dev/null
}

jlog() { ssh_node "$1" "sudo journalctl -u silt --no-pager -n ${2:-400}"; }

# Poll a node's silt journal until PATTERN appears (or TIMEOUT s). Echoes the
# matching line on success. Field networks are noisy → we assert over logs, and
# never require an exact count.
waitfor() { # waitfor NAME 'EXTENDED_REGEX' TIMEOUT_S
  local name="$1" pat="$2" timeout="${3:-120}" start now line
  start="$(date +%s)"
  while :; do
    line="$(jlog "$name" 800 | grep -E "$pat" | tail -1 || true)"
    [ -n "$line" ] && { printf '%s\n' "$line"; return 0; }
    now="$(date +%s)"; [ $((now - start)) -ge "$timeout" ] && return 1
    sleep 4
  done
}

# ── node control (restart / partition / adversarial relaunch) ──────────────────
svc() { ssh_node "$1" "sudo systemctl $2 silt.service"; }   # svc NAME start|stop|restart

# Append extra flags to a node's silt ExecStart and restart it — used to turn a
# node adversarial (-equivocate/-forge-block) or apply a runtime -denylist without
# re-running Terraform. `restore_argv` puts the baked argv back.
relaunch_with() { # relaunch_with NAME "-extra flags"
  local name="$1" extra="$2"
  ssh_node "$name" "sudo sed -i 's#^ExecStart=/usr/local/bin/silt .*#&#; s#^ExecStart=/usr/local/bin/silt \\(.*\\)\$#ExecStart=/usr/local/bin/silt \\1 ${extra}#' /etc/systemd/system/silt.service && sudo systemctl daemon-reload && sudo systemctl restart silt.service"
}
restore_argv() { # restore_argv NAME  — reset ExecStart to the baked metadata argv
  local name="$1" argv
  argv="$(ssh_node "$name" 'curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/attributes/silt-argv')"
  ssh_node "$name" "sudo sed -i 's#^ExecStart=.*#ExecStart=/usr/local/bin/silt ${argv}#' /etc/systemd/system/silt.service && sudo systemctl daemon-reload && sudo systemctl restart silt.service"
}

# ── result recording (feeds gen_report.sh) ─────────────────────────────────────
_json_str() { python3 -c 'import json,sys;print(json.dumps(sys.argv[1]))' "$1"; }

record() { # record FLOW VERDICT SEVERITY DETAIL [ELAPSED_S]
  local flow="$1" verdict="$2" sev="$3" detail="$4" elapsed="${5:-}"
  printf '{"flow":%s,"verdict":%s,"severity":%s,"detail":%s,"elapsed_s":%s,"ts":%s}\n' \
    "$(_json_str "$flow")" "$(_json_str "$verdict")" "$(_json_str "$sev")" \
    "$(_json_str "$detail")" "${elapsed:-null}" "$(date +%s)" >> "$RESULTS_JSONL"
  case "$verdict" in
    pass) printf '  \033[32m✓ PASS\033[0m  %s — %s\n' "$flow" "$detail" ;;
    gap)  printf '  \033[33m~ GAP \033[0m  %s — %s\n' "$flow" "$detail" ;;
    skip) printf '  \033[90m- SKIP\033[0m  %s — %s\n' "$flow" "$detail" ;;
    *)    printf '  \033[31m✗ FAIL\033[0m  %s — %s\n' "$flow" "$detail" ;;
  esac
}

# require_nodes FLOW SEVERITY NODE...  — record skip + return 1 if any node is absent
# (so SMOKE=1 / trimmed topologies don't false-fail scenarios they can't run).
require_nodes() {
  local flow="$1" sev="$2"; shift 2
  local n
  for n in "$@"; do
    if ! node_exists "$n"; then record "$flow" skip "$sev" "skipped — node '$n' not in this topology"; return 1; fi
  done
  return 0
}

# slo_assert FLOW SEVERITY DETAIL  — call after setting `ok` (0/1) and `elapsed`.
slo_assert() { # slo_assert FLOW SEVERITY "detail" OK ELAPSED
  local flow="$1" sev="$2" detail="$3" ok="$4" elapsed="${5:-}"
  if [ "$ok" = 1 ]; then record "$flow" pass "$sev" "$detail" "$elapsed"
  else record "$flow" fail "$sev" "$detail" "$elapsed"; fi
}
