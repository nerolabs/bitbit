# shardnet

A content-addressed, erasure-coded, distributed file store — built as a
real product from day one, simulated in-process until it needs real
sockets. See `HANDOFF.md` for the full design brief and
`docs/math/` for friendly explanations of the math.

## Status: M2 complete (erasure resilience)

- ✅ M1 — chunk → encrypt → hash → manifest → Merkle root, and back;
  CLI `add`/`get` against a content-addressed disk store
- ✅ M2 — Reed-Solomon stripes: delete any n−k shards per stripe and
  `get` reconstructs bit-perfectly; one more and it fails loudly
- ⬜ M3 — Kademlia DHT over simulated network
- ⬜ M4 — churn & repair (the money demo)
- ⬜ M5 — credit economics observatory

## Try it

```sh
go build -o shardnet ./cmd/shardnet

./shardnet add somefile.pdf            # prints the file's Merkle root
./shardnet ls                          # registry contents
./shardnet info <root>                 # stripe map: every shard, its stripe, its presence
./shardnet get <root> -o restored.pdf  # full verify-everything retrieval
./shardnet add secret.txt -mode private  # random key, no dedup, no confirmation attack
./shardnet add big.iso -k 4 -n 7       # custom erasure geometry
```

Chunks land in `.shardnet/objects/` named by SHA-256. Add the same file
twice in (default) convergent mode and you get the same root with zero
new bytes stored. Delete or corrupt up to n−k shards per stripe
(default: any 6 of 16) and `get` silently reconstructs them from parity;
one loss beyond that and it names the dead stripe and refuses.

## Layout

```
ports/       all cross-component interfaces + shared primitives
core/        pure logic: chunking, crypto, manifests/Merkle, pipeline, registry
adapters/    the effects: memstore, diskstore, fileregistry (simnet/simclock come with M3)
cmd/         CLI
internal/depcheck  the architecture rule as a failing test
docs/math/   the math, explained for humans
```

Core packages import no adapters, no `os`/`net`/`time`/ambient
randomness — enforced by `go test ./internal/depcheck`. Every effect
arrives through an interface in `ports`, which is what will make the
M3/M4 network simulation deterministic and seed-replayable.

## Test

```sh
go test ./...
go test -bench . ./core/...
```
