# Gates 1–3 completeness audit (2026-08-02)

> **Purpose.** Before opening the Gate 4 design turn, certify that the landed
> floors (Gate 1), register-after-distribute (Gate 2), and cross-network
> reachability (Gate 3) are actually *whole* — every promise carried by
> unit + integration/sim + e2e coverage per build-immutable **V5** — not merely
> "PRs merged." Gaps found here were fixed in the same change, each with a
> failing-first regression runnable on the local suite.

**Method.** Full local suite green before and after: `go vet ./...`, `gofmt -l`,
`go test -race ./...` (39 packages), `go test ./e2e/`, and the Docker NAT
harness (`integration/nat/run.sh RESTART=1`, `holepunch.sh` cone + symmetric).
Coverage was mapped piece-by-piece against each gate's "Done when".

---

## Gate 1 — Floors — **DONE**

| Piece | Impl | Adversary / unit | Integration / e2e | Verdict |
|---|---|---|---|---|
| #87 panic-recover + fuzz | `internal/safe`, boundary Guards in `tcpnet.readLoop`, `relay.Server.handle`, `relay.Client.session`, `eventloop.OnPanic` | `safe_test.go`, `eventloop_test.go`; **8 fuzz targets** across the whole decode surface (manifest/chunk/link/chain/tcpnet/relay), none wrapped in recover so a panic is a real fuzz failure | `tcpnet_test.go:TestPanickyHandlerDoesNotKillTheNode` — real TLS, panicking handler fails the *request*, next message still arrives | **DONE** |
| #88 manifest bounds | `manifest.MaxChunks`/`MaxChunkSize`, decoder `MaxArrayElements`, `Validate`, `OpenLayout` | `bounds_test.go` (over-declared count rejected *before* allocation; oversize size rejected on both plain `Unmarshal` and sealed `OpenLayout` paths); `FuzzUnmarshal` | decoder is the tier the bug lives at; fetch path routes hostile bytes through the same bounded `OpenFull` | **DONE** |
| #104 frame cap | `tcpnet.maxFrame = manifest.MaxChunkSize + frameOverhead`; `Send` rejects over-cap | `frame_test.go` | `TestMinProductionChunkFrameRoundTrips` (64 MiB over real TLS), `TestOversizeFrameFailsSendLoudly` (loud, node survives) | **DONE** |
| #89 lock UI/API | `guard` middleware (Host + Origin allow-list, per-daemon bearer token, CORS `*` gone) | `ui_guard_test.go` (11 cases: DNS-rebind Host reject, cross-origin reject, no wildcard CORS, local-origin reflect, token-gated mutation, tokenless reads, preflight, token persistence 0600) | httptest exercises the full request flow | **DONE** |

**Assessment.** Every Gate-1 promise has adversary-shaped coverage at the tier
the bug class lives at, plus an e2e/real-TLS proof where a real process boundary
is involved. The V3 "attacker outcome must fail" tests are present: malformed
frame → clean per-request error (not a crash); over-declared manifest → refused
before allocation; over-cap frame → loud, not silent drop; cross-origin drive-by
→ 403. No gap.

---

## Gate 2 — register-after-distribute (#65) — **GAP FOUND → FIXED**

**What was whole.** `pipeline.Stage` (stores, does not publish) vs `Add`
(stages+publishes) is correct and unit-tested (`stage_test.go`). Fetch-side
retry/backoff has three sim tests (`core/node/fetch_retry_test.go`). Relay
limits are raised to 128/16 (`relay/server.go`). Cross-NAT publish→fetch (the
happy path) is proven bit-perfect, restart-surviving, by `run.sh RESTART=1`.

**The gap.** The register-after-distribute *failure* outcome — a **failed
scatter must leave no dangling registry entry** — had **no regression**. The one
sim test touching an unplaceable scatter (`TestPublishFailsLoudWhenManifestUnplaceable`)
uses the old `Add` path, which publishes up front, so the entry is *already in
the registry* when the scatter fails; it asserts the loud error but structurally
**cannot** catch a dangling entry. The actual gate ("publish iff `derr == nil`")
lived hand-rolled and duplicated in `cmd/silt/swarm.go` and `cmd/silt/ui.go`,
untested on its failure branch — reorder those lines and every test still passed.

**Fix.** Extracted the gate to a single tested helper
`pipeline.RegisterAfterDistribute(ctx, reg, entry, placed, derr)` (publish only
on a confirmed scatter; surface the scatter error otherwise). Both publish paths
now call it instead of duplicating the idiom. Regressions:
- `core/pipeline/stage_test.go` — unit, both branches (fail → registry
  untouched + error surfaced; success → registered).
- `sim/publish_durability_test.go:TestRegisterAfterDistributeLeavesNoDanglingEntry`
  — drives the **real** `node.Distribute` failure (zero-pledge stores) through
  the gate and asserts the registry is empty.

Both fail if the gate is reverted to publish unconditionally (verified). Minor
sibling gap also closed: the relay's **per-target** cap (`PerPeerSessions`, the
#65 knob) had no test — only the global `MaxSessions` branch of `server.go:261`
was exercised. Added `relay_test.go:TestPerPeerSessionCap` (one target's slot
fills while a different target still connects; fails if the per-peer branch is
removed).

**Assessment after fix.** Unit + sim now cover the promise's failure path; e2e
covers the success path. **DONE.**

---

## Gate 3 — cross-network reachability (#27 / #111) — **DONE** (one doc gap fixed)

**Part B — NAT traversal (the substance).** Complete and CI-gated:
- Relay path (universal, content-blind): `run.sh` — cross-NAT publish→fetch
  bit-perfect through a real kernel-NAT relay.
- Hole-punch: `probe.sh` proves the raw primitive (cone punches, symmetric
  can't); `holepunch.sh` proves the **integrated** daemons upgrade the relay
  path to direct on cone and correctly stay on the relay on symmetric — the
  regression gate for #111 (punch never firing, then never binding). Both run
  locally under colima and in CI (`nat-holepunch` job, cone + symmetric).
- `viaRelay` flag + shared `internal/reuseport` (SO_REUSEPORT bind) — the two
  #111 root-cause fixes — are exercised end-to-end by `holepunch.sh`.

**Part A — rendezvous / bootstrap (finding the swarm).** The mechanism is
complete and tested: `-bootstrap`, `-dns-seed` (`discovery.FromDNS`/`ParseTXT`,
covered by `discovery_test.go`), Kademlia peer-exchange, persisted address book.
The default `-dns-seed` is **empty** — and, per the directive's question, this is
a **deliberate no-op, not a hole**: baking in a well-known seed domain would make
joining depend on infrastructure the project operates, against the
neutral-infrastructure stance (#27 Part A: "SiltHQ operates nothing"; seeds are
community-run). *Gap fixed:* that intent was undocumented in code, so a reader
couldn't distinguish "deliberate" from "unfinished." Now documented in the
`discovery` package doc and at both flag definitions.

**Scoped out (honest deferral, not a gap).** UPnP / NAT-PMP is **not
implemented** — the epic listed it as an optional "cheap win for the lucky … add
later," never a completion criterion. The cross-network gap #27 names (two home
nodes can neither find nor connect) is closed by relay + hole-punch; UPnP is a
relay-load optimization tracked separately.

**Auto-trigger unit test — considered, not required.** `maybeRequestPunch` (the
#111 auto-trigger) is covered by `holepunch.sh` — the correct tier (it needs
real kernel NAT + a live relay), and it is in the **local** suite per V5, so a
re-break surfaces locally in minutes. A sim of the auto-trigger would duplicate,
not strengthen, that guarantee; the sim already models the punch *primitive*
(`simnet.HolePunch`).

**Assessment.** Substance done and CI-gated at the e2e/integration tier; the
only real gap was the undocumented deliberate default, now fixed. **DONE — #27
can close.**

---

## Verdict

| Gate | Verdict |
|---|---|
| 1 — Floors (#87/#88/#89/#104) | **DONE** — complete adversary + e2e coverage |
| 2 — register-after-distribute (#65) | **DONE after fix** — failure-path regression + per-peer cap test added |
| 3 — cross-network (#27/#111) | **DONE** — substance CI-gated; deliberate empty seed now documented; UPnP deferred |

Gates 1–3 are certified complete. The floors are trustworthy to build Gate 4 on.
