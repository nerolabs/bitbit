# silt GCP field test (`#52`)

Spin up a **real, multi-machine silt network** on Google Cloud in a realistic
topology, run a full acceptance pass over the real wire, generate a **shareable
report**, and tear the whole thing down — one command, cost-bounded.

This is the automated form of roadmap **#52** (the multi-machine field test) and
the **RC gate**: a thorough green pass here is required to advance a release
candidate. It is also meant to be run by **outside developers** who want to see
silt operate end-to-end and produce a report of the outcome — it runs against
*their own* GCP project from a clean clone, needs nothing from the authors, and
hands back `report.md` + `report.html`.

> **What this adds over `integration/nat/`** (the local Docker harness): real
> separate machines (independent clocks, real crashes/restarts), real
> inter-region internet latency, and real scale — the things a single-host
> loopback cannot model. The Docker harness still owns fast NAT-matrix testing;
> this complements it, it does not replace it.

## What it exercises

A ~13-node topology across three regions:

| role | count | what it proves |
|------|-------|----------------|
| validators (`val-a..d`) | 4 | earned objective standing, multi-validator convergence, f=1 fault tolerance, Byzantine safety |
| storage (`store-1/2`) | 2 | content scatter, serve, per-operator takedown, restart survival |
| registry | 1 | `-registry-only` role comes up and serves |
| relay | 1 | NAT fallback / hole-punch rendezvous |
| fetcher | 1 | publish → fetch bit-perfect from a different node |
| natgw + natted | 3 | real NAT (cone/symmetric) → cross-NAT file movement via the relay |
| adversary | 1 | the `#184` drills: equivocation→slash, forged/low-bond→reject |

Scenarios map 1:1 onto the acceptance brief (`docs/reviews/m0-acceptance-brief.md`,
flows 1–9) plus the `#184` adversarial consensus-safety cases. Each records a
`pass` / `gap` / `fail` verdict with a severity and elapsed time.

## How it works (deterministic, self-configuring)

`silt id -id-seed N` is deterministic and the internal IPs are static, so
**every peer / anchor / attester / relay reference is computed before any VM
exists** (`topology.py`). Each node boots with its complete `silt` argv baked
into instance metadata — no discovery wait, no post-apply reconfiguration.

```
topology.py    seeds → NodeIDs → static IPs → the full `silt` argv per node
terraform/     VPC, public + NAT subnets, firewall, SPOT instances, budget alarm
provision/     startup scripts: pull the binary from GCS, run the argv under systemd
lib.sh         SSH-over-IAP, log-wait, SLO assertions, result recording
scenarios.sh   the 9 flows + 3 #184 drills
gen_report.sh  results.jsonl → report.md + report.html
fieldtest.sh   the orchestrator: build → apply → run → report → destroy
```

## Prerequisites

- A **GCP project with billing enabled**, and `gcloud auth login` done.
- Enable the APIs once: `gcloud services enable compute.googleapis.com iap.googleapis.com storage.googleapis.com` (+ `cloudbilling.googleapis.com` if you set a budget).
- Your account needs `roles/compute.admin`, `roles/iap.tunnelResourceAccessor`, and `roles/storage.admin` on the project (project **Owner** covers all of it).
- Local tools: `terraform`, `gcloud`, `go`, `python3`, `curl`.

## Run it

```bash
cd integration/fieldtest
cp config.env.example config.env      # set PROJECT_ID (+ optional knobs)
./fieldtest.sh                        # build → apply → run → report → DESTROY
```

At the end you get `report.md` and `report.html`. That HTML file is the artifact
to share / attach to an RC checklist or a GitHub issue.

Other lifecycles:

```bash
SMOKE=1 ./fieldtest.sh   # cheapest 4-node run — validate the plumbing for pennies first
./fieldtest.sh up        # bring the network up and leave it (debugging / iterate on scenarios)
./fieldtest.sh run       # re-run the scenarios against an up network (no new spend)
./fieldtest.sh down      # terraform destroy
./fieldtest.sh nuke      # last resort: delete everything labelled fieldtest=<run>
```

**First time? Follow `HANDOFF.md`** — a step-by-step shakedown runbook (no-spend
validate → SMOKE → full run → confirm teardown) that a fresh session can drive with
you. `SMOKE=1` trims to 4 nodes so you validate apply/provision/SSH/publish before
paying for the full topology; scenarios that need absent nodes skip cleanly.

## Cost model — three independent guards

The default lifecycle **destroys on exit, even on error or Ctrl-C** (`trap … EXIT`).
On top of that:

1. **SPOT/preemptible instances** — cheapest tier; GCP may reclaim them, and they
   never survive 24h.
2. **Per-VM self-destruct** — every VM runs `shutdown -h +TTL_MINUTES` at boot, so
   even a crashed orchestrator cannot leave a VM running past the TTL (default 3h).
3. **Optional budget alarm** — set `BUDGET_AMOUNT_USD` + `BILLING_ACCOUNT` for a
   GCP billing-budget backstop.

If Terraform state is ever lost, `./fieldtest.sh nuke` deletes every resource by
its `fieldtest=<run_id>` label. A full run on `e2-small` nodes for ~30 minutes is
a few dollars; `faithful` bond mode (bigger disks, longer plot time) costs more.

## FAST vs FAITHFUL bonds (read this before trusting a green)

`BOND_MODE` controls what "earned standing" costs in the test:

- **`fast`** (default) — demo-sized bonds (`-bond 64M`, floor 0). Proves the
  **mechanism**: the objective consensus path, convergence, restart survival,
  NAT traversal, and the adversarial drills all run over real machines and real
  latency. It does **not** prove the C1 economic *magnitude*.
- **`faithful`** — real plotted bonds (`-bond 2G`, `-min-bond-floor 1G`). Bonds
  cost real disk + plot time, so standing is economically faithful. Use bigger
  `MACHINE_TYPE` + `BOOT_DISK_GB`. Slower and pricier; this is the mode for an
  economics-faithful RC gate.

A `fast` green means "the system works over the real wire"; a `faithful` green
additionally means "standing cost real resources". Don't conflate them.

## First-run shakedown (honest status)

This harness is validated locally where it can be: `topology.py` generates the
real, deterministic argv for every node (proven against `silt id`), and all shell
is syntax-checked. The **cloud path — `terraform apply`, the SSH/journald log
matching, and the exact quorum arithmetic — is validated on the first real run**
(it needs a live GCP project + spend). Expect first-run tuning in two places, both
flagged in code:

- **Quorum sizing for fault tolerance** — `-quorum` vs. the default-on
  `-byzantine-quorum` interplay for the validator count. The `6-fault-tolerance`
  scenario records the *observed* threshold as a `gap` (not a hard fail) so the
  first run tells you the exact number to pin.
- **Log-match patterns** — `waitfor` regexes in `scenarios.sh` are matched
  against the daemon's `-log info` output; if a phrasing differs on the live
  build, the check reports `gap`, not a false `pass`.

Nothing here fails *silently*: an un-met SLO is always recorded in the report.
