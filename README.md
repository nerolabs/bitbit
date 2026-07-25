# BitBit

A content-addressed, erasure-coded, distributed file store — built as a
real product from day one, simulated in-process until it needs real
sockets. See `HANDOFF.md` for the full design brief and
`docs/math/` for friendly explanations of the math.

## Status: HANDOFF complete (M1–M5), plus M6–M7

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
  the share handle is a bitbit link (root + key), and a one-way key
  hierarchy yields CARE LINKS — repair and audit rights with no
  ability to decrypt. Infrastructure now hosts noise describing noise
- ✅ M12 — the chain: the registry is an append-only block chain kept
  by the daemons; blocks commit only with a quorum of attestations
  from validators with EARNED reputation (audits + serving), fresh
  identities can't write, every replica re-validates everything
  (`sim run consensus`, or three `daemon -validator` processes)
- ✅ M13 — web UI: `daemon -ui 127.0.0.1:8081` serves an embedded
  dashboard (pledge, chunks, network estimate, chain, held roots), a
  drag-and-drop publish page (→ bitbit link + care link), a
  paste-a-link fetch page, and a network observatory that aggregates
  many daemons — capacity, serving bandwidth, per-file shard spread.
  One Go binary, `go:embed`, zero extra runtime

Where this is all going: [`ROADMAP.md`](ROADMAP.md) — identity/TLS,
encrypted manifests, the reputation-quorum chain, web frontends, and
the desktop client. The resolver layer that maps meaning onto opaque
identifiers is a deliberately separate product:
[`docs/aslan-boundary.md`](docs/aslan-boundary.md).

## Try it

The guided version of everything below — with what to look for at each
step — is [`docs/v1-test.md`](docs/v1-test.md).

```sh
go build -o bitbit ./cmd/bitbit

./bitbit add somefile.pdf            # prints the file's Merkle root
./bitbit ls                          # registry contents
./bitbit info <root>                 # stripe map: every shard, its stripe, its presence
./bitbit get <root> -o restored.pdf  # full verify-everything retrieval
./bitbit add secret.txt -mode private  # random key, no dedup, no confirmation attack
./bitbit add big.iso -k 4 -n 7       # custom erasure geometry

# the network, simulated: 100 nodes, 3% packet loss, 8 nodes killed
./bitbit sim run scatter -nodes 100 -loss 0.03 -kill 8 -seed 7

# the money demo: watch repair outrun two waves of node death
./bitbit sim run churn -seed 11

# the economy: hosts earn per byte served; freeloaders go broke
./bitbit sim run economy -seed 21

# storage audits: liars keep the proof, ditch the data, get caught
./bitbit sim run audit -seed 31

# the same core over real TCP sockets on localhost
./bitbit net demo -nodes 8
```

## Run a real swarm (separate processes)

```sh
# terminal 1: seed daemon, also hosts the swarm registry
./bitbit daemon -listen 127.0.0.1:7101 -serve-registry 127.0.0.1:7100 -store d1
# it prints:  peer: <ID>@127.0.0.1:7101   ← this is your bootstrap string

# terminals 2..n: more daemons
./bitbit daemon -listen 127.0.0.1:7102 -store d2 \
  -bootstrap <ID>@127.0.0.1:7101 -registry http://127.0.0.1:7100

# publish from anywhere: an ephemeral client joins, scatters, leaves
./bitbit swarm add movie.mp4 -peers <ID>@127.0.0.1:7101 -registry http://127.0.0.1:7100
# → prints the root hash; the client keeps nothing

# retrieve from anywhere (kill a daemon first, for sport)
./bitbit swarm get <root> -o out.mp4 -peers <ID>@127.0.0.1:7101 -registry http://127.0.0.1:7100
```

Daemons use real disk stores and survive restarts (they re-announce
what they hold). `-serve-registry` is the v1 "single honest instance"
made reachable over HTTP — the seam a chain replaces someday. No TLS,
no auth: trusted networks only, and the code says so out loud.

Chunks land in `.bitbit/objects/` named by SHA-256. Add the same file
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
