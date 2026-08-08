# Proof-of-retrieval (PoR) audit field test — the real "liar" node (Docker)

An automated, over-the-wire test of the storage-honesty story from
`silt sim run audit`. It stands up a **real multi-daemon swarm in containers**
— a registry/bootstrap seed, four honest holders, and a caretaker running the
real repair loop — publishes an erasure-coded file, then makes holders behave
like the sim's **liars**: they keep their persisted storage proofs but their
shard bytes are `rm`'d off disk while the daemons keep running. The harness
asserts, against real `<store>/debug.log` lines, that the caretaker **detects
the loss over the wire, reconstructs from parity, and re-scatters** the rebuilt
shards — and that the file stays bit-perfect.

```
            seed  (registry + bootstrap, 10.60.0.10, -capacity 1 = holds nothing)
              |
   holder1 .21   holder2 .22   holder3 .23   holder4 .24     (honest holders)
                          \        |        /
                            caretaker .30   (-care <careLink>, -log info)
```

Every daemon binds `0.0.0.0` but **self-advertises its own container IP** via
`entry.sh` — a wildcard bind is never advertised on its own (see
`cmd/silt/daemon.go` `-advertise`), so on a flat Docker network each daemon must
stamp its real dialable address itself.

## Run it

```sh
./integration/audit/run.sh          # build, test, tear down; exit 0 = PASS
KEEP=1 ./integration/audit/run.sh   # leave it up afterward to poke at
SWEEP_WAIT=90 ./integration/audit/run.sh   # widen the repair-sweep wait on a slow host
```

Needs Docker and a Go toolchain. The `silt` binary is compiled **on the host**
(CGO off → trivial cross-compile) and copied into a slim image, so the image
stays tiny and there's no Go-build memory spike inside Docker. Takes ~2–3 min:
most of it is one ~80 s wait for a caretaker repair sweep (the repair interval
is 60 s and is not a daemon flag).

## What each piece is

| file | role |
|------|------|
| `docker-compose.yml` | the topology: one `swarm` network, seed, 4 holders, caretaker |
| `Dockerfile` | slim runtime image (silt binary), one image for every role |
| `entry.sh` | daemon entrypoint: discovers the container IP, stamps `-advertise` |
| `run.sh` | the driver: build → publish → damage → assert detect+repair → tear down |

## What it asserts (all keyed to real observed behavior)

1. **Placement** — publish scatters shards across the four holders; the seed
   (`-capacity 1`) holds none, so the damage is unambiguous.
2. **Positive control** — before any damage the caretaker has logged **no**
   `stripe repaired`, and the file is retrievable bit-perfect from the intact
   swarm.
3. **The attack** — `rm -rf /data/objects/*` on **3 of 4 holders** while those
   daemons keep running. They still hold their persisted storage proofs
   (`n.proofs`) — "keep the receipt, ditch the goods" — but the bytes are gone.
4. **Detection over the wire** — the caretaker's availability probe
   (`MsgHasChunk`, which an honest holder now answers `false`) sees the shards
   vanish and logs `stripe degraded … within repair slack — watching` and/or
   `stripe repaired`.
5. **Repair + re-scatter** — at least one `stripe repaired root=… missing=…`
   line, and the emptied holders' stores go from `0` back to `>0` objects — the
   rebuilt shards, reconstructed from parity and re-seeded to fresh nodes.
6. **Durability** — the file is still bit-perfect after the loss+repair.

Why 3 of 4 holders? With the default `Replication=3` and `RepairSlack=2`,
deleting one or two holders leaves every stripe within slack (the caretaker
*correctly* stays quiet — that is the mechanism, not a stall). Deleting **all**
holders drops below `k` reachable shards and the caretaker logs
`repair below k` (it cannot reconstruct). Three of four is the regime that
strands stripes past the slack while ≥`k` survive — the one that forces a
visible reconstruction.

## Findings

### F1 — the literal PoR-audit / standing-slash claim is NOT reachable over the real daemon (confidence: high)

The sim's headline is a **verify-without-fetch** PoR challenge: an auditor
holding the care-link key catches a liar that kept the proof but dropped the
bytes, *without fetching ground truth*, and **slashes its standing** into debt
(`sim/audit.go`, `core/node/por.go` `Node.Audit`, `core/por`). Over the real
daemon, that path is **not drivable**:

- **No liar seam on the wire.** `Node.SetLiar` exists and is exercised by the
  sim, but no `silt daemon` flag toggles it. (Contrast the consensus red-team
  flags that *are* exposed: `-equivocate`, `-forge-block`, `-lowbond-propose`.)
- **No audit trigger.** `Node.Audit` — the PoR sweep that challenges every
  provider and settles passes/slashes into the credit ledger — is invoked
  **only** from the in-process sim (`sim.Audit`). Nothing in `cmd/silt`
  (daemon, caretaker `-care`, or any subcommand) ever calls it. The repair loop
  (`repairTick` → `repairRoot`) probes availability with `MsgHasChunk`; it never
  issues a PoR `MsgChallenge` as an audit. (The PoR *prover* side —
  `answerChallenge` — IS wired over TCP, and the caretaker DOES issue one
  identity-bound PoR challenge when *verifying a repair-bounty claim*
  (`challengeHolderRetrievability`), but there is no standalone audit sweep and
  no slash-on-fail-audit over the wire.)

**Repro:** `silt daemon -h` lists no `-liar` and no audit-trigger flag;
`grep -rn '\.Audit(' cmd/ core/ | grep -v _test` shows the only non-test/non-sim
call site is `sim/audit.go`. `run.sh` step 6 asserts this gap so the test fails
loudly (prompting a harness extension) if a future build wires the audit path.

**Consequence:** on the real network a "liar" is caught only **indirectly** —
once its bytes are gone it answers `MsgHasChunk=false`, the caretaker's probe
sees the shard disappear, and repair re-scatters. That indirect path is exactly
what this harness proves end-to-end. The *stronger* sim property — catching a
liar that is still lying about holding the bytes, *before* retrieval fails, via
a nonce PoR challenge, and docking its standing — has no daemon/CLI seam yet.

### F2 — a healthy repair sweep emits no narration, even at `-log debug` (confidence: medium)

Only the degraded/repaired/below-k branches log; a sweep that finds everything
within slack writes nothing at any level. That is defensible (no news is good
news), but it means "is the caretaker actually sweeping?" is unobservable from
logs until something is wrong — worth a single `-log debug` "sweep complete"
line for field diagnosis. `run.sh` works around it by damaging enough to force a
logged branch.

## Poking at a running topology (`KEEP=1`)

```sh
KEEP=1 ./integration/audit/run.sh
cd integration/audit
docker compose exec caretaker sh -c 'grep -E "stripe (degraded|repaired)|repair below k" /data/debug.log'
docker compose exec holder2 sh -c 'find /data/objects -type f | wc -l'   # re-scattered shards
docker compose down -v
```
