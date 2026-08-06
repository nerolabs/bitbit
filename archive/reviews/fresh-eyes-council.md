# The Fresh-Eyes Council

> **⚠️ ARCHIVED (2026-08-06) — a point-in-time council, not current.** This is the
> record of the *first* fresh-eyes council (convened ~2026-08-02, pre-composition-reset).
> Its findings were folded into [`risk-register.md`](../../docs/risk-register.md) and
> [`launch-plan.md`](../../docs/launch-plan.md), which are the live docs. A **new** council
> is planned; its commissioning brief is
> [`docs/reviews/fresh-eyes-council-brief.md`](../../docs/reviews/fresh-eyes-council-brief.md).
> Kept for provenance; read the live docs for current posture.

A convened review by experienced hands wearing different hats — Legal,
Trust & Safety, Security, PR, Marketing, Governance. They know Silt is
open source; they want to protect everyone who builds and runs it. Each
names its single biggest concern and its recommended mitigation. The
[risk register](../../docs/risk-register.md) ranks these; the
[launch plan](../../docs/launch-plan.md) acts on the marketing findings.

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
cryptographic and trust-plane code.** The V1 M0 mechanism is now **built**
(Gate 4): a verify-without-fetch proof-of-retrieval, a proof-of-space-time
bond (a space-hard identity-bound plot × a VDF, persisted), standing composed
only from bond + audit (self-reported serving deliberately buys none), with
fork-choice reconciliation and equivocation slashing — proven at unit +
in-process sim + real-daemon e2e. **What it has not had is independent
review**, and any of it could fail at real scale until it does.

**Mitigations.** An **independent adversarial review before any "production"
claim** — now scheduled, and the standing that concern most wants: a fresh,
memory-less acceptance pass and then a red-team pass, from the public repo
only (see [`docs/reviews/`](reviews/)). Keep the "not production-hardened /
review-pending" labeling honest until a clean pass and, ideally, a human
bug-bounty; a coordinated-disclosure policy; harden the DHT against
Sybil/eclipse; and run the multi-machine field test (#52) before real
economic value rides on it.

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
with **no token, no coin, no speculation**. Its Sybil-resistance is a systemic
composition — C1 (no discount: a fraction q of standing costs ≈ q·C_honest,
where C_honest = disk × address-diversity × time × served-demand) + C2 (no quiet
capture) — held in tension, not a single Sybil-proof primitive. On privacy, silt
refuses to surveil (absolute); access-unobservability is a metadata-layer goal
held in tension (the anonymity trilemma is a hard wall), not a blob-layer
absolute — the standing that earns a fetch is unlinkable from it at the shipped
layers, but we do not claim access is never observable. That claim
separates Silt cleanly from storage-coin projects and from tools that read
as built for wrongdoing. It is a claim we bind to a falsifiable test: the C1/C2
composition claim held in tension, verified by an *external* red-team — some
seams held-not-closed is a PASS under M0's own definition — not a victory we
declare. Details in [launch-plan.md](launch-plan.md) and
[design/m0.md](design/m0.md).

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
