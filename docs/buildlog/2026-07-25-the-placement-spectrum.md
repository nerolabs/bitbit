# The placement spectrum, and why we moved off one end of it

For a long time Silt placed every chunk independently, by its own hash.
That's the **maximum-fanout** extreme of a spectrum, and it has real
virtues: perfect spread, dead-simple logic, no coordination. But it has
two costs that only show up at scale, and both are the kind of thing you
must fix *before* a live network of independent operators ossifies the
wire format — migrating them through a placement change later is far more
expensive than doing it while the network is empty.

**Cost one: reads got expensive.** A file of `S` stripes cost roughly
`S × k` separate lookups-and-fetches, because every shard was found on its
own. **Cost two: concentration risk wasn't actively managed.** Nothing
stopped two shards of the same stripe from drifting onto the same node,
or the same failure domain, as churn reshaped the network — and if that
happens, one machine's death can cost you *two* shards of a stripe
instead of one.

The fix was to stop thinking in chunks and start thinking in **columns**.
A column is one shard position across all stripes of a file. Place,
find, repair, and audit by column — keyed by `hash(root‖j)` — and three
things fall out at once:

- A column's providers are found in **one** lookup, not `S` of them.
- Losing one unit costs exactly **one shard per stripe** — you can lose
  up to `n−k` whole columns and still rebuild every stripe.
- Within-column anti-affinity becomes **structural**, not a heuristic you
  hope holds.

Then we pushed further, because "don't put two shards on one *node*" isn't
enough — you want "don't put two shards in one *failure domain*" (an
autonomous system, a rack, a datacenter, an operator). Nodes now carry a
`-domain` label and gossip its hash on every message, the same way they
gossip their capacity pledge. Publish steers each column onto a domain no
other column has used; repair re-seeds rebuilt columns into domains the
survivors aren't using. It's best-effort and never a veto — a node
spreads across the domains it has actually learned about — but on an
identical layout it roughly *halves* worst-domain co-residence versus
domain-blind placement.

The through-line: the earliest, simplest thing (place-by-hash) wasn't
wrong, it was one end of a spectrum. Naming the spectrum let us choose a
point on it deliberately, and — crucially — choose it while the cost of
choosing was still just a code change, not a network migration.
