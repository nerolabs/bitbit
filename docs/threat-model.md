# Silt threat model

**Status: draft for community review.** This document exists to be
attacked. It states, as plainly as we can, what Silt tries to protect,
what it assumes, where it is weak, and what we have *not* built yet. If
you find something we got wrong or missed, that is exactly the
contribution we are asking for at this stage — see
[How to help](#how-to-help-us-break-this) at the end.

Silt is **early and unaudited.** Nothing here has had an independent
security review. Treat it as an experiment you are helping us pressure-
test, not as a system to trust with data you cannot afford to lose.

---

## What Silt is (in one paragraph)

Silt is content-addressed, erasure-coded, encrypted storage run by its
participants and owned by none. A file is split into chunks named by the
SHA-256 of their bytes, encrypted, erasure-coded into columns, and
scattered across a Kademlia network of daemons. Nodes cannot read what
they hold: chunks and manifests are ciphertext, and the top-level
identifier is an opaque hash carrying no metadata. Availability comes
from erasure coding plus a repair loop; trust comes from content
addressing (you verify every byte against its hash), work-backed
standing on an append-only chain — reputation bought with proven storage
work, never a coin/stake or deanonymization — and mutual-TLS identity. The organization that
publishes the software **runs no part of the network** — see
[GOVERNANCE.md](../GOVERNANCE.md).

## What we are trying to protect

| Asset | Property | Mechanism |
|-------|----------|-----------|
| File contents | Confidentiality | Convergent or private encryption; hosts hold ciphertext only |
| File contents | Integrity | Content addressing + Merkle proofs; every byte verified against its hash |
| Files | Availability | Erasure coding (any k of n), repair loop, failure-domain spreading, demand fan-out |
| Identity of what's stored | Unlinkability to meaning | Opaque 32-byte roots; naming/meaning lives in a separate layer (out of scope, see [aslan-boundary.md](aslan-boundary.md)) |
| The registry/chain | Tamper-evidence | Append-only, hash-linked; blocks committed by a reputation quorum |
| Node operators | Legal defensibility of *carrying* opaque bytes | Content-blindness, the takedown mechanism, neutral-infrastructure posture |

## Trust and assumptions

- **Hosts are untrusted.** Content addressing means you never trust a
  host's word — you verify bytes against hashes. A host serving garbage
  wastes your time but cannot corrupt your file.
- **The transport is authenticated but the network is not anonymous.**
  Mutual TLS with public-key pinning (no CA) authenticates peers. Silt is
  **not** an anonymity system: a network observer, or a participating
  node, can see which opaque hashes move where and infer traffic
  patterns. Content is encrypted; *access patterns are not hidden.*
- **Authorship is unlinked from identity (who-writes), even though access
  is not (who-reads).** A publish is authorized by a quorum-issued **blind
  publish token** (T3/#84), so a committed chain entry carries the token, not a
  Publisher NodeID — an observer can no longer map a durable reputation key to
  every root it published. The fee/anti-spam economics are preserved (the
  publisher pays with its durable identity to *acquire* the token; the issuers
  never see the serial). Residual: a colluding validator set narrows the
  anonymity *set*, and this does not hide *access* patterns.
- **A supermajority of *bonded* validator weight is assumed honest.** The
  default untrusted posture is objective on-chain-bond fork-choice with
  Byzantine (n−f) quorum sizing — safety holds while an adversary controls
  ≤ 1/3 of bonded weight (priced by C1's `C_honest`), with concentration
  bounded by C2. It is not unconditional BFT against an unbounded adversary
  (see the colluding-quorum case below).
- **Cryptographic primitives are standard and assumed sound:** SHA-256,
  Ed25519, HKDF, AES/stream encryption. The *composition* is not audited.

## Adversaries and how Silt fares

### Malicious storage node (the "liar")
Accepts placements, keeps the Merkle proof, throws away the data, then
claims to hold it. **Caught** by a real, published **compact
proof-of-retrieval** (Shacham–Waters homomorphic authenticators): a
challenge is answered from the stored bytes without re-sending them, and a
liar without the bytes cannot compute a valid response; liars are slashed.
Durable, *unique* storage is priced separately by the **proof-of-space-time
bond** (below), so "keep the bytes to pass" costs real, identity-bound disk
over time — not a borrowed shard at challenge time. *Residual:* the
audit's economic weight and re-challenge cadence are held-in-tension
parameters (§Sybil), and center-less **proof-of-correct-repair** (that a
regenerated shard is the right one) is a decided build track (D-S7 / H7),
not yet shipped.

### Sybil / eclipse attacker
Creates many node IDs to surround a key in XOR space. Node IDs are
`SHA-256(pubkey)`: free to *mint in bulk*, not free to *target* a specific key.
**Silt does not claim to *prevent* Sybils** — that is impossible under free
minting with no permanent center (Douceur). Instead it prices them, as a
**systemic composition held in tension** (see [`design/m0.md`](design/m0.md)):
- **C1 — no discount.** Consensus standing is not free. A validator earns write
  standing only across the non-substitutable axes of `C_honest = disk ×
  address-diversity × time × served-demand`: an identity-bound **proof-of-space-time
  bond** (a space-hard, pk-bound plot with a labeling challenge × a Wesolowski VDF,
  persisted), re-challenged over the wire, plus audit history and witnessed demand.
  Forging a fraction *q* of standing costs ≈ *q*·`C_honest` — N Sybils cost N real
  disks, N address domains, N× time. Equivocation is slashed and forks reconciled
  by objective on-chain-bond fork-choice with Byzantine (n−f) quorum sizing.
- **C2 — no quiet capture.** A concentration metric (cost-to-corrupt /
  Nakamoto-coefficient over **bond-distinct operators**, computed from the committed
  bond ledger) keeps the minimum colluding operator set above a target, and gates
  the **anchor training-wheels**, which shed on measured decentralization.
- **DHT eclipse is now hardened** (was open at earlier revisions): provider records
  are self-certifying signed claims (forgery closed) and both announce and resolve
  spread across **failure domains** so a single-/24 key-surround can't suppress
  discovery.

**Held in tension, not closed** (by design, and stated as such): C2 cannot bound a
*real*, infrastructure-independent operator who chooses to collude (the "honest
whale") — only the HHI/economic guardrails + anchors bound that; the bond's tight
ε→k parameterization and the demand/wash ratio are monitored economic parameters,
not proofs. **This composition and its seams are exactly what we most want an
external red team to attack.**

### Free-rider / leech (and wash-serving)
Consumes without serving. **Mitigated** by the credit economy: serving
earns credits, consuming spends them, and a pure consumer goes broke and
loses access. **Wash-serving** — a Sybil serving its *own* content through
its own fetchers to farm standing — is **re-priced, not ignored** (D-DEMAND):
standing is priced on **cost-to-wash, never raw receipt count**, via a blind
demand receipt (unforgeable without a real, paid, correctly-delivered fetch,
and fetcher-unlinkable) combined with a burned fetch fee + a bonded-fetcher
credential, so faking demand costs real fees and real bonded identities.
*Honest limit (stated, not hidden):* demand **authenticity** — proving the
fetcher was economically *independent* of the server — is a Douceur limit no
receipt can close; the defense is economic re-pricing, a monitored inequality,
not a proof.

### Colluding validator quorum
The chain commits blocks (publications and revocations) when a quorum of
bonded validators attest. The default untrusted posture is **objective
on-chain-bond fork-choice** with **Byzantine (n−f) quorum sizing**, so an
adversary must control > 1/3 of *bonded weight* — priced by C1's `C_honest`,
not by cheap reputation — to break quorum-intersection safety, and standing
concentration is bounded by C2 (above). A colluding *super*-quorum could still,
in principle, commit a bad revocation or refuse a legitimate one; the remaining
defenses are that (a) bonded weight is expensive to amass and concentration is
metered, (b) takedown is per-operator and transparency-logged (below), never a
global switch, and (c) genesis is declared, not agreed. This is a
cost-and-concentration argument, not an unconditional cryptographic guarantee —
reviewing it is high-value.

### Network eavesdropper
Sees encrypted transport. Cannot read content. **Can** perform traffic
analysis (who talks to whom, which hashes, sizes, timing). Silt does not
defend against traffic analysis and does not claim to.

### Relay operator (cross-network reachability)
A NATed node can register with a community-run relay (`-relay-via`); a
relay forwards opaque bytes between two peers that cannot accept each
other's inbound connections. The relayed connection is an **end-to-end
TLS session between the two peers** — the relay carries it, cannot read
or alter it, and cannot forge frames (a frame's sender is whoever the
end-to-end handshake authenticated, exactly as on a direct connection).
What a relay **does** learn is metadata: the IPs and NodeIDs of the
peers it serves, when they talk, and roughly how much — a superset of
the on-path eavesdropper above, self-selected by running the relay.
Consistent with Silt's "not anonymous" stance; choose relays the way
you choose any peer you reveal your IP to. No relay is baked into the
binary or operated by the project. **Hole-punching (#27) shrinks this
exposure:** once two NATed peers meet through a relay, it coordinates a
simultaneous-open and — through a cone NAT — they form a *direct* connection
and the relay drops out (demoted to rendezvous), so it no longer sees the
bulk traffic or its timing. This is built and proven (cone → direct;
symmetric NAT still relays). It also lowers relay cost/abuse surface (A16).

### Someone who knows a candidate plaintext (convergent mode)
Convergent encryption keys a file by its own content, which enables
cross-user dedup but permits a **confirmation-of-file attack**: an
adversary who guesses a file's exact bytes can confirm whether it is
stored. **This is why the publish default is now `private`** (random
keying, H6) — it defeats the confirmation attack at the cost of dedup, so
the probeable behaviour is opt-in, not the default. **Convergent** is the
explicit opt-in choice for cross-user dedup, and the CLI prints a
confirmation-attack warning when it is selected. The trade-off is surfaced
at the point of choice, not buried here.

### Abuse: illegal or unwanted content
Because the network is content-blind, illegal content *can* be published
before it is known. Removal happens at the **availability** layer, not
the ledger: an append-only, quorum-committed **revocation** makes
compliant nodes no-op on a denied opaque root (refuse to store, serve,
prove, announce, or repair it) and purge it — **without ever
decrypting**. Operators also load local denylists they choose to honor.
This is **adoption-bound** (like every blocklist) and **post-hoc** (novel
content lands before it can be denied). Full design and honest limits:
[safety-denylist.md](safety-denylist.md). We consider the adequacy of
this an open question for reviewers, legal and technical alike.

### Resource-exhaustion attacker (asymmetric DoS)
Sends cheap requests that cost a node large CPU, disk, or bandwidth — the
class a CDN/WAF cannot help with, because Silt's core is a P2P TLS protocol
on raw sockets, not an HTTP origin. **Largely open.** Vectors include
repair-storms (a cheap "shard lost" signal triggering whole-stripe
reconstruction), erasure-decode and proof-of-retrieval challenge
amplification, mTLS handshake floods, unbounded manifests, disk-fill spam,
and relay-slot exhaustion. One structural relief is free: the transport is
**TLS-over-TCP, not UDP**, so spoofed-source reflection amplification is off
the table (a requester's address is proven before we do work). The planned
defense is a per-peer **resource-accounting** framework with
operator-configurable rate limits. Full enumeration: A1–A16 in
[`threat-catalog.md`](threat-catalog.md).

### The launch window itself
Nearly every attack above — eclipse, quorum capture, evading a version-floor
— is **easiest on a small network**, i.e. exactly at launch. We treat the
early network as honestly-labeled "training wheels" (time-boxed seeded
trust, gated reputation, thresholds that shed on measured decentralization).
See [`network-protection.md`](network-protection.md).

### Zero-day response
How the *network* updates without a central kill-switch is a
**criticality-graded, threshold-signed, recallable version-floor** model
(Low/Medium/High/Critical, enforced only for security, operator-controlled
by default). Design in [`network-protection.md`](network-protection.md).

> **The full breadth of enumerated threats — with state markers and
> mitigation directions — lives in [`threat-catalog.md`](threat-catalog.md).
> This document is the narrative; that one is the checklist.**

## What we are NOT claiming

- **Not anonymous.** No onion routing, no traffic-analysis resistance.
- **Not audited.** No independent security or cryptographic review.
- **Not censorship-proof** against a resourceful Sybil/eclipse adversary.
- **BFT-style safety conditional on ≤ 1/3 bonded weight** — objective on-chain-bond fork-choice with Byzantine (n−f) quorum sizing gives quorum-intersection safety *while an adversary controls ≤ 1/3 of bonded weight*. This is **not *unconditional* BFT** against an unbounded adversary (a colluding super-quorum can still commit a bad revocation — see above), and it is **not a classic reputation quorum** either (standing is priced by C1's `C_honest`, not self-reported reputation).
- **Not a proof-of-space/replication system.** Proof-of-retrieval is a
  liar-catcher, not a durability guarantee.
- **Not production-ready.** This is 0.x, experimental.

## Operator risk (not legal advice)

Running a node means storing opaque ciphertext you cannot inspect and
did not choose. The design that makes this defensible — content-
blindness, the takedown/denylist mechanism, and a project that operates
nothing and holds no override — is deliberate (see
[GOVERNANCE.md](../GOVERNANCE.md) and
[safety-denylist.md](safety-denylist.md)). But legal exposure varies by
jurisdiction and is unsettled for this class of system. If you run a
public node, understand your local law and load a denylist you trust.
**We are not lawyers and this is not legal advice** — that assessment is
itself something we want informed community and, eventually, professional
input on before anyone is encouraged to run nodes at scale.

## How to help us break this

We are releasing early **specifically to get this reviewed.** The most
valuable contributions right now:

1. **Sybil / eclipse.** Design an attack on discovery, repair, or the
   size estimate using cheap bulk identities. What identity cost (PoW,
   stake, web-of-trust) would you add, and what would it break?
2. **The proof-of-retrieval.** How would you cheat it? What would a real
   proof-of-data-possession cost here?
3. **The bonded-quorum chain (C1 + C2).** Attack the composition, not a
   primitive: find a strategy that earns quorum-controlling standing for
   less than *q*·`C_honest` (C1), or concentrates bonded weight past capture
   under adversarially-skewed measurement (C2), or captures the young,
   anchor-scaffolded regime before it sheds. What does a colluding or
   bootstrapping quorum enable?
4. **The crypto composition.** HKDF key hierarchy (link → layout key +
   content key + care links), convergent vs private modes, the manifest
   sealing. Is anything mis-composed?
5. **The economy.** Wash-serving, credit farming, collusion across Sybil
   identities.
6. **The abuse/takedown model.** Is availability-layer revocation the
   right answer? Where does it fail, legally or technically?

Open an issue, or use the security-disclosure path in
[CONTRIBUTING.md](../CONTRIBUTING.md) for anything sensitive. Tell us what
we got wrong. That is the point of shipping this now.
