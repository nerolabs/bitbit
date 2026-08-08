# Silt

A content-addressed, erasure-coded, distributed file store — built as a
real product from day one, simulated in-process until it needs real
sockets. The design canon is [`docs/TENETS.md`](docs/TENETS.md) and the
path to V1 is [`ROADMAP.md`](ROADMAP.md); `docs/math/` has friendly
explanations of the math, and the current M0 security spec is
[`docs/design/m0.md`](docs/design/m0.md). Superseded history lives under
[`/archive/`](archive/).

> **Early & experimental — 0.x, unaudited.** Silt is published to get
> technical feedback, not to be trusted with data you can't afford to
> lose. Please read the **[threat model](docs/threat-model.md)** — it
> names the weak parts on purpose — and help us break it.

**▶ [Build your own Silt test network on your own computer](docs/local-test-network.md)** —
a hands-on, end-to-end walkthrough: run the whole swarm in one command, then
stand up a real multi-node network on your laptop, publish a file, and watch it
survive a node death.

## Status: storage plane field-proven; trust plane mechanism built, review pending

silt has two planes. The **storage plane** — content-addressed,
erasure-coded, peer-served chunks with NAT traversal — is
**field-proven at scale**: bit-perfect retrieval under churn, the
silent-loss failure shapes fixed (#46/#60/#64), and cross-network
publish/fetch proven through cone NAT via TCP hole-punching (CI Docker
harness). The **trust plane** — consensus-secured registry, reputation,
and revocation — is where M0 lives. M0's Sybil-resistance is a **systemic
composition held in tension**, not a single Sybil-proof primitive (no such
primitive can exist under free identity + no permanent center — that's Douceur).
The claim is **C1 — no discount** (earning a fraction *q* of consensus standing
costs ≈ *q* × the real resource an honest provider pays: disk × address-diversity
× time × served-demand) **plus C2 — no quiet capture** (honest standing can't
silently concentrate past the capture threshold). The parts that make each axis
real are built and internally hardened (Gate 4): a verify-without-fetch
proof-of-retrieval, an identity-bound proof-of-**space-time** bond over a proven
depth-robust graph, objective on-chain-bond fork-choice so partitions heal to the
heavier-standing chain, publisher-unlinkable publishing (ephemeral identity +
prepaid blind-signed credits), and per-operator, existence-checked, reversible
takedowns — covered at the unit, in-process-simulation, and real-daemon end-to-end
tiers. What they are **not** yet: **externally re-verified.** A primitive failing a
standalone "is-it-Sybil-proof?" test is *expected* (Douceur), not an M0 failure;
M0 is *held* only when a fresh external red-team finds no discount (¬C1), no quiet
capture (¬C2), and no broken composition seam. That pass (plus an operator
acceptance round) is still ahead. M0 ships proven or it does not ship.

The 0.x releases are **experimental learning releases**, not steps to
V1: the cadence is learning phase → feature-complete = 0.9.0 (RC line)
→ 1.0.0 = V1, field-proven multi-machine. The build so far grew through
a series of learning-phase milestones (the chunk/erasure/DHT/repair
core, real TCP transport, daemon mode, capacity pledging, identity/TLS,
encrypted manifests and care links, the reputation-quorum chain, the
web UI and desktop client) — that history and its marker system live in
[`docs/buildlog/`](docs/buildlog/). silt has **not** had an independent
security review — the [threat model](docs/threat-model.md) is the
honest account of what's weak.

Where this is all going: [`ROADMAP.md`](ROADMAP.md), tracked as a single
`V1` GitHub milestone and its issue spine. The resolver layer that maps
meaning onto opaque identifiers is a deliberately separate product:
[`docs/aslan-boundary.md`](docs/aslan-boundary.md).

## Try it

The guided version of everything below — with what to look for at each
step — is [`docs/v1-test.md`](docs/v1-test.md).

```sh
go build -o silt ./cmd/silt

./silt add somefile.pdf            # prints a silt: link (silt:v1:<root>:<key>)
./silt ls                          # registry contents
./silt info <silt-link>            # stripe map: every shard, its stripe, its presence
./silt get <silt-link> -o restored.pdf  # full verify-everything retrieval
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
./silt swarm get <silt-link> -o out.mp4 -peers <ID>@127.0.0.1:7101 -registry <ID>@https://127.0.0.1:7100
```

The registry is served over **key-pinned HTTPS**, so its reference is
`<ID>@https://host:port` — copy the exact `registry:` line the daemon
prints; plain `http://` or bare `https://` will fail. Daemons use real
disk stores and survive restarts (they re-announce what they hold). The
`-serve-registry` "single honest instance" is the seam a chain replaces
someday.

Chunks land in `.silt/objects/<xx>/<hash>` — sharded one level deep by a
2-hex prefix, each file named by its SHA-256. Add the same file twice in
**convergent mode** (`-mode convergent`) and you get the same root with zero
new bytes stored; the **default is `-mode private`** (a random per-file key —
privacy-by-default, no cross-file dedup, no guessed-plaintext confirmation
attack, H6). Delete or corrupt up to n−k shards per stripe (default: any 6 of
16) and `get` silently reconstructs them from parity; one loss beyond that
and it names the dead stripe and refuses.

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
arrives through an interface in `ports`, which is what makes the
network simulation deterministic and seed-replayable.

## Test

```sh
go test ./...
go test -bench . ./core/...
```
