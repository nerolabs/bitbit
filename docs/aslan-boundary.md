# The Aslan Boundary: why BitBit must never know what it carries

(Aslan = working codename for the separate resolver product. This doc
lives in the BitBit repo only to define the BOUNDARY — Aslan itself is
a different codebase, different repo, different deployment, on purpose.)

## The legal architecture

BitBit's stance is the common-carrier stance, engineered rather than
argued:

- A **chunk** is ciphertext. Without a manifest key it is
  indistinguishable from random bytes. No daemon can decrypt what it
  stores, even in principle — it never receives key material.
- A **manifest** is itself encrypted chunks (M11). A daemon hosting
  manifest chunks hosts noise that happens to describe other noise.
- A **top-level identifier** is a 32-byte Merkle root: no filename, no
  size hint beyond the chain entry, no MIME type, no description.
  Nothing about it distinguishes a home movie from a leak from a
  blockbuster.
- The **chain** (M12) records identifiers and manifest pointers —
  content-free bookkeeping, like a ledger of tracking numbers.

The network cannot moderate what it cannot identify, the same way
TCP/IP cannot. That is not a loophole; it is the design. Every duty of
identification lives ABOVE this layer, with whoever holds keys and
publishes meaning.

## What Aslan is

Aslan is to BitBit what DNS is to IP: a resolver from human meaning to
opaque identifiers. It is a *separate product* with its own chain (or
other distributed store) whose records are, at minimum:

```
{ name/title, description, tags, preview…,        ← meaning (Aslan's business)
  bitbit_root: 32 bytes, manifest_key: 32 bytes,  ← the pointer (the only coupling)
  publisher signature }
```

Users browse/search Aslan; clicking a record hands `root + key` (a
"bitbit link") to any BitBit client, which fetches ciphertext from the
infrastructure and decrypts locally. BitBit never sees Aslan traffic;
Aslan never touches chunks.

Design notes for whoever builds it (possibly us, in a different repo):

- **Coupling is one-way and thin.** Aslan depends on the bitbit-link
  format only. BitBit has zero knowledge of Aslan — no hooks, no
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
  store its own bulk data (previews, posters) IN BitBit — it's just
  another publisher.

## Bandwidth is a pledge too (per Andrew, 2026-07-25)

Storage without serving is another freeloader shape: a daemon that
hoards chunks but serves at a trickle contributes nothing at retrieval
time. The reputation inputs (M12) therefore include both:

- storage honesty — M7 audit pass rate (already measured), and
- **serving throughput** — bytes served over time (`Stats.BytesServed`
  already counts it; the M9 gossip pattern extends to carry a served-
  bytes rate, giving the network a self-computed total bandwidth the
  same way it computes total storage).

Endgame: every client is also a node. The desktop client (M14) pledges
a slice of disk and uplink by default — you download through the same
daemon you contribute with, BitTorrent's lesson built into the
architecture instead of bolted on.
