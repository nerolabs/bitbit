# Multi-validator consensus + partition-heal harness (Docker)

Field test #1. It stands up **four real `silt daemon -validator` processes** on a
flat Docker network, drives real publishes through consensus, severs the set
with a real network partition, and asserts the **M0 keystone claim**:

> **Objective on-chain-bond fork-choice**: under a network partition, honest
> validators do not diverge onto a shared-but-conflicting head; each side commits
> its own fork, and when the partition heals they converge to the
> **heavier-bonded** chain.

This is the Docker-real, real-socket counterpart of the in-process
`e2e/partition_test.go` (`TestPartitionHealsToHeavierForkOverTCP`) — same
topology and flags, but over real kernel networking and container isolation, with
the partition driven by silt's own built-in test-harness flag `-block-peers`.

```
  flat network 10.50.0.0/24  (all four can route to each other)

    valA 10.50.0.11  registry ─┐  GROUP 1 (heavier): commits [g, a1, a2]  height 2
    valB 10.50.0.12  attester ─┘  — NOT partitioned; a healed valC reconnects here

    valC 10.50.0.13  registry ─┐  GROUP 2 (lighter): commits [g, c1]       height 1
    valD 10.50.0.14  attester ─┘  — carry -block-peers valA,valB → severed link
```

All four share the **same `-anchors` launch set**, so every replica starts from an
identical genesis config and objective fork-choice weight is computed the same way
everywhere. `run.sh` derives each NodeID from its `-id-seed` up front (via
`silt id`, run inside a throwaway container), so `-anchors` / `-attesters` /
`-block-peers` are all fillable before launch.

## Run it

```sh
./integration/consensus/run.sh          # build, test, tear down; exit 0 = PASS
KEEP=1 ./integration/consensus/run.sh    # leave it up afterward to poke at
```

Needs Docker and a Go toolchain. The `silt` binary is compiled **on the host**
(CGO off → trivial cross-compile to the container's arch) and copied into a slim
image, one image for all four roles — same approach as `integration/nat/`.

Isolation (shared machine): image tag `silt-consensus`, compose project
`consensus` (run from this dir), subnet `10.50.0.0/24`, host binary at
`integration/consensus/silt` (gitignored, rebuilt by `run.sh`).

## What each piece is

| file | role |
|------|------|
| `docker-compose.yml` | the topology: one flat network, 4 validators, named `/data` volumes so the chain persists across a recreate |
| `docker-compose.heal.yml` | heal override: recreate valC WITHOUT `-block-peers`, bootstrapped to valA — reload the persisted fork and reconcile |
| `Dockerfile` | slim runtime image carrying the prebuilt `silt` binary |
| `run.sh` | the driver: build → bring up → P0…P3 assertions → tear down |

## The assertions (each keys off a REAL observed CLI flag / log line / `chain-status` field)

- **P0 — negative control.** A lone objective validator with no bonded, qualified
  attester is asked to commit a publish. Assert **no `chain: committed block`**
  appears — the write path is *earned* standing, not a rubber-stamp (mirrors
  `examples/flow4-earned-standing.sh`).
- **P1 — convergence (pre-partition).** Group 1 commits a publish; assert its two
  replicas report an **identical `silt chain-status` head hash** (real command,
  real field: `head hash:`).
- **P2 — partition.** Assert the partition is in effect (`⚠ PARTITION: -block-peers
  set` on valC/valD — a real daemon stdout line), then that the two groups commit
  **different heads**: heavier group 1 reaches `chain: committed block 2`
  (height 2), lighter group 2 `chain: committed block 1` (height 1), distinct head
  hashes, and group 1 (which never blocked anyone) shows **no cross-partition
  reorg**. Honest sides forked; they did not diverge onto one conflicting head.
- **P3 — heal → converge.** Recreate valC WITHOUT `-block-peers`, bootstrapped to
  valA, on its **persisted `/data`**. It reloads `[g, c1]`, reconciles group 1's
  heavier fork, and its `chain-status` head becomes **identical to group 1's**
  (`a93…` at height 2) — convergence to the heavier-bonded chain, the M0 claim.

## Result

All four gates PASS on real Docker (Desktop, darwin/arm64), `run.sh` exits 0. The
M0 keystone reproduces exactly over real sockets: the two groups fork under the
partition, and on heal the lighter side reloads its persisted fork from disk and
reorgs onto the heavier one — narrated by the real daemon as, in one run:

```
valC on heal:
  chain: restored 2 block(s) from disk
  chain: reorged onto a heavier fork (dropped 1 block(s), new head height 2)
after heal — valA head: height=2 hash=88046cb2b9bbe1a8…
after heal — valC head: height=2 hash=88046cb2b9bbe1a8…   ← identical → converged
```

**No product deficiency found.** Objective on-chain-bond fork-choice behaves as
claimed, and the operator-facing reorg narration fires correctly over the real
wire (matching what the in-process `e2e/partition_test.go` asserts).

## Notes for the builder (harness bugs found and fixed while building)

- **Persistent store is load-bearing for the heal.** A first cut mounted no volume,
  so recreating valC gave it a fresh empty `/data`; it then *caught up* into the
  heavier fork without a reorg (nothing to drop), and the `reorged onto a heavier
  fork` line never fired — which looked like a product finding but was purely the
  harness losing the persisted `[g,c1]`. Fixed with named `/data` volumes so the
  heal is a genuine reload-and-reconcile; the reorg line then fires as expected.
  P3 still keys its PASS on the ground-truth `chain-status` head-hash equality and
  treats the reorg line as supporting evidence (robust to either code path).
- `^`-anchored log regexes never matched `docker compose logs`' `svc | ` line
  prefix — fixed (match `peer: <hex>@` unanchored).
- The `silt` binary is built for linux (the container), so `silt id` is run inside
  a throwaway container, not on the host.
- Single-project isolation: run only one invocation of `run.sh` at a time — its
  cleanup trap does `compose down -v` on the shared `consensus` project, so an
  overlapping second run can tear down the first's containers.

## Poking at a running topology (`KEEP=1`)

```sh
KEEP=1 ./integration/consensus/run.sh
cd integration/consensus
docker compose ps
docker compose exec valA silt chain-status -store /data     # head height + hash
docker compose logs valC | grep -E 'reorg|caught up|committed'
docker compose -f docker-compose.yml -f docker-compose.heal.yml down -v   # tear down
```
