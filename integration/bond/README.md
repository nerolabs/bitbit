# Proof-of-space-time bond-cost field test (Docker)

Field test **#8**. Turns the M0 claim **C1 ("no discount")** into NUMBERS. C1
says a validator's Sybil-resistance bond is genuinely **expensive to fake**: to
hold consensus standing a node must generate a real depth-robust plot on real
disk and prove **space-TIME** on it over time — a shortcut (undersized, no
plot, released plot) earns nothing. This harness stands up **real silt
daemons in containers** that seal **real plots on real disks**, then measures
the cost and proves the shortcuts are rejected.

```
                         network bond (10.100.0.0/24)
   honest (10.100.0.10) ─────────────────────────────── peer (10.100.0.11)
     -validator, seals a real ${BOND_SIZE}      -validator, MUTUALLY audits honest
     plot on /data/plot, answers live           over the wire (recomputes labels
     space-time challenges                       from H(pk,n) — no plot held)

   adversary (10.100.0.12)  ── NEGATIVE control: 4K "bond", -lowbond-propose
     an under-bonded validator whose well-formed proposal honest REFUSES to attest
```

Both real daemons run the **legacy subjective path** (`-objective=false`, like
`examples/flow4-earned-standing.sh`) so a small two-validator cluster commits
without the objective path's `-anchors`/`-mature-validators` launch set. The
bond MECHANISM under test — seal, on-disk plot, live space-time challenge,
earned standing, and refusal of a shortcut — is identical on both paths.

## Run it

```sh
./integration/bond/run.sh            # build, run all phases, tear down; exit 0 = PASS
BOND_SIZE=128M ./integration/bond/run.sh   # bigger bond → longer plot, more disk (the point)
KEEP=1 ./integration/bond/run.sh     # leave the positive topology up to poke at
```

Needs Docker and a Go toolchain. The `silt` binary is compiled **on the host**
(CGO off → trivial linux cross-compile) and copied into a slim image, exactly
like `integration/nat` — tiny image, no in-Docker Go-build memory spike. The
binary is `.gitignore`d and never committed.

## What each piece is

| file | role |
|------|------|
| `docker-compose.yml` | one bridge network; `honest` + `peer` real validators, `adversary` (profile) the under-bonded negative control |
| `Dockerfile` | slim runtime image (silt binary + coreutils for `du`/`stat`/`date`); one image, all roles |
| `run.sh` | the driver: build → PHASE 0 sub-floor refusal → PHASE 1 seal+measure+verify → PHASE 2 under-bonded refusal → assert → tear down |

## The three phases (real observed behavior)

Every assertion keys off a **real** CLI flag, stdout line, or `<store>/debug.log`
line — confirmed against `core/bond/`, `core/node/bondaudit.go`, and
`cmd/silt/daemon.go`, not invented:

- **PHASE 0 — NEGATIVE (sub-floor → no standing).** A 16M bond under a 64M
  `-min-bond-floor` must **fail closed at startup** rather than run a validator
  that silently earns nothing (M0 F1/F2 anti-release). Asserts nonzero exit +
  the real refusal string
  `bond: -bond (16M) is below the anti-release floor (64 MiB), so this validator would earn NO standing…`.

- **PHASE 1 — POSITIVE + MEASUREMENT.** `honest` seals a real `${BOND_SIZE}`
  plot (`bond: sealed a … storage bond for consensus standing`). The driver
  measures **plot wall-clock time** (up→sealed), **on-disk plot bytes** (the
  single `<store>/plot/<nodeid>.plot` file), and reads the live verify verdicts:
  `peer` challenges `honest` over the wire and logs
  `bond challenge peer=<honest> passed=true standing=<N>`, while `honest`
  self-narrates rising `standing self=<honest> reputation=<N>`. Verification is
  O((Samples+5k)·log n) — cheap even for a big plot.

- **PHASE 2 — NEGATIVE (under-bonded → refused).** The 4K-"bonded" `adversary`
  proposes a well-formed block to `honest` (`-lowbond-propose`, a documented
  red-team seam). An honest validator refuses to attest a proposer without a
  qualifying bond:
  `adversary: lowbond-propose proposal correctly REJECTED by <honest>`.

`run.sh` `trap`s `docker compose down -v` on exit (unless `KEEP=1`), so it
leaves nothing running.

## Measured numbers (Apple-silicon host, Docker Desktop, linux/arm64)

The `standing`/`reputation` a bond earns is **proportional to its size**
(reputation ≈ bond-MiB × 16), and the on-disk plot equals the bond size
byte-for-byte (plus a small fixed header) — the resident cost C1 charges:

| `-bond` | plot seal (pure `Seal`, in-container) | on-disk plot | earned reputation |
|--------:|--------------------------------------:|-------------:|------------------:|
| 16M     | ~0.13 s                               | 16 MiB       | 256               |
| 64M     | ~0.36 s                               | 64 MiB       | 1024              |
| 128M    | ~0.69 s                               | 128 MiB      | 2048              |
| 512M    | ~2.82 s (≈181 MB/s)                   | 512 MiB      | 8192              |

(The end-to-end `run.sh` PHASE-1 wall time is ~0.65 s for 64M **including**
container + daemon start.) These numbers are hardware-dependent — the plotting
throughput is the quantity the anti-release floor must be set against (see the
FINDING in the PR: `-min-bond-floor` should exceed challenge-window ×
*this host's* plot throughput, and 181 MB/s ≠ the 270 MB/s in the flag's help).

## Poking at a running topology (`KEEP=1`)

```sh
KEEP=1 ./integration/bond/run.sh
cd integration/bond
docker compose exec honest sh -c 'grep "standing self" /data/debug.log | tail'
docker compose exec peer   sh -c 'grep "bond challenge" /data/debug.log | tail'
docker compose exec honest sh -c 'du -h /data/plot/*.plot'
docker compose --profile adversary down -v      # tear down when done
```
