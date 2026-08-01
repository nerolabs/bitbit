# silt — Tenets

> Status: **canon.** Ratified 2026-08-01 (#54); amended 2026-08-02 to add the
> mission-immutable (M0), a three-tier structure (immutable / tenet / evolving),
> and the build principle B8. Changing an **Immutable** (Part 0 + the six in
> Part IX) requires deliberate, reviewed consensus and is close to redefining
> the project. **Tenets** are canon too, amendable with reviewed consensus and
> evidence. **Evolving** parameters are expected to change as we learn.
>
> Format: each tenet is a **stance**, stated plainly. Personas are defined by
> their **desired outcomes** (what "good" looks like from their seat) and the
> **promise** silt makes to them. Where two personas' outcomes collide, the
> tenet is our **stance on the tradeoff**.

---

## Part 0 — Why silt exists (the mission-immutable)

**M0 — silt exists to *hold* the privacy × accountability × Sybil trilemma —
to refuse to trade any corner away.** Three properties every prior system in
this space has been forced to trade off against each other, held together
without sacrificing one for the other two:

- **Privacy** — publishing is *unlinkable* to a durable identity; who fetches
  what is *unsurveilled*.
- **Accountability** — genuinely harmful content can be removed, curators are
  themselves accountable, and takedown is **pluralistic** — never a global
  switch.
- **Sybil-resistance** — standing cannot be cheaply forged; no actor can spin up
  identities to capture consensus, wash reputation, or flood a denylist.

The incumbents each pick two corners and surrender the third. **silt's reason to
exist is to hold all three** — and **abandoning any corner is abandoning the
project.**

**This is "hold," not "resolve."** It is not a claim to have *solved* a research
problem all at once; it is the refusal to trade a corner away, and a design in
which the corners *co-mature* rather than arriving finished. Precisely:

- **Privacy is architectural from day one** — convergent encryption, opaque
  ciphertext, unsurveilled access, blind-signed publish tokens. It does not wait
  for the network to grow.
- **Accountability is content-level and reactive from day one** — takedown acts
  on a *hash*, pluralistically and after the fact; it is never identity-level,
  never pre-emptive, never a global switch.
- **Sybil-resistance is the corner that bootstraps.** It is the *weakest on a
  young network* — a small network is cheapest to flood — and it *strengthens as
  real, sustained work accrues*. During the launch window, anchor validators are
  explicit, time-boxed training wheels that shed on measured decentralization
  (see immutable #3: *no permanent center*). This is the live edge where the
  novel contribution concentrates.

**M0 is falsifiable, not a slogan.** It is *held* if and only if the adversarial
red-team suite (V3), written by a party *other than the author* (see B8),
**denies all three failure modes**: publish→identity linkage (privacy),
identity-level or global takedown (accountability), and cheap Sybil-farm
standing (Sybil-resistance). "Did we hold M0?" therefore has a yes/no answer, on
the board, that an outsider can check — not a victory declared by the builder.

Everything below is either a corner made structural, or the discipline that
keeps the composition honest.

**The bet, stated without a slogan.** Two of the three edges are dissolved by
architecture silt already has: privacy-vs-accountability dissolves because we
act on **content, not identity** (deny a *hash*; hosts are content-blind and
carry no liability for what they cannot read). The live edge — where the novel
contribution concentrates — is **privacy vs. Sybil**: we **decouple the cost of
*creating* an identity from the cost of *having standing***. Identity is free
and pseudonymous; *influence* costs sustained, challenged, real work; and the
publishing act stays cryptographically **unlinkable** from that bonded identity.
The load-bearing, field-defining claim is therefore:

> **Token-less, work-backed, identity-bound reputation that publishing stays
> cryptographically unlinkable from** — cheap for one honest node, ruinous for a
> Sybil farm, with no coin and no capital lockup.

This is not a labeled placeholder for V1 (see Part IX): it *is* the mission, so
it is the one mechanism deliberately pulled into V1 scope, and it must ship
**specified and adversarially proven**, not asserted.

---

## Part I — What silt is

**T0 — Two planes, one substrate.** silt is *BitTorrent + BitCoin*: a
**storage plane** (content-addressed, erasure-coded, peer-served chunks with
NAT-traversal) and a **trust plane** (consensus-secured registry, reputation,
and revocation). The storage plane stands alone and is the default; the trust
plane is opt-in and secures governance. Neither is the product — silt is the
**substrate** other things are built on, and the trust plane is where M0 lives.

**T1 — Capabilities, not infrastructure.** Every role (store, relay, registry,
validate, caretake) is a *capability any node can offer*, never a special node
baked into the binary. No node is *permanently* load-bearing; none is
irreplaceable. A young network may lean on explicit, time-boxed scaffolding
(launch-window anchors), but that scaffolding is designed to shed on measured
decentralization — the forbidden thing is a *standing* dependency on any one
node, not the honest scaffolding that retires itself (immutable #3).

**T2 — The link is the primitive.** A `silt:` link is the whole product surface
for a user: content-addressed identity + the key to read it. Durability,
placement, and repair are the network's job, not the holder's.

**T3 — The naming boundary is immutable (Aslan).** Core resolves *hashes*,
never *names*. Turning an opaque root into human meaning (names, descriptions,
moderation, curation) is a separate resolver layer ("Aslan"), and Silt core
**carries zero meaning, forever**. This is a liability firewall as much as an
architecture: the moment core learned to resolve names it would inherit every
takedown, copyright, and safety obligation it exists to shed. Any change that
teaches core about meaning is a top-severity regression. A link guarantees the
*bytes it names*; trusting a *name* is trusting whatever resolver you asked —
like trusting a DNS provider. "File poisoning" (a name resolving to hostile
content) is therefore a resolver-layer concern by design.

**silt is use-agnostic.** Core carries zero meaning, and silt takes zero
position on *use*. We do not enumerate, endorse, or concern ourselves with what
flows through it — file-sharing, archival, a library of record, or anything
else users choose is unenumerated and not silt's business. silt's only ambition
is to be the most trusted, private, secure, scalable, and efficient DFS ever
built, chosen for its feature set. **"Aslan" names any client or application
built *on top of* silt** — the application layer, expected to be richly diverse,
is where use lives; silt below it neither knows nor cares. We expect Aslan use
cases to drive interest and grow the network, and we care about none of them
beyond ordinary DFS use.

---

## Part II — How we build

**B1 — Hexagonal core.** Domain logic is pure and portable; all I/O (disk,
network, clock) lives in adapters at the edge. The seam is where a real
implementation swaps in for a simulated one with zero core changes.

**B2 — Lock-free, single-loop core.** Node logic runs on one serialized loop:
no locks, no goroutines in the core. This is what makes the simulator
deterministic and the real network inherit the same guarantee.

**B3 — Content-addressed, and never trusted blindly.** Identity is the hash of
the bytes. Every read re-verifies against its hash — *disks lie, networks lie*.
Convergent encryption makes identical content converge to identical identity
(dedup), and the publisher's identity is metadata, not part of what content
*is*.

**B4 — Privacy by construction.** Hosts store opaque ciphertext they cannot
read and did not choose by content. This is both a user promise (privacy) and
an operator promise (no liability for the unknowable).

**B5 — Legibility.** Code reads like the code around it; a behavior narrates
itself (the `-log info` path exists so the field can see the normal path, not
just failures). We optimize for the next reader.

**B6 — Reactive, not eager.** Data moves when it must (repair on loss, fan-out
on heat), not on a schedule. Idle is cheap; the system quiesces.

**B7 — Trust but verify; no optimistic operations.** B3 generalized from reads
to *every* operation: an operation is not "done" until its effect is
**confirmed**, never assumed. A write is durable only when read back or
acknowledged by ≥k independent parties; a placement is complete only when
providers confirm they hold it; a publish returns a link only once the content
is provably retrievable. We take the **cynical** default — disks, networks,
peers, and *our own prior steps* lie until proven otherwise — so an optimistic
ack is a **defect, not an optimization** (learned the hard way — a publish once
returned a valid-looking link for content it never durably stored, #60). Where
verification genuinely conflicts with another tenet (e.g. latency/cost vs. S6,
or eagerness vs. B6), the exception is made **explicit and discussed**, never
taken silently.

**B8 — Best components, novel composition.** We never reinvent a primitive —
cryptography, transport, codec, hash. We adopt the single strongest *proven*
one and treat rolling our own as the amateur tell it is. Novelty is reserved
for the **composition and incentive design**, where the hard problems (M0)
actually live — and that novelty must be **specified and adversarially proven**
(a spec a skeptic can read and a red-team suite they can't break), never
hand-waved. **The adversary must be external.** Self-marked homework is not
adversarial proof: the attacks that certify a novel composition must be written
by a party *other than its author* — an independent audit, a public bounty, or a
separate red-team — because single-author crypto graded by its own author is the
exact failure mode this tenet exists to forbid. Boring parts; a novel car.
Novel-and-unproven is worse than me-too — it ships insecurity to the exact
audience we need to convince.

---

## Part III — What a successful outcome is (the bar)

**S1 — Integrity is non-negotiable; availability is engineered.** Bytes are
either bit-perfect or an explicit failure — **never silently wrong**. (Our
scaling test: 0 corruption across every fetch — that is the floor, not an
achievement.) Availability, by contrast, is a probability we raise with
redundancy, placement, and repair.

**S2 — Content outlives any node.** Durability comes from erasure coding ×
cross-node replication × failure-domain spread × an ongoing repair loop that
outruns failure — not from any node being reliable.

**S3 — No silent-loss shapes.** Any operation that can't complete must fail
*visibly and recoverably*. A publish that half-succeeds and strands content
with no retrievable link is a bug of the highest severity (learned the hard way
— #46).

**S4 — No single point of failure or control.** If one machine dying — or one
operator deciding — can take content down globally, we have failed a core
promise.

**S5 — Honest observability.** An operator can see what is *true*: real peers
vs. ghosts, actual redundancy, caretaker coverage, cache effectiveness. We do
not ship dashboards that flatter (the network-size estimate counting dead
ephemeral identities was a lie we had to fix — #43).

**S6 — Cheap to participate.** The bar for running *any* role is low enough
that a hobbyist can. Cost that scales unbounded with network activity is a
design defect, not a fact of life. *(This is also the constraint that makes M0's
Sybil corner hard: cheap for the honest, ruinous for the liar — without a
capital lockup that would price out the hobbyist.)*

**S7 — Durability must pay for itself.** The repair loop that keeps content
alive under churn must be **funded in equilibrium, not run on charity.**
Reputation buys *presence*; it does not, by itself, buy *sustained repair
bandwidth* when nodes are leaving faster than they arrive. An economy where
storing and repairing is a net cost with no matching reward decays to zero the
moment altruism runs out — this is the wound that killed Freenet and GNUnet, one
genre earlier. So durability is not "solved" until the caretaker who repairs a
stripe they neither own nor can read is **paid by the demand that content
serves** (ties to caretakers #44 and the economics gates). If we cannot state
the equilibrium in which repair funds itself, we have not decided durability —
and it is durability that decides whether files exist in three years, not
whether they exist today.

---

## Part IV — How we test

**V1 — Sim first, field second, both required.** Deterministic simulation
proves logic (including Byzantine and churn cases, reproducibly); a
multi-machine field test proves it against real NAT, real WAN, real timeouts.
A behavior is not "done" until it has cleared *both*.

**V2 — Conformance over trust.** Every implementation of a seam (e.g.
`ChunkStore`) passes one shared conformance suite, so "however many
implementations, one contract."

**V3 — Test the adversary.** We write the attacker's desired outcome as a test
that must fail for them: forgery, tamper, equivocation, freeload, Sybil,
censorship. Security is validated by denial, not assertion. For M0's novel
mechanisms this is the *primary* proof: the red-team suite is the deliverable,
and the M0 verdict is exactly its result (Part 0). The suite that *certifies*
M0 must be written by an **external** party (audit / bounty / independent
red-team, per B8) — we may write our own attacks to develop against, but the
proof that ships is the one an outsider could not break.

**V4 — Evidence, not vibes.** A change clears the success bar (Part III) with
observed evidence — a checksum match, a survived kill, a green Byzantine
suite — reported faithfully, including what was skipped.

---

## Part V — How we release

**R1 — Gated by proof.** An RC requires the plane's core promises
*field-proven*: cross-network publish/fetch, scaling under load, crash
recovery, and — because the trust plane is a V1 pillar — the M0 mechanisms
proven **multi-machine**, not sim-or-single-host only. Sim-proven-only work is
labeled as such and is not an RC gate on its own.

**R2 — Canon tracks behavior.** Changelog discipline; docs and these tenets are
updated in the same change that alters behavior. No silent drift between what
the system does and what we say it does.

**R3 — Throwaway stays throwaway.** Test/dev infrastructure (a dev relay, a
demo registry) is never allowed to quietly become load-bearing.

**R4 — Operator-autonomous updates, security-gated.** Operators control their
own software; the network **never silently auto-updates**. Only security uses
graduated enforcement, and the maintainers set the tier: **criticality-graded**
(Low = advisory · Medium = 30 days · High = 7 days · Critical = 24–48h before
patched peers refuse old versions), via **threshold-signed** (m-of-n),
**recallable** (monotonic sequence), **observation-clocked** version-floor
advisories that fail *open*. Critical is a gate of last resort. Whoever can
declare Critical can halt the network, so no single key may — the signing
threshold *is* the safety property.

---

## Part VI — The don'ts (bright lines)

1. **Never force a registry or relay operator to also store or serve content.**
2. **Never enable silent or global censorship.** Takedown is transparent,
   consensual, and pluralistic — never a single kill switch.
3. **Never surveil access.** Who fetches what is not observable.
4. **Never a silent-loss failure shape** (see S3).
5. **Never bake in a special or central node** (see T1).
6. **Never trust bytes without verifying** (see B3).
7. **Never let the economy reward useless or harmful work** — reward tracks
   value delivered to the layer above.

---

## Part VII — Per-persona tenets (outcomes → promise)

### A. Value personas

**1. Content consumers (clients — "Aslan users").**
- **Outcomes:** available when asked; authentic; private access; runs no
  infrastructure; a given link keeps working.
- **Promise:** a link is enough — retrieval, verification, and durability are
  the network's job, and no one can see what you fetched.

**2. Publishers / creators.**
- **Outcomes:** content stays available as long as intended, and they can *tell
  whether it will*; access control + revocation; integrity + attribution;
  cheap publishing; publish without being deanonymized; no silent disappearance.
- **Promise:** you can make content durable and know its durability; you can
  revoke; no one alters it under your name; your publish is unlinkable to your
  standing (M0); taking it down is visible, never silent.

**3. Link recipients (audience).**
- **Outcomes:** the link resolves; bytes are the intended ones; private stays
  private.
- **Promise:** a valid link is a cryptographic guarantee of *what* you'll get.

### B. Infrastructure personas

**4. Storage-node operators.**
- **Outcomes:** fair, predictable reward for disk + bandwidth; legal safety
  (host unknowable ciphertext; refuse lists they choose); free to come and go;
  reputation that accrues and ports.
- **Promise:** contribute capacity, earn proportional standing/credit, carry no
  liability for what you cannot inspect, and never be punished for churn.

**5. Registry operators.**
- **Outcomes:** a public good that *doesn't cost them* (minimal disk/bandwidth);
  never forced to store/serve; not a single point of failure or liability.
- **Promise:** running rendezvous is cheap enough to be near-free; the registry
  is a cache/accelerator over the DHT, so losing one costs latency, not
  availability. *(This is the crux of #47/#48.)*

**6. Relay operators.**
- **Outcomes:** content-blind (no liability); bounded, capped cost; rewarded/
  reputed for reachability.
- **Promise:** you forward ciphertext you can't read, within a cap you set, and
  can't be turned into anyone's free CDN.

**7. Caretakers (durability providers).**
- **Outcomes:** rewarded for keeping content alive; can repair content they
  neither own nor can decrypt; clear signal of what needs care and whether
  they're succeeding.
- **Promise:** repair rights are delegable and auditable; durability is a
  *service* a publisher can buy and a caretaker can sell. *(The gap in #44.)*

### C. Trust & governance personas

**8. Validators / consensus participants.**
- **Outcomes:** honest work grows their standing; Byzantine peers can't cheat
  them; influence is earned, not bought raw; low overhead.
- **Promise:** consensus is deterministic and reputation-gated; standing costs
  sustained, challenged real work (M0), not a stake or a coin; equivocation and
  forgery are detected and cost the actor.

**9. Suppression / takedown / kill-list curators.**
- **Outcomes:** their lists are honored by operators who *choose* to trust them
  (voluntary, pluralistic); real teeth on genuinely harmful content; curation
  is trusted and auditable, with a false entry visible and reputation-costly;
  funded/rewarded for a public-safety service.
- **Promise:** you can publish a takedown list with *effect proportional to who
  trusts you* — never a global switch; accuracy is rewarded, over-reach is
  penalized, and every honored suppression is auditable.
- **Stance:** takedown is **consent-based and plural** — many competing lists,
  operators subscribe to those matching their jurisdiction and values. We build
  the mechanism for *transparent, chosen* suppression, and refuse to build a
  mechanism for *imposed, silent* suppression.

**10. Legal authorities / regulators.**
- **Outcomes:** a real path to remove illegal content within a jurisdiction,
  with accountability.
- **Promise:** jurisdiction-scoped takedown via operators and curators who
  honor it — and *no* lever for global censorship or user surveillance.

### D. Builder personas

**11. Developers / integrators.**
- **Outcomes:** stable, documented APIs/SDKs; the daemon-as-local-service is
  trivial to embed; the link is a clean primitive; predictable, composable
  behavior; deliver utility without running infrastructure.
- **Promise:** silt is a dependency you can reason about — documented seams,
  stable link format, a local API. *(This section is the website's on-ramp.)*

**12. Application / utility operators — the infra↔utility bridge.**
- **Outcomes:** rely on the substrate's guarantees (availability, integrity,
  privacy) to build higher-order products, reasoning clearly about what silt
  promises vs. what they must add.
- **Promise:** the substrate's guarantees are explicit and testable, so you know
  exactly which load you're carrying and which you're inheriting.

### E. Meta / systemic

**13. The commons (the network itself).**
- **Outcomes:** no single point of control/failure; self-healing under churn;
  economics where reward tracks useful work; resistant to Sybil, pollution, and
  DoS.

**14. Adversaries (anti-personas — outcomes we DENY).**
- Censor, surveil, forge, free-ride, Sybil the DHT, equivocate in consensus,
  exhaust resources, poison routing. **Each adversary outcome, inverted, is a
  security tenet.** We measure success by how impossible *or unrewarding* we
  make each.

---

## Part VIII — The value loop (how outcomes interlock)

The economy closes as a chain of served guarantees:

```
operators + relays + registries + caretakers  →  a substrate with guarantees
developers + application operators             →  turn guarantees into utility
publishers + consumers                         →  the demand that funds the loop
validators + curators + authorities            →  keep the loop honest and lawful
```

**The through-line:**
- Every persona's incentive is **satisfied by serving the persona above it** —
  reward flows down the stack from the value consumed at the top.
- **No persona's good may require another's harm.**
- Where outcomes genuinely tension, the tenet is our **explicit stance on the
  tradeoff**, held in the open:

| Tension | Our stance |
|---|---|
| Publisher permanence vs. takedown | Permanence by default; takedown only via *chosen, transparent* lists — never silent or global. |
| Consumer privacy vs. legal accountability | Privacy of *access* is absolute; accountability acts on *content* (jurisdiction-scoped), not on who read it. |
| Privacy vs. Sybil-resistance (the M0 edge) | Identity is free and private; *standing* costs sustained challenged work; publishing stays unlinkable from standing. We buy Sybil-resistance with *proven work*, never with a coin, a stake, or deanonymization. |
| Operator freedom vs. availability | Operators are free to leave; availability is the network's job (repair), not a shackle on any operator. |
| Decentralization vs. usability | Rendezvous may be centralized *for convenience* but never *load-bearing*; the decentralized path must always exist. |
| Openness vs. abuse resistance | Open to participate, but reward is gated on delivered value and reputation, so abuse doesn't pay. |

---

## Part IX — The three tiers (immutable / tenet / evolving)

**Immutable — amending is close to redefining the project; requires deliberate,
reviewed consensus.**

- **M0 — the mission:** *hold* the privacy × accountability × Sybil trilemma —
  refuse to trade any corner away; abandoning any corner abandons the project.
  Held **iff** the external V3 red-team suite denies all three failure modes
  (Part 0). Not a victory claim — a refusal, bound to a falsifiable test.
- The six corners made structural:
  1. **Content-blind by construction** (B4) — hosts store ciphertext they cannot
     read or choose.
  2. **The bytes are the truth** (S1/B3) — content-addressed, re-verified,
     bit-perfect or an explicit failure; never silently wrong.
  3. **No *permanent* center** (T1/S4) — nothing *permanently* load-bearing; no
     machine or operator can take content down globally. Bootstrap
     centralization is allowed but must be **explicit, time-boxed, and shed on
     measured decentralization**: the launch-window anchor validators are
     training wheels, not a center, and the decentralized path exists from day
     one. What is forbidden is a *standing* dependency on any node — not the
     honest admission that a young network leans on scaffolding while it matures.
  4. **Access is unsurveilled** (Don't #3) — who fetches what is never
     observable.
  5. **No silent or global censorship** (Don't #2) — takedown is transparent,
     consensual, and plural; never one switch.
  6. **Core carries zero meaning, forever** (T3) — the Aslan boundary.

**Tenets — canon, amendable with reviewed consensus and evidence.** Everything
else in Parts I–VIII, including the strong disciplines we hold nearly as firmly
as the immutables but that describe *how we build and govern* rather than *what
silt is*: trust-but-verify / no-optimistic-operations (B7), best-components-
novel-composition (B8), reward-tracks-value (Don't #7), operator-autonomous-
security-gated-updates (R4), and the per-persona promises and tradeoff stances
(Parts VII–VIII).

**Evolving — expected to change as we learn.** Specific algorithms and
parameters (erasure k/n, replication factor, cache policy, DHT constants), the
*exact* economic mechanism, and which roles ship first.

> **The principle-not-mechanism rule, and its one exception.** A tenet gates V1
> as a *principle*, never as a *mechanism* — "reward tracks value" is canon, but
> *which* economic mechanism satisfies it, and *when*, is a roadmap call (see
> `ROADMAP.md`). The **single deliberate exception** is M0: the trilemma
> resolution — token-less, work-backed, unlinkable reputation — is not a
> feature that satisfies a principle, it *is* the reason silt exists, so its
> real (not placeholder) mechanism is pulled into V1 by definition. Decided
> 2026-07-31 (trust plane is a V1 pillar) and sharpened 2026-08-02 (hold the
> real-crypto bar; build the car from best-in-class components, prove it
> multi-machine). Publisher privacy (F1) ships as **blind-signed publish
> tokens** (Chaumian) that unlink a publish from the durable reputation key.

---

## Amendment log

- **2026-08-01** — Ratified as canon (#54). Reviewer questions resolved:
  persona #1 is "Aslan users" (name kept); application-operator (#12) is a
  distinct seat from developer (#11); trust plane is a V1 pillar (Open Q#3
  closed).
- **2026-08-02** — Added **M0** (mission-immutable, the trilemma) as Part 0 and
  the supreme immutable; restructured Part IX into three tiers (immutable /
  tenet / evolving) with six mechanism-immutables; **moved B7, Don't #7, and R4
  out of the immutable set into the tenet tier** (they are disciplines/stances,
  not project identity); added **B8** (best components, novel composition);
  stated the load-bearing novel claim and the principle-not-mechanism exception
  explicitly.
- **2026-08-02 (intention review)** — Acted on the fresh-eyes *intent* review
  (`docs/reviews/fresh-eyes-2026-08-02-intention.md`). **Requalified M0** from
  "resolve the trilemma" to "*hold* it — refuse to trade any corner away," named
  which corner bootstraps (Sybil-resistance is weakest early and co-matures;
  privacy and accountability hold from day one), and **bound M0 to a falsifiable
  test** (held iff the external V3 red-team suite denies all three failure
  modes). **Reconciled "no center" with the training wheels**: immutable #3 and
  T1 now say "no *permanent* center — bootstrap scaffolding is explicit,
  time-boxed, and sheds on measured decentralization." **Added S7 — durability
  must pay for itself** (the repair loop funded in equilibrium, not charity; the
  Freenet/GNUnet wound). **Named the external adversary** in B8 and V3: the suite
  that certifies a novel composition (and M0) must be written by an outside party
  (audit / bounty / independent red-team), not self-graded.
