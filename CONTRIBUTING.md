# Contributing to Silt

Thanks for wanting to help build Silt. It's neutral storage
infrastructure — content-addressed, erasure-coded, encrypted at every
level, run by its participants and owned by none. This guide is how work
gets in.

## Ground rules

- **The infrastructure is not the content.** Keep it that way: core code
  must never be able to read, identify, or attach meaning to what the
  network carries. See [GOVERNANCE.md](GOVERNANCE.md) and
  [docs/aslan-boundary.md](docs/aslan-boundary.md).
- **Core stays pure.** Packages under `core/` and `ports/` import no
  adapters and no effects (`os`, `net`, `time`, ambient randomness) —
  enforced by `go test ./internal/depcheck`. Effects live in `adapters/`
  behind interfaces in `ports/`.
- **Determinism is sacred.** The simulator runs single-threaded and
  seeded; a given seed reproduces byte-for-byte. Don't add goroutines or
  map-iteration-order dependence to core, node, dht, or sim paths.

## Build and test

```sh
go build ./...
go test ./...                 # the whole suite, incl. deterministic sims
go vet ./... && gofmt -l .     # must be clean
go test -bench . ./core/...    # throughput numbers, if you touched hot paths
```

Try a change end to end with the sims, e.g. `go run ./cmd/silt sim run
churn` or `... takedown`.

## The flow

Nothing reaches `main` or a release without a pull request, green CI, and
review. The branch is protected to enforce it.

1. **Branch** off `main` (`feat/…`, `fix/…`, `chore/…`), or fork.
2. **Open a PR.** CI runs automatically: `go vet` + `gofmt` + the full
   test suite, a check that `website/changelog.html` is in sync with
   `CHANGELOG.md`, and a website link check. All must pass. Netlify posts
   a preview for site changes.
3. **Update the paper trail.** If you change behavior in `core/`,
   `adapters/`, or `cmd/`, add a line to the **`## [Unreleased]`** section
   of `CHANGELOG.md` (and run `python3 scripts/gen_changelog.py` to update
   the page), and update `docs/` where relevant. Docs ship with code.
4. **Review.** Every PR gets a maintainer review (currently
   [@nerolabs](https://github.com/nerolabs)) **and** a Claude
   `/code-review` pass for correctness and cleanup. Address the findings.
5. **Merge.** Squash-merge once approved and green. Delete the branch.

## Commits & PRs

- Small, focused PRs review faster than large ones.
- Write commit messages that explain *why*, not just *what*.
- The PR template asks what changed, how you tested it, whether docs and
  the changelog were updated, and any safety/abuse implications — fill it
  in honestly.

## Safety

Silt is designed to be governable without being surveillable (see
[docs/safety-denylist.md](docs/safety-denylist.md)). If a change touches
storage, serving, the chain, or the takedown path, call out the
safety implications in your PR. To report a security vulnerability,
**do not open a public issue** — a private disclosure path will be
published; until then, contact the maintainer directly.

## Scope

Silt is infrastructure only. Anything that resolves opaque identifiers
into human meaning — search, browsing, catalogs, names — belongs to a
separate layer, not this repository. PRs that blur that line will be
asked to move.
