# Takedown integration harness (Docker) — field test #6

An automated, real-container proof of Silt's safety model — the same claim
`silt sim run takedown` (`sim/takedown.go`) makes in-process and
`examples/flow8-takedown.sh` makes on loopback, but over the **real wire**:
three real daemons, real TLS, real sockets, real `-denylist` enforcement and a
real on-chain `-revoke`.

**The claim: a takedown is per-operator, existence-checked, and reversible.**

- **Per-operator / per-hash.** An operator that loads a `-denylist` naming a
  root PURGES and REFUSES that root — while a *different* operator that never
  loaded the list keeps serving the identical root, and unrelated content
  survives everywhere. There is no global switch (`core/denylist` doc; a `Set`
  is per-node, `SetDenylist`/`EnforceDenylist`).
- **Existence-checked.** The on-chain takedown path (`-revoke`, honored by
  nodes that `-honor-chain-revocations`) may only revoke a root the chain has
  already committed — `ValidateProposal → validateTakedowns → ErrRevokeUnknownRoot`,
  "a root never published on this chain". A revoke of a bogus root never
  proposes and never commits.
- **Reversible.** Removing the denylist entry (restart without it) restores
  serving — the denial was conditional on the list, not a permanent ban.

```
                 10.90.0.0/24  (one bridge network)
  ┌─────────────────┬────────────────────┬────────────────────┐
  │ val 10.90.0.10  │ opA 10.90.0.11     │ opB 10.90.0.12     │
  │ chain-backed    │ independent op —   │ independent op —   │
  │ validator+reg   │ loads a -denylist  │ NEVER denies       │
  │ (existence      │ → PURGES + REFUSES │ → still SERVES the │
  │  check vantage) │   the target root  │   identical root   │
  └─────────────────┴────────────────────┴────────────────────┘
```

opA and opB are deliberately **separate operators with their own registries**,
so the per-operator scope is unambiguous: the same *convergent* file yields the
**same root** on both (`-mode convergent` — required, else two private roots),
and a takedown on opA leaves opB's copy untouched.

## Run it

```sh
./integration/takedown/run.sh          # build, test, tear down; exit 0 = PASS
KEEP=1 ./integration/takedown/run.sh   # leave the topology up afterward to poke at
```

Needs Docker (Desktop on macOS, or the engine on Linux) and a Go toolchain. The
`silt` binary is compiled **on the host** (CGO off → trivial cross-compile) and
copied into a slim image — so the image stays tiny and there's no ~1 GB
Go-build memory spike inside Docker.

## What each piece is

| file | role |
|------|------|
| `docker-compose.yml` | the topology: one network, a chain-backed validator, two independent operators; each `/data` is a named volume so a restart preserves the store |
| `docker-compose.deny.yml` | override: recreate opA with `-denylist=/data/deny.txt` (static IP preserved) |
| `docker-compose.revoke.yml` | override: recreate the validator with `-revoke=${REVOKE_ROOT}` |
| `Dockerfile` | slim runtime image (silt binary + iproute2 + coreutils) |
| `node.sh` | entrypoint: self-advertise the container's own IP, then exec the daemon |
| `run.sh` | the driver: build → bring up → publish → deny → assert → revoke → reverse → tear down |

## What run.sh asserts (all keyed off real behavior)

1. **Baseline** — both operators serve the identical convergent target root; a
   distinct control file serves too (so a real PASS is distinguishable from a
   global wipe).
2. **Takedown on opA only** — opA logs `denylist: 1 root(s) denied; purged 13
   held chunk(s)`, its object count drops, it **REFUSES** the target
   (`MsgFetchChunk`/`MsgHasChunk`/`MsgChallenge` all no-op for a denied root),
   while opB's object count is unchanged and it **still SERVES** the identical
   root, and the control file **survives** on opA. Per-operator, per-hash.
3. **On-chain existence check** — the validator revokes the **real committed**
   root (`takedown: proposed on-chain revocation of <root>` + a takedown block
   commits) and then a **bogus, never-published** root (no proposal, no block —
   the daemon's existence gate refuses to propose it; the chain would reject it
   with `ErrRevokeUnknownRoot`).
4. **Reversibility** — restart opA without the denylist and re-publish; opA
   serves the once-denied root again.

## Notes / scope

- The **operator-local `-denylist` file itself is not existence-checked** — by
  design. `denylist.LoadInto` accepts any well-formed 32-byte hex hash so an
  operator can *pre-emptively* deny a root (a jurisdiction's blocklist may name
  content before it is ever published locally). The existence check lives on
  the **on-chain** takedown path, which is what this harness exercises for it.
- The on-chain `-revoke` here runs on a **trusted single-box validator**
  (`-min-rep 0 -quorum 0`), the legitimate one-operator configuration the daemon
  prints a notice for. A full multi-validator objective quorum is out of scope
  for this harness (see the `nat` harness for the multi-daemon consensus rig).

## Isolation

Image tag `silt-takedown`; compose project `takedown`; subnet `10.90.0.0/24`
(clear of the other harnesses). `run.sh` traps cleanup and `docker compose down
-v` on exit unless `KEEP=1`, so it leaves nothing running.
