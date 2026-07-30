# silt — Tenets (DRAFT for review)

> Status: **first cut, not canon.** This is one author's exhaustive attempt
> to distill what we've learned into principles worth ratifying. Argue with
> every line. Once reviewed and agreed, sections graduate to **canon** and
> changing them requires a deliberate, reviewed amendment — not a drive-by.
>
> Format: each tenet is a **stance**, stated plainly. Personas are defined by
> their **desired outcomes** (what "good" looks like from their seat) and the
> **promise** silt makes to them. Where two personas' outcomes collide, the
> tenet is our **stance on the tradeoff** — that is the part worth arguing.

---

## Part I — What silt is

**T0 — Two planes, one substrate.** silt is *BitTorrent + BitCoin*: a
**storage plane** (content-addressed, erasure-coded, peer-served chunks with
NAT-traversal) and a **trust plane** (consensus-secured registry, reputation,
and revocation). The storage plane stands alone and is the default; the trust
plane is opt-in and secures governance. Neither is the product — silt is the
**substrate** other things are built on.

**T1 — Capabilities, not infrastructure.** Every role (store, relay, registry,
validate, caretake) is a *capability any node can offer*, never a special node
baked into the binary. No node is load-bearing; none is irreplaceable.

**T2 — The link is the primitive.** A `silt:` link is the whole product surface
for a user: content-addressed identity + the key to read it. Durability,
placement, and repair are the network's job, not the holder's.

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
design defect, not a fact of life.

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
censorship. Security is validated by denial, not assertion.

**V4 — Evidence, not vibes.** A change clears the success bar (Part III) with
observed evidence — a checksum match, a survived kill, a green Byzantine
suite — reported faithfully, including what was skipped.

---

## Part V — How we release

**R1 — Gated by proof.** An RC requires the plane's core promises
*field-proven*: cross-network publish/fetch, scaling under load, crash
recovery. Sim-proven-only work is labeled as such and is not an RC gate on its
own.

**R2 — Canon tracks behavior.** Changelog discipline; docs and these tenets are
updated in the same change that alters behavior. No silent drift between what
the system does and what we say it does.

**R3 — Throwaway stays throwaway.** Test/dev infrastructure (a dev relay, a
demo registry) is never allowed to quietly become load-bearing.

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

**1. Content consumers (clients).** *Working name "Aslan users" — confirm.*
- **Outcomes:** available when asked; authentic; private access; runs no
  infrastructure; a given link keeps working.
- **Promise:** a link is enough — retrieval, verification, and durability are
  the network's job, and no one can see what you fetched.

**2. Publishers / creators.**
- **Outcomes:** content stays available as long as intended, and they can *tell
  whether it will*; access control + revocation; integrity + attribution;
  cheap publishing; no silent disappearance.
- **Promise:** you can make content durable and know its durability; you can
  revoke; no one alters it under your name; taking it down is visible, never
  silent.

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
- **Promise:** consensus is deterministic and reputation-gated; equivocation
  and forgery are detected and cost the actor.

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

**The through-line (proposed canon):**
- Every persona's incentive is **satisfied by serving the persona above it** —
  reward flows down the stack from the value consumed at the top.
- **No persona's good may require another's harm.**
- Where outcomes genuinely tension, the tenet is our **explicit stance on the
  tradeoff**, held in the open:

| Tension | Our stance (draft) |
|---|---|
| Publisher permanence vs. takedown | Permanence by default; takedown only via *chosen, transparent* lists — never silent or global. |
| Consumer privacy vs. legal accountability | Privacy of *access* is absolute; accountability acts on *content* (jurisdiction-scoped), not on who read it. |
| Operator freedom vs. availability | Operators are free to leave; availability is the network's job (repair), not a shackle on any operator. |
| Decentralization vs. usability | Rendezvous may be centralized *for convenience* but never *load-bearing*; the decentralized path must always exist. |
| Openness vs. abuse resistance | Open to participate, but reward is gated on delivered value and reputation, so abuse doesn't pay. |

---

## Part IX — Immutable vs. evolving

**Immutable (amending requires deliberate, reviewed consensus):**
integrity-is-non-negotiable (S1), privacy-by-construction (B4), no-central-
control (T1/S4), no-silent-censorship (Don't #2), no-access-surveillance
(Don't #3), reward-tracks-value (Don't #7), trust-but-verify / no-optimistic-
operations (B7).

**Evolving (current best practice; expected to change with evidence):**
specific algorithms and parameters (erasure k/n, replication factor, cache
policy, DHT constants), the exact economic mechanism, which roles ship first,
and whether the trust plane is a v1 pillar or a later one.

---

## Open questions for the reviewer

1. Confirm/rename the **"Aslan"** persona (#1). What is it?
2. Is **application operator (#12)** a distinct seat from **developer (#11)**, or
   the same person wearing two hats?
3. Is the **trust plane required for v1**, or is a v1 storage-plane substrate
   legitimate on its own (with the chain as a fast-follow)?
4. Which **tensions in Part VIII** do you want to rule differently?
5. Anything in **Part IX** you'd move between immutable and evolving?
