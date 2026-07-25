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
  (done); `CONTRIBUTING.md`, `CODEOWNERS`, and a PR template are in (done);
  required approvals raised to 1 so a maintainer review is enforced (done).
  "Review by Claude" = a `/code-review` pass on each PR before merge.

### Housekeeping
- Cut the **v0.1 release** and publish signed binaries (see ROADMAP);
  the site's downloads point at build-from-source until then.
- Removed 44 MB of stale `dist/` binaries from the repo and gitignored
  the directory (done in this change).

## Strategy — the "fresh-eyes council"

Andrew's ask: convene experienced Legal, PR, and Marketing executives
who understand this is open source but want to **protect everyone
involved in building Silt**. What are the major cross-functional flaws,
what are the risks and mitigations, and what's the launch/marketing plan
to reach people who'd run nodes and improve the software?

This deserves its own focused deliverable. Planned outputs:
1. **`docs/fresh-eyes-council.md`** — a written council: each "hat"
   (Legal, Trust & Safety / abuse, Security, PR, Marketing, Governance)
   names its single biggest concern and its recommended mitigation.
2. **`docs/risk-register.md`** — a ranked risk register (likelihood ×
   impact) with an owner and mitigation per risk.
3. **`docs/launch-plan.md`** — positioning, messaging pillars, the
   audiences (node operators, contributors, researchers), the channels
   to reach each, and a phased launch sequence.

An initial pass at the top findings was delivered alongside this
backlog; these documents expand it. The single most important
cross-cutting theme to resolve early: a content-neutral, "cannot-know-
what-it-carries" network needs its **abuse-handling and legal posture
designed in from the start** — both to protect operators and to keep the
messaging honest (Silt is neutral infrastructure, not an evasion tool).
