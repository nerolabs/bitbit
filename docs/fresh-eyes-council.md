# The Fresh-Eyes Council

A convened review by experienced hands wearing different hats — Legal,
Trust & Safety, Security, PR, Marketing, Governance. They know Silt is
open source; they want to protect everyone who builds and runs it. Each
names its single biggest concern and its recommended mitigation. The
[risk register](risk-register.md) ranks these; the
[launch plan](launch-plan.md) acts on the marketing findings.

The one theme that crosses every hat: **a content-neutral network needs
its abuse-handling and legal posture designed in from the start, and its
messaging must be "neutral infrastructure," never "evasion tool."**
Everything below is a variation on that.

---

## Legal

**Biggest concern — secondary liability, and the CSAM regime in
particular.** "Nodes can't know what they carry" is a real shield but a
partial one. DMCA §512 safe harbor expects a designated agent, a
takedown path, and a repeat-infringer policy. CSAM law (US 18 U.S.C.
§2258A reporting duties; the EU's evolving CSAM regulation) is closer to
strict liability and won't fully immunize node operators.

**Mitigations.**
- The **takedown mechanism** ([safety-denylist.md](safety-denylist.md))
  is the linchpin — precise, decryption-free blocking by opaque hash,
  governed by quorum, operated by no one central.
- **The publishing organization must never operate the network or the
  policy.** A pure software publisher stands on the strongest ground —
  publishing code is expression, and there is no service to hold liable.
  This is a structural decision, not a disclaimer: no project-run nodes,
  no project-run list, no override. (Contrast the legal exposure of
  entities that *operated* a service versus those that only *published*
  tools.)
- Form a **lightweight legal entity / foundation** before a wide launch:
  it holds the trademark and domain, can receive a DMCA-agent
  designation for the *project's* touchpoints (website, releases), and
  shields individual contributors — while still running no infrastructure.
- Publish an **abuse policy** and a clear statement of what Silt is and
  is not.

## Trust & Safety

**Biggest concern — Silt becoming known as a haven** for CSAM, malware,
or large-scale infringement. That is an ethical failure first and an existential
reputational/legal event second.

**Mitigations.** Abuse-reporting from day one; make it easy for
operators to subscribe to denylists (including CSAM-hash lists via a
trusted intermediary); design the resolver boundary so **moderation
lives at discovery**, not in the carrier; rate-limit publication;
and be explicit in docs and messaging that Silt is not built to hide
wrongdoing and actively supports takedown.

## Security

**Biggest concern — this is unaudited, largely single-author
cryptographic and trust-plane code.** The *design* target for V1 is the
real mechanism — real proof-of-retrieval, a memory-hard bond, and
unlinkable standing — but the **current code is an explicit first cut on
labeled placeholders**: a space-lite in-RAM bond, a toy proof-of-retrieval,
and a reputation signal that is still gameable (self-reported serving),
proven only sim + e2e on a single host. Any of these could fail badly at
real scale if shipped as-is.

**Mitigations.** An **independent security audit and a written threat
model before any "production" claim**; keep the "first cut / not
production-hardened" labeling honest until then; a coordinated-disclosure
policy and a bug-bounty; harden the DHT against Sybil/eclipse attacks;
land and multi-machine-prove the *real* proof-of-retrieval, bond, and
unlinkable-standing mechanisms before real economic value rides on them.

## PR

**Biggest concern — framing is a risk control, not just growth.** Launch
on "uncensorable / anonymous / unstoppable" and you attract the worst
users, hand critics a headline, and paint a prosecutorial target. Launch
on "resilient, private, neutral infrastructure" and you attract
operators, researchers, and builders. Same code, opposite trajectories.

**Mitigations.** Disciplined positioning (see the launch plan); lead with
the engineering and the privacy-by-architecture story; have a
crisis-comms plan ready for the inevitable "bad content found on Silt"
story, centered on the takedown mechanism and the no-operator stance.

## Marketing

**Biggest concern — reaching the *right* first users** (people who will
run nodes and improve the software) without drifting into the wrong
communities.

**Mitigations.** Target self-hosters, data-hoarders, and
distributed-systems / cryptography practitioners; lead with the honest
headline differentiator — silt is built to **hold the privacy ×
accountability × Sybil trilemma**, refusing to trade any corner away where
every prior system surrendered one: work-backed, identity-bound reputation
with **no token, no coin, no speculation**, and publishing that stays
cryptographically unlinkable from the standing that earns it. That claim
separates Silt cleanly from storage-coin projects and from tools that read
as built for wrongdoing. It is a claim we bind to a falsifiable test — held
only if an *external* red-team can't achieve any of the three failure modes —
not a victory we declare. Details in [launch-plan.md](launch-plan.md).

## Governance & operations

**Biggest concern — bus factor and the absence of a home.** One
maintainer, no entity, no funding model, no incident process.

**Mitigations.** Stand up the foundation/entity (above); write down
governance for the chain's reputation thresholds and for how revocations
are proposed and adopted; a documented incident/disclosure process;
a funding model that is **grants/sponsorship, not a token** (a token
would undercut the neutral-infrastructure positioning and invite
securities questions). Grow the contributor base deliberately —
`CONTRIBUTING.md`, `CODEOWNERS`, review-gated merges (already enforced).

---

*This is a living document. As mitigations ship, note them here and in
the [risk register](risk-register.md).*
