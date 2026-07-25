# Counting the Crowd: how every node knows the network's size

## The problem

"Seven hundred daemons at 2G each — the network holds 1.4TB, and every
node should know it." But no node can see the network. A Kademlia node
knows O(k·log N) peers out of N; there is no roster, no census-taker,
and asking everyone would BE the census problem. Yet each Silt node
continuously reports a network-total capacity that lands within a few
percent of the truth. Two small ideas do it.

## Idea 1: density — you can measure N without seeing N

Node IDs are uniform random points in a space of 2^256 positions. Ask:
*how far away is my m-th nearest neighbor?*

If the whole network has N nodes scattered uniformly, then around any
point, a ball containing m of them has "volume" of about m/N of the
whole space. So if your m-th nearest neighbor sits at XOR distance d:

```
d / 2^256  ≈  m / N        ⟹        N  ≈  m · 2^256 / d
```

The crowd's size is written in the elbow room around you. This is the
same trick ecologists use to estimate animal populations from
nearest-neighbor distances, and the same reason you can estimate a
city's population from your view of one block of it.

The kicker: Kademlia hands you exactly the ingredient you need. A
node's self-lookup fills its close buckets with its TRUE nearest
neighbors — the one region of the network every node knows exhaustively
is its own neighborhood. `dht.EstimateNetworkSize` reads the distance
to the 8th-closest known peer and does the division (in big-integer
arithmetic; d is a 256-bit number). Our test finds it lands within a
small factor across network sizes from 50 to 5000 — typically within a
few percent once averaged over observers.

## Idea 2: gossip — the average pledge rides along for free

Knowing N is half the answer; the other half is the average pledge.
Every Silt message already travels between peers, so every message
now carries two extra integers: the sender's used and total capacity.
Each node passively accumulates a sample of pledges from everyone it
happens to talk to — no survey round-trips, no extra messages at all.

```
network capacity  ≈  N_est × mean(sampled pledges)
network usage     ≈  N_est × mean(sampled usage)
```

Both halves are approximations; both are unbiased enough to multiply.
In the capacity scenario, the median node's estimate of a 19.0MB
network is 20.0MB — off by 5%, computed from purely local knowledge.

## Why this matters beyond the dashboard

The capacity estimate is the network's self-awareness: it's what a
client consults to decide "is there room for my file?", what the
observatory frontend will chart, and eventually an input to the
economics (scarcity should price storage). And the estimator's
structure — local measurement × gossiped average — is a template that
extends to bandwidth totals, serving rates, and anything else the
network needs to know about itself without anyone being in charge of
knowing it.

Code: `core/dht/estimate.go` (the density estimate),
`core/node/capacity.go` (the combination), capacity gossip stamped in
`node.send`. Run `silt sim run capacity` to watch 38 bounded nodes
fill up while every one of them tracks the total.
