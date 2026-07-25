# Launch Plan

How to introduce Silt to the people who will run nodes and improve the
software — and only those people, on purpose. Positioning here is a
safety control as much as a growth lever (see the
[fresh-eyes council](fresh-eyes-council.md)).

## Positioning

**Silt is resilient, private-by-architecture, neutral storage
infrastructure — owned by none, run by its participants, funded by no
token.** Lead with the engineering and the privacy-by-design story.
Never lead with "uncensorable," "anonymous," or anything that reads as a
tool for wrongdoing — that framing attracts the wrong users and paints a
target.

The honest one-line differentiators:
- **No token, no coin, no ICO.** Access is *earned* (reputation from
  audits + serving), not bought. This separates Silt from the
  storage-coin field (Filecoin/Storj/Sia) and from speculation entirely.
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
   reputation-quorum chain + decryption-free takedown). A short paper or
   talk earns credible, technical coverage.

Deliberately **not** a launch beachhead: crypto-token communities and
piracy communities. Wrong audience, wrong signal, real downside.

## Messaging pillars → proof

| Pillar | The proof you show |
|--------|--------------------|
| Survives failure | `silt sim run churn` — a third of the network dies, the file comes back bit-perfect |
| Private by architecture | encrypted chunks + sealed manifests + care-links that repair without decrypting |
| Neutral but governable | `silt sim run takedown` + [safety-denylist.md](safety-denylist.md) |
| Earned, not bought | `silt sim run economy` / `consensus` — reputation gates writes; no token |
| Runs anywhere, contributes by default | `silt client` — one binary, consumes and serves at once |

## Actions, phased

**Phase 0 — before any announcement (gate).**
Independent security review + a legal read; stand up the entity; cut a
signed v0.1 with checksums; write `CONTRIBUTING.md`. Do not launch wide
until these exist — see the [risk register](risk-register.md).

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
