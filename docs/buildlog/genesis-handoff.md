> ⚠️ **HISTORICAL — the genesis handoff (project inception). FROZEN, NOT A SPEC.**
> This is the original greenfield brief from the project's learning phase, kept
> verbatim as the record of how silt started. The project is now called **silt**
> and is substantially built; the milestone/wave/tier markers here are
> **retired**. **Do not mine this document for current design** — for that, the
> single source of truth is the GitHub **`V1`** milestone and its issues, narrated
> in [`ROADMAP.md`](../../ROADMAP.md), with canon in [`docs/TENETS.md`](../TENETS.md)
> and shipped history in [`CHANGELOG.md`](../../CHANGELOG.md).
>
> **Two fragments below now *contradict* immutables — they are archaeology, not
> guidance:**
> - The `Registry`/`Entry` sketch (§4) lists an **"optional name"** field. Core
>   now **carries zero meaning, forever** (the Aslan boundary, TENETS T3 /
>   immutable #6) — no name/description/tag ever enters core. The name field was
>   never built and must never be.
> - The `CreditLedger.RecordServe(server, **requester**, id, bytes)` sketch (§4)
>   threads *who fetched what* together. **Access is unsurveilled** (immutable #4):
>   the shipped ledger discards the requester/chunk pair, and the build-vs-intention
>   audit (2026-08-02) flags dropping that argument so the surveillance-shaped tuple
>   can't be reintroduced. Do not treat this signature as a target.
>
> Everything else is preserved as-written for the record.

# Project Handoff: `shardnet` (working name — rename freely)

**A content-addressed, erasure-coded, distributed file store — built as a real product from day one, simulated in-process until it needs real sockets.**

This document is a complete handoff for Claude Code. Read it fully before writing code. The human collaborator (Andrew) is here for the fun of the math and the software architecture — explain the interesting math as you build it (short comments and a `docs/math/` notebook are welcome), and keep the code readable over clever.

---

## 1\. The Idea (context, not marketing)

Large files are split into fixed-size chunks (default 64 MiB in production; **use 64 KiB in the sim** so tests are fast — make it a config value). Each chunk is encrypted, then hashed (SHA-256). Chunks are stored across many independent nodes. A **manifest** — an ordered list of chunk hashes plus reassembly metadata — describes how to rebuild a file. The manifest is itself stored as chunks; only its **Merkle root** goes into a global append-only **registry**. Retrieval \= look up root → fetch manifest chunks → fetch data chunks by hash from whoever has them → verify every hash → decode → decrypt.

Two properties are non-negotiable and drive the whole design:

1. **Content addressing everywhere.** A chunk's ID *is* its hash. Verification is intrinsic; trust in hosts is never required.  
2. **Partial availability is survivable.** Files are erasure-coded (Reed-Solomon): encode k data shards into n total shards; any k of n reconstruct the file. Losing nodes degrades redundancy, not the file.

A future ambition (build the *interface* now, a toy *implementation* later): nodes earn "credit" for provably serving chunks, spendable as write-access to the registry. Do not build the cryptographic proof-of-retrieval — just leave a clean seam for it.

## 2\. Prime Directive: Componentize as a Real Product

**No fleet of virtual servers, no real networking in v1** — the whole network runs in one process. BUT: the code must be structured so that swapping the in-process simulation for real TCP/QUIC/libp2p transport later touches *only adapter code, zero core logic*.

Use hexagonal architecture (ports and adapters):

- The **core domain** (chunking, crypto, erasure coding, manifests, DHT logic, repair policy) is pure logic. It imports no networking, no filesystem, no wall clock, no global RNG. All effects go through interfaces.  
- **Adapters** implement those interfaces: in-memory ones for the sim, real ones later.  
- The **simulator** is not a mode scattered through the code — it is just a harness that wires nodes together with the in-process adapters and drives a simulated clock.

Rules that keep this honest:

- Core packages must not import adapter packages. Enforce with a dependency-lint test in CI.  
- **Determinism:** every source of nondeterminism (time, randomness, message ordering/latency) is injected. A sim run with the same seed produces byte-identical results. This is what makes churn bugs debuggable.  
- Every component gets its own package, its own tests, and talks to siblings only through interfaces defined in a shared `ports` package.

## 3\. Language

**Go.** Rationale: this is the lineage of real systems in this space (IPFS, Storj-adjacent tooling); `github.com/klauspost/reedsolomon` is a production-grade RS library (used by MinIO); goroutines map naturally onto simulated nodes; single-binary CLI at the end. If you hit a strong reason to switch, stop and ask rather than fighting the language.

Standard library crypto (`crypto/sha256`, `crypto/aes` GCM, `golang.org/x/crypto` as needed). Do **not** hand-roll field arithmetic or ciphers.

## 4\. Components and Their Ports

Suggested repo layout:

shardnet/

  cmd/shardnet/        \# CLI (cobra or stdlib flag)

  core/

    chunk/             \# split/join, chunk framing

    crypto/            \# encryption modes, key derivation

    erasure/           \# RS encode/decode wrapper

    manifest/          \# manifest build/parse, Merkle tree

    dht/               \# Kademlia routing logic (pure)

    node/              \# node behavior: store, serve, fetch, repair

    registry/          \# append-only root registry logic

    credit/            \# credit ledger interface \+ naive impl

  ports/               \# ALL cross-component interfaces live here

  adapters/

    memstore/          \# in-memory ChunkStore

    diskstore/         \# filesystem ChunkStore (content-addressed dirs)

    simnet/            \# in-process Transport with latency/loss/partition injection

    simclock/          \# controllable clock

  sim/                 \# simulation harness \+ scenarios

  docs/math/           \# short, friendly explanations of the math used

Key interfaces (sketches — refine as needed, but keep them this small):

type ChunkStore interface {

    Put(ctx context.Context, c Chunk) error          // Chunk carries its own hash; Put verifies

    Get(ctx context.Context, id ChunkID) (Chunk, error)

    Has(id ChunkID) bool

    List() \[\]ChunkID

    Delete(id ChunkID) error                          // for capacity eviction

}

type Transport interface {                            // node \<-\> node messaging

    Send(ctx context.Context, to NodeID, msg Message) error

    SetHandler(func(from NodeID, msg Message))

}

type Clock interface { Now() Time; After(d Duration) \<-chan Time }

type Registry interface {                             // future blockchain seam

    Publish(ctx context.Context, e Entry) error       // Entry: root hash, size, codec params, optional name

    Lookup(root Hash) (Entry, bool)

    All() \[\]Entry

}

type CreditLedger interface {                         // future proof-of-retrieval seam

    RecordServe(server, requester NodeID, id ChunkID, bytes int64)

    Balance(n NodeID) int64

    CanPublish(n NodeID) bool

}

### Component specs

**chunk** — Split a stream into fixed-size chunks (last one padded with explicit length framing, so join is exact). `ChunkID = SHA-256(ciphertext)`. Property test: `join(split(x)) == x` for random sizes including 0, 1, exactly-one-chunk, and off-by-one boundaries.

**crypto** — Two modes behind one interface:

- `convergent`: key \= HKDF(SHA-256(plaintext chunk)); identical plaintext → identical ciphertext → global dedup. Document the confirmation-attack tradeoff in a comment.  
- `private`: random per-file key, AES-256-GCM per chunk with chunk index in the nonce/AAD (prevents chunk reordering attacks). The manifest records the mode; for `private`, the file key lives in the manifest (the manifest itself can then be encrypted — v2 concern, leave a field).

**erasure** — Wrap `klauspost/reedsolomon`. Config `(k, n)` per file, default `k=10, n=16`. Encode operates over the ciphertext chunk stream grouped into stripes. Decode must succeed given any ≥k shards of a stripe and fail loudly below k. Property test: for random data and random shard-loss patterns up to `n-k`, decode(encode(x)) \== x.

**manifest** — Ordered shard hashes \+ stripe layout \+ (k, n) \+ crypto mode \+ file size \+ Merkle tree over the shard hashes. Root hash identifies the file globally. Manifests serialize to canonical CBOR or deterministic JSON (canonical bytes matter — the root must be reproducible), and are themselves chunked and stored like any data. Include Merkle *proof* generation/verification: prove shard i belongs to root R — this is the seam the future credit system will need.

**dht** — Kademlia: 160/256-bit node IDs, XOR distance, k-buckets, iterative lookup. Pure logic over the `Transport` port. Stores *provider records* ("node X has chunk H"), not chunks themselves. Keep it textbook-simple; no NAT traversal, no security hardening — but structure lookup as its own function so it's unit-testable without a network.

**node** — Composes store \+ transport \+ dht \+ policies. Behaviors: announce held chunks, answer fetch requests, fetch-and-verify, and a **repair loop**: periodically sample manifests it cares about, count reachable shards per stripe, and if redundancy falls below a threshold (e.g., k+2), reconstruct missing shards and re-distribute them. Repair is the most fun emergent behavior in the sim — instrument it well.

**registry** — v1: a single in-process append-only log (one honest instance shared by the sim). It is *not* a blockchain and must not pretend to be; it just has the same interface a chain-backed one would. `CanPublish` consults the CreditLedger.

**credit** — v1: naive accounting (every served byte \= 1 credit, publishing costs a flat fee). Explicitly mark it gameable in comments; its job is to make the economics *observable* in sim metrics, not to be secure.

**simnet / simclock / sim** — The harness spins up N nodes (goroutines) wired via `simnet`, which supports per-link latency distributions, packet loss, partitions, and node kill/restart — all seeded. `simclock` lets scenarios fast-forward time (repair loops shouldn't need real minutes). Scenarios are table-driven Go tests AND runnable from the CLI with a live stats printout.

**cmd/shardnet** — Subcommands: `add <file>` (chunk→encrypt→encode→distribute→publish, prints root hash), `get <root> -o out` (the full retrieval path), `sim run <scenario>`, `stats`. `add`/`get` run against an embedded sim network in v1; the CLI must not know that — it talks through the same ports.

## 5\. Milestones (each ends green-tested and demoable)

1. **M1 — Roundtrip core:** chunk → encrypt → hash → manifest → Merkle root; and back. CLI `add`/`get` against a single in-memory store. Property tests pass.  
2. **M2 — Erasure resilience:** RS stripes wired in. Demo: delete any `n-k` shards from the store, `get` still reconstructs the file bit-perfectly; delete one more, it fails with a clear error.  
3. **M3 — The network:** Kademlia over simnet, provider records, multi-node fetch. Demo: `add` on node A, `get` on node Z with chunks scattered across 50 nodes.  
4. **M4 — Churn & repair:** kill 30% of nodes mid-scenario; repair loop restores redundancy; file survives. **This is the money demo** — make `sim run churn` print a satisfying live view (shards per stripe, redundancy histogram, repairs triggered).  
5. **M5 — Economics observatory:** credit ledger \+ registry gating \+ metrics (Gini coefficient of credits, publish throughput, freeloader nodes that fetch but never serve). Demo: watch freeloaders lose the ability to publish.

Stop for review at the end of each milestone. Do not start M(x+1) with M(x) red.

## 6\. Testing & Quality Bar

- Property-based tests for every pure transform (roundtrips are the invariant king here). Use `testing/quick` or rapid.  
- Deterministic sim: any failing scenario must print its seed; re-running with the seed reproduces the failure exactly.  
- The dependency rule (core imports no adapters) enforced by a test.  
- Verify hashes on **every** chunk receipt, always — a node that trusts is a bug.  
- Benchmarks for chunk/encode/hash throughput (`go test -bench`) — fun numbers to watch, and they guard against accidental O(n²).

## 7\. Explicit Non-Goals for v1

No real networking or NAT traversal. No consensus/blockchain (Registry is a seam). No cryptographic proof-of-retrieval (CreditLedger is a seam). No content moderation/policy layer. No GUI. Resist all of these until the sim is delightful.

## 8\. Math Notes to Write as You Go (`docs/math/`)

One short page each, written for a smart reader who isn't a mathematician: (1) why any k points determine a degree-(k-1) polynomial, and how Reed-Solomon uses that; (2) Merkle trees and proofs in pictures; (3) XOR distance and why Kademlia lookups take O(log N) hops; (4) convergent encryption and the confirmation attack. Andrew explicitly wants to *learn* this material from the build — these notes are a first-class deliverable, not documentation chores.

## 9\. Open Questions (ask before assuming)

- Chunk/stripe geometry: encode RS across chunks of one file (chosen above) vs. within each chunk — confirm the stripe layout before M2.  
- Manifest encryption for private files: v1 stores file keys in plaintext manifests — acceptable for the sim, but confirm.  
- Any preference on CLI UX / naming before it ossifies.

Have fun. Bias toward demos that make the math *visible* — a file coming back from the dead with a third of the network gone is the whole point.  

