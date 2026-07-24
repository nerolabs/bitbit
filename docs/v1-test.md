# v1 Test Drive

The full tour from a fresh terminal — about two minutes, in increasing
order of magic. Every command is copy-pasteable. If you want the *why*
behind each step, the reading order is
[`math/01-merkle-trees.md`](math/01-merkle-trees.md) (step 1),
[`math/03-reed-solomon.md`](math/03-reed-solomon.md) (step 2), and
[`math/04-kademlia.md`](math/04-kademlia.md) (steps 3–4).

## 0. Build

```sh
cd <repo root>
go build -o shardnet ./cmd/shardnet
```

(If `go` isn't found, your shell doesn't have Homebrew on PATH — use
`/opt/homebrew/bin/go` instead.)

## 1. The roundtrip — a file becomes a hash and comes back

```sh
head -c 1000000 /dev/urandom > demo.bin   # any file works; a PDF is more fun
./shardnet add demo.bin
```

It prints a 64-char hex string — the file's Merkle root, its one true
global name. Then:

```sh
./shardnet get <that-root> -o restored.bin
cmp demo.bin restored.bin && echo BIT-PERFECT
```

Also try `./shardnet add demo.bin` a second time: same root, zero new
bytes stored — that's convergent encryption deduplicating. (For files
whose *existence* is a secret, `add -mode private` trades dedup away;
see the confirmation attack in
[`math/02-convergent-encryption.md`](math/02-convergent-encryption.md).)

## 2. The math made tangible — delete shards, `get` anyway

```sh
./shardnet info <root>
```

This prints the stripe map: every 64 KiB chunk of your file, grouped
into stripes of 10 data + 6 parity shards, each named by its hash. Now
vandalize it — pick any **6** shard hashes from one stripe and delete
their files (chunks live at
`.shardnet/objects/<first-2-hex-chars>/<hash>`):

```sh
rm .shardnet/objects/ab/abcdef...   # ×6, any mix of data and parity
./shardnet get <root> -o back.bin && cmp demo.bin back.bin \
  && echo "SURVIVED 6 DELETIONS"
```

It reconstructs silently from parity. Delete a **7th** shard from the
same stripe and `get` refuses, naming the dead stripe:

```
only 9 of 16 shards available, need at least k=10 — data is unrecoverable
```

Run `info` again to see ✗ marks next to everything you killed.

## 3. The network — 100 simulated nodes, lossy links, dead peers

```sh
./shardnet sim run scatter -nodes 100 -loss 0.03 -kill 8 -seed 7
```

Node A adds a 1 MB file, pushes every chunk out to the swarm via
Kademlia lookups, and deletes its own copies. Then 8 random nodes are
killed, and node Z — knowing nothing but the root hash — retrieves it
through 3% packet loss. Expect `OK — bit-perfect` plus
message/drop/timeout counts.

## 4. The money demo — a file outruns the death of a third of the network

```sh
./shardnet sim run churn -seed 11
```

Worth watching line by line: `shards/stripe` is live redundancy for all
13 stripes. It starts at `[16 16 16 …]`, crashes to `min 10` when a
kill wave hits, then climbs back as caretaker repair loops rebuild lost
shards from parity and re-seed them on fresh nodes — twice — ending
with the file retrieved bit-perfectly from a swarm missing 18 of its 60
nodes. Note there is **zero replication** in this scenario: each shard
exists once; Reed-Solomon is the entire safety net.

## 5. The economy — freeloaders go broke

```sh
./shardnet sim run economy -seed 21
```

Four phases: everyone gets a grant worth one publish, everyone spends
it, traffic pays the hosts (watch the Gini coefficient go 0.00 → 0.49),
then everyone tries to publish again. The closing lines are the
punchline — the top earner served ~800 KB and publishes freely; the
freeloader fetched ~440 KB, served nothing, and is locked out of the
registry. (The ledger is deliberately gameable — v1's job is making the
economics observable, not secure; see `core/credit`.)

## While you play

- **Every sim is deterministic.** Re-run any command with the same
  `-seed` and you get identical output down to every counter; change
  the seed and you get a different (but equally reproducible) universe.
  That's the payoff of the no-goroutines event-loop architecture: any
  weird run is perfectly replayable.
- `go test ./...` runs the whole suite (~5s).
  `go test -bench . -run XXX ./core/...` prints the fun throughput
  numbers (Reed-Solomon encode ~9 GB/s, convergent encryption
  ~1.7 GB/s, full pipeline ~350 MB/s).
