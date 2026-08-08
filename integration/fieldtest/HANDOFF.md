# Field-test shakedown — handoff for a fresh Claude session

**You are a new Claude session with no prior context.** Your job: drive the silt
GCP field test through its **first real run, together with the operator (Andrew)**,
until it produces a clean report — or honest, well-understood gaps — and then leave
the cloud with **nothing running**. Work from this directory:
`integration/fieldtest/`. Read `README.md` first for what the harness is; this file
is the *how to drive it the first time* guide.

This run **spends real money** on the operator's GCP project and **must not leave
resources running**. Treat every `apply` as a spend gate.

---

## Ground rules (do not skip)

1. **Confirm before every `apply`.** Never run `./fieldtest.sh up` / `all` without
   the operator saying go. Each brings up real VMs.
2. **Always tear down.** The default lifecycle destroys on exit, but you verify it:
   after any run, `gcloud compute instances list --filter labels.fieldtest:* ` must
   be empty. If not, `./fieldtest.sh nuke`.
3. **Go cheap-first.** Validate with **no spend**, then a **4-node SMOKE** run
   (pennies), then the **full 13-node** run only once SMOKE is green.
4. **Iterate without re-paying.** Bring the network up once with `KEEP_UP=1`, then
   re-run scenarios for free with `./fieldtest.sh run` while you fix log-regexes /
   quorum. Tear down when done.
5. **Nothing fails silently.** Every unmet SLO lands in `results.jsonl` / the report
   as `gap` or `fail`. A `gap` means "couldn't confirm", not "broken" — investigate,
   don't paper over.

---

## Phase 0 — prerequisites (operator does these once, no spend)

Ask the operator to confirm each; help debug if any fails:

- [ ] `gcloud auth login` done, and a **billing-enabled GCP project** chosen.
- [ ] APIs enabled:
      `gcloud services enable compute.googleapis.com iap.googleapis.com storage.googleapis.com`
      (+ `cloudbilling.googleapis.com` only if using the budget alarm).
- [ ] The account has `roles/compute.admin`, `roles/iap.tunnelResourceAccessor`,
      `roles/storage.admin` (project **Owner** covers all three).
- [ ] Local tools installed: `terraform`, `gcloud`, `go`, `python3`, `curl`.
      (On macOS: `brew install terraform` and the Google Cloud SDK.)
- [ ] `cp config.env.example config.env`, then set `PROJECT_ID`. Leave the rest at
      defaults for the first run.

If `gcloud auth login` or `gcloud` is needed interactively, ask the operator to run
it themselves with the `! <command>` prefix so its output lands in the session.

---

## Phase 1 — validate with NO spend

Run these; they create nothing in the cloud:

```bash
cd integration/fieldtest

# a) the deterministic topology generator (builds a throwaway local silt binary)
( cd ../.. && go build -o integration/fieldtest/.silt-local ./cmd/silt )
SILT_BIN="$PWD/.silt-local" SMOKE=1 python3 topology.py    # should print "4 nodes, 2 validators"
python3 -c "import json,sys; t=json.load(open('topology.json')); print(t['nodes']['val-a']['argv'])"

# b) terraform validates the config (no apply)
terraform -chdir=terraform init -input=false
SILT_BIN="$PWD/.silt-local" SMOKE=1 python3 topology.py     # regenerate tfvars for validate
terraform -chdir=terraform validate
```

- If `terraform validate` errors, fix the HCL before spending anything. Likely
  first-run issues: a provider field renamed across `google` provider majors, or the
  `google_billing_budget` resource needing the budget vars unset (it's guarded by
  `count` — leaving `BUDGET_AMOUNT_USD=0` disables it).
- If `topology.py` errors, the local silt build failed — fix that first.

**Report Phase 1 results to the operator before proceeding.**

---

## Phase 2 — the SMOKE run (cheap, ~4 nodes, a few cents)

This validates the whole cloud path — apply, binary-pull, systemd boot, IAP SSH,
publish → commit → fetch — at minimum cost. The NAT / adversary / 4th-validator
scenarios **skip cleanly** (they aren't in the smoke topology).

**Get the operator's go**, then bring it up and leave it up so you can iterate:

```bash
SMOKE=1 KEEP_UP=1 ./fieldtest.sh up
```

Watch for: `all nodes ready`. If a node never goes active, jump to **Debugging**.

Then run the scenarios (repeatable, no new spend):

```bash
./fieldtest.sh run          # runs scenarios + writes report.md / report.html
```

Iterate: read the console + `report.md`. For every `gap`/`fail`, use the Debugging
playbook, fix `scenarios.sh` (usually a log-regex) or `topology.py` (quorum), then
just `./fieldtest.sh run` again. **You do not need to re-apply to change scenarios.**
(If you change `topology.py`, you *do* need to re-apply: `./fieldtest.sh down` then
`SMOKE=1 KEEP_UP=1 ./fieldtest.sh up`.)

When SMOKE is green (or only expected skips remain), **tear down**:

```bash
./fieldtest.sh down
gcloud compute instances list --project "$PROJECT_ID" --filter "labels.fieldtest:*"   # must be EMPTY
```

---

## Phase 3 — the full run (13 nodes, 3 regions)

Only after SMOKE is green. **Get the operator's go** (this is the real spend):

```bash
./fieldtest.sh            # full lifecycle: build → apply → run → report → DESTROY
```

Or, to iterate the full topology like SMOKE:

```bash
KEEP_UP=1 ./fieldtest.sh up && ./fieldtest.sh run    # then ./fieldtest.sh down
```

The full run exercises everything: multi-validator convergence, f=1 fault
tolerance, restart survival, per-hash takedown, cross-NAT via the relay, and the
`#184` adversarial drills (equivocation→slash, partition→heal, forged/low-bond
→reject). Hand the operator `report.html` at the end.

---

## The two KNOWN first-run tuning points (expect these)

1. **Quorum vs. Byzantine-quorum sizing (`6-fault-tolerance`).** `-byzantine-quorum`
   defaults ON for objective validators and may raise the effective commit
   threshold above the `-quorum` floor. The scenario records the *observed*
   behaviour as a `gap` (not a false pass). If it gaps: read val-a's journal around
   the publish while val-d is down, see what threshold it actually needed, and pin
   `quorum` in `topology.py` (the `quorum = max(1, n_val - 2)` line) accordingly —
   or add a validator. Then re-apply.
2. **Log-match regexes (`waitfor` in `scenarios.sh`).** Each check greps the
   daemon's `-log info` output for a phrase. If the live build phrases something
   differently, the check `gap`s. Fix: SSH to the node, read the real log line (see
   Debugging), update the regex, `./fieldtest.sh run` again. The patterns to expect
   are drawn from the e2e tests (`e2e/*.go`) — e.g. `chain: committed block N`,
   `slashed equivocator`, `reorged onto a heavier fork`.

Neither should require re-architecting anything — they're phrasing/number tuning.

---

## Debugging playbook

All nodes are reachable over IAP even without external IPs:

```bash
# instance names/zones for this run:
terraform -chdir=terraform output -json nodes | python3 -m json.tool

# SSH to a node (use the instance_name + zone from the output above):
gcloud compute ssh silt-ft-val-a-<run> --zone us-central1-a --tunnel-through-iap

# on the node:
sudo systemctl status silt.service
sudo journalctl -u silt.service --no-pager -n 200      # the daemon's own log
sudo journalctl -t silt-startup --no-pager             # the startup script (binary pull, unit write)
cat /etc/systemd/system/silt.service                   # the exact argv it's running
```

Common failures and fixes:

| symptom | likely cause | fix |
|---|---|---|
| node never `active`, `silt-startup` shows a curl 403 | VM can't read the GCS bucket | confirm `storage.objectViewer` bound (Terraform does this) + the service_account scope; re-apply |
| `silt-startup` shows binary pull OK but silt exits | bad argv / flag mismatch on the live build | read `journalctl -u silt`; compare the argv in the unit against `silt daemon -h`; fix `topology.py` |
| SSH hangs / permission denied | IAP not enabled or role missing | enable `iap.googleapis.com`; grant `roles/iap.tunnelResourceAccessor`; the firewall rule allows 35.235.240.0/20 |
| `apply` fails: SPOT capacity / quota | region/zone out of preemptible capacity | change a zone in `topology.py`, or `MACHINE_TYPE`; re-apply |
| publish never returns a `silt:` link | validators haven't earned standing yet / token issuer cold | it retries for `PUBLISH_RETRY_S`; raise it, or check val journals for standing |
| cross-NAT fails | natgw route/firewall or relay not reachable | check the natgw instance's `journalctl -t natgw-startup`; confirm the relay node is up |

---

## Definition of done

- SMOKE run: green (publish→commit→fetch bit-perfect over real machines).
- Full run: every flow `pass` or an understood `gap`; `report.html` generated.
- The two tuning points resolved or documented in the report.
- **Teardown confirmed empty** (`gcloud compute instances list` shows nothing for
  this run's label).
- Any `topology.py` / `scenarios.sh` fixes you made are ready to land as a PR
  (`main` is ruleset-protected — branch + PR, don't push to main). Summarize what
  you changed and why for the operator.

Then report back: the verdict, the report, total spend if visible, and any residual
gaps worth a follow-up issue.
