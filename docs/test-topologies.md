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
earned standing. Manual commands: [user-seam.md](user-seam.md) Role 4. The
**automated** version is the e2e harness — real daemons as OS processes over
real TCP:

```sh
go test ./e2e/ -run TestBondEarnedStandingCommitsOverTCP -v   # -short skips process spawns
```

To exercise the field scenarios (convergence across replicas, kill-a-validator,
restart-standing), scale that harness up: more `-validator` daemons, a higher
`-quorum`, kill/restart a daemon between publishes and assert the chain still
commits and the restarted node keeps its standing (its `plot/` reloads). Doing
these from the [user-seam](user-seam.md) commands is the roadmap-#52 field test.

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

## Notes for building new topologies

- **Deterministic ids.** `-id-seed N` gives a stable NodeID so one daemon can
  name another as an attester/bootstrap before it exists (the e2e and NAT
  harnesses rely on this).
- **Registry ref is key-pinned.** Copy the exact `ID@https://…` line a
  `-serve-registry` daemon prints; a bare URL is refused.
- **Assert on log lines.** Daemons print machine-greppable lines (`peer:`,
  `registry:`, `chain: committed block N`, `relay-via: registered`,
  `hole-punch: direct connection established`) — the harnesses wait on these
  rather than on sleeps. Reuse the pattern.
- **Persistence lives in `-store`.** To test restart survival, reuse the same
  store dir; to test a clean node, use a fresh one. See the store-directory
  table in [user-seam.md](user-seam.md).
