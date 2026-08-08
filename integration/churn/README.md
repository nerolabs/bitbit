# Repair-under-churn integration harness (Docker)

The real-daemon analog of `silt sim run churn`. It stands up a real swarm — a
registry seed, a pool of holder daemons, and a caretaker — on one flat Docker
network, publishes an erasure-coded file, then **kills holders in waves** and
asserts the caretaker **reconstructs the lost shards from parity and re-scatters
them to fresh survivors**, with the file staying **bit-perfect** throughout.

Where the in-process sim proves this deterministically on a seeded scheduler,
this proves it over **real processes, real disk stores, and real sockets**, with
`docker kill` as a genuine abrupt node death.

```
  seed ── registry + bootstrap (holds no content: -capacity 1)
    │
    ├─ holder×N ── real disk stores; these get killed in waves
    └─ caretaker ── holds the care link, runs the repair loop (-log info)
```

There is **no NAT here** (that is `integration/nat`'s job) — every node is
directly dialable. The thing under test is **durability under churn**, not
traversal.

## Run it

```sh
./integration/churn/run.sh          # build, churn, assert, tear down; exit 0 = PASS
KEEP=1 ./integration/churn/run.sh   # leave the swarm up afterward to poke at
HOLDERS=20 WAVES=3 FILE_BYTES=50000000 ./integration/churn/run.sh   # crank it up
```

Needs Docker and a Go toolchain. The `silt` binary is compiled **on the host**
(CGO off → trivial cross-compile) and copied into a slim image — same approach
as `integration/nat`, keeping the image tiny and avoiding a Go-build memory
spike inside Docker.

## Why it takes a few minutes (on purpose)

Repair is not instant, and this test does not pretend it is:

- A caretaker sweeps every **RepairInterval = 60s** (compiled in, not a flag).
- Content is **3× replicated** *and* erasure-coded (**k=10 of n=16**). A stripe
  only needs rebuilding once node deaths drop **every replica of more than
  RepairSlack (=2) of its columns** — losing one or two holders is absorbed with
  no reconstruction at all (that is the redundancy working, not the test
  stalling).

So the harness kills **heaviest-coverage-first, one holder at a time**, waiting a
full sweep window after each, until the caretaker is *forced* to reconstruct,
then proves the fetch still succeeds. (Heaviest-first because with 3× replication
the few nodes closest to the most column keys hold almost everything — random
kills barely strand a column until near-total wipeout.) A longer run is a more
real run; we don't rush the sweeps.

## What it asserts

| Assertion | How it's observed |
|-----------|-------------------|
| Publish + baseline fetch bit-perfect | ephemeral `swarm add` / `swarm get`, SHA-256 match |
| Killing holders eventually strands columns | progressive `docker kill`, watching the caretaker's `debug.log` |
| The caretaker **reconstructs** | exact log line **`stripe repaired`** appears (at `-log info`) |
| Reconstructed shards **re-seed onto survivors** | the live swarm's stored-object count grows across the repair |
| The file survives the churn | `swarm get` is **bit-perfect** after each wave |
| It holds under *sustained* churn | the whole kill→repair→refetch cycle repeats for `WAVES` waves |

Intermediate states are surfaced too: `within repair slack — watching` (loss
absorbed by replicas, repair correctly holding) and `repair below k` (too few
shards momentarily reachable to reconstruct — it retries next sweep). On a
failing assertion the driver prints a `FIELD-TEST FINDING` block — caretaker
repair-decision tally, fetch error, and surviving-holder coverage — so a failure
is a report, not a mystery.

## Current status (as of this harness landing)

**Light churn passes; heavy churn does not — and that surfaced real findings.**
On a fresh clone, killing a couple of holders (no columns stranded) keeps the
file bit-perfect. But driving churn hard enough to force *erasure-level*
reconstruction, this harness reproduced repair-under-churn failures: the
caretaker either never completes a repair sweep (drowning in dial-timeouts to
dead holders' stale provider records) or reconstructs yet a fresh client still
can't re-fetch. See the PR description for the full findings, evidence, and
repro. **This harness is expected to fail against current silt** — that failure
is the deliverable, for the builder to address.

### The small-swarm caveat (read this)

A 12-node laptop swarm has **severe placement skew**: a few nodes sit closest to
most of the 16 column keys and hold the bulk of the shards, so redundancy can
collapse from healthy to below-k in a *single* node death, giving the 60s repair
sweep no repairable window to act in. At real scale (hundreds/thousands of
nodes) placement is far more even and the window is wider. So treat the "cliff"
behavior as **partly a small-swarm artifact** — a faithful repair-under-churn
test likely wants a large (50+ node / multi-VM) swarm. The dial-storm and
repair-then-fetch observations, by contrast, look scale-independent.

## Knobs

| env | default | meaning |
|-----|---------|---------|
| `HOLDERS` | 15 | size of the storage pool |
| `PROTECTED` | 4 | holders never killed (a permanent survivor set) |
| `WAVES` | 2 | how many kill→repair→refetch cycles to run |
| `FILE_BYTES` | 20000000 | published file size (≈32 stripes at the default geometry) |
| `STEP_WAIT` | 150 | seconds to wait for a repair sweep after each kill (>2 sweeps) |
| `KEEP` | 0 | `KEEP=1` leaves the topology up for inspection |

## Poking at a running swarm (`KEEP=1`)

```sh
KEEP=1 ./integration/churn/run.sh
cd integration/churn
docker compose ps
docker compose exec caretaker sh -c 'grep -E "stripe repaired|watching|below k" /data/debug.log'
docker compose exec caretaker sh -c 'find /data/objects -type f | wc -l'   # shards it re-seeded
docker compose down -v
```

## Scenarios (this is the seed; more to come)

- **`./run.sh`** — publish → kill-until-repair → refetch, twice. Proves
  caretaker reconstruction and re-scatter keep a file alive across real node
  death.
- **Next:** a boundary scenario — keep killing *past* the k-of-n floor and
  assert `swarm get` **fails loudly**, naming the unrecoverable stripe rather
  than returning wrong bytes; then bring nodes back and watch it recover.
- **Next:** multiple caretakers (the sim uses 3) to prove repair throughput and
  that concurrent caretakers don't duplicate or corrupt re-seeded shards.
- **Next:** cross with `integration/nat` — repair where the holders are behind
  NAT and reconstruction traffic crosses the relay.
