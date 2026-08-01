# Release checklist — cutting the V1 release

**The target is V1, harden-first.** (Any earlier `0.x` tags were learning
misfires from before the harden-first decision, not real launches — ignore
them; we aim straight at V1 and will update everything at that point.) V1 is
gated by the tenets being **field-proven** — floors *and* pillars — per the
launch track in [ROADMAP.md](../ROADMAP.md) and
[launch-plan.md](launch-plan.md): credible from day one, not an experimental
drop the community is asked to finish hardening. Everything here is
ready-to-fire; the last step — pushing the tag — is deliberately manual and is
Andrew's to pull, *after* the field-proof gate and a personal review.

Nothing is signed or notarized (a wider-push concern). Binaries ship with
a `SHA256SUMS` file so anyone can verify them.

## Before the tag (maintainer)

- [ ] **Personal review + hardening pass.** Run the daemons yourself, try
      to break them, sanity-check the honest labeling reads honestly.
- [ ] **CI green on `main`** — vet, fmt, `-race`, coverage, e2e sims,
      changelog/roadmap freshness, docs-ship, links.
- [ ] **Threat model is published and current**
      ([docs/threat-model.md](threat-model.md)) — it's the centerpiece of
      the feedback ask.
- [ ] **Honest labeling is in place and accurate:**
  - README banner (0.x, unaudited, feedback, link to threat model).
  - Website download section + `node.html` operator caution.
  - No "signed binaries" claims anywhere (we ship checksums, not signatures).
- [ ] **`CHANGELOG.md` `[Unreleased]` is accurate** — it becomes the 1.0.0
      notes verbatim. It should read like an honest first-release summary.

## Cutting the tag (the trigger)

1. Move `CHANGELOG.md`'s `## [Unreleased]` to a dated `## [1.0.0] — <date>`
   section (this is the moment "unreleased" becomes real), then
   `python3 scripts/gen_changelog.py` and commit.
2. Tag and push:
   ```sh
   git tag v1.0.0
   git push origin v1.0.0
   ```
3. The **release workflow** (`.github/workflows/release.yml`) fires on the
   tag: it runs `go vet` + `go test`, cross-compiles the Mac / Windows /
   Linux binaries via `build.sh` (which also writes `dist/SHA256SUMS`),
   extracts the notes from `CHANGELOG.md`, and publishes a GitHub Release
   with all of `dist/*` attached. The site's download links resolve once
   the Release exists.

## After the tag

- [ ] **Verify the Release**: all five binaries + `SHA256SUMS` attached;
      download one and check its hash against `SHA256SUMS`.
- [ ] **Verify the site**: the download section points at real files and
      the checksum note is present.
- [ ] **Only then**, begin feedback outreach per
      [launch-plan.md](launch-plan.md) — narrow, technical, "help us break
      this," never "store your data here."

## Rolling back

A tagged Release can be deleted from GitHub, but treat a release as
public and permanent (people may have downloaded it). If something is
wrong, prefer cutting `v1.0.1` with the fix over pretending 1.0.0 never
happened.
