# Build your own Silt test network on your own computer

A hands-on, end-to-end tour of Silt — from a one-command simulation to a
real multi-node swarm on localhost that publishes a file, heals a node
death, and shows itself in a live dashboard. Everything here runs on one
laptop. Nothing touches the public internet.

> **This is 0.x — experimental and unaudited.** Great for learning and
> for demos; not for data you can't afford to lose. If you find something
> broken or confusing, that's the point — see
> [feedback](#found-something-tell-us) and the
> [threat model](threat-model.md).

## Get the binary

Download the release for your platform from
[github.com/nerolabs/silt/releases](https://github.com/nerolabs/silt/releases)
(and verify it against `SHA256SUMS`), or build from source:

```sh
git clone https://github.com/nerolabs/silt && cd silt
go build -o silt ./cmd/silt        # one static binary, no cgo
```

For the rest of this doc we assume `silt` is on your path. If you're
running a downloaded binary, alias it once per terminal:

```sh
alias silt=./silt-v0.1.0-darwin-arm64     # adjust to your file
```

On macOS, an unsigned binary is blocked by Gatekeeper the first time —
that's expected and disclosed. Clear it with:
`xattr -d com.apple.quarantine ./silt-v0.1.0-darwin-arm64`.

---

## Tier 0 — the whole network in one process (30 seconds)

The fastest way to *see* Silt: the built-in simulations run an entire
swarm, deterministically, in one process. Same core code as the real
thing — only the network and clock are swapped for in-memory ones.

```sh
silt sim run churn
silt sim run scatter -nodes 50
silt sim run economy
silt sim run audit -seed 31
silt sim run takedown
silt sim run capacity
silt sim run consensus
silt net demo -nodes 8
```

What each shows:
- **churn** — kill a third of the swarm; the file returns bit-perfect.
- **scatter** — publish on node A, fetch on node Z; A keeps nothing.
- **economy** — hosts earn per byte served; freeloaders go broke (watch the Gini climb).
- **audit** — liars keep the proof but ditch the data, and get caught and slashed.
- **takedown** — revoke a root; compliant nodes purge it while an unrelated file survives.
- **capacity** — bounded stores fill, placement spills over, nodes estimate the network size.
- **consensus** — the reputation-quorum chain commits a block; fresh identities cannot write.
- **net demo** — the same core, over real TCP sockets on localhost.

`churn` is the one to watch: the live view shows redundancy draining as
nodes die and pumping back up as caretaker repair loops rebuild lost
shards from parity, ending with a bit-perfect retrieval from a swarm that
lost a third of its members.

## Tier 1 — a real file, one node, on disk

Now leave the simulator. This is the actual crypto + erasure pipeline
against a content-addressed disk store — no network yet.

```sh
# make a test file (or use any file you like)
dd if=/dev/urandom of=movie.bin bs=1m count=20

silt add movie.bin
#  → prints a care link (repair-only) and the silt link (root:key) you share
silt ls                                     # roots in your local store
silt info silt:v1:<root>:<key>              # inspect without fetching
#   (heads-up: on a big file this prints every shard hash — a lot of output)
silt get silt:v1:<root>:<key> -o out.bin
diff movie.bin out.bin && echo "BIT-PERFECT"
```

Two things worth trying:

- **Dedup.** `silt add movie.bin` again in the default convergent mode →
  same root, ~zero new bytes stored (identical content addresses to
  identical chunks).
- **Erasure, by hand.** The file is split into stripes of 16 shards; any
  10 rebuild each stripe. Delete a few shards and fetch again — it heals
  from parity, and fails *loudly* only past the 6-per-stripe budget:
  ```sh
  ls .silt/objects | head
  rm .silt/objects/<a-few-of-them>
  silt get silt:v1:<root>:<key> -o out2.bin   # still bit-perfect
  ```

## Tier 2 — a real swarm on your machine

This is the end-to-end demo: several daemons in separate terminals,
finding each other, storing a file scattered across them, and serving it
back to a node that never saw it.

Open **four terminals**. In each: `cd` to where `silt` is and set the
alias.

**Terminal 1 — the seed daemon** (also hosts the shared registry + a web UI):

```sh
silt daemon -listen 127.0.0.1:7101 -serve-registry 127.0.0.1:7100 \
            -store d1 -ui 127.0.0.1:8081 -capacity 2G
```

It prints two lines you'll copy from — **use these exact strings**, don't
retype them:

```
registry: serving <SEED_ID>@https://127.0.0.1:7100 (persisted in d1)
peer:     <SEED_ID>@127.0.0.1:7101
```

> **Important:** the registry is served over *key-pinned HTTPS*, so the
> registry reference is `<SEED_ID>@https://127.0.0.1:7100` — **not**
> `http://...`. Plain `http://` or bare `https://` will fail with a TLS
> error. Copy the `registry:` line verbatim.

**Terminals 2 & 3 — more daemons**, bootstrapped to the seed, each with
its own store, UI port, and pledge:

```sh
# Terminal 2
silt daemon -listen 127.0.0.1:7102 -store d2 -ui 127.0.0.1:8082 -capacity 2G \
  -bootstrap <SEED_ID>@127.0.0.1:7101 -registry <SEED_ID>@https://127.0.0.1:7100

# Terminal 3
silt daemon -listen 127.0.0.1:7103 -store d3 -ui 127.0.0.1:8083 -capacity 2G \
  -bootstrap <SEED_ID>@127.0.0.1:7101 -registry <SEED_ID>@https://127.0.0.1:7100
```

Each should print `discovery: 1 peer(s) via -bootstrap` and
`bootstrapped (N table entries)` — they've found the swarm.

**Terminal 4 — publish and retrieve.** An ephemeral client joins, scatters
the file across the daemons, and leaves, keeping nothing:

```sh
silt swarm add movie.bin \
  -peers    <SEED_ID>@127.0.0.1:7101 \
  -registry <SEED_ID>@https://127.0.0.1:7100
#  → prints the root hash. Note it.

# retrieve it — the publisher is already gone, so this comes from the swarm
silt swarm get <root> -o got.bin \
  -peers    <SEED_ID>@127.0.0.1:7101 \
  -registry <SEED_ID>@https://127.0.0.1:7100
diff movie.bin got.bin && echo "RETRIEVED FROM THE SWARM"
```

**The payoff — survive a node death.** Now `Ctrl-C` one of the daemon
terminals (kill a holder), then fetch again:

```sh
silt swarm get <root> -o got2.bin \
  -peers <SEED_ID>@127.0.0.1:7101 -registry <SEED_ID>@https://127.0.0.1:7100
diff movie.bin got2.bin && echo "SURVIVED A NODE DEATH"
```

Erasure coding means any 10 of a stripe's 16 shards rebuild it, so losing
a node doesn't lose the file. Open **http://127.0.0.1:8081** to watch the
dashboard (pledge, chunks held, roots) update live.

## Bonus — self-healing with a caretaker

Killing a node degrades redundancy; a *caretaker* rebuilds it. `swarm add`
printed a **care link** (`siltcare:...`) — repair rights with no ability
to decrypt. Start a fourth daemon that repairs this file:

```sh
silt daemon -listen 127.0.0.1:7104 -store d4 -ui 127.0.0.1:8084 -capacity 2G \
  -bootstrap <SEED_ID>@127.0.0.1:7101 -registry <SEED_ID>@https://127.0.0.1:7100 \
  -care <the-care-link>
```

Kill a couple of holders and watch redundancy recover on its own before
you retrieve — the caretaker fetches surviving shards, reconstructs the
lost ones from parity, and re-seeds them, all without ever seeing your
plaintext.

## Bonus — the observatory

The observatory aggregates the live state of daemons **that expose a UI
and that you list** (it has no privileged view — it's knowledge any
participant can assemble). Since every daemon above ran `-ui`, open
**http://127.0.0.1:8081/observatory.html**, type the other UIs into the
box:

```
http://127.0.0.1:8082, http://127.0.0.1:8083, http://127.0.0.1:8084
```

and click **observe**. You'll see all the daemons, their pledges, chunk
counts, and per-file shard spread. (It only shows daemons you list that
are running `-ui` — it doesn't auto-discover the swarm.)

## Bonus — the desktop client on your swarm

The `client` is a node with a browser UI that both serves and consumes.
Point it at your swarm and use its drag-and-drop publish / paste-a-link
fetch pages:

```sh
silt client -bootstrap <SEED_ID>@127.0.0.1:7101 \
            -registry <SEED_ID>@https://127.0.0.1:7100
#  opens http://127.0.0.1:8090 in your browser
```

## Tier 3 — try to break it

The [threat model](threat-model.md) names the soft spots on purpose. As
you go, probe:

- **Capacity honesty.** Give daemons a tight `-capacity 200M` and publish
  something bigger than one node holds — does placement spill over
  gracefully, or wedge?
- **Restart durability.** `Ctrl-C` a daemon and restart it on the same
  `-store` directory — it should re-announce what it holds and become
  reachable again.
- **The registry is a single point** (`-serve-registry`, no auth, no TLS
  client identity beyond pinning — the code says so). Kill it mid-fetch;
  what happens, and what recovers?
- **Weird inputs.** An empty file, a very large file, a file exactly
  `k × chunk-size`, the same file added in `-mode private` vs the default
  `-mode convergent`.

## Cleanup

`Ctrl-C` each daemon. The stores are just directories — remove them when
you're done:

```sh
rm -rf d1 d2 d3 d4 .silt movie.bin got*.bin out*.bin
```

---

## Found something? Tell us.

This walkthrough exists partly so people find the sharp edges. If a
command fails, output confuses you, or something felt wrong, open an
issue at [github.com/nerolabs/silt/issues](https://github.com/nerolabs/silt/issues) —
that feedback is the most valuable thing you can send us right now. And
if you can, try to break the things the [threat model](threat-model.md)
admits are weak.
