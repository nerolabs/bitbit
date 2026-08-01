## What & why
<!-- What does this change do, and why? Link any discussion. -->

## How I tested it
<!-- Commands run, sims exercised, manual checks. CI runs the full suite. -->
- [ ] `go test ./...` passes locally
- [ ] `go vet ./...` and `gofmt -l .` clean
- [ ] **Outcomes covered at all three tiers** for each major use case — **unit · integration/sim · e2e/nat** — asserting the OUTCOME (does the thing achieve what the persona is promised), not the method. This is immutable: a tier may be skipped only with a reason recorded below.
  - Tier(s) skipped + why (or "none"): 

## Paper trail
- [ ] Added a line to `CHANGELOG.md` `[Unreleased]` (and ran `scripts/gen_changelog.py`) — or N/A
- [ ] Updated `docs/` where relevant — or N/A

## Safety / abuse considerations
<!-- Does this touch storage, serving, the chain, or takedown? Could it let
     infrastructure learn what it carries, or weaken the takedown path?
     Say so explicitly, even if the answer is "no". -->
