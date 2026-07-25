# Kademlia: finding anything in O(log N) hops

## The problem

A million nodes hold chunks. Given a chunk hash, which nodes have it?
No central index — the index must *be* the network. The idea: give
every node an ID in the same 256-bit space as chunk hashes, define a
distance, and store the answer to "who has chunk H?" on the nodes whose
IDs are *closest to H*. Then "find data" and "find nodes" become the
same operation: navigate toward a point in ID space.

## XOR as a distance

Kademlia's distance between two IDs is just `a XOR b`, read as a
256-bit integer. It looks arbitrary; it's actually a metric with a
bonus property:

- `d(a,a) = 0`, `d(a,b) = d(b,a)` — trivially.
- Triangle inequality holds, and in a stronger, *exact* form:
  `d(a,b) XOR d(b,c) = d(a,c)` (our tests check this identity).
- **Unidirectionality**: for any a and distance Δ there is exactly ONE
  point at distance Δ from a. So "the k closest nodes to H" has one
  unambiguous answer — no ties, ever.

The right mental picture is a binary trie of IDs. The XOR distance
between two IDs is governed by their longest common prefix: agree on
the first bit and you're in the same half of the space; every extra
bit of agreement halves the territory again. Small XOR = deep shared
prefix = same tiny subtree.

## The routing table: exponentially nearsighted

Each node keeps 256 *k-buckets*; bucket i holds up to k peers whose
distance is in [2^i, 2^(i+1)) — i.e. peers whose first differing bit is
bit i:

```
bucket 255: the OTHER half of the network        (huge, k samples)
bucket 254: the other quarter                     (big, k samples)
   ⋮
bucket   1: nodes differing only in the last 2 bits
bucket   0: nodes differing only in the last bit  (tiny, known exactly)
```

Half your table describes far places coarsely; the close neighborhood
you know almost perfectly. Total memory: O(k log N). This shape is the
entire secret of the speed — and it's load-bearing, not an
optimization. While building M3's tests we accidentally proved this:
give nodes knowledge of only their *nearest neighbors* and lookups get
permanently stuck, because your neighbors share your prefix and know
nothing about the subtree the target lives in. You need contacts at
every distance scale to move at all. (The broken version is memorialized
in a comment in `core/dht/dht_test.go`.)

## The lookup: halving your way home

To find the k closest nodes to target T: ask the α closest peers you
know "who do YOU know near T?"; they each return their k best; merge,
re-sort, ask the new closest not-yet-asked candidates; repeat until the
top k have all answered.

Why O(log N) rounds? Suppose your best candidate agrees with T on p
leading bits — it sits in a subtree of ~N/2^p nodes containing T. Its
bucket facing T covers that subtree's *other half*, so (if any node
exists there) it can name one agreeing on ≥ p+1 bits. Every round of
answers extends the matched prefix by at least one bit, i.e. halves the
remaining candidate territory. After log₂ N halvings the subtree
contains only T's true neighborhood, and the lookup converges. Our test
finds the exact true top-8 of 500 nodes in ≤ a handful of rounds and a
few dozen messages — check `TestLookupConvergesInLogNRounds`.

## How silt uses it

- **Provider records, not chunks.** The DHT stores tiny claims — "node
  X has chunk H" — placed on the nodes closest to H. Chunks themselves
  go wherever they go; the DHT is a phone book, not a warehouse.
- **Publishing** (`Distribute`): for each chunk, look up the closest
  nodes to its hash, push the chunk to 3 of them; each recipient
  records itself as a provider.
- **Fetching** (`NetGet`): walk toward the chunk hash asking "any
  providers?", stop at the first hit, fetch, and — always — verify the
  hash. Provider claims are unverified by design: a liar costs you a
  round-trip, never your data.
- **Liveness is observed, not assumed**: every message refreshes the
  sender in the routing table; every timeout evicts. The table is a
  living gossip of who's really up.

Everything above runs identically over the in-process simnet or (later)
real sockets — the DHT logic in `core/dht` is a pure state machine that
has never heard of either.
