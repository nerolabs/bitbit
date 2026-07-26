# Silt

A content-addressed, erasure-coded, distributed file store — built as a
real product from day one, simulated in-process until it needs real
sockets. See `HANDOFF.md` for the full design brief and
`docs/math/` for friendly explanations of the math.

> **Early & experimental — 0.x, unaudited.** Silt is published to get
> technical feedback, not to be trusted with data you can't afford to
> lose. Please read the **[threat model](docs/threat-model.md)** — it
> names the weak parts on purpose — and help us break it.

**▶ [Build your own Silt test network on your own computer](docs/local-test-network.md)** —
a hands-on, end-to-end walkthrough: run the whole swarm in one command, then
stand up a real multi-node network on your laptop, publish a file, and watch it
survive a node death.

## Status: feature-complete core; durability + scale backbone done

The milestones below (M1–M14) are done, plus the Phase 1 durability
backbone: column-based placement, failure-domain-aware spreading, a
caretaker dispersion audit, and demand-responsive replication (see
[`ROADMAP.md`](ROADMAP.md) and [`docs/design/column-placement.md`](docs/design/column-placement.md)).
It has **not** had an independent security review — the
[threat model](docs/threat-model.md) is the honest account of what's
weak.

- ✅ M1 — chunk → encrypt → hash → manifest → Merkle root, and back;
  CLI `add`/`get` against a content-addressed disk store
- ✅ M2 — Reed-Solomon stripes: delete any n−k shards per stripe and
  `get` reconstructs bit-perfectly; one more and it fails loudly
- ✅ M3 — Kademlia over a deterministic in-process network: `add` on
  node A, `get` on node Z, chunks scattered across 50 nodes, A keeps
  nothing; survives packet loss and dead nodes
- ✅ M4 — churn & repair: 30% of the swarm killed in waves, caretaker
  repair loops rebuild lost shards from parity and re-seed them; the
  live view shows redundancy draining and recovering, and the file
  comes back bit-perfect
- ✅ M5 — economics observatory: credit-gated registry (1 credit = 1
  byte served, publishing costs a fee); watch the Gini coefficient
  climb as hosts earn, and freeloaders lose the ability to publish

Beyond the original handoff:

- ✅ M6 — real TCP transport: the identical core scatters and retrieves
  over actual sockets; only adapters changed (the hexagonal bet, won)
- ✅ M7 — toy proof-of-retrieval: chunks travel with Merkle proofs,
  audits challenge providers with nonce tags, and nodes that kept the
  proof but ditched the data get slashed into debt
- ✅ M8 — daemon mode: separate OS processes form a real swarm (disk
  stores, registry over HTTP, ephemeral add/get clients); daemons
  re-announce their held chunks on restart
- ✅ M9 — capacity: `daemon -capacity 2G` pledges bounded storage;
  placement spills over when nodes fill and spreads stripes for
  availability; every node estimates total network storage from local
  knowledge alone (`sim run capacity`)
- ✅ M10 — identity, TLS, discovery: NodeID = SHA-256(public key),
  every connection is mutual TLS pinned to the key (no CA — the
  identity IS the key, and reputation can't be shed without it);
  registry over pinned HTTPS; Bitcoin-style discovery (bootstrap
  flags → DNS seeds → peer exchange, address book persisted for
  flagless warm restarts)
- ✅ M11 — encrypted manifests: manifests are ciphertext twice over;
  the share handle is a silt link (root + key), and a one-way key
  hierarchy yields CARE LINKS — repair and audit rights with no
  ability to decrypt. Infrastructure now hosts noise describing noise
- ✅ M12 — the chain: the registry is an append-only block chain kept
  by the daemons; blocks commit only with a quorum of attestations
  from validators with EARNED reputation (audits + serving), fresh
  identities can't write, every replica re-validates everything
  (`sim run consensus`, or three `daemon -validator` processes)
- ✅ M13 — web UI: `daemon -ui 127.0.0.1:8081` serves an embedded
  dashboard (pledge, chunks, network estimate, chain, held roots), a
  drag-and-drop publish page (→ silt link + care link), a
  paste-a-link fetch page, and a network observatory that aggregates
  many daemons — capacity, serving bandwidth, per-file shard spread.
  One Go binary, `go:embed`, zero extra runtime
- ✅ M14 — desktop client: `silt client` consumes AND serves in one
  process (pledges disk by default), keeps a link-book library (files
  you hold keys for — the rest of the network is opaque to you by
  design), and opens a browser UI. `build.sh` cross-compiles Mac /
  Windows / Linux from one source tree (see docs/desktop-client.md)

Where this is all going: [`ROADMAP.md`](ROADMAP.md) — identity/TLS,
encrypted manifests, the reputation-quorum chain, web frontends, and
the desktop client. The resolver layer that maps meaning onto opaque
identifiers is a deliberately separate product:
[`docs/aslan-boundary.md`](docs/aslan-boundary.md).

## Try it

The guided version of everything below — with what to look for at each
step — is [`docs/v1-test.md`](docs/v1-test.md).

```sh
go build -o silt ./cmd/silt

./silt add somefile.pdf            # prints the file's Merkle root
./silt ls                          # registry contents
./silt info <root>                 # stripe map: every shard, its stripe, its presence
./silt get <root> -o restored.pdf  # full verify-everything retrieval
./silt add secret.txt -mode private  # random key, no dedup, no confirmation attack
./silt add big.iso -k 4 -n 7       # custom erasure geometry

# the network, simulated: 100 nodes, 3% packet loss, 8 nodes killed
./silt sim run scatter -nodes 100 -loss 0.03 -kill 8 -seed 7

# the money demo: watch repair outrun two waves of node death
./silt sim run churn -seed 11

# the economy: hosts earn per byte served; freeloaders go broke
./silt sim run economy -seed 21

# storage audits: liars keep the proof, ditch the data, get caught
./silt sim run audit -seed 31

# the same core over real TCP sockets on localhost
./silt net demo -nodes 8
```

## Run a real swarm (separate processes)

The full, tested, step-by-step walkthrough — several daemons, a published
file, and a node death it survives — lives in
[**docs/local-test-network.md**](docs/local-test-network.md). The short
version:

```sh
# terminal 1: seed daemon — hosts the registry and a web UI
./silt daemon -listen 127.0.0.1:7101 -serve-registry 127.0.0.1:7100 \
              -store d1 -ui 127.0.0.1:8081 -capacity 2G
# it prints two lines to COPY VERBATIM:
#   registry: serving <ID>@https://127.0.0.1:7100   ← the registry ref
#   peer:     <ID>@127.0.0.1:7101                    ← the bootstrap string

# terminals 2..n: more daemons
./silt daemon -listen 127.0.0.1:7102 -store d2 -ui 127.0.0.1:8082 -capacity 2G \
  -bootstrap <ID>@127.0.0.1:7101 -registry <ID>@https://127.0.0.1:7100

# publish from anywhere: an ephemeral client joins, scatters, leaves
./silt swarm add movie.mp4 -peers <ID>@127.0.0.1:7101 -registry <ID>@https://127.0.0.1:7100

# retrieve from anywhere (kill a daemon first, for sport)
./silt swarm get <root> -o out.mp4 -peers <ID>@127.0.0.1:7101 -registry <ID>@https://127.0.0.1:7100
```

The registry is served over **key-pinned HTTPS**, so its reference is
`<ID>@https://host:port` — copy the exact `registry:` line the daemon
prints; plain `http://` or bare `https://` will fail. Daemons use real
disk stores and survive restarts (they re-announce what they hold). The
`-serve-registry` "single honest instance" is the seam a chain replaces
someday.

Chunks land in `.silt/objects/` named by SHA-256. Add the same file
twice in (default) convergent mode and you get the same root with zero
new bytes stored. Delete or corrupt up to n−k shards per stripe
(default: any 6 of 16) and `get` silently reconstructs them from parity;
one loss beyond that and it names the dead stripe and refuses.

## Layout

```
ports/       all cross-component interfaces + shared primitives
core/        pure logic: chunking, crypto, erasure, manifests/Merkle,
             pipeline, registry, dht (Kademlia), node behavior
adapters/    the effects: memstore, diskstore, fileregistry,
             simclock (deterministic scheduler), simnet (latency/loss/partitions)
sim/         the harness: clusters, scenarios, stats
cmd/         CLI
internal/depcheck  the architecture rule as a failing test
docs/math/   the math, explained for humans
```

The sim runs on a single-threaded event scheduler — no goroutines, no
wall clock, every random draw seeded — so any run reproduces exactly
from its seed. Failing scenarios print the seed that kills them.

Core packages import no adapters, no `os`/`net`/`time`/ambient
randomness — enforced by `go test ./internal/depcheck`. Every effect
arrives through an interface in `ports`, which is what will make the
M3/M4 network simulation deterministic and seed-replayable.

## Test

```sh
go test ./...
go test -bench . ./core/...
```
