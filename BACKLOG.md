# Silt Backlog

Work captured for later, most-strategic first. The milestone history
(M1–M14, genesis, rename, website, CI/CD) lives in git and the
[ROADMAP](ROADMAP.md); this file is the running list of what's next.

## Engineering & process

### Harden the CI/CD gate
- **Integration tests before every commit.** The suite runs on every
  push/PR (`go test ./...`, gofmt, vet, link check, changelog sync —
  required on `main` and `staging`). A race-detector build
  (`go test -race`) and a 45% coverage floor are now in (Phase 0). Still
  to add (Phase 2): multi-process end-to-end tests — spin real daemons
  over TCP, publish → chain-commit → fetch, assert bit-perfect — run in
  CI, not just the in-process sim.
- **The website ships with the code.** *Done (Phase 0).* `ROADMAP.md`
  now auto-renders to `website/roadmap.html` (via `scripts/gen_roadmap.py`,
  linked in the site nav), with a CI staleness check like the changelog's;
  and a `Docs ship with code` CI job fails a PR that touches `cmd/`,
  `core/`, or `adapters/` without updating `CHANGELOG.md`. Documentation
  under `docs/` is not yet enforced the same way — a possible tightening
  later.

### Observability & debugging
- **`--debug` mode → `debug.log`.** Give the client a `--debug` flag that
  writes `warn`/`error`/`fatal` events to a `debug.log` while it runs, so a
  failure in the field leaves an artifact we can pull into Claude Code to
  diagnose — turning one-off crashes into fixes and durability over time.
  Needs a small leveled logger behind a `ports` interface (core stays pure;
  the file sink is an adapter), structured enough to grep, quiet by default.
- **An `info` level to validate assumptions.** Above the error tiers, an
  `info` setting that narrates the normal path (chunks placed, stripes
  repaired, quorum reached, providers resolved) so we can confirm the
  system behaves as designed under real-world conditions, not just in the
  deterministic sim. Same logger; `--debug` implies a verbose threshold,
  `info` a normal one. Keep it off the hot path when disabled.

### Build log / narrative
- **A public "how it was built" log.** A chronological, human-readable
  narrative of building Silt — the milestones, the design forks (placement
  spectrum, takedown-by-revocation, the reputation-quorum chain), the
  fresh-eyes council, the dead ends. Distinct from the CHANGELOG (what
  shipped) and ROADMAP (what's next): this is the *story and reasoning*.
  Likely never a released product, but valuable as a LinkedIn build series
  and an artifact future employers can inspect (cf.
  `getcamino.app/how-i-was-built/log`, from an earlier project). Could
  render from a `docs/buildlog/` of dated entries into a
  `website/buildlog.html`, same pipeline as the changelog. Keep it strictly
  about *building the infrastructure* — no Aslan/resolver crossover.

### Review-gated contributions
- **Nothing reaches mainline or a release without review by Andrew +
  Claude.** Branch protection requires a PR + green CI on `main`/`staging`
  (done); `CONTRIBUTING.md`, `CODEOWNERS`, and a PR template are in (done).
  Required approvals is 0, not 1 — a solo account can't approve its own
  PRs, so 1 would deadlock. Review is process-enforced: a `/code-review`
  pass on each PR, and Claude merges only after asking Andrew and
  offering him a chance to inspect. When external contributors arrive,
  set approvals to 1 for their PRs (he can approve those) + admin bypass.

### Housekeeping
- Cut the **v0.1 release** and publish signed binaries (see ROADMAP);
  the site's downloads point at build-from-source until then.
- Removed 44 MB of stale `dist/` binaries from the repo and gitignored
  the directory (done in this change).

## Storage layer — placement & distribution

Captured from design discussion (2026-07-25). Today Silt places every
chunk independently by its own hash — the maximum-fanout extreme: good
spread, but reading a file of S stripes costs ~S×k separate
lookups-and-fetches, and concentration/failure-domain risks aren't
actively managed. Decisions and tasks, priority order:

- **Column-based placement** — *done (Phase 1).* Placement, provider
  records, retrieval, repair, and audit now operate on whole **columns**
  (one shard position across all stripes), keyed by `hash(root‖j)`, instead
  of individual chunks. A column's providers are found in one lookup,
  unit-failure costs exactly one shard per stripe (lose up to n−k columns
  and still rebuild), and within-column anti-affinity is structural.
  Manifest chunks and uncoded files keep per-chunk placement. Design and
  tradeoffs in
  [docs/design/column-placement.md](docs/design/column-placement.md);
  proven by the `scatter`, `churn`, `capacity`, `audit`, and
  anti-affinity sims.
- **Failure-domain-aware placement** — *done (Phase 1).* Nodes carry a
  domain label (`-domain`, e.g. AS / rack / geo / operator) and gossip its
  hash on every message, like the capacity pledge. Publish steers each
  column onto a domain no other column has used; repair re-seeds rebuilt
  columns into domains the survivors aren't using — so a whole domain
  failing costs a stripe as little as possible, and cross-column
  co-residence is bounded instead of drifting as churn shrinks the network.
  It's best-effort (a placer spreads only across the peer domains it has
  learned) and never a veto. `TestFailureDomainPlacementSpreadsColumns`
  shows it roughly halves worst-domain co-residence vs domain-blind
  placement on an identical layout. Remaining polish: querying a candidate's
  domain when gossip hasn't reached it yet, and domain-aware capacity spill.
- **Demand-responsive dispersion** — *done (Phase 1, push half).* Each node
  counts how often it serves each chunk (decayed every demand tick); when
  one crosses `HotThreshold`, it **pushes** `FanoutReplicas` leased cache
  copies to other hosts, steered away from its own failure domain, which
  announce as providers so readers divide across more sources. The copies
  are **leased**: reads refresh them, and if the file cools they expire
  after `LeaseTTL`, so a flash-popular file can't permanently hoard
  capacity (baseline `Replication` stays the floor). The demand loop sleeps
  itself when nothing is hot or leased. `TestDemandFanoutAndCooldown` drives
  a hammered file from 0 → 153 cache copies → back to 0.
  **Follow-up (pull tier):** let a node that had to *fetch* a chunk under
  load opportunistically cache and announce it, decaying when unused — so
  hot copies also gravitate toward readers, not just away from hot holders.
- **Repair preserves anti-affinity** — *done (Phase 0).* Repair used to
  re-place rebuilt shards on the raw closest nodes without the
  `preferAvoiding` stripe-spreading that `Distribute` applies, so stripes
  drifted toward clustering over many repair cycles. `repairStripe` now
  seeds a per-stripe host count from the surviving shards' current
  providers and prefers candidates that don't already hold the stripe;
  `sim`'s `TestRepairPreservesStripeAntiAffinity` proves repair stays
  within +1 of the publish baseline where the old path drifted +2 to +4.
  Column placement will subsume this later.
- **Dispersion audit** — *done (Phase 1).* The caretaker sweep now, per
  stripe, tallies the failure domains that actually hold each column
  (confirmed by HasChunk, so a stale record can't fake spread). When one
  domain exclusively holds more than the n-k budget of a stripe's columns —
  a domain failure would drop it below k — it re-spreads: places one extra
  copy of each over-exposed column into a domain the stripe isn't using,
  until no single domain loss could break a stripe. `Stats.Dispersals`
  counts it; `TestDispersionAuditRespreadsConcentratedStripe` drives a
  two-domain publish (~8 of each stripe's 16 columns exclusive to each) to
  full convergence. Also surfaces the per-stripe domain spread the
  content-blind observatory couldn't compute.

## Networking — cross-network reachability (#27)

Captured from design discussion (2026-07-26). Everything works on
`127.0.0.1`; two nodes on separate home networks can neither **find** each
other (rendezvous) nor **connect** (NAT). Design splits the two and keeps
the relay *capability* in the binary while the public *deployment* stays
throwaway dev scaffolding — never project-run. Full spec, tradeoffs, the
neutrality line, and open questions (relay incentives/abuse, IPv6, symmetric
NAT) in [docs/design/cross-network.md](docs/design/cross-network.md).
Build order:

- **mDNS local discovery** — free two-nodes-in-a-house rendezvous, no infra.
- **Reachability check** (our AutoNAT) — a node verifies whether it's
  publicly dialable before advertising a direct address; the decision the
  rest hangs on.
- **Relay** (`-relay`, content-blind ciphertext forwarding, rate-capped) —
  the universal fallback when both peers are NATed; a node capability, not
  special infrastructure. The bulk of the work.
- **Dev node** — a throwaway public `-relay` box (VPS or tunnel) in a
  separate `deploy/`, clearly *not* project-operated, to develop against.
- **Prove it** — Andrew's Mac ↔ wife's Mac, different networks, meeting
  through the dev relay; publish and retrieve.
- **UPnP/NAT-PMP** and **hole-punching** (DCUtR-style relay→direct upgrade)
  — later optimizations that reduce relay dependence.

Guardrails: **no baked-in seed/relay** (`-dns-seed`/`-bootstrap` stay
arguments, never defaults); threat model gains a relay-metadata note.

## Strategy — the "fresh-eyes council"

Andrew's ask: convene experienced Legal, PR, and Marketing executives
who understand this is open source but want to **protect everyone
involved in building Silt**. What are the major cross-functional flaws,
what are the risks and mitigations, and what's the launch/marketing plan
to reach people who'd run nodes and improve the software?

**Done** — the three documents are written and merged:
`docs/fresh-eyes-council.md`, `docs/risk-register.md`, and
`docs/launch-plan.md`, plus `docs/safety-denylist.md` and `GOVERNANCE.md`.
The *actions* inside them (legal entity, security audit, denylist
distribution) are tracked in the roadmap's prioritized sequence.

The single most important
cross-cutting theme to resolve early: a content-neutral, "cannot-know-
what-it-carries" network needs its **abuse-handling and legal posture
designed in from the start** — both to protect operators and to keep the
messaging honest (Silt is neutral infrastructure, not an evasion tool).
