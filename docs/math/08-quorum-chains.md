# Quorum Chains: agreement without mining

## What a blockchain actually is

Strip away the coins and the hype and a blockchain is one data
structure plus one social rule:

- **The structure**: each block contains the hash of the previous
  block. That's it. The consequence is enormous: tampering with any
  historical block changes its hash, which breaks the next block's
  `Prev`, which changes ITS hash, and so on to the tip. History is a
  chain of commitments; you cannot edit the middle without forging
  everything after it. (This is the Merkle idea from note 01, bent
  from a tree into a line through time.)
- **The rule**: who gets to append? Everything interesting about a
  blockchain lives in this one question.

## Why Silt doesn't mine

Bitcoin's answer is proof-of-work: appending costs electricity, and
history is whichever chain burned the most of it. That is a marvel of
adversarial engineering — and wildly wrong for a storage registry. PoW
buys permissionless global consensus at the price of enormous waste and
probabilistic finality; Silt's registry needs cheap, immediate
finality among parties who already maintain measurable relationships.

Because Silt nodes already EARN measurable reputations — passed
storage audits (note 05), attached to unforgeable key-hashed
identities — the network has something Bitcoin's anonymous miners never
had: a native notion of standing. Crucially, that standing now **costs
challenged, held storage**: a validator seals an identity-bound storage
bond, and validators challenge each other's bonds over the wire,
verifying against only the committed Merkle root. Serving alone no
longer buys standing (two colluding nodes could wash-mint served-bytes
for free); standing is bond-gated, must be *sustained* (it decays if a
bond stops being re-proven), so N Sybil identities cost N distinct bonds
on N disks. So the rule is reputation-weighted quorum:

```
a block commits iff:
  reputation(proposer) ≥ MinProposerRep
  and ≥ Quorum DISTINCT attesters, each with reputation ≥ MinAttesterRep,
      none of them the proposer, signed the block hash
```

An attestation is an Ed25519 signature over the block hash, and the
hash covers height, ancestry, entries, and proposer — so a signature
endorses the block's exact content AND its exact place in history.
Forge any byte and every signature dies at once (the tamper test in
`chain_test.go` is one line for this reason).

## Why the quorum math holds up

- **You can't manufacture standing.** Reputation attaches to
  NodeID = SHA-256(pubkey). A Sybil flood of fresh keys is a flood of
  zeros: each new identity must seal and sustain a challenged storage
  bond (which costs real, held storage on a real disk) before its
  signature counts. The rate limit on writing history is the rate at
  which strangers can earn trust — exactly Andrew's rule that "no single
  node agrees until reputation is highly established."
- **Validators don't trust each other's arithmetic.** Every replica
  re-validates everything: latecomers syncing the chain re-check every
  signature, reputation, and quorum themselves. A lying peer can waste
  your bandwidth but cannot feed you an invalid history.
- **Each validator judges by its OWN ledger.** Reputation isn't a
  global number anyone asserts; it's what each validator has locally
  observed (audits it ran, serving it saw). Lying to one validator
  buys nothing with the others — a proposal needs a quorum of
  independently-convinced judges.

## The honest fine print

This is a quorum chain for a network with an honest validator
majority — not Byzantine fork-choice consensus. v1 has no
reorganizations: the first valid block at a height wins, and
simultaneous proposals race (one gets `ErrWrongParent` and retries on
the new head). A colluding quorum of high-reputation validators could
write bad entries — the design bets that entities who spent months
earning bond-backed reputations have more to lose than to gain, the
same wager proof-of-stake makes with capital. What the chain buys over
a hosted registry is concrete: no single owner, replicated history,
tamper-evidence, and gatekeeping by earned standing rather than by
whoever runs the server.

One more piece of honest fine print: the bond that backs standing is a
*first cut*. The bond is currently held in RAM and its seal is
proof-of-*space*-lite — not yet memory-hard, so it is a labeled
placeholder, not the finished cost function. Hardening it (a
memory-hard seal, disk-persisted bonds) is a V1 target; the interface
and the outcome — standing costs sustained, challenged storage — are in
place today.

Code: `core/chain` (blocks, rules), `core/node/chainrole.go` (propose /
attest / commit / sync), `credit.Reputation` (the standing formula),
`core/bond` (the challenged storage bond that gates standing).
Run `silt sim run consensus` for the deterministic version, or three
`silt daemon -validator` processes for the real one.
