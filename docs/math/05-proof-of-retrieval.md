# Proof of Retrieval: making "I'm storing it" mean something

## The problem

The credit economy pays nodes for serving chunks. But serving is
observable only when someone happens to fetch. Storage — the promise
that the chunk will *be there* when needed — is invisible. A rational
cheater accepts every chunk placement, throws the bytes away, and
enjoys full provider status until the day someone actually asks. Our
audit scenario builds exactly this node (the *liar*) and then catches
it. Two ingredients:

## Ingredient 1: the Merkle proof — binding the claim

Every chunk is distributed together with its inclusion proof: the
O(log n) sibling path showing "this chunk is leaf i of root R" (see
[01-merkle-trees.md](01-merkle-trees.md)). So a host can always be
asked: *which* file's shard do you claim to hold, and can you prove
that shard belongs to that file? No proof, no standing — you can't
even pretend to store something that isn't part of a published root.

But here's the subtlety the liar exploits: **the proof is about the
hash, not the bytes.** A proof is small, and nothing stops a node from
keeping the proof and deleting the chunk. Membership ≠ possession.

## Ingredient 2: the nonce tag — binding the bytes

The challenge carries a fresh random nonce, and the answer must include

```
tag = SHA-256(nonce ‖ chunk bytes)
```

Three properties do all the work:

1. **Uncomputable without the data.** The tag is a function of every
   byte of the chunk. Keep the proof, lose the bytes, and the best you
   can do is guess a 256-bit value.
2. **Unreplayable.** A fresh nonce per challenge means yesterday's
   honest answer (or an answer overheard from an honest replica) is
   worthless today.
3. **Deterministic.** Every honest holder of the same chunk produces
   the *same* tag for the same nonce — answers can be compared.

Our liar answers challenges with a perfectly valid Merkle proof and a
tag it cannot make true. The audit slashes it into negative balance;
honest hosts earn audit rent from the identical challenge.

## The toy part (what the V1 target fixes)

Be honest about what the mechanism above *is*: a challenge-time
possession check. It proves the prover could produce the bytes at the
instant it was asked — nothing more. It does not prove the bytes are
stored *durably* (they could be fetched from a neighbour the moment the
nonce lands), and it does not prove *unique* storage (three names can
front one copy). It is a placeholder, useful for catching the outright
liar in the audit sim, not the real thing.

The V1 target (tenet **M0** — resolving the privacy × accountability ×
Sybil trilemma — and **ROADMAP** Gate 4) is a *real*
proof-of-retrieval/storage: a published, peer-reviewed scheme from the
compact-PoR / PDP / proof-of-space family, used as-is. Per build tenet
**B8** (best-in-class proven components, novelty only in composition),
silt does not invent the cryptography. What is novel is how a proven
scheme is *bound to identity and to time* — so that standing becomes
token-less, work-backed, and Sybil-hard (an identity's weight costs
real, sustained, challenged storage) while publishing stays unlinkable.
The real scheme does not exist in the code yet; this section frames it
as the V1 target, against which the mechanism above is the current toy.

To *grade* a tag today, our auditor fetches the chunk itself and
recomputes the truth. That works — content addressing makes ground
truth cheap to recognize — but it means auditing costs as much
bandwidth as downloading, which defeats the purpose at scale. The
published schemes exist precisely to delete that fetch:

- **Precomputed challenges**: at upload time, generate many
  (nonce, expected-tag) pairs and store only those; each is a one-shot
  audit that costs the verifier 32 bytes. Simple, but the supply of
  challenges is finite.
- **Homomorphic authenticators / polynomial commitments** (the
  literature from Juels–Kaliski's POR and Ateniese's PDP through
  Filecoin's Proof-of-Spacetime): the verifier can check a compact
  response against a public commitment forever, no ground truth
  needed — the same "commit once, verify cheaply many times" magic
  that Merkle roots give us for membership, extended to possession.

Also honestly noted: our audit trusts the auditor, and colluding
provers could proxy challenges to a single node that does hold the data
(possession proved, *redundancy* not — you can't tell three copies from
one copy with three names). Real systems fight that with encryption
per-replica or sealing (Filecoin again). All of this slots in behind
`ports.CreditLedger.RecordAudit` — the interface doesn't change, the
cryptography behind it does.

## What silt does today

- `Distribute` and repair ship every shard with its `StorageProof`;
  hosts refuse chunks whose proofs don't verify (never hold what you
  can't defend).
- `MsgChallenge{ChunkID, Nonce}` → `MsgChallengeReply{Proof, Tag}`;
  honest answering is ~30 lines in `core/node/por.go`.
- The auditor challenges *every* provider of every shard, fetches
  ground truth once per shard, grades all answers, and settles into
  the ledger: `+AuditReward` per pass, `−AuditSlash` per fail.
- Run it: `silt sim run audit` — 6 liars, all caught, zero false
  accusations, file still retrievable because honest replicas and
  parity never depended on them.

A bonus lesson this milestone taught us: the liars exposed a real
retrieval fragility. Provider resolution used to stop at the first
record found — fine when every provider is honest, fatal when the
first one is a fake. The walk now runs to convergence and collects
every record. Adversarial thinking found an availability bug that a
long run of honest-node testing never could.
