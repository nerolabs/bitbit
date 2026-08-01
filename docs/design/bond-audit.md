# Bond audit — challenging held storage over the wire (Trust roadmap T1b)

**Status: design, 2026-08-01.** This is the wire protocol that turns the
`core/bond` primitive and `credit.RecordBondChallenge` (T1a, #78) into a live
mechanism: validators challenge *each other's* storage bonds over the network,
so consensus standing is continuously backed by real, held, identity-bound
storage — not self-reported serving. It is deliberately written before the
code because it adds wire surface, and the roadmap's rule is not to ossify the
wire on a live network.

## What exists (T1a)

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

## The auditor loop (this is T1a's deferred `DecayStale` call-site)

A validator runs a periodic sweep (`n.clock.AfterFunc(BondAuditInterval, …)`,
the pattern the caretaker `repairTick` already uses — there is no other
standing validator loop, which is why T1a deferred the call-site here):

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

At startup a validator seals its bond from its `NodeID` and a size (default =
its capacity pledge, or a `-bond` size) and **holds it**. Holding it on disk is
the real cost; a memory-hard seal makes recompute-on-demand more expensive than
a disk read.

- **V1 (this pass):** seal in memory at startup; enough to prove the *mechanism*
  (challenges flow, standing accrues, decay works) end-to-end.
- **Hardening (recorded follow-up, not skipped):** persist the bond in
  `capstore` so it occupies pledged disk and survives restart (the
  `diskproofs`/#69 pattern), and swap `sealBlock` for a memory-hard function.
  Until then the Sybil cost is honestly *space-lite*, per the `core/bond` doc.

## Security properties & honest limits

- **Identity cost.** N identities need N distinct bonds; with disk persistence +
  a memory-hard seal, that is N × size of real disk. Today (in-memory,
  non-memory-hard) it is weaker and labeled so.
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
  challenges and commits a block; a fresh node is refused. This closes T1a's
  missing e2e tier; the branch does not merge without it.
