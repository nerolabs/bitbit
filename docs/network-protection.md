# How we protect the network and its operators

**Status: draft (2026-07-31).** This is the operator- and user-facing
companion to [`threat-model.md`](threat-model.md) (written to be attacked)
and [`threat-catalog.md`](threat-catalog.md) (the full breadth). Same facts,
different job: here we say plainly what is defended, what is not yet, and how
the network keeps its operators safe — including how it responds to a
zero-day. We would rather under-promise. Silt is early and unaudited.

## What content addressing already gives you (for free)
- **You never trust a host's word.** Every byte is verified against its
  hash on read; a host serving garbage wastes a moment and is skipped, it
  cannot corrupt your file. Substituting different bytes under a hash is a
  SHA-256 break.
- **A link is a cryptographic guarantee of the bytes it names** — not of
  what a *name* claims those bytes are. Meaning (names, descriptions) lives
  in a separate resolver layer (Aslan) that Silt core deliberately never
  touches. **Trusting a name is trusting that resolver, like trusting a DNS
  provider.** File poisoning — making a name resolve to hostile content — is
  a resolver-layer concern, not something content addressing can prevent.
- **Operators host opaque ciphertext they cannot read and did not choose.**
  That content-blindness is the basis of an operator's legal defensibility.

## The Aslan boundary is a firewall (and it is immutable)
Silt core resolves hashes, never names. This is not just architecture — it
is what keeps operators out of the moderation-and-liability business. The
moment core learned to resolve names, it would inherit every takedown,
copyright, and safety obligation it is designed to shed. So **core carries
zero meaning, forever**; any change that adds meaning to core is treated as
a top-severity regression. Moderation, curation, and name-trust belong to
the resolver layer, where they can be plural and chosen.

## Denial-of-service: what a WAF can and cannot do
A CDN/WAF (Cloudflare-style) protects **HTTP** surfaces — the registry API
and the local UI. It **cannot** protect Silt's core, which is a
peer-to-peer TLS protocol on raw sockets plus a DHT: there is no origin to
hide behind. So the core is defended natively:
- **Spoofed-source reflection is off the table by design** — the transport
  is TLS-over-TCP, not UDP, so a requester's address is proven by the
  handshake before any expensive work.
- **Per-peer resource accounting** (in progress) — a configurable budget
  across connections, compute, storage, and bandwidth; a peer that exceeds
  it is throttled, then disconnected. This covers handshake floods,
  slow-loris, lookup/repair amplification, and disk-fill spam. See the
  resource-exhaustion items (A1–A16) in the catalog.
- **Rate limits are operator-configurable from the start** — sensible
  defaults, tunable per deployment.
- **Relay abuse has structural relief now** — TCP hole-punching (#27) is
  proven through cone NAT (CI-gated Docker harness), so a punched path
  carries bulk NAT↔NAT bytes directly and the public relay is demoted to a
  rendezvous. Symmetric-NAT peers still relay, and per-identity relay quotas
  stay open (A16).

Honest gap: minting an identity is still cheap — and by theorem (Douceur) no
single primitive can make it otherwise under free minting and no permanent
center. So a primitive failing a standalone "is-it-Sybil-proof?" test is
expected, not an M0 failure. What resists Sybils is not one mechanism but a
**systemic composition — C1 (no discount: a fraction q of consensus standing
costs ≈ q·C_honest) + C2 (no quiet capture) — held in tension, not a single
Sybil-proof primitive.** `C_honest` is a product of non-substitutable factors:
disk × address-diversity × time × served-demand, so no cheap shortcut on one
axis buys past another. The mechanisms that instantiate this are built — the
storage bond, the verify-without-fetch proof-of-retrieval, fork-choice
reconciliation, equivocation slashing, signed provider records — proven at
unit + sim + real-daemon e2e. **The internal hardening pass is complete; M0
awaits EXTERNAL re-verification against the systemic C1/C2 claim** (independent
review + the multi-machine field test, #52). A hard proof-of-work /
proof-of-space primitive on *minting* stays deferred; it would not change the
theorem. We do not hide any of this; it is the review we most want. See
[`design/m0.md`](design/m0.md) for the composition claim in full.

## How the network responds to a zero-day (change management)
Assume the fix exists (GitHub is our source of truth; dependency fixes flow
from upstream). The question is how the *network* updates without either (a)
a central auto-update kill-switch or (b) leaving vulnerable nodes unpatched
for weeks. Our answer is **operator autonomy by default, with signed,
recallable, criticality-graded enforcement reserved for security.**

**Criticality tiers** (we, the maintainers, set the tier):

| Tier | What the network does | Deadline |
|------|-----------------------|----------|
| Low | advisory only; no enforcement | — |
| Medium | advisory, then surfaced loudly | 30 days |
| High | patched peers deprioritize/refuse old versions after the deadline | 7 days |
| **Critical** | patched peers refuse to peer with old versions | **24–48 h** |

Critical is a **gate of last resort** — a live security breach. The 24–48h
window is deliberate: long enough for us to react, and to **recall** the
alert if it was mistaken or attacker-provoked.

What makes this a *safety* mechanism and not a new attack surface:
1. **Threshold-signed, never single-key.** A Critical advisory requires
   *m-of-n* maintainer signatures. Whoever can declare Critical can halt the
   network, so no single key may — otherwise a stolen key becomes a network
   kill switch.
2. **Recallable and monotonic.** Advisories carry a sequence number; a newer
   signed advisory supersedes any older one. That is the recall path, and
   why Critical waits 24–48h before nodes actually reject.
3. **Clocked from observation, not the wall.** The deadline runs from when a
   node first sees the signed advisory (or a chain anchor), not system time,
   which is manipulable. A node that never sees the advisory never enforces
   — enforcement fails *open*, the safe direction.

The alert itself is a lightweight network-to-network gossip of
`{advisory-sequence, min-version, criticality, signatures}`; each node
computes the delta between its own version and the floor and acts per tier.
Operators are never silently auto-updated — but a one-command,
signature-verified upgrade path makes "operator-controlled" cheap, and a
Critical advisory is impossible to miss.

Caveat we state plainly: the version-floor is enforced *by the patched
majority against the unpatched*, so on a small early network it is weak
(few enforcers). See "Launch window."

## Software and supply-chain integrity
- **Reproducible builds** (CGO-off, `-trimpath`) so a binary can be verified
  against source.
- **Releases threshold-signed** by multiple maintainers, with checksums and
  provenance — compromising one maintainer is not enough to ship a bad
  release *or* a bad advisory.
- **Minimal, vendored dependencies** with `go.sum` verification and
  `govulncheck` in CI — a small dependency surface is a security feature.

## The launch window is itself a control
Nearly every attack is easiest on a tiny network. We treat the early network
as **training wheels**, honestly labeled: seeded/anchored trust that is
time-boxed and pre-committed to shed, a gated reputation ramp, and
maturity-scaled quorum thresholds. This is **built** (T2/#83:
maturity-gated anchor sign-off on commits, now over the real Gate-4 bond), and
crucially the wheels come off on **measured decentralization thresholds**
(reusing the Gini/observatory metrics), not on a political flag-day — so
shedding them is mechanical. Hardening this to the real, multi-machine
mechanism is V1 work.

## What we do NOT claim (see threat-model.md for the full list)
Not anonymous · not audited · not Byzantine-fault-tolerant · no single
Sybil-proof primitive (impossible by Douceur — a standalone primitive failing
that test is expected, not an M0 failure; resistance is the systemic C1/C2
composition, and eclipse/S5 is addressed by the H5 composition — signed
provider records + failure-domain diversity) · not a proof-of-space system ·
not censorship-proof against a resourceful adversary · not production-ready. And your operator
legal exposure varies by jurisdiction and is unsettled for this class of
system — run a node only with a denylist you trust and an understanding of
your local law. This is not legal advice.

## How to help
The highest-value review targets, unchanged: Sybil/eclipse, the toy
proof-of-retrieval, the reputation-quorum chain, the crypto composition, the
economy (wash-serving/credit-farming), and the abuse/takedown model. See
[`threat-model.md`](threat-model.md#how-to-help-us-break-this).
