# Key Hierarchies: permissions made of arithmetic

## The problem

BitBit needs three levels of access to one file, held by three kinds of
actors who don't trust each other:

- **Infrastructure** (daemons) stores and serves shards. It must know
  *nothing* — not the content, not even which shards form which file's
  stripes beyond what storage proofs already show.
- **Caretakers** repair and audit. They must know the *layout* — chunk
  IDs, stripe boundaries, erasure parameters — or they can't rebuild a
  lost shard. But layout access must not leak a single content byte.
- **Readers** get everything.

In a centralized system you'd build an access-control list and a server
to enforce it. BitBit has no server to trust, so the permissions have to
be made of mathematics: what you can do IS what you can decrypt.

## The construction: one-way derivation

Everything hangs off one 32-byte value, the *link key* (the second half
of a bitbit link):

```
linkKey ──HKDF("…/layout")──► layoutKey
linkKey ──HKDF("…/content")──► contentKey
```

and the stored manifest is ciphertext twice over:

```
blob = Enc_layoutKey( layout ‖ Enc_contentKey(secrets) )
```

HKDF is built from HMAC-SHA-256, and inherits the one-wayness of the
hash: deriving child from parent is one cheap computation, while going
from `layoutKey` back to `linkKey` — or sideways to `contentKey` — is
as hard as inverting SHA-256. So the hierarchy of *strings* becomes a
hierarchy of *powers*:

| holds            | sees layout | reads content | can repair/audit |
|------------------|:-----------:|:-------------:|:----------------:|
| nothing (daemon) |      ✗      |       ✗       |   ✗ (serves only)|
| care link        |      ✓      |       ✗       |        ✓         |
| full link        |      ✓      |       ✓       |        ✓         |

Granting a permission is *sending a string*. Revoking one is impossible
— which is honest: you can never un-tell a secret, and cryptography
that pretends otherwise is lying. (Real revocation means re-encrypting
under a new key, i.e. publishing a new file.)

## Why the keys can be deterministic

The link key is `SHA-256(plaintext manifest)` — not random. That's the
convergent trick one level up: the same file added twice produces the
same manifest, the same link key, the same ciphertext blob, and
therefore the same *link*. Dedup extends all the way to the handle you
share. It also makes fixed AEAD nonces safe here for the same reason as
in convergent chunk encryption (note 02): a key derived from the exact
plaintext can never be reused for two *different* plaintexts, so the
(key, nonce) pair never repeats with different messages. And it buys
self-verification for free — decrypt a manifest, hash it, compare with
the link key you used.

The cost is the same confirmation-attack tradeoff as note 02: someone
who can guess your entire file can confirm the guess. For files with a
secret *existence*, private mode randomizes the chunk keys, which
randomizes the manifest, which randomizes the link.

## The pattern generalizes

This is the smallest instance of a broadly useful idea — capability
trees. Nothing stops a v2 from deriving deeper:

```
linkKey ─► layoutKey
        ─► contentKey ─► previewKey   (decrypt only the first stripe)
                      ─► fullKey
```

Filesystems (Tahoe-LAFS's read-caps/verify-caps), messaging ratchets
(Signal), and hierarchical wallets (BIP-32) are all this same shape:
one secret at the root, powers fanning out through one-way functions,
authority delegated by handing someone a node of the tree and physics
preventing them from climbing back up.

Code: `core/link` (the hierarchy), `core/crypto` SealBox/DeriveKey
(the boxes), `core/manifest/sealed.go` (the two-layer blob),
`core/node/repair.go` (`repairRootWithLayout` — a caretaker doing its
whole job inside the layout ring). Try it: `bitbit add` prints both
links; `bitbit info <care-link>` shows stripes with mode "(sealed)".
