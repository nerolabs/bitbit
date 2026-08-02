# Gate 4 — the M0 mechanism: design constraints (pre-code)

> **Status: design doc, ahead of code.** ROADMAP requires Gate 4 to own a design
> doc + threat model *before* implementation — the binding, not the primitives,
> is the research. This file collects the constraints the Gate-4 build must
> respect, surfaced by the [build-vs-intention audit](../reviews/build-vs-intention-2026-08-02.md).
> It is not the full spec; it is the set of "get this wrong and you re-architect"
> boundaries. Issues: #90 (4a PoR), #91 (4b bond), #92 (4c standing), #93 (4d
> persistence), #97/#98/#99 (permanence traps), #100 (4f equivocation), #52 (4e
> field test).

## What Gate 4 must produce

The genuine M0 composition, replacing the honestly-labeled placeholders:
identity-bound, time-integrated, **unlinkable** standing built from a real
proof-of-retrieval and a real memory-hard / proof-of-space bond — proven by an
**external** red-team (B8/V3), multi-machine (#52).

## Why the seams already permit this (the good news)

The audit confirmed the swap is clean, not a rewrite:

- The bond seal is one unexported function (`core/bond/bond.go:170 sealBlock`);
  the verifier checks answers against the published Merkle root, so the seal
  algorithm is a free variable — swapping in a memory-hard construction touches
  no caller, no wire shape, no chain record.
- PoR is contained to `core/node/por.go`.
- The audit crypto reaches consensus **only as a reputation number**
  (`credit.Reputation` → `chain.rep`); the chain record encodes neither the bond
  scheme nor the PoR scheme. Record format is decoupled from mechanism.

Build forward on these seams. The constraints below are the places where "build
forward" has a sharp edge.

## Constraint 1 — off-loop, persisted proof work (B2 tension)

**B2 says the core runs on one serialized loop, no goroutines in core.** The
placeholder bond reseals cheaply *on that loop* (`core/node/bondaudit.go:27-29`,
iterated SHA-256). A real memory-hard / proof-of-space seal is, by design,
**expensive to generate and expensive to regenerate on restart**. Running it on
the core loop would stall the whole node.

Therefore the real mechanism must:
- Generate/plot the bond **off the core loop**, in an adapter, handing only the
  finished commitment (a `Root` + size) back to core — the loop stays lock-free
  and fast; only cheap *verification* touches it.
- Gain a **persisted-bond blob** adapter (analogous to `adapters/diskproofs`), so
  a restart does not re-plot. The placeholder gets away with reseal-from-NodeID
  precisely because it is cheap; the real one cannot.

Decide this before writing 4b/4d, or it forces a mid-build refactor of where bond
work runs.

## Constraint 2 — unlinkability is cross-layer, not just ledger-layer

Blind publish tokens unlink the **chain entry** from the reputation key. But a
publish also touches the **DHT/transport** from a `NodeID` + IP (announce /
reprovide), and the token-issuance step can be narrowed by colluding validators
(documented, `core/publishtoken/publishtoken.go:9-14`). M0's privacy corner is
only *held* if the linkage is severed at **every** layer the V3 suite can observe.

The 4c design and the V3 "publish → identity linkage" test must therefore span:
- **Ledger/chain layer** — tokened, `Publisher`-less entry as the default (#97).
- **Network layer** — what a peer/relay/registry learns about the publisher's
  `NodeID`/IP during announce and reprovide.
- **Issuance layer** — the anonymity set of a blind-token request; bound the
  colluding-validator narrowing (canonical validator set, or a mixing step).

Treat unlinkability as a property of the whole publish path, not of one record.

## Constraint 3 — subjective reputation is the partition boundary

`credit.Reputation` (`core/credit/credit.go:179`) is each node's **local** view,
computed from its own observations. "Who is a qualified validator" is therefore
not globally agreed — which is the root reason the quorum cannot survive an
adversarial partition (there is no fork-choice; first valid block at a height
wins, `chain.go:22-25`). This is fine under the stated honest-majority assumption
and is honestly labeled.

Gate 4 must **state this boundary explicitly** as part of the M0 claim: what
adversarial fraction, what partition model, the quorum holds against — and either
(a) accept and document the honest-majority bound, or (b) if a stronger guarantee
is targeted, recognise that it touches this foundation (reputation as consensus
input) and is a larger change than a mechanism swap. The equivocation/slashing
work (#100) lives against this boundary.

## Constraint 4 — permanence: get the record right before real blocks

The chain is append-only, no reorg. Three record-level decisions must land
**before any persistent network writes blocks**, because they cannot be undone
afterward (#97 tokened-publish default, #98 schema version, #99 Gated registry).
The Gate-4 mechanism changes (real bond/PoR commitments, mandatory tokens) ride on
top of #98's version guard.

## Open threat-model questions (for the external red-team, B8/V3)

- Can a Sybil farm amortize one memory-hard bond across N identities, or is it
  strictly identity-bound (N bonds for N identities)?
- Is "N× ruinous for a Sybil farm, hobbyist-cheap for one honest node" (S6)
  actually achievable at the target honest-node cost, given time-integration is
  the equaliser? State the cost model.
- Does the blind-token anonymity set stay large under realistic validator
  collusion?
- Can a validator equivocate or capture the quorum on a small/young network before
  the training wheels shed (#100, risk #15)?
</content>
