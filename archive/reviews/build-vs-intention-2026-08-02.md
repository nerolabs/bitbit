# Build-vs-Intention Audit — 2026-08-02

> A code-grounded audit of the **current build** against the **intentions**
> (`docs/TENETS.md` immutables, M0, and the `V1` gate spine). The question it
> answers: *is there a clear path forward to V1 by building forward, or are there
> places we must reverse and re-architect?* Companion to the intention-level
> [`fresh-eyes-2026-08-02-intention.md`](fresh-eyes-2026-08-02-intention.md);
> that one reviewed the *intentions*, this one reviews the *code against them*.
>
> Method: four independent code dives — trust-plane seams, consensus/chain,
> S7 durability-economics, and a mechanism-immutable violation hunt — each
> reporting with `file:line` evidence and ranking findings by re-architecture
> severity. Load-bearing claims were re-verified by hand.

## Verdict

**The architecture is sound and the path is clear.** No mechanism-immutable is
violated in a way that forces a reversal, and the trust-plane placeholders sit
behind genuinely clean seams: the real Gate-4 mechanisms swap in without touching
chain-record or wire schemas, because the audit crypto reaches consensus only as
a reputation *number*, fully decoupled from the record format. That decoupling is
the best structural decision in the build — it is why Gate 4 is a *swap*, not a
rewrite.

The risk is not architecture; it is **sequencing**. The chain is append-only with
no reorg (`core/chain/chain.go:22-25`), so a few things become *permanent and
unrecoverable* if we build forward before fixing them. And three genuinely hard
problems are under-named in the gate spine.

## ✅ Solid — keep building forward

- **All six mechanism-immutables clean.** Content-blind (encryption always before
  `store.Put`, `core/pipeline/pipeline.go:91-116`); bytes-are-truth (re-verify on
  every store read, `diskstore.go:75`, `memstore.go:47`, reconstruction re-hash
  `pipeline.go:341-345`); no-permanent-center (genesis key is public-by-design and
  earns no reputation, `core/genesis/genesis.go:39-46`; training wheels shed via
  monotonic `Mature()`, `chain.go:224-235`); access-unsurveilled (no log/record
  pairs requester with content); no-global-censorship (per-operator denylist +
  quorum tombstones); **core-carries-zero-meaning** (exhaustive grep: no
  filename/description/mime/tag anywhere in core).
- **Three of four Gate-4 swaps are clean forward-builds.** `4b` memory-hard bond
  = replacing one unexported function (`core/bond/bond.go:170-181 sealBlock`); the
  verifier checks answers against the published Merkle root, so the seal function
  is a free variable. `4a` real PoR = contained to `core/node/por.go` (rewrite
  `gradeAnswers`, drop the ground-truth fetch). `4d` = on-chain double-spend is
  *already* live (`chain.spent`, `chain.go:194,303-307,400-402`); only issuer-key
  persistence remains. **No placeholder is baked into any chain-record or wire
  schema.**

## 🔴 Reversal traps — fix before a real network writes blocks

The chain is append-only, no reorg. Anything wrong that is written is wrong
forever. Three such items are live now:

1. **Publisher-privacy default is OFF.** `-require-tokens` defaults to `0`
   (`cmd/silt/daemon.go:67`), so `tokenQuorum == 0` (`chain.go:192`) and every
   publish writes a permanent `Publisher → root` identity map into the immutable
   chain (`ports/ports.go:118`). This is the M0 *privacy* corner silently
   surrendered in the historical record. The blind-token crypto and the tokened
   (Publisher-less) chain entry both already exist — the default just doesn't use
   them. **The moment a persistent network writes real blocks under this default,
   unlinkability for that history is unrecoverable.** Highest-leverage item in the
   audit. → issue (chain-permanence trap #1).
2. **No schema version.** `Block`/`Entry` carry no `Version` field (verified — no
   hit in `chain.go`/`ports.go`). Any Gate-4 change to what the block hash commits
   to, or to validation semantics, is a hard fork with nothing to gate the eras;
   old blocks silently mis-validate under new rules. Add a version field *now*,
   while the chain is still throwaway. → issue (trap #2).
3. **`Gated` registry hard-requires `Publisher`** (`core/registry/gated.go:25-26`,
   `ErrPublisherRequired`) — structurally incompatible with unlinkable publish.
   Give it a token branch or mark it test-only. → issue (trap #3).

## 🟠 Under-scoped in Gate 4 — three re-architecture items to name

Not reversals of what exists, but structural additions the spine treats as
smaller than they are — and the V3 red-team will target all three:

1. **Equivocation, fork-choice, and slashing-for-chain-misbehavior are absent.**
   A validator can attest two competing blocks at one height with zero consequence
   (`Attest` is stateless, `chain.go:125`); slashing exists only for storage-audit
   failures (`credit.go:109`), not consensus misbehavior; a forked node stays
   forked forever (`apply` is append-only with no undo, `chain.go:396`; `SyncChain`
   silently `break`s on divergence, `chainrole.go:207`). Anti-persona #14
   ("equivocate in consensus") is an outcome we *deny* — but **no gate-4 issue
   builds that defense.** This is an intention↔plan gap. → new gate-4 issue.
2. **B2 (single-loop core) vs. expensive Gate-4 crypto.** The placeholder bond
   reseals cheaply *on the core loop* (`bondaudit.go:27-29`). A real memory-hard /
   proof-of-space seal is expensive to generate and to regenerate on restart — it
   must move off-loop into an adapter and gain a persisted-bond blob (analogous to
   `adapters/diskproofs`). Additive, but a design constraint for the 4b/4d design
   doc *before* code.
3. **Subjective reputation as consensus input.** `credit.Reputation`
   (`credit.go:179`) is a per-node local view; "who is a qualified validator" is
   not globally agreed. This is the root reason the quorum cannot survive
   adversarial partition — fine under honest-majority (and honestly labeled), but
   any stronger guarantee touches this foundation. State it as an explicit boundary
   of the V1 claim.

## 🟡 S7 — the one net-new subsystem (correctly placed in Gate 5)

Repair is **100% charity today** — `core/node/repair.go` and `demand.go` contain
zero ledger calls. The data model has no per-content account: registry `Entry` has
no balance/endowment (`ports/ports.go:112-125`), and credit is keyed by `NodeID`,
never by `Root` (`credit.go:27,52`). S7 needs a **root-keyed durability escrow**
(accrues from the demand the content serves; pays caretakers for *verified*
repair). A new subsystem — but a forward-build: the care-link privilege separation
already lets you "pay a stranger to repair what they can't read"
(`link.CareHandle` / `pipeline.LoadLayout`), and the `por.go` verify-then-settle
pattern is the anti-fraud template. Budget it as a subsystem, not a tweak.

Orthogonal durability-correctness gaps S7 will also touch: publish returns a link
with no dispersion/caretaker guarantee (`pipeline.go:176`); manifest chunks have
no parity — caretakers are their only redundancy (`repair.go:172-179`); no
caretaker ⇒ no repair (`repair.go:33-35`).

## ⚪ Cheap immutable-hardening (fold into Gate 1)

- **`RecordServe(id, from, chunkID, …)` threads (requester, chunkID) together**
  (`node.go:500`, port `ports.go:157`). Not a leak today (the sole impl discards
  `chunkID`, `credit.go:93`), but the signature *invites* a future who-fetched-what
  record — an immutable-#4 violation waiting to happen. Drop the `ChunkID` param so
  surveillance is un-violable *by construction*.
- **`cachestore` returns cache hits without re-hashing** (`cachestore.go:56-79`,
  documented perf call) — the one place "re-verify every read" is relaxed.
  Re-verify, or amend the immutable's wording so the exception stays conscious.
- **Anchor shed-thresholds are unpinned operator config** (`-mature-validators`,
  `daemon.go:219-237`) — nothing bounds it, so a deployment could set it so high
  the network never sheds. "No permanent center" is enforced by config discipline,
  not code. Consider a sanity bound.

## Bottom line

Will we have to reverse or re-architect? **Not the architecture — the seams are
right.** Close three chain-permanence traps before real blocks are written (token
default, schema version, Gated registry), grow the spine by one issue (consensus
equivocation/slashing), and carry two constraints into the Gate-4 design doc
(off-loop crypto, subjective-reputation boundary). Then build forward.

## Actions taken from this audit (2026-08-02)

- Filed chain-permanence traps and the equivocation gap as `V1`-milestone issues.
- Created [`docs/design/gate4-m0-mechanism.md`](../design-history/gate4-m0-mechanism.md)
  capturing the design constraints Gate-4 code must respect.
- Updated `docs/risk-register.md` rows 3, 12, 14 and added the chain-permanence row.
</content>
</invoke>
