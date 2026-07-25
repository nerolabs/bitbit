# Silt Backlog

Work captured for later, most-strategic first. The milestone history
(M1–M14, genesis, rename, website, CI/CD) lives in git and the
[ROADMAP](ROADMAP.md); this file is the running list of what's next.

## Engineering & process

### Harden the CI/CD gate
- **Integration tests before every commit.** The suite already runs on
  every push/PR (`go test ./...`, gofmt, vet, link check, changelog
  sync — required on `main` and `staging`). Add: multi-process
  end-to-end tests (spin real daemons over TCP, publish → chain-commit →
  fetch, assert bit-perfect) run in CI, not just the in-process sim;
  a race-detector build (`go test -race`); and a coverage floor.
- **The website ships with the code.** Make documentation, the
  changelog, and the public roadmap first-class build outputs so a
  mainline change can't land without its docs. Concretely: a CI check
  that fails a PR touching `cmd/`, `core/`, or `adapters/` if it doesn't
  also update `CHANGELOG.md` (Unreleased) and, where relevant,
  `docs/`; auto-render `ROADMAP.md` into a public `website/roadmap.html`
  the way the changelog is rendered today.

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
- **Repair preserves anti-affinity** — *small, verified gap.* Repair
  re-places rebuilt shards on the raw closest nodes without the
  `preferAvoiding` stripe-spreading that initial `Distribute` uses
  (`core/node/repair.go`), so stripes can drift toward clustering over
  many repair cycles. Column placement subsumes this; if that's deferred,
  fix repair directly first.
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
