# Economy + blind-credit field test (Docker)

Field test #5. Two claims, validated against **real** silt behavior — real CLI
flags, real daemon stdout, the real committed `chain.cbor`:

**(a) Hosts earn per byte served; freeloaders go broke.** Driven by the
in-process observatory `silt sim run economy` (the M5 scenario over the real
`core/credit` ledger): a top earner that *served* bytes ends able to publish
again, while a freeloader that only *consumed* served 0 B and cannot.

**(b) M0 adds publisher-unlinkable prepaid blind-signed publish credits.** A
publish can carry a **blind-signed publish token** (quorum-issued, over a
prepaid-credit path) *instead of* a durable Publisher identity — so the
committed chain entry carries **no** `Publisher→root` link. Exercised on a real
container swarm end-to-end, plus its controls.

```
  econ (flat bridge 10.80.0.0/24)
    ├─ val1  (10.80.0.11)  validator + token ISSUER + serves the registry
    ├─ val2  (10.80.0.12)  validator + token ISSUER (second quorum signer)
    ├─ holder(10.80.0.20)  storage node — the would-be earner (hosts/serves)
    └─ leech (10.80.0.30)  -freeload: routes but REFUSES to store/serve
```

Both validators run `-require-tokens 1`, so the chain admits an entry only if it
carries a publish token — never a Publisher identity (`-allow-publisher` is off).
`node.sh` stamps each container's own IP via `-advertise` (a wildcard bind isn't
dialable on its own), so token requests and attestations land — no test-only
daemon flags.

## Run it

```sh
./integration/economy/run.sh          # build, test, tear down; exit 0 = PASS
KEEP=1 ./integration/economy/run.sh   # leave it up afterward to poke at
```

Needs Docker and a Go toolchain. The container `silt` is compiled **on the host**
(CGO off → trivial cross-compile) into `integration/economy/silt` (git-ignored,
never committed) and copied into a slim image. The sim (claim a) runs on the
host, so run.sh also builds a throwaway host-native binary for it.

## What each assertion keys off (real, observed)

| assertion | real signal |
|-----------|-------------|
| (a) earner earns per byte, can re-publish | `sim`: `top earner … served <N> B … can publish: true` |
| (a) freeloader broke, refused | `sim`: `freeloader … served 0 B … can publish: false`; `all 6 freeloaders among the rejected: true` |
| (b) control: token-less publish refused | `chain: entry has no publish token (required)` (registry 500) |
| (b) control: durable-Publisher refused | `chain: entry carries a durable Publisher (records permanent linkage…)` |
| (b) positive: tokened publish commits | val1 stdout `chain: committed block N`; chain head advances |
| (b) positive: **unlinkable** on the wire | committed entry in `chain.cbor` carries a `Token`+`Serial` and a **zero-NodeID** Publisher (32 zero bytes) |
| (b) double-spend rejected | unit guard present (`ErrTokenSpent` / issuer spent-set) — **see FINDING 1** |

## FINDINGS

### FINDING 1 — the double-spend rejection has NO CLI/wire seam (confidence: high)

The claim "double-spends are rejected" is **real in core** but is **not reachable
from the product's CLI**. Two independent guards exist:

- `core/chain/chain.go`: `ErrTokenSpent = "publish token serial already spent
  (double-spend)"` — a committed serial is added to `c.spent`; a later entry with
  the same serial is rejected chain-wide.
- `core/node/tokenrole.go`: the online issuer keeps a `creditSpent` set and
  refuses a reused credit serial.

But `silt swarm add -token-quorum N` mints a **fresh random serial**
(`blindtoken.NewSerial`) on *every* invocation, and there is **no flag** to
present a saved/replayed token or serial. So two `swarm add` calls never collide,
and no CLI path drives a token or credit into either spent-set a second time.

**Repro of the gap:** publish twice with `-token-quorum` — both commit (distinct
serials); there is no way to reproduce a double-spend over the wire. The harness
therefore asserts the guard at the **unit** level:
`go test ./core/blindtoken/ ./core/chain/ -run 'DoubleSpend|Spent|Token'`
(covers `TestPublishTokensGateAndPreventDoubleSpend`,
`TestForgedTokenCannotSpend`) and reports the wire gap here.

*Suggested fix for the builder:* let `swarm add` accept a caller-supplied
token/serial (or emit the acquired token to a file that a second `add` can
replay), so the anti-double-spend property is demonstrable — and red-team-able —
over the real wire, not only in unit tests.

### FINDING 2 — per-byte credit accounting is sim-only, not on the daemon (confidence: high)

Claim (a)'s economics — per-byte earning, balances, `CanPublish`, the
`ErrInsufficientCredit` gate, Gini — live entirely in `sim/economy.go` over an
**in-process** `credit.Ledger` driven by the simnet cluster. The **real daemon
exposes no wire/CLI seam** for a served-byte credit balance: no `silt balance`,
no credit field on `chain-status`, and the `registry.NewGated(ledger)` credit
gate is wired only in the sim, not behind `-serve-registry`. On a live swarm a
holder that serves chunks and a freeloader that only fetches are, as far as any
CLI can observe, indistinguishable in standing. `core/credit`'s own header notes
the ledger is "naive and gameable BY DESIGN" — so this is expected for M5, but
the claim's *earning/broke* half is only demonstrable via the sim, which is what
this harness asserts.

## Poking at a running topology (`KEEP=1`)

```sh
KEEP=1 ./integration/economy/run.sh
cd integration/economy
docker compose -p economy ps
PEERS="171e68…@10.80.0.11:4001,49a396…@10.80.0.12:4001"     # val1,val2 (see run.sh)
REG="171e68…@https://10.80.0.11:4003"
docker compose -p economy exec holder sh -c \
  "head -c 65536 /dev/urandom > /tmp/f.bin; silt swarm add /tmp/f.bin -token-quorum 2 -peers '$PEERS' -registry '$REG'"
docker compose -p economy exec val1 silt chain-status -store /data
docker compose -p economy down -v
```

## What each piece is

| file | role |
|------|------|
| `docker-compose.yml` | flat-net topology: 2 validator/issuers, a holder, a freeloader |
| `Dockerfile` | slim runtime image (silt binary + iproute2), one image for all roles |
| `node.sh` | entrypoint: reads the container's own IP and self-advertises it, then execs the daemon |
| `run.sh` | the driver: build → sim → bring up → controls → tokened publish → unlinkability proof → tear down |
