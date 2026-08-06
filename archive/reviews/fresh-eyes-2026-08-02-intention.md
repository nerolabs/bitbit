# Fresh-eyes review — INTENTION ONLY (2026-08-02)

> A pre-read for the next fresh-eyes session. This run uses the reusable persona
> in [`fresh-eyes-brief.md`](../../docs/reviews/fresh-eyes-brief.md) with **one rule change: the
> reviewer read intent, not code.** Scope = the canon as written (immutables,
> tenets, roadmap, the GitHub `V1` spine). A code-truth pass is separate and
> still owed. As the reviewer put it: *intent is where a project lies to itself
> first, so it's fair to audit on its own.* Treat this as a challenge to answer,
> not a verdict to accept.

## Verdict (one paragraph)

**On the right track — yes, with two caveats that are load-bearing, not
cosmetic.** This is the most *honest* canon I've reviewed in this space, and
honesty is the rarest asset here — the tiered immutables, the
principle-not-mechanism discipline, the labeled-placeholder candor, and the
refusal to reach for "anonymous/uncensorable" put you ahead of 90% of the
projects that died. But the intention has two cracks. First, **M0 oversells**:
"resolve the privacy × accountability × Sybil trilemma *all at once*" is not what
the design actually intends to deliver — it intends to *hold all three in
tension, all three weakest simultaneously on a young network, with
accountability quietly narrowed to reactive content-level takedown.* Still novel,
still worth doing — but the claim as written is a hostage to fortune. Second,
**the single most important work in the project is not on the board**: Gate 4,
"the car," is one prose bullet and zero GitHub issues, while the tracker you just
declared the single source of truth carries five issues, none of which build M0.
The spine already lies by omission. Fix those two and this is genuinely
differentiated. Leave them and it's another beautifully-documented casualty of
the fourth wound — unproven incentives — with excellent tenets on its headstone.

## 1. The immutables

Cutting nine to six was right; B7/Don't#7/R4 are disciplines, not identity. Two
intent-level problems:

- **[fatal-if-unaddressed] "No center" contradicts the training wheels — and the
  canon doesn't reconcile it.** Immutable #3 says no node is load-bearing. The
  launch design *deliberately* makes anchor validators load-bearing until the
  network matures. An immutable can't be false for the first N months of the
  network's life. Either "no center" is *aspirational-at-maturity* (then it isn't
  immutable from day one, and say so) or the anchors violate it. Honest fix, one
  clause: **"no *permanent* center; bootstrap centralization is explicit,
  time-boxed, and sheds on measured decentralization."** A hostile reviewer finds
  this in thirty seconds and impeaches the whole immutable set over it.
- **[important] M0 is a different *kind* of thing than the other six.** The six
  are *checkable* ("content-blind" → can a host read the bytes? no). M0 —
  "resolve the trilemma" — is a research aspiration with no true/false test. An
  immutable you can't falsify is the "wall-art" your own tenets warn against. Keep
  it at the top (writing your reason-to-exist as an unbreakable constraint is a
  good, unusual move) but **bind it to something falsifiable**: the specific
  mechanism + its adversarial suite, so "did we hold M0?" has a yes/no answer.

## 2. The tenets

Strong. B7, B8, V3, S1/S3 are real invariants with teeth (#60 proves B7 bites).
Two gaps:

- **[fatal] No tenet says the durability economics must close.** This is the
  wound that killed Freenet and GNUnet and that Filecoin/Storj/Sia organize their
  whole existence around. You have S2 ("content outlives any node") and S6
  ("cheap to participate") — but *nothing* asserting **the repair loop must be
  funded in equilibrium, not run on charity.** Reputation buys *presence*, not
  *sustained repair bandwidth under churn*. Caretakers (#44) are a gap and the
  tenets don't even state the goal. **Add the invariant: durability must be
  economically self-sustaining.** If you can't state it, you haven't decided it —
  and it decides whether files exist in three years.
- **[important] B8's proof obligation names no adversary.** "Novel composition,
  proven by a red-team suite" is only real if someone *other than the author*
  writes the attacks. Self-marked homework isn't adversarial proof — it's the
  exact failure the Security council hat flagged (single-author crypto). Name the
  external adversary (audit, bounty, separate red-team) in the tenet, or B8 is
  wall-art the author can satisfy by grading their own exam.

## 3. The roadmap

Single-spine consolidation is correct and overdue; gate order (floors → car →
cut) and the 1→4→6 critical path are right. But the intent underestimates its own
long pole:

- **[fatal] Gate 4 is a research program written as one bullet.** "Adopt real PoR
  + memory-hard bond + unlinkable identity-bound standing + persistence +
  multi-machine proof" is five projects, and 4a/4b carry known hard tensions the
  doc doesn't acknowledge. "Adopt best-in-class, don't invent" does **not** save
  you — the novel part, by your own B8, is the *binding* (identity-bound,
  time-integrated, cheaply-verifiable, with unlinkable publishing on top). No
  drop-in library gives you that composition. The hard research isn't avoided;
  it's *relocated to the binding*, where soundness proofs get ugly. This deserves
  its own milestone, design doc, and threat model *before* code. Prior art, named
  honestly: Chia/Filecoin spent years and industrial resources on the
  "cheap-to-verify, expensive-to-fake, identity-bound" corner. You intend that
  *and* hobbyist-cheap (S6) *and* unlinkable publishing on top. Best novel
  contribution — or the rock the ship hits.
- **[reorder] Prior-art nerve:** your closest ancestors aren't Filecoin (token) —
  they're **Tahoe-LAFS** (convergent encryption + erasure, no incentive layer)
  and **Freenet/GNUnet** (reputation without funded durability). Silt intends to
  fix the wound those died of, but the roadmap doesn't yet show *how the
  durability economics close* — same hole, one genre later. Pull it forward from
  "Gate 5, co-designed" to a first-class design question; it gates whether Gate
  4's reputation even matters.

## 4. The GitHub objectives (the V1 spine)

Where intent and execution diverge most — and the cheapest to fix:

- **[fatal, cheap] The load-bearing work has no issue.** You declared the `V1`
  milestone the single source of truth and said "every issue traces to a tenet."
  Backwards: **the most important tenet — M0 — has no issue implementing it.** The
  milestone holds #27/#47/#48/#52/#65; none builds the real PoR, the memory-hard
  bond, or the unlinkable-standing composition. #52 is a *field-test* issue —
  you're tracking verification of a mechanism you haven't filed the construction
  for. The spine is fiction until Gate 4 is decomposed into issues (4a–4e), each
  tracing to M0. **File them.** A single source of truth that omits the one thing
  you're betting everything on is worse than four messy trackers — it *looks*
  complete.

## The three things to fix before anything else

1. **Put Gate 4 on the board.** File 4a–4e construction issues under `V1`, each
   tracing to M0. The long pole must be visible in the spine or the spine lies.
2. **Qualify M0 to what you intend to deliver.** "Hold all three without
   sacrificing any — all three strengthening as the network matures;
   accountability is content-level and reactive" beats "resolve all three at
   once." The immutable is the *refusal to trade a corner away*, not a victory
   claim.
3. **Name the external adversary** who proves B8 / M0. Not the author. Audit,
   bounty, or red-team — write it into the tenet.

## The one thing that would make me walk away

**M0 kept as an unqualified headline, with no falsifiable mechanism on the board
and no outside party to break it.** A research-grade claim, unfiled,
self-graded — that specific combination is how an honest-looking project becomes
marketing. Everything else I'd stay and help fix. That one, uncorrected, is the
fourth wound with better documentation.
