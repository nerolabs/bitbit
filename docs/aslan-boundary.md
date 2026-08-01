# The Aslan Boundary: why Silt must never know what it carries

**silt is use-agnostic.** Core carries zero meaning and takes zero
position on *use*. We do not enumerate, endorse, or concern ourselves
with what flows through it — file-sharing, archival, a library of
record, or anything else users choose is unenumerated and not silt's
business. silt's only ambition is to be the most trusted, private,
secure, scalable, and efficient distributed file store ever built,
chosen for its feature set.

**"Aslan" names the boundary — and everything above it.** Aslan is the
name for *any* client or application built ON TOP of silt: the
application layer, expected to be richly diverse, is where use lives.
silt below it neither knows nor cares. This doc lives in the silt repo
only to define the BOUNDARY; anything above it is a different codebase,
different repo, different deployment, on purpose. The resolver sketched
below is one such thing built on top — an illustration of the layer,
not the whole of it.

## Content-blind by construction

silt's stance is engineered, not argued: the network cannot act on what
it cannot identify.

- A **chunk** is ciphertext. Without a manifest key it is
  indistinguishable from random bytes. No daemon can decrypt what it
  stores, even in principle — it never receives key material.
- A **manifest** is itself encrypted chunks. A daemon hosting manifest
  chunks hosts noise that happens to describe other noise.
- A **top-level identifier** is a 32-byte Merkle root: no filename, no
  size hint beyond the chain entry, no MIME type, no description.
  Nothing about it distinguishes one file from another.
- The **chain** records identifiers and manifest pointers —
  content-free bookkeeping, like a ledger of tracking numbers.

The network cannot moderate what it cannot identify, the same way
TCP/IP cannot. That is not a loophole; it is the design. Every duty of
identification lives ABOVE this layer, with whoever holds keys and
publishes meaning.

## An example: a resolver on top

The clearest thing to build above the boundary is a resolver — to silt
what DNS is to IP: a map from human meaning to opaque identifiers. It is
a *separate product* with its own chain (or other distributed store)
whose records are, at minimum:

```
{ name/title, description, tags, preview…,        ← meaning (Aslan's business)
  silt_root: 32 bytes, manifest_key: 32 bytes,  ← the pointer (the only coupling)
  publisher signature }
```

Users browse/search Aslan; clicking a record hands `root + key` (a
"silt link") to any Silt client, which fetches ciphertext from the
infrastructure and decrypts locally. Silt never sees Aslan traffic;
Aslan never touches chunks.

Design notes for whoever builds it (possibly us, in a different repo):

- **Coupling is one-way and thin.** Aslan depends on the silt-link
  format only. Silt has zero knowledge of Aslan — no hooks, no
  callbacks, no shared code. Multiple competing resolvers must be
  possible (that's what makes each one genuinely separate).
- **Aslan carries the content responsibility.** Because Aslan records
  attach meaning and keys, THAT layer is where publication decisions,
  takedown policies, jurisdictions, and moderation live — per-resolver,
  per-community. A curated family-photos Aslan and a journalism Aslan
  can point into the same infrastructure with different rules.
- **Technically**: an append-only signed-record chain replicated by its
  own nodes, records ordered by namespace/name with proof-of-ownership
  of names (first-claim + signature, or auction — Aslan's problem);
  full-text/tag search built client-side or by indexers. Aslan can even
  store its own bulk data (previews, posters) IN Silt — it's just
  another publisher.

## Bandwidth is a pledge too (per Andrew, 2026-07-25)

Storage without serving is another freeloader shape: a daemon that
hoards chunks but serves at a trickle contributes nothing at retrieval
time. The reputation inputs therefore include both:

- storage honesty — proof-of-retrieval audit pass rate, and
- **serving throughput** — bytes served over time (`Stats.BytesServed`
  already counts it; the capacity-gossip pattern extends to carry a
  served-bytes rate, giving the network a self-computed total bandwidth
  the same way it computes total storage).

Endgame: every client is also a node. The desktop client pledges a
slice of disk and uplink by default — you download through the same
daemon you contribute with, BitTorrent's lesson built into the
architecture instead of bolted on.
