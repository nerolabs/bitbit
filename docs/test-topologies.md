# Test topologies — building the network shapes to test against

Both QA teams need to stand up and vary network shapes so their findings are
reproducible: a single box, a real socket swarm, a validator quorum, genuine
NATs, a partition that heals. This is the menu, cheapest first, with the
existing automated harness for each. The operations these shapes exercise are
in [user-seam.md](user-seam.md).

## 0 — One process (deterministic, no sockets)

The whole network inside a single deterministic event loop. Fastest, and
byte-reproducible from a seed — ideal for pinning a bug before you scale it.

```sh
silt sim run scatter -nodes 100 -loss 0.03 -kill 8 -seed 7   # storage under churn
silt sim run audit   -seed 31                                # liars caught
silt sim run consensus -seed 7                               # reputation-quorum commit
silt net demo -nodes 8                                       # same core over real localhost TCP
```

The in-process sims also cover the **trust-plane** shapes the wire tests can't
cheaply reach — see `sim/` (e.g. `TestPartitionHealsToHeavierFork`,
`TestBondAuditEarnsStandingOverTheNetwork`, the equivocation tests). Run them
with `go test ./sim/ -run <name> -v`.

## 1 — Localhost swarm (real sockets, one box)

Several `silt daemon` processes on `127.0.0.1`, real TLS, real disk stores.
Publish from an ephemeral client; kill a daemon and re-fetch. Full walkthrough:
[local-test-network.md](local-test-network.md). The shape:

```
  daemon1 (registry + UI) ── daemon2 ── daemon3 …    client: swarm add / swarm get
```

## 2 — Validator swarm (consensus, the M0 surface)

Two or more `-validator` daemons with bonds, committing a publish through
earned standing. Manual commands: [user-seam.md](user-seam.md) Role 4.

The **fastest stand-up** is the runnable [`examples/`](../examples/README.md)
playbooks — `flow4-earned-standing.sh` (a validator quorum + an unbonded-refused
negative control) and `flows567-convergence-fault-restart.sh` (three validators,
kill one, restart one). They use two helpers you'll want for any adversarial
setup: **`silt id`** prints a node's NodeID *without launching it* (so you can
fill `-attesters <ID>`/`-bootstrap <ID>@ADDR` before the peer exists), and
**`silt chain-status -store DIR`** prints a replica's head height + head hash —
your read-only instrument for whether two replicas actually agree. There is also
an **automated e2e** harness (real daemons over real TCP):

```sh
go test ./e2e/ -run TestBondEarnedStandingCommitsOverTCP -v   # -short skips process spawns
```

To exercise the field scenarios (convergence across replicas, kill-a-validator,
restart-standing), scale up: more `-validator` daemons, a higher `-quorum`,
kill/restart a daemon between publishes; confirm each replica agrees with
`silt chain-status` (same head height AND hash) and that a restarted node keeps
its standing (its `plot/` reloads, no re-plot). Doing these from the
[user-seam](user-seam.md) commands — or the `examples/` scripts — is the
roadmap-#52 field test.

## 3 — Cross-NAT internet (Docker, real kernel NAT)

A real multi-NAT "internet" on one host: NATed nodes behind `iptables`
MASQUERADE, a public relay, real TLS. This is `integration/nat/`
([its README](../integration/nat/README.md)), and it runs in CI.

```sh
cd integration/nat
./run.sh                 # cross-NAT publish → fetch, bit-perfect via the relay
RESTART=1 ./run.sh       # + full-swarm restart: stores persist, re-announce, re-fetch
./holepunch.sh           # cone NAT: two NATed daemons upgrade the relay path to DIRECT
NAT_MODE=symmetric ./holepunch.sh   # symmetric NAT correctly stays on the relay
```

Requires Docker (locally: `colima start`, PATH needs `/opt/homebrew/bin`). This
is the template for any topology needing genuine NAT behaviour or a relay.

## 4 — Partition and heal (fork-choice)

Split the network, let each side build its own history, then reconnect and watch
the lighter side reorg onto the heavier one. The **automated in-process** version
is `sim/reorg_test.go` `TestPartitionHealsToHeavierFork`. To reproduce over real
containers, extend topology 3: put validators on two container networks, sever
the link between them (`docker network disconnect`), let each commit, then
reconnect and run `SyncChain` — the diverged side should adopt the heavier fork.

This is the shape the **fork-choice / equivocation** adversary attacks, and
`silt chain-status -store DIR` is how you *observe* the outcome: run it on a
validator from each side. Two committed heads at the **same height with different
head hashes** means both partitions stood a history (the thing to make permanent);
after reconnect the lighter side's head hash should converge to the heavier's. A
break is: it doesn't converge, or *both* survive — the honestly-labelled residual
that locally-qualified fork-choice weight can diverge under an adversarial
partition (design §3e / CHANGELOG "honestly labelled") is the target to confirm or
exceed here.

## Notes for building new topologies

- **Deterministic ids.** `-id-seed N` gives a stable NodeID so one daemon can
  name another as an attester/bootstrap before it exists. `silt id -id-seed N`
  (or `-store DIR`) prints that ID *without launching a daemon* — the clean way
  to fill `-attesters`/`-bootstrap` up front.
- **Observe the chain read-only.** `silt chain-status -store DIR` prints head
  height, head hash, and block/entry counts from a replica's `chain.cbor`
  without a daemon — compare across replicas to detect agreement, divergence, or
  a stalled catch-up (no hashing files by hand).
- **Registry ref is key-pinned.** Copy the exact `ID@https://…` line a
  `-serve-registry` daemon prints; a bare URL is refused.
- **Assert on log lines.** Daemons print machine-greppable lines (`peer:`,
  `registry:`, `chain: committed block N`, `relay-via: registered`,
  `hole-punch: direct connection established`) — the harnesses wait on these
  rather than on sleeps. Reuse the pattern.
- **Persistence lives in `-store`.** To test restart survival, reuse the same
  store dir; to test a clean node, use a fresh one. See the store-directory
  table in [user-seam.md](user-seam.md).
