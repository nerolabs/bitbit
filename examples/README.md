# silt examples — runnable operator playbooks

Self-contained scripts that stand up a real silt swarm on loopback and walk the
core operator flows end to end. Each one builds the binary if needed, prints a
clear `PASS`/`FAIL`, and **kills only the daemons it started** (tracked PIDs, no
blanket `pkill` — safe to run on a box already running silt). Scratch lives
under `<repo>/_examples_*` and is git-ignored; delete it anytime.

These double as the multi-machine field test the project owes itself (roadmap
#52): running `flows567-…` from the docs *is* that test, done locally.

## Run them

```sh
go build -o silt ./cmd/silt        # or let each script build it
./examples/flow2-publish-fetch.sh
./examples/flow4-earned-standing.sh
./examples/flows567-convergence-fault-restart.sh
./examples/flow8-takedown.sh
```

Run **one at a time** — each binds fixed loopback ports (7100–7103, and
7200/7201 for the two-operator takedown). From outside the repo, point at it:
`SILT_REPO=/path/to/silt ./examples/flow2-publish-fetch.sh`.

| Script | Flows | What it proves |
|---|---|---|
| `flow2-publish-fetch.sh` | 2 | Cross-process swarm publish/fetch, bit-perfect after the publisher leaves and after a holder is killed. |
| `flow4-earned-standing.sh` | 4 | Earned-standing commit **plus** a negative control: an unbonded publish is refused — not a rubber-stamp. |
| `flows567-convergence-fault-restart.sh` | 5, 6, 7 | 3-validator convergence (identical head hash), survivor-quorum commit after a kill, and restart with **no re-plot** + chain catch-up + storage re-announce. |
| `flow8-takedown.sh` | 8 | Per-hash denylist on one operator; the other operator still serves. No global switch. |

## Two helpers the playbooks lean on

- **`silt id [-id-seed N | -store DIR] [-listen ADDR]`** — prints the NodeID a
  daemon *would* use, without launching one. That's how the validator scripts
  fill `-attesters <ID>` before starting the peer it names (validator setup is
  otherwise chicken-and-egg — you'd need B's ID to start A, and A to start B).
- **`silt chain-status [-store DIR]`** — read-only summary of a validator's
  committed chain (head height, head hash, block/entry counts). Run it on each
  replica: identical head height **and** head hash means they agree byte-for-byte
  — how the convergence and restart-catch-up checks work without hashing
  `chain.cbor` by hand.

## The flows not scripted here

- **Flow 1 (build & first run)** and **Flow 3 (care link)** are one-liners:
  ```sh
  go build -o silt ./cmd/silt
  ./silt sim run churn                  # flow 1: erasure durability, in-process
  ./silt add somefile                   # prints a silt: link + a siltcare: care link
  ./silt get  <siltcare:...> -o x       # flow 3: REFUSED — a care link can't decrypt
  ./silt info <siltcare:...>            # flow 3: shows sealed structure only
  ```
- **Flow 9 (cross-NAT)** uses the project's Docker harness directly:
  ```sh
  ./integration/nat/run.sh              # cross-NAT publish→fetch, real kernel NAT
  RESTART=1 ./integration/nat/run.sh    # + full-swarm restart survival
  ```

## Notes

- The `sleep` values that wait for bond-audit standing to accrue (12–14s) are
  generous for a laptop; bump them on a slow box if a commit step flakes.
- These scripts originated as the M0 acceptance reproduction pass and were
  adopted here as the standing operator playbook.
