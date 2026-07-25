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
- **The website ships with the code.** Make documentation, the
  changelog, and the public roadmap first-class build outputs so a
  mainline change can't land without its docs. Concretely: a CI check
  that fails a PR touching `cmd/`, `core/`, or `adapters/` if it doesn't
  also update `CHANGELOG.md` (Unreleased) and, where relevant,
  `docs/`; auto-render `ROADMAP.md` into a public `website/roadmap.html`
  the way the changelog is rendered today.

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

- **Column-based placement** — *the backbone; next storage milestone.*
  Place whole **columns** (one shard position across all stripes), keyed
  by `hash(root‖j)`, instead of individual chunks. Reads become **k
  conversations for the whole file** regardless of size, unit-failure
  costs exactly one shard per stripe (lose up to n−k columns and still
  rebuild), and anti-affinity becomes optimal and automatic. Design and
  tradeoffs written up in
  [docs/design/column-placement.md](docs/design/column-placement.md).
- **Failure-domain-aware placement** — *highest-value durability item.*
  Nodes advertise a domain hint (AS / rack / geo / operator); placement
  spreads across distinct **domains**, not just node IDs. Content
  addressing prevents random concentration but not *correlated* failure
  (16 shards on 16 IDs in one datacenter). Pairs naturally with column
  placement (the n columns should land in n distinct domains).
- **Repair preserves anti-affinity** — *done (Phase 0).* Repair used to
  re-place rebuilt shards on the raw closest nodes without the
  `preferAvoiding` stripe-spreading that `Distribute` applies, so stripes
  drifted toward clustering over many repair cycles. `repairStripe` now
  seeds a per-stripe host count from the surviving shards' current
  providers and prefers candidates that don't already hold the stripe;
  `sim`'s `TestRepairPreservesStripeAntiAffinity` proves repair stays
  within +1 of the publish baseline where the old path drifted +2 to +4.
  Column placement will subsume this later.
- **Dispersion audit** — the caretaker repair loop counts *reachable
  shards* per stripe; extend it to count distinct *hosts and domains* per
  stripe and trigger redistribution below a diversity threshold. Turns
  the observatory's existing "daemons hosting per root" health signal
  into an enforced invariant.

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
