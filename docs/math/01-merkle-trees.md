# Merkle Trees: one hash that vouches for everything

## The problem

A file in silt is a list of chunk hashes. We want a single short
value — one 32-byte name — that commits to the *entire* list, such that:

1. Anyone holding the name can verify any chunk they receive.
2. Proving "chunk #i belongs to this file" doesn't require sending the
   whole list.

Hashing the concatenated list gives you (1) but not (2): to check one
chunk you'd need every hash. The Merkle tree fixes that.

## The construction

Pair up the hashes and hash each pair. Pair up *those* and hash again.
Repeat until one hash remains — the **root**. For 8 chunks:

```
                 root
               /      \
           H(AB,CD)   H(EF,GH)
           /     \     /     \
        H(A,B) H(C,D) H(E,F) H(G,H)
        /  \    /  \   /  \   /  \
       A    B  C    D E    F G    H     ← chunk hashes (leaves)
```

The root depends on every leaf *and their order* — flip one bit anywhere,
or swap two chunks, and the change cascades all the way up.

## Inclusion proofs: the good part

To prove chunk `E` belongs to the root, I don't send you the tree. I send
`E` plus the **siblings along its path to the root** — here `F`,
`H(G,H)`, and `H(AB,CD)`:

```
        root  ← you compute this
       /    \
  H(AB,CD)  H(EF,GH)  ← you compute this
   (given)   /    \
         H(E,F)  H(G,H)  ← you compute H(E,F)
          /  \   (given)
         E    F
      (given) (given)
```

You hash your way up: `H(E,F)`, then `H(that, H(G,H))`, then
`H(H(AB,CD), that)` — and compare against the root you already trust. If
they match, `E` is in the file. A tree with *n* leaves needs only
⌈log₂ n⌉ sibling hashes: a million-chunk file has 20-hash proofs
(640 bytes) instead of a 32 MB hash list.

This is the seam silt's future credit system needs: a node can prove
it holds shard *i* of root *R* without anyone re-downloading the file.

## Two details that matter in our implementation

**Domain separation.** We hash leaves as `SHA-256(0x00 ‖ leaf)` and
interior nodes as `SHA-256(0x01 ‖ left ‖ right)`. Without the tag bytes,
an interior node is just 64 bytes of data — an attacker could present it
*as a leaf* and forge proofs for a "chunk" that is really an internal
node. The prefixes make the two vocabularies disjoint.

**Unbalanced trees.** Chunk counts are rarely powers of two. Following
RFC 6962 (the Certificate Transparency standard), a tree over *n* leaves
splits at *k* = the largest power of two *strictly less than* *n*, left
subtree gets *k* leaves, right subtree gets the rest, recursively. Every
leaf count gets exactly one tree shape — no padding leaves, no duplicated
last element (the duplication trick, as used in Bitcoin, famously allows
two different leaf lists to share a root — CVE-2012-2459).

Code: `core/manifest/merkle.go`. Tests exercise every leaf count from 1
to 33 and reject wrong-leaf, wrong-index, and truncated proofs.
