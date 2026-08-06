# Silt Backlog

> **The spine is elsewhere.** *What M0 asserts* → [`docs/design/m0.md`](docs/design/m0.md);
> *what the owner decided* → [`docs/decisions.md`](docs/decisions.md); *the forward
> tracks and their order* → [`ROADMAP.md`](ROADMAP.md); *live state* → the GitHub
> **`V1` milestone**. This file is a **scratch list of small, still-open captured
> ideas** that don't (yet) merit their own issue — polish and latent wins, not
> strategy. When one matures, file it against the tenet it serves and drop it here.
>
> History note: the storage placement, cross-network reachability, observability,
> CI/CD, and fresh-eyes-council work captured in earlier revisions of this file is
> **shipped** — that detail lives in git, `CHANGELOG.md`, and `docs/buildlog/`, not
> here. What remains below is only what is still open.

## Storage layer — placement & distribution (polish)

- **Demand-responsive dispersion, pull half.** The push half ships (a hot holder
  leases cache copies away from its own failure domain). Still open: let a node that
  had to *fetch* a chunk under load opportunistically cache and announce it, decaying
  when unused — so hot copies also gravitate *toward* readers, not just away from hot
  holders.
- **Domain-aware placement gaps.** Query a candidate's failure domain when gossip
  hasn't reached it yet (today placement spreads only across *learned* peer domains);
  domain-aware capacity spill. Column placement will subsume the per-stripe
  anti-affinity repair path later.

## Networking — cross-network reachability (latent wins)

- **Try a direct IPv6 dial before assuming a relay is needed** — a cheap latent win
  before falling back to relayed transport.
- **Relay selection + failover.** A NATed node that discovers relays by gossip
  currently adopts the lowest-ID one and commits to it; if the chosen relay won't
  register, it retries that one forever instead of failing over. Fine while a swarm
  has one dev relay; wants selection + failover once community relays are plural.
- **UPnP / NAT-PMP automatic port-mapping** (#115) and **DCUtR-style hole-punch
  upgrade** — relay-load optimizations that reduce relay dependence (cone-NAT punch
  is already proven).

## Observability & process (polish)

- **Enforce `docs/` freshness like code.** A `Docs ship with code` CI job fails a PR
  that touches `cmd/`/`core/`/`adapters/` without a `CHANGELOG.md` update; extending
  the same staleness enforcement to `docs/` is a possible later tightening.
- **e2e harness extensions.** A relay-in-the-middle variant and a kill-a-node
  erasure-resilience variant of the multi-process e2e suite.

## Narrative

- **The public "how it was built" log.** A chronological, human-readable narrative of
  building silt — the design forks, the fresh-eyes council, the dead ends, and (now)
  the composition reset + the research commission. Renders from `docs/buildlog/` to
  `website/buildlog.html` via the same pipeline as the changelog. Strictly about
  building the infrastructure — no Aslan/resolver crossover. Valuable as a build
  series and an artifact future collaborators can inspect.
