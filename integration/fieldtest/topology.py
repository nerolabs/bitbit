#!/usr/bin/env python3
"""topology.py — the "brains" of the field test.

Turns a small, human-editable table of nodes (role + deterministic id-seed +
static internal IP + zone) into two artifacts:

  topology.json                        — the orchestrator + scenarios address
                                          nodes by role over SSH from this.
  terraform/topology.auto.tfvars.json  — Terraform materialises one SPOT instance
                                          per node, each with its fully-rendered
                                          `silt` argv baked into the startup script.

Why this works with zero post-apply reconfiguration: `silt id -id-seed N` is
DETERMINISTIC (cmd/silt/id.go) and the internal IPs are STATIC (we choose them
before apply), so every -bootstrap / -anchors / -attesters / relay reference is
known BEFORE any VM exists. Each node's startup script is therefore fully
self-configuring and the swarm converges without a discovery wait.

Edit NODES to change the topology; re-run to regenerate. Env knobs:
  SILT_BIN    how to invoke silt to compute NodeIDs (default: "go run ./cmd/silt"
              from the repo root; a prebuilt "silt" binary is faster)
  BOND_MODE   fast (default) | faithful   — see README (mechanism vs economics)
  SWARM_PORT / RELAY_PORT / REGISTRY_PORT
"""
import json, os, shlex, subprocess, sys

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
SILT_BIN = os.environ.get("SILT_BIN", "go run ./cmd/silt")
SWARM_PORT = int(os.environ.get("SWARM_PORT", "4001"))
RELAY_PORT = int(os.environ.get("RELAY_PORT", "4002"))
REGISTRY_PORT = int(os.environ.get("REGISTRY_PORT", "8443"))
BOND_MODE = os.environ.get("BOND_MODE", "fast")
PUBLIC_CIDR = os.environ.get("PUBLIC_CIDR", "10.20.0.0/24")
NAT_CIDR = os.environ.get("NAT_CIDR", "10.30.0.0/24")
STORE = "/var/lib/silt"

# ── The node table ─────────────────────────────────────────────────────────────
# role: validator | storage | registry | relay | fetcher | natgw | natted | adversary
# IPs are STATIC and must sit in the subnet the role belongs to (public vs nat).
# Zones are spread across regions on purpose, to exercise REAL inter-node latency.
NODES = [
    # name        role         seed  ip            zone
    ("val-a",     "validator", 6001, "10.20.0.11", "us-central1-a"),
    ("val-b",     "validator", 6002, "10.20.0.12", "us-east1-b"),
    ("val-c",     "validator", 6003, "10.20.0.13", "europe-west1-b"),
    ("val-d",     "validator", 6004, "10.20.0.14", "us-central1-a"),
    ("store-1",   "storage",   6101, "10.20.0.21", "us-central1-a"),
    ("store-2",   "storage",   6102, "10.20.0.22", "us-east1-b"),
    ("registry",  "registry",  6201, "10.20.0.31", "us-central1-a"),
    ("relay",     "relay",     6301, "10.20.0.41", "us-central1-a"),
    ("fetch-1",   "fetcher",   6401, "10.20.0.51", "us-central1-a"),
    ("adversary", "adversary", 6901, "10.20.0.91", "us-central1-a"),
    ("natgw",     "natgw",        0, "10.30.0.2",  "us-central1-a"),
    ("nat-1",     "natted",    6501, "10.30.0.11", "us-central1-a"),
    ("nat-2",     "natted",    6502, "10.30.0.12", "us-central1-a"),
]

# SMOKE=1 trims the topology to the cheapest set that still exercises the whole
# terraform → provision → SSH → publish → commit → fetch plumbing (~4 nodes, a
# few cents). Use it for the FIRST real run to shake out infra before paying for
# the full 13-node topology. Scenarios that need absent nodes (NAT, adversary,
# 4th validator) skip cleanly rather than fail.
if os.environ.get("SMOKE") == "1":
    _keep = {"val-a", "val-b", "store-1", "fetch-1"}
    NODES = [n for n in NODES if n[0] in _keep]


def node_id(seed):
    """Deterministic NodeID for an id-seed, via `silt id`."""
    out = subprocess.run(shlex.split(SILT_BIN) + ["id", "-id-seed", str(seed)],
                         cwd=REPO_ROOT, capture_output=True, text=True)
    if out.returncode != 0:
        sys.exit(f"topology: `silt id -id-seed {seed}` failed:\n{out.stderr}")
    return out.stdout.strip().splitlines()[0]


def main():
    nodes = {name: {"role": role, "seed": seed, "ip": ip, "zone": zone}
             for (name, role, seed, ip, zone) in NODES}
    for name, n in nodes.items():
        n["nodeid"] = "" if n["role"] == "natgw" else node_id(n["seed"])

    validators = [name for name, n in nodes.items() if n["role"] == "validator"]
    n_val = len(validators)
    anchors = ",".join(nodes[v]["nodeid"] for v in validators)
    # Size quorum for f=1 crash tolerance: after one validator is down, the
    # proposer + `quorum` attesters must still be reachable. quorum = n_val - 2
    # (min 1) means losing any one validator still leaves proposer + quorum.
    # (Byzantine-quorum defaults ON for objective validators and may raise the
    # effective bar; the fault-tolerance scenario records the observed threshold
    # so the first real run tells you the exact number to pin — see README.)
    quorum = max(1, n_val - 2)
    boot = validators[0]                              # first validator = bootstrap anchor
    bootstrap = f'{nodes[boot]["nodeid"]}@{nodes[boot]["ip"]}:{SWARM_PORT}'
    relay = next((name for name, n in nodes.items() if n["role"] == "relay"), None)
    relay_ref = f'{nodes[relay]["nodeid"]}@{nodes[relay]["ip"]}:{RELAY_PORT}' if relay else ""

    # Bond economics: FAST proves the mechanism cheaply; FAITHFUL is economically real.
    if BOND_MODE == "faithful":
        bond, min_bond, min_floor = "2G", "1G", "1G"
    else:
        bond, min_bond, min_floor = "64M", "1M", "0"

    common = f"-listen 0.0.0.0:{SWARM_PORT} -store {STORE} -mdns=false -log info"

    def argv(name):
        n = nodes[name]
        role, ip = n["role"], n["ip"]
        if role == "validator":
            attesters = ",".join(nodes[v]["nodeid"] for v in validators if v != name)
            a = (f"daemon {common} -advertise {ip}:{SWARM_PORT} -validator -objective "
                 f"-min-bond {min_bond} -min-bond-floor {min_floor} -mature-validators {n_val} "
                 f"-anchors {anchors} -attesters {attesters} -quorum {quorum} "
                 f"-bond {bond} -bond-audit 30s -capacity 5G")
            if name == boot:
                a += f" -serve-registry 0.0.0.0:{REGISTRY_PORT}"   # boot validator issues tokens
            else:
                a += f" -bootstrap {bootstrap}"
            return a
        if role == "storage":
            return f"daemon {common} -advertise {ip}:{SWARM_PORT} -bootstrap {bootstrap} -capacity 5G"
        if role == "registry":
            return f"daemon -store {STORE} -log info -registry-only -serve-registry 0.0.0.0:{REGISTRY_PORT}"
        if role == "relay":
            return (f"daemon {common} -advertise {ip}:{SWARM_PORT} -bootstrap {bootstrap} "
                    f"-relay 0.0.0.0:{RELAY_PORT} -capacity 5G")
        if role == "fetcher":
            return f"daemon {common} -advertise {ip}:{SWARM_PORT} -bootstrap {bootstrap} -capacity 2G"
        if role == "natted":
            # un-dialable (no -advertise): must reach the swarm THROUGH the relay
            return f"daemon {common} -bootstrap {bootstrap} -relay-via {relay_ref} -capacity 2G"
        if role == "adversary":
            # honest by default; #184 scenarios relaunch it with -equivocate/-forge-block/etc.
            return (f"daemon {common} -advertise {ip}:{SWARM_PORT} -bootstrap {bootstrap} "
                    f"-validator -objective -min-bond {min_bond} -min-bond-floor {min_floor} "
                    f"-anchors {anchors} -quorum {quorum} -bond {bond} -bond-audit 30s -capacity 2G")
        if role == "natgw":
            return "NATGW"   # not a silt node — runs integration/nat/natgw.sh instead
        sys.exit(f"topology: unknown role {role} for {name}")

    for name, n in nodes.items():
        n["argv"] = argv(name)

    meta = {
        "swarm_port": SWARM_PORT, "relay_port": RELAY_PORT, "registry_port": REGISTRY_PORT,
        "bond_mode": BOND_MODE, "n_val": n_val, "quorum": quorum, "bootstrap": bootstrap,
        "relay_ref": relay_ref, "anchors": anchors, "boot": boot,
        "public_cidr": PUBLIC_CIDR, "nat_cidr": NAT_CIDR,
    }
    here = os.path.dirname(os.path.abspath(__file__))
    with open(os.path.join(here, "topology.json"), "w") as f:
        json.dump({"meta": meta, "nodes": nodes}, f, indent=2)

    os.makedirs(os.path.join(here, "terraform"), exist_ok=True)
    tfvars = {
        "nodes": {name: {"role": n["role"], "ip": n["ip"], "zone": n["zone"], "argv": n["argv"]}
                  for name, n in nodes.items()},
        "public_cidr": PUBLIC_CIDR, "nat_cidr": NAT_CIDR,
        "swarm_port": SWARM_PORT, "relay_port": RELAY_PORT, "registry_port": REGISTRY_PORT,
    }
    with open(os.path.join(here, "terraform", "topology.auto.tfvars.json"), "w") as f:
        json.dump(tfvars, f, indent=2)

    sys.stderr.write(
        f"topology: wrote topology.json + terraform/topology.auto.tfvars.json "
        f"({len(nodes)} nodes, {n_val} validators, quorum {quorum}, bond={BOND_MODE})\n")


if __name__ == "__main__":
    main()
