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
addressing (you verify every byte against its hash), earned reputation on
an append-only chain, and mutual-TLS identity. The organization that
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
- **The majority of reputation-bearing validators are assumed honest.**
  The chain is a reputation quorum, **not** a Byzantine-fault-tolerant
  consensus (see below).
- **Cryptographic primitives are standard and assumed sound:** SHA-256,
  Ed25519, HKDF, AES/stream encryption. The *composition* is not audited.

## Adversaries and how Silt fares

### Malicious storage node (the "liar")
Accepts placements, keeps the Merkle proof, throws away the data, then
claims to hold it. **Caught** by proof-of-retrieval: a challenge sends a
fresh nonce and demands `H(nonce ‖ bytes)`, which a liar without the
bytes cannot compute; liars are slashed. *Weakness:* the proof is a
**toy** — it proves *possession at challenge time*, not durable or unique
storage. A node that keeps the bytes trivially passes; a node can pass k
challenges from k shards it briefly borrows. It is not a rigorous
proof-of-data-possession / proof-of-space. Hardening this is on the
roadmap.

### Sybil / eclipse attacker
Creates many node IDs to surround a key in XOR space. **Not hardened.**
An attacker who eclipses a chunk's neighborhood could suppress its
provider records (censor discovery of a specific file), or bias the
network-size estimate (which is derived from local XOR density). Node IDs
are `SHA-256(pubkey)`, so IDs aren't free to *target* a specific key, but
they are free to *mint in bulk*. There is no proof-of-work, stake, or
identity cost on joining today. **This is the weakness we most want
review on.**

### Free-rider / leech
Consumes without serving. **Mitigated** by the credit economy: serving
earns credits, consuming spends them, and a pure consumer goes broke and
loses access. *Weakness:* the economy is simulation-validated but not
adversarially hardened; collusion and wash-serving between Sybil
identities to farm credits/reputation is not addressed.

### Colluding validator quorum
The chain commits blocks (publications and revocations) when a quorum of
*earned-reputation* validators attest. **This is not BFT.** A quorum that
colludes could, in principle, commit a bad revocation (censor a file) or
refuse a legitimate one. The defense is that reputation is *earned* over
time (audits + serving), so a quorum is expensive to fake — but it is a
social/economic assumption, not a cryptographic guarantee. Genesis is
declared, not agreed. Reviewing this model is high-value.

### Network eavesdropper
Sees encrypted transport. Cannot read content. **Can** perform traffic
analysis (who talks to whom, which hashes, sizes, timing). Silt does not
defend against traffic analysis and does not claim to.

### Someone who knows a candidate plaintext (convergent mode)
Convergent encryption keys a file by its own content, which enables
cross-user dedup but permits a **confirmation-of-file attack**: an
adversary who guesses a file's exact bytes can confirm whether it is
stored. **Private mode** (random keying) defeats this at the cost of
dedup. Users who need secrecy of *presence* must use private mode; this
trade-off must be documented at the UI, not buried here.

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

## What we are NOT claiming

- **Not anonymous.** No onion routing, no traffic-analysis resistance.
- **Not audited.** No independent security or cryptographic review.
- **Not censorship-proof** against a resourceful Sybil/eclipse adversary.
- **Not Byzantine-fault-tolerant.** The chain is a reputation quorum.
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
3. **The reputation-quorum chain.** Attack the earned-reputation
   assumption. What does a colluding or bootstrapping quorum enable?
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
