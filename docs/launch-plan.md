# Launch Plan

How to introduce Silt to the people who will run nodes and improve the
software — and only those people, on purpose. Positioning here is a
safety control as much as a growth lever (see the
[fresh-eyes council](fresh-eyes-council.md)).

## The first launch must be credible from day one

We are **harden-first** (Andrew, 2026-07-31 — a re-sequencing of the earlier
"community-feedback-first" plan; see [ROADMAP.md](../ROADMAP.md)). The
reasoning: a half-baked experimental drop on a project this ambitious —
content-addressed storage plus a work-backed, identity-bound reputation
plane, in a space thick with "AI/web3 storage" noise — reads as a *poser
build* and burns the one first impression we get with the exact technical
audience we need. So the tenets are **field-proven — floors and pillars
both — before we launch**: the durability floors, the DoS resistance,
cross-network hole-punching, and a *real* verify-without-fetch proof-of-retrieval
and a proof-of-space-time bond behind the trust plane — now **built and pending
independent review** (Gate 4) — with publishing that stays cryptographically
unlinkable from the reputation that earns it.

Feedback is still the point of the first release — market for technical
people to **break it and tell us what's wrong**, especially the weaknesses
named in the [threat model](threat-model.md), and lead every message with
"help us pressure-test this," never "store your data here." What changed is
the *bar for what we hand them*: something that already stands up, not a
request to finish our hardening for us. Legal posture and any entity are
still decided later, informed by the engagement — nothing formal is committed
on spec — but now *after* a credible artifact exists, not instead of one.
The pre-launch work is tracked on the [ROADMAP.md](../ROADMAP.md) `V1`
milestone and its issue spine; Andrew's personal review + hardening pass is
the final gate before any outreach.

## Positioning

**Lead with the trilemma.** silt exists to *hold* the
privacy × accountability × Sybil trilemma — not by trading a corner away, but by
composition held in tension. Sybil resistance in particular is a **systemic
composition — C1 (no discount: a fraction q of consensus standing costs
≈ q·C_honest) + C2 (no quiet capture) — held in tension, not a single
Sybil-proof primitive**, where prior systems bought any two corners by
sacrificing the third. The headline differentiator
is **token-less, work-backed, identity-bound reputation that publishing stays
cryptographically unlinkable from** — no coin, no stake, no speculation;
standing is *earned*, and the act of publishing can't be tied back to the
identity that earned the standing. That is the novel claim, and positioning
should open on it.

Under that headline: **Silt is resilient, private-by-architecture, neutral
storage infrastructure — owned by none, run by its participants, funded by no
token.** Lead with the engineering and the privacy-by-design story.
Never lead with "uncensorable," "anonymous," or anything that reads as a
tool for wrongdoing — that framing attracts the wrong users and paints a
target.

The honest one-line differentiators:
- **The trilemma, held in tension — not a corner traded away.** Privacy *and*
  accountability *and* Sybil resistance together — not two of three. Sybil
  resistance is a systemic composition (C1 no-discount + C2 no-quiet-capture),
  not a single Sybil-proof primitive. No token, no coin, no speculation;
  standing is earned reputation, and publishing is unlinkable from it. (A
  refusal to sacrifice a corner — the internal hardening pass is complete; M0
  awaits EXTERNAL re-verification against the systemic C1/C2 claim. Not a
  "solved it" boast.)
- **No token, no coin, no ICO.** Access is *earned* (reputation from
  audits + serving), not bought. This separates Silt from the
  storage-coin field (Filecoin/Storj/Sia) and from speculation entirely.
- **Boring parts, novel car.** The primitives are best-in-class and
  *proven* — Reed-Solomon, Kademlia, convergent encryption, memory-hard
  work — and the novelty is the *composition*, backed by spec and a build-time
  test harness. The internal hardening pass is complete; M0 awaits EXTERNAL
  re-verification against the systemic C1/C2 claim. Said plainly, this reads as
  credibility, not hype.
- **The infrastructure is not the content.** A daemon can't read what it
  holds; meaning lives in a separate layer. Neutral carrier, by design.
- **It heals itself.** Erasure coding + a repair loop mean files survive
  machines dying — the demo that makes people feel it.

## Audiences (in sequence)

1. **Node operators** — self-hosters, home-labbers, data-hoarders,
   people with spare disk and a small VPS. They are the network. Reach
   them where they already are: r/selfhosted, r/datahoarder, the
   self-hosting and homelab communities, awesome-selfhosted.
2. **Contributors** — Go developers, distributed-systems and cryptography
   people. The `docs/math` notes and the deterministic simulator are the
   hook: this is a codebase you can *learn from*.
3. **Researchers & press** — the design is genuinely interesting
   (content-addressed + erasure + encrypted capability links +
   work-backed identity-bound reputation with unlinkable publishing +
   decryption-free takedown). A short paper or talk earns credible,
   technical coverage — holding the trilemma is the hook.

Deliberately **not** a launch beachhead: crypto-token communities and
infringement-oriented communities. Wrong audience, wrong signal, real downside.

## Messaging pillars → proof

| Pillar | The proof you show |
|--------|--------------------|
| Survives failure | `silt sim run churn` — a third of the network dies, the file comes back bit-perfect |
| Private by architecture | encrypted chunks + sealed manifests + care-links that repair without decrypting |
| Neutral but governable | `silt sim run takedown` + [safety-denylist.md](safety-denylist.md) |
| Earned, not bought | `silt sim run economy` / `consensus` — work-backed, identity-bound reputation gates writes; no token, and publishing stays unlinkable from the standing that earns it |
| Runs anywhere, contributes by default | `silt client` — one binary, consumes and serves at once |

## Actions, phased

**Phase 0 — before any announcement (gate).**
The technical bar comes first (harden-first): the `V1`-milestone issue spine
in [ROADMAP.md](../ROADMAP.md) field-proven, an independent security review, a
signed, checksummed release (an early **0.x experimental/learning** build —
feature-complete lands at 0.9.0/RC, true V1 at 1.0.0), and `CONTRIBUTING.md`.
A *legal read* (understand
the exposure) is prudent here, but **standing up the entity is not a
pre-launch gate** — the legal posture (entity, jurisdiction, or stay a pure
code-publishing project) is still decided *after* the engagement reveals
whether it's warranted, never on spec (see [ROADMAP.md](../ROADMAP.md) and
the [risk register](risk-register.md)). Do not launch wide until the
technical gate and review exist.

**Phase 1 — technical soft launch.**
Turn the `docs/math` notes into 2–3 blog posts (Merkle/erasure,
Kademlia, the takedown model). Post to Hacker News / lobste.rs with a
"run a node in two minutes" quickstart and the churn demo GIF. Engage
honestly in comments, including the hard safety questions — the answers
are strong.

**Phase 2 — operator growth.**
Targeted posts in self-hosting / data-hoarding communities; a simple
network dashboard (the observatory) so early operators can see the
swarm they're building; a public roadmap so contributors know where to
help.

**Phase 3 — durability.**
Conference talk or short paper; a modest seed-node program run by
*community* operators (never by the project); a funding push
(grants/sponsorship).

## What "success" looks like early

Not user counts — **operator counts and contributor counts.** A few
dozen independent nodes run by people who understand what they're
contributing, and a handful of external contributors who've read the
code and sent PRs, is a healthier six-month outcome than a viral spike
of the wrong attention.
