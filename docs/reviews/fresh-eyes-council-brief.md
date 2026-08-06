# Fresh-eyes council — commissioning brief

**What this is.** The brief for convening a *new* Fresh-Eyes Council: a cross-functional
review by experienced hands wearing different hats — **Legal, Trust & Safety, Security,
PR, Marketing, Governance** — whose job is to **protect everyone who builds and runs
silt.** Each hat names its single biggest concern and a recommended mitigation. This is
distinct from the technical/architecture review ([`fresh-eyes-brief.md`](fresh-eyes-brief.md),
"is the system sound?") and from the adversarial security red-team
([`m0-redteam-brief.md`](m0-redteam-brief.md), "break M0"). The council asks: *given what
silt now is, what cross-functional risk could hurt the people involved, and how do we
design it out before launch?*

The prior council (2026-08-02, pre-composition-reset) is archived at
[`archive/reviews/fresh-eyes-council.md`](../../archive/reviews/fresh-eyes-council.md);
its findings live on in [`risk-register.md`](../risk-register.md) and
[`launch-plan.md`](../launch-plan.md). This brief commissions a **fresh** pass because a
lot has changed since (below).

## Read first (current canon — the ground truth)

- [`docs/TENETS.md`](../TENETS.md) — what silt is and refuses to be (the immutables).
- [`docs/design/m0.md`](../design/m0.md) — the M0 mission as a **systemic composition**.
- [`docs/decisions.md`](../decisions.md) — the owner decisions and why.
- [`docs/threat-model.md`](../threat-model.md) + [`docs/safety-denylist.md`](../safety-denylist.md)
  + [`GOVERNANCE.md`](../../GOVERNANCE.md) — the abuse/legal posture.
- [`docs/launch-plan.md`](../launch-plan.md) + [`docs/risk-register.md`](../risk-register.md)
  — the current go-to-market stance and ranked risks.

**Ground rule:** the code and these docs are the truth. Do not accept marketing
adjectives; if a claim isn't in the canon or the code, treat it as unproven.

## What changed since the last council (so this pass is fresh, not a re-run)

1. **The mission was reframed (composition reset).** M0's Sybil-resistance is no longer
   pitched as a single "Sybil-proof" primitive (impossible by Douceur). It is a **systemic
   composition held in tension** — **C1 (no discount)** + **C2 (no quiet capture)** — where
   some corners are *held, not closed*, and the docs say so. **T&S / PR / Legal implication:**
   the honesty posture is now "priced and bounded, with named residuals," not "solved."
2. **The M0 mechanism is built + internally hardened** (H1–H6) and the two routed-to-research
   constructions were **delivered** (center-less proof-of-repair exists; a non-globality
   takedown metric is constructed). Durability is stated as **finite-but-renewable**, not
   "perpetual."
3. **Takedown is per-operator + transparency-logged, never a global switch**, and there is
   **no decryption backdoor at the core** (D-DISCLOSURE). Access privacy is a stated
   **metadata-layer tradeoff**, not a blob-layer absolute (D-PRIV).
4. **Still pre-launch, build-from-source, explicitly unaudited.** No public binaries; the
   external security re-verification and multi-machine field test are the launch gates.

## What each hat should return (one biggest concern + a mitigation)

- **Legal.** Operator liability under a content-blind network; the "software publisher, not
  operator" stance (GOVERNANCE.md); the content-blind firewall as liability shield; DMCA /
  jurisdictional exposure of the transparency-logged takedown model; the throwaway dev-relay
  posture. *Biggest legal landmine before a public launch?*
- **Trust & Safety.** The abuse-handling design (post-hoc, adoption-bound denylists;
  per-operator honoring; the non-globality metric). Is the honest "we cannot pre-screen"
  posture defensible and humane? *Where does the current design most fail a real-world abuse
  scenario, and what's the cheapest mitigation that doesn't become a global kill switch?*
- **Security (non-cryptographic).** Operational/supply-chain/release-integrity risk:
  signing/notarization, the update model, key handling, the dev-relay box, dependency
  posture. *(The cryptographic M0 attack surface is the red-team's job — do not duplicate;
  focus on the operational blast radius.)*
- **PR / Comms.** Does the messaging read as "neutral infrastructure," never "evasion tool"?
  Is "held in tension / unaudited / build-from-source" being communicated honestly, or does
  any surface over-claim? *The single message most likely to be mis-read by press or a
  hostile actor?*
- **Marketing.** Reaching the narrow technical audience who'd run nodes and break the system,
  without attracting the wrong crowd first. *The one positioning choice that most protects
  the people building silt?*
- **Governance.** Decision rights, the anchor training-wheels and their shed, contributor
  protection, the "no permanent center" commitment vs. practical bootstrap centralization.
  *Where could governance quietly re-centralize, and how do we make that visible?*

## The through-line to test

The one theme that crossed every hat last time — still the acid test — is: **a
content-neutral, "cannot-know-what-it-carries" network needs its abuse-handling and legal
posture designed in from the start, and its messaging must be "neutral infrastructure,"
never "evasion tool."** Confirm or break that this still holds under the reframed mission
and the pre-launch reality.

## Output

A short memo per hat: **one biggest concern + one recommended mitigation**, ranked by how
much it protects the people building/running silt. Findings should land in
[`risk-register.md`](../risk-register.md) (ranked) and, where they touch messaging,
[`launch-plan.md`](../launch-plan.md).
