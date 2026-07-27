# One process, but a real product from day one

The temptation, building a distributed storage network, is to reach for
a fleet of servers immediately — spin up VMs, wire real sockets, watch
nodes gossip. We did the opposite, and it's the single decision the whole
codebase still rests on: **the entire network runs in one process**, and
will keep doing so until it genuinely needs real sockets.

The catch that makes this honest rather than a shortcut: the code is
structured so that swapping the in-process simulation for real
TCP/QUIC/libp2p transport later touches *only adapter code, zero core
logic*. That's hexagonal architecture — ports and adapters — taken as a
hard rule, not an aspiration:

- The **core domain** — chunking, crypto, erasure coding, manifests, DHT
  routing, the repair policy — is pure logic. It imports no networking,
  no filesystem, no wall clock, no global RNG. Every effect leaves
  through an interface defined in a shared `ports` package.
- **Adapters** implement those interfaces: in-memory ones for the sim,
  real ones (`tcpnet`, `diskstore`) for production. The core can't tell
  which it's talking to.
- The **simulator** isn't a mode threaded through the code — it's just a
  harness that wires nodes together with in-process adapters and drives a
  simulated clock.

Two rules keep it from rotting. First, a dependency-lint test fails CI if
a core package ever imports an adapter — the boundary is enforced, not
trusted. Second, **determinism**: every source of nondeterminism (time,
randomness, message ordering and latency) is injected, so a sim run with
a given seed produces byte-identical results. That last property is worth
more than it sounds. When a churn bug shows up 40 nodes and 10,000
messages deep, "re-run with seed 0x5117 and watch it happen again" is the
difference between a fix and a shrug.

The payoff came later, and it was pointed: when we finally *did* leave the
sim for real machines on real home networks, the swap touched adapters
and left the core alone — exactly as promised. The bugs we found there
(see the cross-network entry) were transport bugs, in transport code,
where they belonged.
