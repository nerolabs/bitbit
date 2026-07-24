# shardnet

A content-addressed, erasure-coded, distributed file store — built as a
real product from day one, simulated in-process until it needs real
sockets. See `HANDOFF.md` for the full design brief and
`docs/math/` for friendly explanations of the math.

## Status: all five milestones complete

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
- ⬜ M4 — churn & repair (the money demo)
- ✅ M5 — economics observatory: credit-gated registry (1 credit = 1
  byte served, publishing costs a fee); watch the Gini coefficient
  climb as hosts earn, and freeloaders lose the ability to publish

## Try it

```sh
go build -o shardnet ./cmd/shardnet

./shardnet add somefile.pdf            # prints the file's Merkle root
./shardnet ls                          # registry contents
./shardnet info <root>                 # stripe map: every shard, its stripe, its presence
./shardnet get <root> -o restored.pdf  # full verify-everything retrieval
./shardnet add secret.txt -mode private  # random key, no dedup, no confirmation attack
./shardnet add big.iso -k 4 -n 7       # custom erasure geometry

# the network, simulated: 100 nodes, 3% packet loss, 8 nodes killed
./shardnet sim run scatter -nodes 100 -loss 0.03 -kill 8 -seed 7

# the money demo: watch repair outrun two waves of node death
./shardnet sim run churn -seed 11

# the economy: hosts earn per byte served; freeloaders go broke
./shardnet sim run economy -seed 21
```

Chunks land in `.shardnet/objects/` named by SHA-256. Add the same file
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
