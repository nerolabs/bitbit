# Reed-Solomon: why any k pieces are enough

## The core fact

**Two points determine a line. Three points determine a parabola. Any k
points determine one and only one polynomial of degree k−1.**

That's the whole trick. Everything else is bookkeeping.

Why is it true? Existence: given k points with distinct x-coordinates you
can always build a degree-(k−1) polynomial through them (Lagrange
interpolation writes it down explicitly). Uniqueness: suppose two
degree-(k−1) polynomials `p` and `q` pass through the same k points. Then
`p − q` is a polynomial of degree at most k−1 with k roots — but a
nonzero polynomial can't have more roots than its degree. So `p − q` is
zero everywhere: `p = q`.

## From polynomials to file storage

Take a stripe of k data shards. Look at byte position 0 across all k
shards: that's k numbers. Treat them as the coefficients of a
degree-(k−1) polynomial `f`, and evaluate it at n fixed points:

```
f(x₀), f(x₁), …, f(x_{n-1})        (n evaluations, n > k)
```

Store one evaluation per shard. Now *any* k of these n values pin down
the unique degree-(k−1) polynomial — Lagrange-interpolate them, read the
coefficients back, and you've recovered the original k bytes. The other
n−k values were pure redundancy. Repeat for every byte position and
you've protected the whole stripe. Losing shards just means losing
evaluation points, and you have n−k to spare:

```
k data shards  ──encode──►  n total shards  ──lose any ≤ n−k──►  still k left  ──interpolate──►  original
```

(In practice the encoding is arranged so the first k shards *are* the
original data unchanged — a "systematic" code — so reading an intact file
requires no math at all. Parity only gets touched when something is
missing. That's why `shardnet get` on a healthy store never decodes.)

## The finite field footnote

One wrinkle: real polynomial arithmetic over bytes would overflow —
interpolating produces fractions and huge numbers. So all arithmetic
happens in **GF(2⁸)**, a number system with exactly 256 elements (one
per byte value) where addition is XOR and every nonzero element has a
multiplicative inverse. Division always works, nothing ever overflows,
every intermediate value stays one byte. Same theorems, cozier universe.
The n ≤ 256 limit on our `(k, n)` config is exactly the number of
distinct evaluation points GF(2⁸) offers.

The `klauspost/reedsolomon` library we wrap does this with SIMD
instructions at several GB/s — the same library MinIO runs in
production. We do not hand-roll field arithmetic; HANDOFF forbids it and
history agrees.

## What shardnet does with it

- Stripe = k consecutive 64 KiB ciphertext chunks (confirmed geometry:
  coding across chunks, so every shard is itself a normal
  content-addressed chunk).
- Encode adds n−k parity shards per stripe; default (k=10, n=16) means
  any 6 losses per stripe are free, at 1.6× storage.
- A short final stripe is padded with *implicit* all-zero shards that
  are never stored — the decoder gets them "for free", which the tests
  exploit.
- Corruption is treated as loss: a shard failing its SHA-256 check is
  handed to the decoder as missing. Then every reconstructed chunk is
  re-verified against the hash the Merkle root committed to — we trust
  the math, but we check it anyway.

Try it: `shardnet info <root>` prints the stripe map; delete up to n−k
of any stripe's files out of `.shardnet/objects/` and `get` won't even
mention it. Delete one more and it tells you exactly which stripe died
and why. Code: `core/erasure/erasure.go`, wiring in
`core/pipeline/pipeline.go` (`fetchDataChunks`).
