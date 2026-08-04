# Safety: takedown on an append-only network

Two hard questions shaped this design. Both are answered honestly here,
and the answers are implemented, not just asserted (`core/denylist`,
`core/chain` revocations, node enforcement, `sim run` — the
`TestTakedownMakesContentUnreachable` test proves it end to end).

## 1. If illegal content is published, the identifier lives forever on an append-only chain. So how is it removed?

**Removal happens at the availability layer, not the ledger layer.**

What is actually on the chain is a **top-level identifier** — an opaque
32-byte hash — plus pointers to (encrypted) manifest chunks. That is a
*fingerprint*, not content. It reveals nothing: not a filename, not a
type, not the bytes. An immutable record that "root X was once
published" is content-free bookkeeping, like a permanent ledger of
tracking numbers.

You cannot, and should not, rewrite an immutable chain. So takedown is
not a deletion — it is an **addition**: an append-only *revocation
record* (a tombstone) is committed to the chain. Two properties keep this
from becoming a global kill switch (hardened after the M0 red-team, F5):
the revocation may only name a root **already published on the chain**
(a quorum cannot censor a hash that was never there), and honoring a
chain revocation is a **per-operator subscription**
(`node.SetHonorChainRevocations`, off by default) — following the chain
does not force you to enforce its takedowns. A takedown is also
**reversible** by the same quorum (an un-revoke record). From that point,
**a subscribing node no-ops on the denied root**:

- they **refuse to store** its chunks (a `StoreChunk` carrying a proof
  for a denied root is rejected),
- they **refuse to serve** them (`FetchChunk`, `HasChunk`, and audit
  `Challenge` all answer "not found"),
- they **stop announcing** them as providers,
- caretakers **stop repairing** the file, so its redundancy decays,
- a **subscribing** chain-backed registry stops resolving the root — a
  reader that opted in cannot retrieve the manifest pointers to begin a
  download (a non-subscribing reader still resolves it: the effect is
  proportional to who trusts the takedown, not universal),
- and each subscribing node **purges** the denied chunks it already holds.

The net effect: the *name* persists in history (harmless — it is just a
hash), while the *content* becomes unreachable and its chunks rot off
the network. The ledger remembers; the network forgets. Nothing is ever
decrypted to do this — denial matches an opaque hash, so infrastructure
stays content-blind even while enforcing takedown. That property — the
ability to block precisely, by identifier, without decryption — is a
direct benefit of content addressing, and it is why Silt can be both
private and governable.

### Honest limits
- **Adoption-bound, like every blocklist.** A node that ignores the
  denylist and already holds the chunks could still serve them. But
  discovery breaks (compliant nodes drop provider records), repair stops
  (redundancy decays), and the registry won't resolve the root — so
  keeping content alive against the compliant majority is costly and
  degrades over time. This is the same reality as DNS blocklists, mail
  RBLs, and PhotoDNA: effectiveness scales with adoption.
- **Post-hoc, mostly.** Novel illegal content is not on any list when
  first published, so it will land before it can be denied. Takedown is
  therefore primarily reactive; a pre-publish check catches only
  *known* bad hashes.
- **A denied root looks like ordinary data loss to a fetcher.** Because
  compliant nodes answer "not found" (they do not advertise "I refuse
  this"), a client fetching a root the *local* operator has denied sees a
  generic erasure-exhaustion error (`stripe … shard(s) lost`), not a
  distinct "refused by policy" signal. This is deliberate — an operator
  enforcing a takedown need not broadcast that it is doing so — and it is
  not a failure: the takedown is per-operator, so the fetcher simply
  retrieves the same root from any operator that hasn't denied it. Only the
  operator's own log names the denial (`denylist: N root(s) denied; purged …`).

## 2. Who runs the denylist? (Not the project.)

**The organization that publishes Silt's source code never operates the
network, and never operates the policy.** It ships the *mechanism* as
software; it ships no list, runs no node, and holds no override. This is
a deliberate, load-bearing stance — legally (a pure software publisher
has far stronger protection than an operator; publishing code is
expression) and structurally (there is no central switch to seize,
subpoena, or coerce).

So a node's denials come only from sources **the operator and the
network choose**, never from a built-in authority:

1. **On-chain revocations** — committed by the *same reputation quorum*
   that commits publications. No single node can take a file down; a
   takedown needs a quorum of high-reputation validators to attest it,
   exactly like adding a file. The takedown then replicates to every
   replica and is as tamper-evident as any block. Governance of removal
   is identical to governance of publication — decentralized, earned,
   and auditable. (`node.ProposeRevocation`, `chain` `Revocations`.)
   **Curator accountability**: because every revocation is a committed,
   attributable record, a bad or overreaching entry is *visible* — the
   attesting validators are on the record and answer for it with the same
   earned reputation (the work-backed standing bond, T1/#82) that let them
   attest at all. A curator who denies lawful content spends reputation to
   do it. There is no silent takedown and no global override switch — denial
   is never a hidden, project-flipped kill switch.
2. **An operator's local list** — a file each operator *chooses* to load
   (`silt daemon -denylist file`), e.g. a jurisdiction's legal list or a
   trusted third-party blocklist (an NCMEC-derived hash set surfaced
   through a trusted intermediary, say). Operators pick which lists to
   honor, the way every network operator picks which blocklists to
   subscribe to. Silt ships none of these lists. **Honest scope**: loading a
   local list file is built; automatic *distribution/subscription* of
   third-party lists (feeds an operator can subscribe to and have refresh)
   is **planned, not yet built** — today an operator supplies the file
   themselves. The pluralism is by design: many independent lists an
   operator can mix, never one canonical project list.

The code makes this concrete: there is no hardcoded list anywhere, and
the node never phones home. See the package comment in
`core/denylist` — "the software ships the mechanism; it never ships a
list." Whoever runs the network runs the policy.

## Where moderation actually lives

Silt is the neutral carrier. The layer that maps opaque identifiers to
human meaning — a separate resolver product, out of scope here (see
[aslan-boundary.md](aslan-boundary.md)) — is where discovery and
curation happen, and it is the natural place for content policy: a
resolver can refuse to *list* something without the infrastructure ever
knowing what it was. Silt answers only the small, honest question: is
this data still here? Takedown is how the network can answer "no."
