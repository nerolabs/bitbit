# Design: column-based placement

**Status:** implemented and field-proven. Placement, provider records,
retrieval, repair, and audit all operate on columns; the manifest is unchanged.
See [BACKLOG.md](../../BACKLOG.md) and [ROADMAP.md](../../ROADMAP.md). The two
follow-ups noted below have since landed: **failure-domain-aware placement**
(bounding *cross-column* co-residence) and the **dispersion audit** are done,
and the **push half** of demand-responsive dispersion (fan-out on heat) is
implemented — the pull-cache tier remains a noted follow-up.

## The problem

Silt today places every chunk **independently, by its own content
hash**: `Distribute` runs an `IterativeFindNode(chunkID)` per chunk and
puts it on the closest nodes to that hash. That is the *maximum-fanout*
extreme of the placement spectrum. It gives excellent spread, but
retrieval is inefficient:

> To read a file of **S stripes** you resolve and fetch on the order of
> **S×k** chunks from up to that many distinct nodes — a separate DHT
> lookup and connection per chunk. You talk to a crowd to reassemble one
> file.

The real tuning knob is not "spread vs. concentrate." It is **how many
chunks travel together as one placement unit**:

| unit | conversations to read a file | failure of one unit costs | verdict |
|------|------------------------------|---------------------------|---------|
| 1 chunk | ~S×k | 1 chunk | max fanout — *today*; inefficient reads |
| 1 **column** (S shards) | **k** | one shard **per stripe** | the sweet spot |
| whole file | 1 | everything | catastrophic on failure |

## The decision: place by column

Lay the file out as a matrix — rows are stripes, columns are shard
position `0…n-1`. A **column j** is "the j-th shard of every stripe."
Place column j on the nodes closest to `hash(root ‖ j)` (not on the
closest nodes to each individual chunk's hash).

```
            shard position →
          0     1     2   …  k-1    k   … n-1
 stripe 0 c00   c01   c02      c0,k-1  p00 … p0,m
 stripe 1 c10   c11   c12      c1,k-1  p10 … p1,m
   ⋮       │     │
 stripe S ─┘     └─ column 1 = {c01,c11,…,cS1}   ← one node/group holds this
          └─ column 0 = {c00,c10,…,cS0}
                              (columns k…n-1 are parity)
```

Why the column is the principled unit — it is the **largest placement
unit that still costs only one shard per stripe when it dies**, because
it aligns the placement boundary with the erasure boundary:

- **Retrieval** — a reader fetches any **k of the n columns** from the k
  fastest column-holders; each fetch is one bulk stream of S shards, and
  those k columns reconstruct *every* stripe. Cost: **k conversations for
  the whole file, independent of its size** (vs. ~S×k today).
- **Availability** — a column contributes exactly one shard to each
  stripe, so losing a whole column costs each stripe exactly one shard.
  You can lose up to **n−k entire columns** (nodes, batches) and still
  rebuild everything — unit-failure maps precisely onto the erasure
  budget.
- **Anti-affinity, for free and optimal** — no node ever holds two
  shards of the same stripe, because a column is one-per-stripe by
  construction. This is the strongest possible anti-affinity, and it
  makes failure-domain spreading easier too: you need n distinct domains
  (one per column), not S×n.

## Graceful degradation across file sizes

- **Small files (one stripe):** column and chunk coincide — the scheme
  is exactly per-chunk placement. No special case needed.
- **Large files (many stripes):** a full column is a large object.
  Sub-segment a column into stripe-ranges (a "column segment" = one shard
  position × a bounded range of stripes) so no single stored/fetched
  object is unbounded. The segment size (stripes per segment) is the one
  remaining tuning parameter — it trades object size against
  conversation count. Suggested target: segments sized so a read is a
  few dozen conversations at most for any file.

## What changes in the code

The unit of **placement, routing, and provider records shifts from chunk
to column (or column-segment)**. What is preserved:

- **Chunks stay individually content-addressed and Merkle-verified.** We
  keep intrinsic verification and the takedown-by-opaque-hash property
  ([safety-denylist.md](../safety-denylist.md)). A column is a *grouping
  for placement*, not a new trust unit.
- Concretely:
  - `Distribute` places columns: for each column j, one `IterativeFindNode(hash(root‖j))`, then push the column's shards to the closest nodes.
  - **Provider records** say "node X holds column j of root R," so retrieval resolves **n column locations**, not S×n chunk locations.
  - **Retrieval** fetches k columns (bulk), verifies each shard's hash against the manifest, reconstructs per stripe.
  - **Repair** rebuilds at column granularity: a caretaker that finds a stripe short pulls surviving columns, reconstructs the missing shards, and re-seeds whole columns — and this is where the repair-time anti-affinity gap (below) disappears, since columns are inherently one-per-stripe.
  - The **manifest** already lists shards in a fixed order; column j is a strided view over it, so no manifest format change is required — only a documented convention for the stride.

## Tradeoffs (honest)

- **Chunk-level placement dedup weakens.** Placing by `hash(root‖j)`
  means the same ciphertext chunk appearing in two *different* files
  lands under different column keys. Whole-file dedup (identical content →
  identical link) is unaffected, and a node still stores a repeated
  ciphertext chunk once locally. Cross-file chunk-level placement dedup —
  a marginal benefit today — is the cost.
- **Hot columns.** A popular file concentrates read load on its n column
  holders. Mitigate by replicating hot columns (the replication factor
  can be per-column and demand-driven) and by the k-of-n choice giving
  readers n candidates to spread across.
- **Migration.** Existing per-chunk-placed files keep working (retrieval
  can fall back to per-chunk resolution); new files use column placement.
  A background re-placement can migrate old files opportunistically. No
  flag-day.

## Open questions

- Segment size default, and whether it should adapt to file size or
  network size.
- Whether column placement keys should fold in a failure-domain hint so
  the n columns land in n distinct domains by construction (ties into the
  failure-domain backlog item).
- Whether to keep a small amount of per-chunk fanout for the very hottest
  files as an explicit cache tier.

## Demand-responsive dispersion (hot files at scale)

The network is meant to reach worldwide scale, and that changes the risk.
A column of a popular file lives on only `Replication` hosts. If one file
draws, say, a quarter of all retrieval traffic while its columns sit on a
handful of nodes among millions, those nodes get **hit hard** — a hotspot
that hurts latency and can look like a self-inflicted DoS. Concentration
is good for a cold file (cheap, tidy reads) and dangerous for a hot one.

*Status: the push half is implemented — see below; the pull-cache tier is
a noted follow-up.*

So replication must be **elastic with demand**, not fixed:

- **Fan out on heat.** Track per-column serve rate (bytes/sec, request
  count). When a column runs hot, raise its replication — spread copies to
  more hosts (and, with failure-domain hints, more domains) so load
  divides. A reader already gets `n` candidate columns for `k` needed, so
  more holders per column means more parallel sources.
- **Contract on cooldown.** When demand falls, let the extra replicas
  expire (TTL/lease) so a flash-popular file doesn't permanently hoard
  capacity. Baseline `Replication` is the floor; heat is a temporary
  multiplier.
- **Where the signal lives.** Serving nodes see their own load; caretakers
  (via care-links) can aggregate it per column without decrypting content.
  The caretaker/announce path is the natural place to raise or retire
  replicas — the same machinery that already repairs.
- **Push vs. pull cache tier.** An alternative/complement: let a node that
  served a chunk under load *offer* to cache it and announce as an extra
  provider (opportunistic, demand-pull), decaying when unused. This keeps
  the hot set naturally near the readers.

This is elasticity in the *number of copies*, orthogonal to the column
*grouping* that this doc establishes. It's captured as its own backlog
item; the grouping had to land first so there's a unit to replicate.

## Relationship to the other placement work

Column placement is the backbone; three smaller items in the backlog
compose with it:
1. **Failure-domain-aware placement** — *done.* Nodes gossip a domain
   label (AS/rack/geo/operator); publish spreads the n columns across
   distinct domains and repair re-seeds rebuilt columns into domains the
   survivors aren't using, so a whole domain failing costs a stripe as
   little as possible. Best-effort (spreads across the peer domains a
   placer has learned), which also bounds the cross-column co-residence
   noted above.
2. **Repair preserves anti-affinity** — *done.* Repair re-places
   domain-aware (`repairStripe` takes an `avoidDomain`, and
   `preferFreshDomain` steers rebuilt shards into domains the survivors
   aren't using), so the earlier gap (re-placing on raw closest nodes) is
   closed. Column placement subsumes it structurally too, since columns are
   one-per-stripe by construction.
3. **Dispersion audit** — *done.* The caretaker sweep tallies the domains
   that actually hold each stripe's columns (HasChunk-confirmed, so a stale
   record can't fake spread) and re-spreads any stripe that a single domain
   failure could drop below k — turning domain diversity into an enforced
   invariant, and surfacing the per-stripe spread the content-blind
   observatory can't compute.
