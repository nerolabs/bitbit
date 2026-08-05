# M0 hardening — the systemic strategy, surface map, and backlog

**Status: LIVING STRATEGY DOC.** This is the source of truth for *why* M0 is not
yet held and *how* we close it systemically rather than one finding at a time.
Update it as surfaces are hardened, decisions are made, and the backlog moves.

**Provenance.** After the G2 fix ([m0-sybil-rebind.md](m0-sybil-rebind.md), PR #166,
commit `4ea5fd7`), a fresh **blind** research team and adversary (red) team went deep
on the whole project. Their outputs live under `silt-reviews/` (out of tree):
`research/research-outcome/` (9 cited memos + a synthesis) and
`redteam/m0-field-test/` (a real multi-container consensus field test + a 6-persona
blind fan-out). This doc is the builder-side synthesis of both.

---

## 0. The one-sentence finding

The red team attacked `4ea5fd7` — **our own G2 merge** — confirmed the plot primitive
we hardened *is now strong*, and **broke the Sybil corner anyway, twice, through two
surfaces we never touched.** M0 is not held because Sybil-resistance, accountability,
and privacy are **system properties spread across many surfaces**, and we have been
hardening one surface per finding instead of enforcing the property across all of them.

---

## 1. The meta-patterns (why "myopic on code" bit us)

1. **We fix instances, not classes.** Both new red-team breaks are re-instances of
   classes we already "fixed" elsewhere:
   - *Identity binding asserted but never verified*: F1 (shared plot) → G2 (prefix
     plot) → **PoR audit proof outsourcing (RT-1, live)**. Three surfaces; we hardened
     the two a red team pointed at.
   - *Fixed but off by default*: F6 objective mode (opt-in) → F4 credits (opt-in) →
     G4-residual floor (was off) → **bond-TTL (RT-2, still off, live)**. Every
     mechanism shipped inert until a red team noticed.
2. **"Standing" is one currency minted by multiple presses with different security.**
   `Reputation = bondedBytes/bondUnit + auditsPassed·25 − …` (`core/credit/credit.go`).
   The **bond-audit** press is identity-bound + deduped + bond-gated (post-G2). The
   **PoR-audit** press (`RecordAudit`) is *none of those*. Same currency, incompatible
   security on the two mints ⇒ RT-1 is inevitable.
3. **A corner is a system, not a mechanism.** The Sybil corner is held only when the
   bond **and** the real-content standing path **and** the DHT (key-surround/eclipse)
   **and** every audit press are all sound (research Memos 0+3+8). We fixed the bond
   and read "still broken."
4. **We accrete mechanisms without a threat→mechanism→default→test map.** Nothing said
   "Sybil has N surfaces; you hardened one." §4 is that map; keep it current.
5. **Some tenets are undecided research problems dressed as engineering TODOs** (S7
   durability economics; access-unsurveilled at the blob layer; a non-globality metric
   for takedown). These need **product decisions**, not patches — see §6.

---

## 2. The two invariants (enforce these, don't re-patch instances)

**Invariant A — no standing without a verified, identity-bound, unpredictable-challenge,
deduped, bond-gated proof.** Every path that converts a resource claim into standing/
reputation/consensus weight MUST:
- bind the proof to the *prover's* identity (a proof for A must not verify for B);
- draw its challenge from a value the prover cannot predict or pre-fetch against
  (not a public monotonic counter);
- dedup the underlying resource (one resource credits ≤ 1 identity);
- gate credit behind the storage bond where the credit implies held space.

*Enforcement (H3, landed):* `core/credit/invariant_a_test.go` **enumerates every
standing-granting press** — a reflection guard over every `*Ledger` method forces each to
be classified `mints`/`reduces`/`neutral`, and a behavioral guard proves no non-`mints`
press lifts a bondless identity, so a new press cannot ship unaudited. The sole `mints`
press (the bond) is asserted identity-bound + deduped + bond-gated. Bond audit ✓ (post-G2).
PoR audit ✓ (grants no standing, H1).

**Invariant B — a mechanism is not "shipped" until its safe configuration is the DEFAULT
for the untrusted posture.** *Enforcement (H3, landed):* `cmd/silt/invariant_b_test.go`
builds the **default** untrusted-validator config and asserts it denies the attack, per
mechanism (S1 anti-release floor on; S3 bond-TTL on — post-H2). This was the 3rd time
off-by-default bit us; the harness makes a new off-by-default mechanism fail loudly.

---

## 3. Cross-corner couplings (why single-corner fixes are unsafe)

- **bond-TTL (safety) ↔ bond renewal (liveness). RESOLVED (H2).** Defaulting `-bond-ttl`
  on naively was a **liveness trap**: renewal happened only when a validator *proposes*
  (`core/node/chainrole.go`), so an attest-only validator never renewed and lapsed. H2
  added the **non-proposer renewal path** (`node.SubmitBondRenewal` → `MsgSubmitBondReg`
  → the next proposer folds queued peer regs into its block; the chain already verifies
  each reg against its own identity, so no proposer-owns-its-regs assumption existed to
  break). Only *then* was `-bond-ttl` made safe-by-default on the untrusted objective
  posture (`effectiveBondTTL`, mirroring the floor). The sim
  `TestObjectiveBondRenewalSustainsAttestOnlyValidator` proves an attest-only validator
  sustains standing across many TTL windows while a released one is pruned.
- **bond ↔ consensus weight ↔ eclipse.** A perfect bond is worthless if a ~$4 key-
  surround suppresses provider records (Memo 08), and consensus safety comes from
  *quorum arithmetic at the Byzantine threshold*, not reputation weight (Memo 05) —
  "reputation, unlike a bond, is not destroyed by detection: accountability's evidence
  without its teeth."
- **content-standing ↔ durability (S7).** Memo 03's endgame (seal real shards into
  identity-bound replicas) is the *same* mechanism that funds S7 repair — one resource
  pricing Sybils and funding durability. Fixing them separately is wasted motion, and
  it is the principled home for retiring the second (PoR-audit) standing press.

---

## 4. The standing / influence surface map

Every way to convert a resource claim into influence (standing, consensus weight,
censorship). **Verdict** = held / broken / residual / undecided as of this writing.

| # | Surface | Threat | Mechanism (code) | Shipped default | Inv A | Inv B | Verdict |
|---|---|---|---|---|---|---|---|
| S1 | Storage bond → objective weight + rep | Sybil: N ids < N storage | v3 labeling PoS (G2) + per-root dedup (F1) + anti-release floor (G4) + read-bound VDF (F2) | floor auto-on ✓ | ✓ (sha256(PK)==id, labels from H(pk,n)) | partial | **held** (plot primitive; awaiting external re-verify) |
| S2 | PoR audit | proof outsourcing/relay (RT-1) | **H1 (landed):** PoR grants **no standing** (removed the `auditsPassed·25` mint — standing = bond only) + challenge is **identity-bound** (`porProverSeed = H(base‖proverID)`); audits fund balance/durability + a negative integrity signal only (`core/credit` `Reputation`, `core/node/por.go`) | grants no standing (n/a) | ✓ (bond-gated; identity-bound) | n/a | **hardened (H1)** — residual colluding-holder recompute re-priced, not closed → sealed replicas (H7) |
| S3 | Objective bond weight across TIME | release-and-coast amortization (RT-2) | `BondTTLBlocks` re-challenge decay (G4) + **non-proposer renewal path (H2)** + `effectiveBondTTL` default-on | **`-bond-ttl` derived-on for untrusted objective** ✓ | ✓ | ✓ | **held (H2)** — attest-only validators renew without proposing; released bond decays |
| S4 | Consensus quorum (propose/attest) | honest-majority false; shed metric Sybil-inflatable; rep not slashable | rep-weighted quorum, MinProposer/AttesterRep, training wheels + `Mature()` (`core/chain`) | quorum not sized at BFT threshold; shed on head-count | — | — | **weak (Memo 05)**: size quorum at 2f+1/3f+1; shed on cost-to-corrupt over bond-distinct operators |
| S5 | DHT routing / provider records | ~$4 key-surround suppresses discovery (B2/B4/J1) | plain Kademlia, NodeID=H(pk), free minting, no IP diversity (`core/dht`) | no S/Kad disjoint paths, no IP-diversity buckets, no wide-region | — | — | **open (Memo 08)** — adopt-now stack re-prices by orders of magnitude |
| S6 | Takedown / revocation | global switch | per-operator opt-in, quorum-gated, existence-checked, un-revocable (`core/chain`, `core/denylist`) | honor-revocations off ✓ | — | ✓ | **held** (red team DENIED); missing CT-log + non-globality metric (Memo 04) |
| S7 | Publisher privacy / issuance | deanonymize publisher | publisher-less ledger + ephemeral publish + Chaumian credits (`core/blindtoken`) | publisher-less default ✓ | — | ✓ | **held for shipped layers**; D3 IP+timing **residual (undecided, Memo 01)** |
| S8 | Convergent-encryption existence oracle | membership oracle for guessable data | convergent `add` default (`core/crypto`) | **convergent is default** | — | ✗ | **weak (Memo 02)** — flip default to `private` + add Proof-of-Ownership |

---

## 5. Durability & the research frontier (not bugs — decisions)

- **S7 durability economics (existential, Memo 07).** No deployed system funds
  cold-data repair token-less **and** center-less; it has **no existence proof
  anywhere**, and our own review flags it possibly-fatal. Every survivor uses a crutch
  we forbid (Storj's central Satellite — *now bankrupt*; Sia's online client;
  Filecoin's token subsidy; Arweave's endowment). Only center-less path found: relax
  "no token" to **"no *speculative external* token,"** keep an internal credit reserve,
  and invent **center-less proof-of-repair** (a primitive that does not exist). **This
  gates whether files exist in 3 years and is the top open decision (§6).**
- **Erasure repair bandwidth (Memo 06).** Engineering ladder, well-charted: instrument
  first → lazy-repair → piggybacking (~35% free). Not a research blocker.

---

## 6. Open decisions (owner: Andrew) — these change what we build

- **D-S7 — relax "no token"?** Adopt "no *speculative external* token" + internal
  credit reserve + proof-of-repair? **If yes**, S7 becomes the spine and we prototype
  proof-of-repair first. **If no**, the evidence says S7 is likely unsolvable and we
  should say so in the tenets. *(Status: OPEN — blocks the durability track.)*
- **D-PRIV — access-unsurveilled as a documented product tradeoff.** The anonymity
  trilemma is a hard wall (Memo 01): achievable at the *metadata* layer (private DHT +
  mixnet + unlinkable tokens), **not** the blob layer. Immutable #4 must become a
  stated tradeoff, and we must decide whether to ship the D3 issuance mixing.
  *(Status: OPEN.)*
- **D-TAKEDOWN — pursue a formal non-globality metric?** Memo 04's distinctive
  "prove-a-negative" contribution; scope decision. *(Status: OPEN, low urgency.)*

---

## 7. Prioritized backlog (with exit criteria)

Exit criteria assume the build-immutable bar: **unit + integration + e2e**, red-team
PoC inverted as a regression, `go vet`/`-race` clean, docs/CHANGELOG reconciled. Every
security item must also add its **Invariant B default-denies-attack test**.

### P0 — live, exploitable, closes the Sybil corner (do first)

- **H1 — Bind the PoR audit to prover identity (RT-1, Critical). Invariant A. ✅ DONE
  (PR #168).** The decisive fix was architectural: **PoR audits grant no Sybil-resistant
  standing** — removed the `auditsPassed·25` mint from `credit.Reputation`, so standing
  rests on the bond press alone (plain PoR over shared content is not Sybil-resistant,
  memo 03). Plus identity-binding: the challenge seed is now `H(base‖proverID)`
  (`porProverSeed`), so a relayed proof for A fails B's verify. This also removes the
  denylist/curation weight a bondless farm got (that reads `Reputation`).
  *Delivered exit criteria:* `core/credit/redteam_rt1_test.go` (audit passes grant 0
  standing without a bond; **Invariant-A property test** — no positive standing without a
  bond; failed audits still penalize); `core/node/redteam_rt1_test.go` (relayed proof
  fails under prover binding); `sim/por_standing_test.go` (a holder passing audits over
  the wire earns 0 standing without a bond).
  *Residual (tracked → H7):* a **colluding bonded holder** can still recompute a fresh
  proof per Sybil to farm *balance* (not standing); closing that needs sealed replicas.
  *Follow-up hardening (not blocking):* draw the challenge base seed from unpredictable
  recent chain state (not `n.rid`); add chunk-owner dedup to the balance credit.

- **H2 — Non-proposer bond renewal path, THEN default `-bond-ttl` on (RT-2, High).
  Invariant B (and unblocks §3 coupling). ✅ DONE.**
  Added the renewal path: `node.SubmitBondRenewal` broadcasts a fresh self-signed
  `BondReg` (new `MsgSubmitBondReg`); a receiver queues it (`pendingBondRegs`, keyed by
  validator, latest wins) after re-verifying it for the current head
  (`chain.ValidateBondReg`); `proposeBlock` folds the queued peer regs — deterministically
  sorted, re-filtered for the head so one stale reg can't poison the block — into the next
  block, exactly mirroring `pendingSlashes`. The chain already verified each reg against
  its *own* identity (`validateBondReg`), so no proposer-owns-its-regs assumption blocked
  peer inclusion. The chain-sync sweep (`chainSyncTick`) drives renewal, so an attest-only
  validator renews without proposing. Only then `DerivedBondTTL` + `effectiveBondTTL`
  auto-enable on the untrusted objective posture (mirrors the floor; explicit `-bond-ttl 0`
  is the trusted opt-out).
  *Delivered exit criteria:* sim `TestObjectiveBondRenewalSustainsAttestOnlyValidator`
  (attest-only validator sustains standing across many TTL windows via the wire renewal
  path — no liveness regression — while a released validator is pruned); inverted PoC
  `core/node/redteam_rt2_test.go::TestRedteamRT2_ReleaseAndCoast` (TTL off ⇒ coast
  survives, the vuln; TTL on ⇒ released plot decays out); Invariant-B test
  `cmd/silt/invariant_b_test.go::TestInvariantB_S3_BondTTLPrunesReleasedBondByDefault`
  (+ `TestBondTTLDefaultsOnForUntrustedValidator`) asserts the untrusted default turns the
  TTL on.

### P1 — the systemic guardrails (do alongside P0 so P0 lands against the system)

- **H3 — Write the enumerate-all-standing-presses Invariant-A test + the
  default-denies-attack Invariant-B harness. ✅ DONE.** `core/credit/invariant_a_test.go`
  (reflection guard forces every `*Ledger` method to be classified; behavioral guard
  proves no non-`mints` press lifts a bondless identity; the sole `mints` press asserted
  identity-bound/deduped/bond-gated — covers S1 grant + S2 no-grant) and
  `cmd/silt/invariant_b_test.go` (default untrusted-validator config denies the attack —
  S1 floor on, S3 TTL on post-H2; S2 denied structurally by Invariant A). Both fail loudly
  if a new press skips classification or a mechanism ships off-by-default.

- **H4 — Consensus quorum sizing + shed metric (Memo 05).** Size `Quorum` at the
  Byzantine threshold (2f+1 of 3f+1); replace the head-count shed trigger with
  cost-to-corrupt / Nakamoto-over-**bond-distinct-operators**, Byzantine-robustly
  sampled; keep a post-shed escape hatch. *Exit:* a sim proving two quorums always
  intersect above the fault bound; a shed-metric test showing one operator with N keys
  cannot trip the wheels off.

### P2 — re-price the surfaces research says are open

- **H5 — DHT eclipse "adopt-now" stack (Memo 08).** S/Kademlia disjoint-path lookups
  (d=4–8) + explicit sibling list; IP/prefix-diversity buckets (/24, per-AS caps);
  wide-region announce/replication near a key; self-certifying (signed) provider
  records. *Exit:* a sim/integration test that a key stays discoverable while an
  adversary holds the k-closest NodeIDs but lacks /24 diversity; provider records
  cannot be silently forged.
- **H6 — Convergent-encryption default (Memo 02).** Flip the default to `private`;
  make convergent opt-in-by-signal; add storage-plane Proof-of-Ownership. *Exit:* a
  guessable-plaintext existence-oracle test fails against the new default; a PoW test
  gates dedup-credit/serve.

### P3 — the research-frontier tracks (gated on §6 decisions)

- **H7 — S7 durability (gated on D-S7).** Prototype **center-less proof-of-repair**
  first (regenerate-and-prove a coded shard, verifiable by a quorum that cannot read
  plaintext), then durability escrow + rarest-shard bounty. *Exit:* a proof-of-repair
  primitive with an inverted PoC (a caretaker claiming a bounty without doing the
  regeneration is slashed).
- **H8 — Privacy metadata layer (gated on D-PRIV).** Mixnet transport over the fetch
  path + PIR private DHT lookup + unlinkable retrieval tokens; document the trilemma
  tradeoff. 
- **H9 — Takedown transparency (gated on D-TAKEDOWN).** CT-style append-only revocation
  log + subscribable labelers + a formal non-globality metric.

---

## 8. Handoff notes for the next session

- **Read order:** this doc → [m0-sybil-rebind.md](m0-sybil-rebind.md) (G2 as-built) →
  the two red-team reports under `silt-reviews/redteam/m0-field-test/` → the research
  synthesis `silt-reviews/research/research-outcome/SILT-RESEARCH-LENS-SYNTHESIS.md`.
- **The field test also flags a NON-security gap:** the §6 D2 *adversarial* consensus
  sub-suite (equivocation-slash / partition-heal / low-bond-reject / forged-block) is
  not yet run over a real wire — `M0-FIELD-TEST-REPORT.md` §11 is a step-by-step build
  guide (needs a gated `-adversary` test-only daemon flag). Convergence/fault/restart
  (liveness) already PASS on a real 5-container wire.
- **Don't repeat the traps:** the H3 harnesses now enforce this structurally — a new
  standing press fails `core/credit/invariant_a_test.go` until classified, and an
  off-by-default mechanism fails `cmd/silt/invariant_b_test.go`. Still: never call a
  corner "held" because one surface is hardened — check §4; every new mechanism ships
  with its Invariant-B default-denies-attack test (add a row to the harness).
- **State after H1+H2+H3:** the Sybil corner's standing surfaces S1/S2/S3 are all held
  with tests + inverted PoCs + Invariant-A/B guardrails. **Next open backlog:** P1 **H4**
  (consensus quorum sizing at the Byzantine threshold + a Sybil-robust shed metric, Memo
  05 — S4 is still `weak`), then P2 **H5** (DHT eclipse, S5 `open`) / **H6** (convergent-
  encryption default, S8 `weak`). P3 (H7–H9) stays gated on the §6 decisions (D-S7 top).
