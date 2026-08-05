# Bond audit — challenging held storage over the wire

> **Historical / first-cut design note (2026-08-01).** This is an early design
> note for the bond *primitive* alone. For the current as-built, read
> [`m0.md`](m0.md) §6 (S1 — the shipped surface) and
> [`m0-sybil-rebind.md`](m0-sybil-rebind.md) (the v3 bond as-built). The bond is
> **one axis (D — distinct sealed disk per identity) of M0's systemic
> composition**, not "the Sybil corner"; the systemic claim lives in `m0.md`, not
> in this one primitive.

**Status: design, 2026-08-01.** This is the wire protocol that turns the
`core/bond` primitive and `credit.RecordBondChallenge` (#78) into a live
mechanism: validators challenge *each other's* storage bonds over the network,
so consensus standing is continuously backed by real, held, identity-bound
storage — not self-reported serving. It is deliberately written before the
code because it adds wire surface, and the roadmap's rule is not to ossify the
wire on a live network.

This is **one axis (D — distinct sealed disk) of the M0 composition** (see
[`m0.md`](m0.md) §3); the systemic claim lives in `m0.md`, not in this one
primitive. The bond makes
standing *cost* something — token-less, work-backed, identity-bound — while
publishing stays unlinkable from standing (via the blind publish tokens,
F1/#84). The design here is a **first cut**: it proves the mechanism
end-to-end but leaves labeled residuals (below) that ROADMAP Gate 4 replaces
with the real, memory-hard, multi-machine-proven M0 composition — and that
composition ships only when an **external** red-team (audit / bounty /
independent red-team, per B8; the Gate 6 security review) cannot break it, not
on the author's own say-so.

## What exists (the shipped primitive, #78)

- `core/bond`: `Seal(id, size)` builds an identity-bound, Merkle-committed blob
  (proof-of-space-lite). `Answer(nonce)` returns the probed blocks + inclusion
  proofs; `Verify(root, size, nonce, answer)` checks them against **only the
  committed root** — no ground-truth fetch, so the verifier is cheap and the
  prover must hold the blocks.
- `credit`: `RecordBondChallenge(prover, provenBytes, passed, tick)` sets/zeros
  challenged-storage standing; `DecayStale(now, maxAge)` retires standing that
  stops being re-proven; `Reputation` is built on it. Promoted to the port.

What's missing: nobody *sends* a challenge, and no node *holds* its own bond.
This note fills both.

## The three wire additions

Kept minimal and modelled on the existing capacity/domain gossip so the
surface is small and the sim/TCP transports are byte-identical.

### 1. Bond gossip (no new message)

Every message already carries the sender's `CapUsed/CapTotal` and `Domain`
(stamped in `node.send`). Add two more fields:

- `BondRoot ports.Hash` — the sender's sealed bond commitment root (32 B).
- `BondSize int64` — the bond size in bytes (so a challenger can recompute the
  challenge indices).

A peer accumulates `peerBonds[nodeID] = {root, size}` from gossip, exactly like
`peerDomains`/`peerCaps`. Zero-overhead when unset (`BondRoot == 0` ⇒ the node
advertises no bond and is simply never bond-challenged). No announcement
round-trip: the root rides traffic the node already sends.

### 2. `MsgBondChallenge` — challenger → target

Reuses the existing `Message.Nonce`. The target's `(root, size)` is already
known from gossip, so **only the nonce travels**. A fresh nonce per challenge
(drawn from `n.rid`, like the PoR nonce) makes yesterday's answer worthless.

### 3. `MsgBondReply` — target → challenger

Carries `Encode(bond.Answer)` in the existing `Message.Data` (`[]byte`). The
answer is `Samples` (20) blocks of `BlockSize` (4 KiB) + their Merkle proofs —
a **bounded ~80 KiB**, so this is not an amplification vector (see limits). A
node that does not hold its bond replies empty ⇒ the challenger records a fail.

`bond.EncodeAnswer`/`DecodeAnswer` (CBOR, the dep the chain already uses) keep
the wire shape in the `bond` package next to `Answer`.

## The auditor loop (the primitive's deferred `DecayStale` call-site)

A validator runs a periodic sweep (`n.clock.AfterFunc(BondAuditInterval, …)`,
the pattern the caretaker `repairTick` already uses — there is no other
standing validator loop, which is why the primitive deferred the call-site
here):

```
bondAuditTick:
  now := n.clock.Now()
  for each peer in peerBonds (excluding self):
     nonce := n.rid++
     send MsgBondChallenge{Nonce: nonce} to peer
     on reply:
        ans := DecodeAnswer(reply.Data)
        ok  := bond.Verify(peer.root, peer.size, nonce, ans)
        ledger.RecordBondChallenge(peer, peer.size, ok, now)
  DecayStale(now, BondMaxAge)     // retire bonds that stopped answering
  reschedule after BondAuditInterval
```

Each validator challenges independently and records into its **own** ledger —
the same "judge by your own observations" property the chain already relies on,
now extended to storage. A Sybil must satisfy *many* independent challengers,
each of which it must actually answer (i.e. actually store the bond).

## The node's own bond

> **Superseded — historical.** The in-RAM, space-lite state described in this and
> the following section is a first-cut placeholder. It is **superseded by the v3
> rebind** ([`m0-sybil-rebind.md`](m0-sybil-rebind.md)): the disk-persisted,
> byte-bound depth-robust-graph plot shipped. The text below is kept for history,
> not as a description of current behavior.

At startup a validator seals its bond from its `NodeID` and a size (default =
its capacity pledge, or a `-bond` size) and **holds it**. Holding it on disk is
the real cost; a memory-hard seal makes recompute-on-demand more expensive than
a disk read.

- **First cut (this pass):** seal in memory at startup; enough to prove the
  *mechanism* (challenges flow, standing accrues, decay works) end-to-end. Both
  the bond and the issuer key are **in-RAM, not persisted across restart**, and
  the seal is the placeholder `sealBlock` (iterated SHA-256 — space-lite, **not**
  memory-hard). Proven in sim + e2e on a **single host only**; not yet
  multi-machine.
- **The V1 target (ROADMAP Gate 4 / tenet M0), not skipped:** Gate 4b replaces
  the space-lite `sealBlock` with a **genuine memory-hard / proof-of-space
  construction** (built from best-in-class *proven* components per B8 — novel
  only in the composition, never in the primitive); Gate 4d persists the bond in
  `capstore` so it occupies pledged disk and survives restart (the
  `diskproofs`/#69 pattern), and persists the issuer key; Gate 4e proves the
  whole trust plane **multi-machine** across real NAT (#52). Until that lands the
  Sybil cost is honestly *space-lite* and single-host, per the `core/bond` doc —
  this note describes a placeholder, not the final construction.

## Security properties & honest limits

- **Identity cost.** N identities need N distinct bonds; with disk persistence +
  a memory-hard seal (Gate 4b/4d), that is N × size of real disk. Today
  (in-memory, non-memory-hard, single-host) it is weaker and labeled so — the
  space↔compute tradeoff still lets an attacker recompute the placeholder blocks
  instead of storing them.
- **No amplification.** The reply is a fixed, small size regardless of bond
  size, and the transport is TLS-over-TCP (requester address proven). Still,
  cap **challenge rate per challenger** (A8/A13 class) so an authenticated peer
  can't pump challenges.
- **Sustained, not one-shot.** `DecayStale` means a validator must keep
  answering; buying standing once and coasting fails.
- **Not unique-storage.** Like the PoR note, this proves *possession of a
  distinct identity-bound blob*, not that it is a unique replica of user data.
  Real-content-as-standing (folding this into durability) is the post-V1 fork,
  deliberately deferred; synthetic bond is the cold-start.
- **Self-challenge excluded**; a node's own bond never scores it.

## Testing (outcome-first, three tiers)

- **unit:** `bond.Verify` (held passes / forged fails) [done]; `Answer`
  encode↔decode round-trips.
- **integration/sim:** validators challenge each other **through the node loop**
  (not seeded); a validator that holds its bond accrues standing and its block
  commits; one that stops answering decays below the bar and **loses its vote** —
  all asserted as outcomes, deterministic and seed-replayable.
- **e2e:** real daemons over TCP — a validator earns standing via bond
  challenges and commits a block; a fresh node is refused. This closes the
  primitive's missing e2e tier; the branch does not merge without it. Note this
  is **single-host** e2e; the multi-machine proof (real NAT/WAN) is Gate 4e
  (#52), not this pass.
