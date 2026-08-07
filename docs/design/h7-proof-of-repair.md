# H7 slice 2 — center-less proof-of-correct-repair (design)

> **Status: design, not built.** This is the build-design for H7 slice 2 (issue
> [#95](https://github.com/nerolabs/silt/issues/95)), the verification half of the
> durability story. Slice 1 (the escrow/bounty *primitives* — `FundEscrow`,
> `RecordServeToObject` auto-skim, `BountyFor`, `PayBounty`) has shipped in
> `core/credit/escrow.go`. This slice builds the gate those primitives hang off:
> *a bounty releases only against a proof that the repair was real and correct.*
> Decision basis: [`decisions.md`](../decisions.md) **D-S7**; spec: [`m0.md`](m0.md).
>
> A dedicated transparent-PCS survey + external critique has now landed and is
> folded in: **§7 (library) is resolved to a transparent Pedersen vector
> commitment (Semi-AVID-PR construction, pure Go)**, and §13 records the review's
> findings and the risks it raised. Read [`m0-sybil-rebind.md`](m0-sybil-rebind.md)
> and [`bond-audit.md`](bond-audit.md) for the standing/bond surfaces this leans on.

## 1. What this slice is for

The repair loop (`core/node/repair.go`) already keeps content alive: when a stripe
loses shards it fetches `k` survivors, reconstructs the missing shards from parity
math, verifies each against the Merkle root, and re-seeds them. Slice 1 made that
work *fundable* — a per-object reserve pays a rarest-shard bounty. But an escrow
that pays on a bare *claim* of repair is a faucet for self-dealing: a node says "I
repaired stripe 7," calls `PayBounty`, and drains the reserve without lifting a
finger.

Slice 2 closes that. It makes a bounty releasable **iff a center-less quorum
confirms both**:

- **Correctness** — the shard the repairer produced *is* the correct codeword
  coordinate for that stripe (not garbage, not a different object's chunk),
  checked **plaintext-blind** against a commitment the network holds.
- **Retrievability** — the repairer **actually stores those bytes now** and can be
  re-challenged over time (not "I computed it and threw it away").

A claim that fails either check is not just rejected — the non-verifying
transcript is a **publicly-checkable fraud proof**, and the false claimant is
**bond-slashed**. That asymmetry (honest repair auto-clears to cost; a false claim
forfeits bonded standing) is what the equilibrium in `A1-cold-repair-equilibrium`
rests on, and it is what Freenet/GNUnet never had.

**In scope:** plain Reed–Solomon repair (silt's shipped code, `core/erasure`,
`klauspost/reedsolomon`, GF(2⁸)). **Out of scope (research frontier, not an M0
blocker):** proof-of-correct-repair for **MSR / regenerating codes** — no published
construction exists; silt ships plain-RS reconstruction, so this is a roadmap item
(`m0.md §10`, [#182](https://github.com/nerolabs/silt/issues/182) sibling).

## 2. The claim, precisely

Reed–Solomon reconstruction is **linear**. A recovered shard `s_j` of a stripe is a
fixed public linear combination of any `k` surviving shards `s_{i_1..i_k}` over
GF(2⁸):

```
s_j = Σ_t  λ_{j,t} · s_{i_t}        (arithmetic in GF(2^8))
```

where the coefficients `λ_{j,t}` come only from the code's generator/Vandermonde
structure and the *set of survivor indices* — both **public**. Nobody needs the
plaintext to state this relation; they need the *survivor set* and the code
parameters `(k, n)`, which are in the manifest.

So "the repair is correct" is exactly: **does this public linear relation hold over
the true shard values?** If our per-shard commitment is **linearly homomorphic** —
`Commit(a·x + b·y) = a·Commit(x) + b·Commit(y)` — a verifier can check the relation
on *commitments* it already trusts, seeing no bytes:

```
Commit(s_j)  ?=  Σ_t  λ_{j,t} · Commit(s_{i_t})
```

That is the whole trick (D-S7): RS repair is a public linear combination, silt's
commitments *can be made* linearly homomorphic, so correctness verification is
"check a public linear relation over committed values." The rest of this doc is the
engineering to make each of those three italicized things real.

**This is a known, published primitive, not a silt invention — adopt it.** The
external review (§13) identified the exact construction: **Semi-AVID-PR**
(Nazirkhanova–Neu–Tse, [eprint 2021/1544](https://eprint.iacr.org/2021/1544.pdf))
commits each shard with an additive-homomorphic vector commitment and verifies "a
shard is indeed a linear combination of the source shards" via the homomorphism,
plaintext-blind — with **Hendricks–Ganger–Reiter** ([PODC'07](https://pdl.cmu.edu/PDL-FTP/SelfStar/podc07.pdf))
as the antecedent homomorphic-fingerprint result that provably blocks
wrong-but-consistent fragments. silt's novelty is *not* this check; it is the
**composition** (§8): binding correctness to SW proof-of-current-possession, an
identity-bound anti-double-count seed, a domain-diverse caretaker quorum, and a
slashing gate. That composition is where the flag is planted.

**Definitional note — "correct" means "consistent with the anchored commitments."**
Proving `Commit(s_j) = Σ λ · Commit(s_i)` proves the shard is consistent with the
committed survivors *under the public code* — it does not appeal to some external
"true plaintext." That is exactly the right and only meaningful notion here,
**because the commitment vector `{C_i}` is Merkle-anchored into the manifest root**
(§4), which is content-addressed. There is no other ground truth in a
plaintext-blind system, and consistency-with-the-anchor *is* correctness. The
residual worry ("wrong-but-consistent shard") therefore reduces entirely to "are
the committed survivors the real ones?" — closed by the manifest anchor, not by the
linear check. State this definition wherever "correct" appears.

## 3. The three-field problem (G4) — the crux to pin down first

There are **three different algebraic domains** in play, and the design lives or
dies on how they bridge:

| Layer | Field / ring | Where |
|---|---|---|
| Erasure coding | **GF(2⁸)** | `core/erasure` via `klauspost/reedsolomon` |
| Retrievability (SW PoR) | prime field **p = 2²⁵⁵−19** | `core/por` |
| Correctness commitment | **binary field GF(2^k)** (transparent PCS) | *new* `core/commit` |

The correctness relation (§2) is over GF(2⁸). To check it on commitments we need a
commitment scheme whose homomorphism carries that relation. **A soundness
pressure-test (§13) established a theorem-level result here that decides the whole
library question — and it is *negative* for the tempting middle path:**

- **There is no ring homomorphism GF(2⁸) → F_r for a prime r ≠ 2** (characteristic 2
  vs r): any additive hom must send `2·1 = 0` in GF(2⁸) to `2·φ(1)` in F_r, forcing
  `2 = 0` in F_r — false. So **no prime-field (Pedersen/KZG) commitment can carry
  GF(2⁸)'s XOR-addition** nontrivially.
- Bit-decomposing a byte to 8 F₂ coordinates handles multiply-by-λ as a fixed 8×8 F₂
  matrix (fine) — **but the XOR-*addition* across the survivor terms is a sum mod 2**,
  and an F_r-additive homomorphism computes the *integer* sum (`1⊕1 = 0` vs `1+1 = 2`).
  Forcing agreement needs a per-bit **parity/range argument** (`x(x−1)=0`), a
  *non-linear* constraint — i.e. a proof system, not a homomorphic commitment check.
- **So the "keep klauspost GF(2⁸) + Pedersen + bit-matrix" middle path is dead.** A
  plaintext/bandwidth-blind homomorphic check requires the erasure code and the
  commitment to **share a field**. Two ways to get there, both with real cost:
  - **Move the code to F_p** (Semi-AVID-PR — its code *is* over the commitment's prime
    field; it never commits GF(2⁸)). Pure-Go and transparent, but a **storage-format
    change** off klauspost GF(2⁸). Roadmap-sized.
  - **A characteristic-2-native binding commitment** (FRI-Binius / lattice-SIS) — keeps
    GF(2⁸), but no mature pure-Go instantiation exists in 2026 (§7). Fast-follow.
- **M0 therefore takes neither blind path; it ships the recompute floor** (§4.3, §7),
  which sidesteps the field problem entirely (no commitment homomorphism — just
  reconstruct and compare content-addresses) at the cost of fetching k survivors.

**G4 discipline:** the map is *decided* — for M0 there is no commitment field to
bridge (recompute compares SHA-256 IDs). If/when a blind path is taken, build-step #1
is to write the exact same-field construction (F_p Semi-AVID-PR, or a char-2 scheme)
and its soundness argument before any `core/commit` code.

Note the retrievability field (prime p) is **independent** — SW PoR proves a
*different* statement (possession of bytes), so it composes with any correctness leg
at the transcript level (§6), not the field level.

## 4. Correctness layer — the homomorphic commitment silt does not yet have

**Honest gap to state up front:** silt's shipped shard commitment is the **SHA-256
Merkle root** over shard IDs (`manifest.Root()`), and SHA-256 is **not** linearly
homomorphic. The phrase "checked against the commitment the network already holds"
in D-S7 is therefore **aspirational for the homomorphic path** — today the network
holds a Merkle root, which supports *recompute-and-compare* correctness but **not**
plaintext-blind correctness. Slice 2 must **add** a homomorphic commitment. This is
real new state, not free.

Concretely, at encode/publish time (`core/erasure.EncodeStripe` /
`core/pipeline`), compute and store one homomorphic commitment per shard:

```
C_i = Commit(s_i)   for every data and parity shard i of every stripe
```

and carry the `C_i` in the manifest alongside `Parity` (a new CBOR field, e.g.
`ShardCommits [][]byte`, `keyasint 12`). These commitments are:

- **Small** relative to a 64 KiB shard (KBs for a transparent PCS opening; ~32 B for
  a group-element vector commitment).
- **Publicly recomputable** by anyone who holds the plaintext shard, so a publisher
  cannot lie about them without also failing content-addressing.
- **Merkle-anchored**: the vector `{C_i}` is itself committed into the manifest root,
  so the set of commitments inherits the manifest's existing integrity and takedown
  binding — no new trust root.

### Verify (plaintext-blind)

Given the survivor index set and `(k, n)`, a verifier derives the public
coefficients `λ_{j,t}` (from the same `reedsolomon` Vandermonde the encoder used) and
checks:

```
C_j  ?=  Σ_t  λ_{j,t} · C_{i_t}     (homomorphic op in the commitment field)
```

No shard bytes are fetched or seen. If it holds, `s_j` is the correct codeword
coordinate with overwhelming probability (binding of the commitment).

### Three candidate constructions (final ranking in §7 / §13 addendum — the
soundness pressure-test moved the M0 pick to #3, the recompute floor)

1. **Transparent Pedersen vector commitment, Semi-AVID-PR construction (pure Go)** —
   *blind, but NOT over GF(2⁸) — deferred (§3/§13 theorem).* Only sound if the code
   shares the commitment's F_p field, i.e. a **storage-format change** off klauspost.
   Additively homomorphic, **no trusted setup**, and the *exact*
   published construction for "prove a shard is the correct linear combination"
   ([eprint 2021/1544](https://eprint.iacr.org/2021/1544.pdf)). Built from proven
   Go primitives (`gnark-crypto` `.../fr/pedersen`, or a plain Pedersen vector
   commitment over `cloudflare/bn256` / `drand/kyber`) — **no FFI, no young crypto,
   no reimplementing a scheme.** Cost: the GF(2⁸)→group-scalar bridge (§3), a bounded
   engineering task the whole DA industry already ships. Proof/commitment is a group
   element per shard — budget the manifest bloat (§12).
2. **Transparent binary-field PCS (FRI-Binius)** — *fast-follow, not now.* Would make
   G4 an identity (native GF(2⁸) tower embedding) — theoretically the cleanest — but
   in 2026 it is the *young-crypto* option: the original `IrreducibleOSS/binius` was
   **archived (2025-09)**, the maintained **Binius64** exposes commitments only
   *inside* a SNARK stack (no standalone `Commit`/`VerifyLinear` API, ZK not
   finalized), and **there is no Go port** — adopting it means CGO/FFI to a v0,
   largely un-audited Rust core, arguably *more* B8 risk than the Pedersen path, not
   less. Revisit when a standalone, audited binary-field PCS library ships.
3. **Merkle recompute — THE M0 PICK** (built: `core/repairproof.VerifyByRecompute`).
   Reconstruct `s_j` from k survivors, check it is byte-identical to the
   manifest-committed shard ID. Sound, pure-Go, publicly checkable, content-blind
   (over ciphertext). NOT bandwidth-blind — the verifier holds k survivors (an
   explicit M0 non-goal). Zero new crypto risk; sidesteps the field problem entirely.
   Doubles as the correctness oracle any future blind path is tested against.

*(Demoted from the earlier draft: **BFKW / subspace homomorphic signatures**. The
review found it is conceptually equivalent-or-worse for our threat model — a
homomorphic *commitment* already lets any caretaker check the relation with no secret
and no per-file signing key, whereas BFKW adds a signer, per-file key material, and
the same GF(2⁸) lift with none of the maturity, and has no maintained implementation.
Keep it only as a footnote for a verifier that lacks the commitment vector — not
silt's case.)*

## 5. Retrievability layer — reuse `core/por`, bind to the repairer

Correctness says the *value* is right. Retrievability says *this node holds the
bytes now.* silt already ships the exact primitive: the Shacham–Waters compact PoR
in `core/por` (private-verification, homomorphic linear authenticator over the prime
field). Reuse it unchanged:

- The repairer, having reconstructed `s_j`, **stores it and computes SW tags** for it
  (tags derive from the file's layout key via `por.DeriveKey`, already wired through
  `link.CareHandle.LayoutKey` and `node.DerivePorKey`).
- A quorum verifier issues an SW `Challenge` and checks the aggregated `(μ, σ)` with
  `por.Key.Verify` — no bytes fetched.

**The double-count closer (memo's requirement), already in the codebase.**
`core/node/por.go:porProverSeed` binds each challenge seed to the *prover's node
identity*: `seed = H("silt/por/challenge/prover/v2" ‖ base ‖ proverID)`. A repairer
claiming a bounty is challenged under **its own** identity-bound seed. So an attacker
cannot claim a repair bounty by **relaying an existing honest holder's** proof of an
already-present replica (the `μ` computed under holder A's seed fails the repairer
B's verify) — which is exactly "claim without regenerating / double-count an existing
replica," closed. This is the same mechanism H1/RT-1 used for bond audits; slice 2
inherits it for free.

## 6. Center-less checking — the care-link quorum (the DAS analogue)

No central verifier signs off a repair. silt's **care-link quorum** is the DAS /
PeerDAS pattern: many light caretakers, each independently checking a small random
piece, and correctness is the **AND of their local checks**.

- Each object already has a set of caretakers (`link.CareHandle`s, `node.Care`).
- On a repair claim for stripe `j`, `q` caretakers each independently:
  1. derive the public `λ_{j,t}` and run the **correctness** check of §4 on the
     committed shards (light: field ops over ~n commitments, no bytes);
  2. issue an identity-bound **SW challenge** (§5) and verify **retrievability**.
- The repair is **quorum-confirmed** iff **≥ threshold `τ` of `q`** caretakers return
  "both checks pass." Threshold, quorum size, and caretaker selection reuse the
  domain-diverse near-set machinery (`announceTargets` / `diverseNear`) so the quorum
  can't be packed into one failure domain.

This inherits silt's existing anti-capture posture: the same domain-diversity that
keeps a stripe's *storage* spread keeps its *verification* spread.

**Refinement (§13) — split the quorum's job by leg, because the two legs have
different trust properties.** The **correctness** check is *deterministic and
publicly recomputable*: anyone holding the manifest-anchored commitments and the
public `(k, n, survivor set)` can rerun `C_j =? Σ λ · C_i` and get the same answer.
So a quorum cannot make a mathematically-false correctness claim true — it can only
*withhold* a vote (a liveness effect, the safe failure mode), and a single honest
recomputation is already a **complete proof or a complete fraud proof**. The
**retrievability** check is where independent verifiers genuinely add value: each
issues its *own* random, identity-bound SW challenge, and "the repairer holds the
bytes" is only as strong as the number of independent challengers who confirm it.
Therefore: make the **correctness leg single-verifier-sufficient** (one honest
recomputation gates release; any node can later contest it by publishing the
recomputation), and **reserve the `τ`-of-`q` quorum for the retrievability leg.** This
shrinks the quorum's soundness surface to liveness only and concentrates the
independence budget where it does work.

## 7. Library choice (G3) — RESOLVED: the Merkle-recompute floor for M0

> **Superseded decision (kept for the record).** An earlier draft of this section
> resolved G3 to a "transparent Pedersen vector commitment, Semi-AVID-PR, pure Go."
> A soundness pressure-test (§13) **killed that** — see §3: there is no ring
> homomorphism GF(2⁸)→F_r, so a prime-field Pedersen commitment *cannot* carry
> silt's GF(2⁸) RS relation with a linear homomorphic check, and Semi-AVID-PR only
> works because it puts the code and the commitment in the *same* field (never
> GF(2⁸)). Adopting it faithfully would mean **changing silt's storage code to F_p** —
> a roadmap-sized change, not a `core/commit` bolt-on. So the M0 decision moves.

Constraints (firm): **B8** adopt-don't-invent; **no trusted setup**; **pure Go**;
**publicly-checkable** (no secret-key verifier). Only two constructions survive all
four (§13 trilemma table), and the plaintext/bandwidth-blind one costs a
storage-format change:

**M0 decision:** ship the **Merkle-recompute correctness leg** —
`core/repairproof.VerifyByRecompute` (built). Reconstruct the target from k
survivors, check it is byte-identical to the manifest-committed shard ID. Sound,
pure-Go, publicly checkable, content-blind (over ciphertext). Its one cost is that it
is **not bandwidth-blind** — the verifier holds k survivors. That property is now an
**explicit M0 non-goal**, not a silently-broken claim.

**Deferred blind upgrades** (either removes the k-survivor fetch; both are
post-M0):
- **F_p re-encode (Semi-AVID-PR + transparent Pedersen, pure Go).** The *only* pure-Go
  blind path — but it requires the correctness-relevant erasure code to live in the
  commitment's field, i.e. **move silt's durability code off klauspost GF(2⁸) to
  F_p** (a storage-format decision; committing a *parallel* F_p code that doesn't
  certify the stored GF(2⁸) bytes reintroduces the same cross-characteristic gap and
  does **not** close — §13 (A2)). Roadmap-sized.
- **Characteristic-2-native binding commitment (FRI-Binius / lattice-SIS).** Keeps
  GF(2⁸) storage *and* gets blindness — the theoretically-right shape — but has **no
  mature, standalone, pure-Go instantiation in 2026** (Binius64: Rust, no standalone
  PCS API, FFI; a binding char-2 commitment otherwise means adopting a lattice/SIS
  scheme = invent-crypto risk). Fast-follow when such a library exists.

**Never** hand-roll a binary-field PCS or a lattice commitment to hit a milestone.

## 8. The release/slash gate — wrapping slice 1

> **Built (the decision logic):** the pure gate is implemented and unit-tested —
> `repairproof.Decide(correctnessOK, retrievabilityVotes, τ)` returns the
> release/slash verdict; `repairproof.VerifyRetrievability` runs the identity-bound
> SW check (`repairproof.RepairChallengeSeed` closes the relay/double-count);
> `repairproof.VerifyByRecompute` is the correctness leg (§4.3); and
> `credit.SlashFalseRepair` is the `reduces`-class slash press. Per the §6
> refinement, `Decide` treats correctness as single-verifier-sufficient (a failing
> recompute is self-attributing ⇒ `Slash`) and reserves the τ-of-q quorum for
> retrievability (a shortfall denies the bounty but does **not** slash — it may be
> transient). **Remaining:** the node/network wiring — carrying a `RepairClaim` over
> the wire, running the caretaker quorum, and applying the verdict to the ledger
> (`PayBounty` / `SlashFalseRepair`). That is the integration step (needs `core/node`
> + a `MsgRepairClaim`/`MsgRepairVote` pair) and lands with sim + e2e coverage.

Slice 1's `PayBounty(root, repairer, amount)` trusts its caller. Slice 2 *is* that
caller, and it only calls on a verified transcript:

```
RepairClaim = { root, stripe, shardIndex, repairerID,
                survivorSet, C_j (claimed),
                SWproof, quorumVotes[] }

release iff:
  correctnessOK  := homomorphic check of §4 passes for C_j        (each voter)
  retrievabOK    := por.Verify under repairer-bound seed passes    (each voter)
  quorumOK       := |{ voters : correctnessOK ∧ retrievabOK }| ≥ τ
  ⇒ ledger.PayBounty(root, repairerID, BountyFor(base, k, n, reachable))
```

- **Attributable fraud → slash.** A claim where `quorumOK` fails on the correctness
  leg carries its own disproof: the committed `C_j`, the public `λ`, and the
  committed survivors are all on-chain/in-manifest, so *anyone* can recompute the
  homomorphic check and see it fail. That non-verifying transcript is the fraud
  proof. The false claimant is **bond-slashed** — a new `credit` press
  (`SlashFalseRepair`, classified **`reduces`** under Invariant A, mirroring
  `SlashEquivocation`: it can only ever *lower* standing, never mint it).
- **Self-dealing defenses.** (a) A repairer cannot pay itself without a real stored,
  re-challengeable, identity-bound replica (§5). (b) It cannot double-count an
  existing replica (identity-bound seed). (c) It cannot fabricate `C_j` — the
  homomorphic relation pins it to the survivors' committed values. (d) The bounty is
  drawn from the *object's* reserve, capped by `BountyFor`, so a self-dealer at best
  churns its own prepaid credit, and only by doing genuine, verifiable repair work.

- **Why BOTH legs are needed even though silt content-addresses every shard**
  (a subtlety the wiring surfaced). Each shard's ID is individually committed in the
  manifest, so it is tempting to drop the recompute leg and let the identity-bound SW
  PoR against the committed shard ID be the *whole* proof. **That fails against a
  malicious caretaker.** SW PoR proves the holder's bytes are consistent with the
  *tags it stores*, and the tags are derived from the **layout key** — which
  caretakers hold. A caretaker that reconstructs *wrong* bytes `Y` can compute valid
  tags for `Y` under `unitID = X` (the committed ID) and hand `(Y, tags)` to a holder;
  that holder then *passes* the retrievability challenge for `X` while holding the
  wrong bytes. Retrievability alone therefore certifies "holds bytes matching some
  tags," not "holds the correct shard." **The recompute leg closes this**: it
  re-derives the correct bytes from the survivors and checks the content address, never
  trusting the caretaker's tags. So M0 keeps both legs — recompute for correctness
  (tag-forgery-proof), identity-bound SW PoR for durable possession.

**The invariant, restated for this slice.** Nothing here mints consensus standing.
`PayBounty` stays `neutral`; the only standing motion slice 2 adds is a
**slash** (`reduces`). The Invariant-A guard (`core/credit/invariant_a_test.go`)
must gain `SlashFalseRepair` classified `reduces`, with the behavioral test firing it
against a bonded identity and asserting it can only subtract. Repair credits fund
durability and confer **zero** standing — the γ→1/N firewall holds through slice 2.

### 8b. Who is the "repairer"? — the paramedic split (decided at wiring time)

The design above spoke of a single "repairer" that both reconstructs the shard and
holds its bytes. **silt's actual repair loop (`core/node/repair.go`) splits those
roles**, and the wiring must respect it: the caretaker is a *paramedic, not a
hoarder* — it fetches k survivors, reconstructs the lost shard, verifies it against
the Merkle root, and **pushes the rebuilt shard to a fresh storage node, keeping
nothing itself**. So there is no single actor that both did the CPU work and can
answer a retrievability challenge.

**Decision (forced by the composition): the bounty pays the NEW HOLDER of the rebuilt
shard.** The gate releases iff retrievability verifies (§6), so the payee must be the
node that actually holds the re-challengeable, identity-bound replica — i.e. the
fresh storage node, not the caretaker. Rationale:

- The durability budget pays for the **scarce, verifiable outcome** — a fresh replica
  that can be re-challenged over time — not for CPU cycles. That is exactly what keeps
  cold data alive.
- The **caretaker orchestrates for free**: it already *chose* to `Care` for the object,
  so its incentive to reconstruct is its existing motivation, not a bounty. (Paying the
  caretaker for reconstruction would also break the composition — it keeps nothing, so
  retrievability cannot bind to it, and it could self-deal by "reconstructing" into the
  void.)
- **Self-deal check still holds:** the new holder earns the bounty only by holding a
  shard that (a) passes the public correctness recompute against the manifest anchor
  and (b) answers an identity-bound SW challenge — it cannot be a data-less relay or a
  wrong-but-claimed shard. A caretaker colluding with a chosen holder still has to
  produce a genuinely correct, genuinely stored replica to extract the object's own
  prepaid credit — i.e. do the real work.

**Wiring consequence:** the caretaker, after placing a rebuilt shard on a fresh node,
initiates the repair-claim naming that holder; the object's other caretakers form the
quorum, each recomputing correctness (they can fetch survivors) and challenging the
*holder* for retrievability, then applying the verdict to their own local ledgers
(`PayBounty` the holder, or `SlashFalseRepair` on an attributable correctness lie).
Ledger updates are **per-node local accounting**, consistent with the existing credit
economy (each node judges by its own ledger; balances/escrow are observational, not
consensus state — which is why no on-chain bounty transaction is needed).

## 9. Package layout & interfaces (sketch)

- **`core/commit`** *(new)* — the adopted homomorphic-commitment adapter (B8). Pure,
  no store/net. Interface:
  ```go
  type Scheme interface {
      Commit(shard []byte) (Commitment, error)          // at encode time
      // VerifyLinear checks C_out == Σ coeffs[t]·C_in[t] in the commitment field,
      // where coeffs are the public RS λ over GF(2^8).
      VerifyLinear(out Commitment, in []Commitment, coeffs []gf256.Elem) bool
  }
  ```
- **`core/erasure`** — extend `EncodeStripe` to also return per-shard commitments (or
  a sibling `CommitStripe`); expose the public `λ` for a given `(survivorSet, target)`
  as `Coeffs(p Params, survivors []int, target int) []gf256.Elem`. **Not a thin
  wrapper (§13, claim 1):** klauspost builds a *systematic* Vandermonde encode matrix
  and does **not** export its internal reconstruction matrix, and `λ` is
  **survivor-set-dependent** — computing it means **inverting the k×k submatrix of the
  surviving rows** of that encode matrix over GF(2⁸), then taking row `target`. This
  is a real GF(2⁸) linear-algebra reimplementation that **must be tested
  byte-identical to `reedsolomon.Reconstruct`** on random survivor sets, and must
  cover both data-shard repair (identity rows) and parity-shard repair (Vandermonde
  rows). A mismatch here is a silent correctness bug.
- **`core/manifest`** — add `ShardCommits [][]byte` (CBOR field 12), Merkle-anchored;
  bump `Version`; keep `omitempty` so M1/M2 manifests without it still decode
  (degraded/Merkle-only mode).
- **`core/repairproof`** *(new)* or additions to `core/node/repair.go` — assemble a
  `RepairClaim`, run the quorum, gate `PayBounty`, emit slash on attributable fraud.
- **`core/credit`** — add `SlashFalseRepair(id)` (`reduces`); extend the Invariant-A
  guard.
- **`ports`** — a `MsgRepairClaim` / `MsgRepairVote` pair if the quorum runs over the
  wire (mirrors `MsgBondChallenge`/`MsgBondReply`).

## 10. Build order (slice 2)

1. **G4 on paper first** — pin the commitment field and prove the GF(2⁸) RS relation
   maps exactly into it. No code until this is written down and checked.
2. **`core/commit`** — adopt the Pedersen/Semi-AVID-PR construction (§7), wrap it
   behind `Scheme`,
   unit-test `Commit` + `VerifyLinear` against `core/erasure` on random stripes
   (property test: for every survivor set of size ≥ k, the homomorphic check passes
   for the honest reconstruction and fails for any single-byte perturbation).
3. **Manifest + encode wiring** — carry `ShardCommits`; recompute-and-compare test
   that they match freshly encoded shards; back-compat decode test for M1/M2.
4. **Retrievability reuse** — repairer computes+stores SW tags for the rebuilt shard;
   quorum verifier challenges under the identity-bound seed. Reuses `core/por`.
5. **Quorum + gate** — assemble `RepairClaim`, wire the `τ`-of-`q` vote, gate
   `PayBounty`, add `SlashFalseRepair` + Invariant-A extension.
6. **Instrument `g`** (bleeds into slice 3) — record realised repair cost per object
   via `EscrowPaid`; publish the funded horizon.

## 11. Test plan (build-immutable discipline)

Every piece ships **unit + integration(sim) + e2e** plus an inverted-PoC
regression, and the whole suite runs after the batch (vet + `-race` + Docker NAT
harness + CI):

- **Unit** — `core/commit` homomorphic-relation property tests; correctness verify
  passes on honest reconstruction, fails on perturbation; SW identity-bound challenge
  rejects a relayed proof; the release gate's truth table (correctness×retrievability
  → release/slash).
- **Sim** — a repair scenario where a churned stripe is repaired, a quorum confirms,
  and the bounty clears from escrow; assert `EscrowPaid` rises and **no** `Reputation`
  moves for any participant.
- **e2e** — end-to-end repair-and-pay across the harness.
- **Red-team (mandatory, the acceptance gate for this slice)** — the
  **self-dealing / false-repair** adversary: (a) claim a repair not done (garbage
  `C_j`) → correctness fails → slash; (b) double-count an existing replica (relay
  another holder's SW proof) → identity-bound seed fails → denied; (c) compute the
  correct value but don't store it → retrievability fails → denied; (d) pack the
  quorum into one domain → diversity selection prevents it. Each must be **denied and
  attributable**, and each becomes a permanent regression test.

## 12. Open questions to resolve before/while building

- **G3 (library):** *resolved* (§7, §13) — transparent Pedersen vector commitment,
  Semi-AVID-PR, pure Go.
- **G4 (field bridge):** *decided* — the lift (Pedersen over an EC group), bridge via
  the fixed F₂ bit-matrix (§3). Remaining task, still build-step #1: **write the exact
  soundness map on paper** (GF(2⁸) RS relation → additive group relation) and check it
  before any `core/commit` code.
- **Commitment cost:** proof/commitment size × shards/manifest — bound the manifest
  bloat; decide whether commitments are inlined or a separate content-addressed side
  vector.
- **Quorum params:** `q`, threshold `τ`, caretaker selection, and what happens when a
  quorum can't be formed (fall back to Merkle recompute? defer the bounty?).
- **"Commitment the network already holds":** we are **adding** it (§4) — decide the
  migration for content published before slice 2 (Merkle-only degraded mode for old
  objects; homomorphic for new).
- **Slash calibration:** the `SlashFalseRepair` magnitude relative to
  `equivocationSlash` and the bond size — enough to make a false claim strictly
  -EV against the bounty it targets.

## 13. External review — findings folded in

> **Addendum — soundness pressure-test (supersedes the Pedersen recommendation
> below).** A follow-up adversarial check of the "commit GF(2⁸) shards with a
> transparent Pedersen vector commitment" plan returned a **theorem-level negative**:
> there is no ring homomorphism GF(2⁸)→F_r (char 2 vs r), so a prime-field Pedersen
> commitment cannot verify silt's GF(2⁸) RS relation with a linear homomorphic check;
> the XOR reduction-mod-2 is a non-linear constraint needing a per-bit parity proof
> (§3). **Semi-AVID-PR is not a counterexample — its code lives in the commitment's
> prime field, never GF(2⁸)**, so adopting it faithfully means *changing silt's
> storage code to F_p*, not adding a `core/commit`. The surviving trilemma (all four
> of pure-Go, publicly-checkable, no-trusted-setup, adopt-don't-invent):
>
> | Path | Pure-Go | Public proof | Keeps GF(2⁸) storage | Blind (no k-fetch) |
> |---|---|---|---|---|
> | **Merkle-recompute floor** *(M0 pick)* | ✅ | ✅ | ✅ | ❌ |
> | F_p re-encode (Semi-AVID-PR + Pedersen) | ✅ | ✅ | ❌ storage change | ✅ |
> | char-2-native commitment (FRI-Binius/SIS) | ❌ FFI/young | ✅ | ✅ | ✅ |
> | ~~GF(2⁸)+Pedersen+bit-matrix~~ | — | — | — | **UNSOUND** |
>
> Hendricks-style homomorphic fingerprints over GF(2¹²⁸) were also checked and
> **rejected**: they are keyed (Carter–Wegman evaluation point), so a *public* point
> lets a shard-choosing adversary forge — sound only in the symmetric/secret-verifier
> model, which is the wrong trust model for a public fraud proof. **Decision (§7): M0
> ships the recompute floor (`core/repairproof.VerifyByRecompute`, built); blindness
> is a stated non-goal for M0.** The bullets below are the *first* review and are
> retained for the prior-art map; where they say "adopt Pedersen," read "adopt the
> recompute floor for M0; Pedersen only via the F_p storage-code change, deferred."

A transparent-PCS library survey + a four-claim adversarial critique of this design
were commissioned and their findings are folded into §2–§9 above. Summary of record:

- **Adopt, don't invent (correctness layer).** The correctness check *is*
  **Semi-AVID-PR** (Nazirkhanova–Neu–Tse, [eprint 2021/1544](https://eprint.iacr.org/2021/1544.pdf)),
  antecedent **Hendricks–Ganger–Reiter** ([PODC'07](https://pdl.cmu.edu/PDL-FTP/SelfStar/podc07.pdf)).
  Instantiate with a **transparent Pedersen vector commitment, pure Go** (§7). silt's
  novelty is the *composition* (correctness ∧ SW-possession ∧ identity-bound
  anti-double-count ∧ diverse quorum ∧ slash), which is genuine white space —
  Storj audits+repairs but trusts a **central satellite** for repair-correctness;
  Filecoin re-seals (PoRep, not RS-linear repair); FRIDA/PeerDAS prove *availability*,
  not correctness-of-a-specific-repair; SW PoR proves *possession*, not correctness.
- **The correctness reduction is sound**, with the two fixes now in §2/§9: state
  "correct ≡ consistent-with-manifest-anchored-commitments," and treat `Coeffs()` as a
  real survivor-set-dependent GF(2⁸) matrix inversion validated byte-identical to
  `Reconstruct`.
- **G4 is a bounded task, not a blocker** (§3): additive homomorphism + public-constant
  scalar-multiply, GF(2⁸)-mult-by-λ as a fixed 8×8 F₂ bit-matrix — the technique every
  RS+Pedersen/KZG DA system ships.
- **The three risks a skeptic raises, and the answers:**
  1. *"The homomorphic commitment is net-new crypto + a manifest migration."* — New
     *state*, not new *crypto* (adopt Semi-AVID-PR/Pedersen). Merkle floor (§4.3)
     de-risks migration: old objects verify non-blind, new blind. Residual to budget:
     commitment/manifest bloat (§12) — prefer a content-addressed commitment side-vector
     over inlining.
  2. *"Correctness proves consistency, not truth."* — Correct by definition: the
     commitments are manifest-anchored and content-addressed, so a publisher committing
     to garbage fails at *publish*, not repair. There is no other ground truth in a
     blind system (§2 definitional note).
  3. *"The quorum is a soundness+liveness surface."* — Split by leg (§6):
     correctness is deterministic + publicly recomputable, so a false quorum vote is
     itself a fraud proof (bounded to *liveness*); reserve the `τ`-of-`q` quorum for the
     retrievability leg, where independent random challenges add real value. **Open
     liveness item (§12):** define quorum-can't-form behavior (single honest
     recomputation for correctness; defer-and-retry the bounty for retrievability).

*Confirming-only, not adoptable:* NC-Audit / SpaceMac / homomorphic MACs
([SpaceMac](https://arxiv.org/pdf/1108.2080), [Homomorphic MACs](https://crypto.stanford.edu/~dabo/pubs/papers/hommac.pdf))
solve integrity-of-linear-combinations *including repair* but are **symmetric-key** —
wrong trust model for a public, center-less fraud proof. They validate the mechanism,
not the primitive.

---

*Related: [`m0.md`](m0.md) (spec, §5 durability + §10 open frontiers),
[`decisions.md`](../decisions.md) D-S7 (the decision + construction basis),
[`m0-sybil-rebind.md`](m0-sybil-rebind.md) (the identity-bound bond this slice
slashes against). The one invariant this slice must not breach: repair credits
confer zero consensus standing (γ→1/N firewall).*
