# The user seam — silt's operational surface

This is the complete set of operations a person can perform with silt, by
role. It is the **contract** the QA phase works against: the
[acceptance team](reviews/m0-acceptance-brief.md) verifies every operation
here *works as described*, and the [red team](reviews/m0-redteam-brief.md)
attacks the same surface *deeper*. If an operation you need isn't here, that
is a gap worth reporting.

Every role is a **capability any node can offer**, not a special binary — one
`silt` binary, different flags. To build the multi-node network shapes these
operations assume, see **[test-topologies.md](test-topologies.md)**. For the
guided narrative versions, see [`v1-test.md`](v1-test.md) (single box) and
[`local-test-network.md`](local-test-network.md) (a real localhost swarm).

## The store directory (what persists)

A daemon's `-store <dir>` holds everything that must survive a restart. Knowing
its contents is how you reason about "does standing/content/identity come back?"

| In `<store>/` | Holds | Restored on restart so that… |
|---|---|---|
| `objects/` | the chunks this node stores | it keeps serving what it holds |
| `proofs/` | each chunk's storage proof (#69) | it re-announces coded shards under the right key and can answer audits |
| `plot/` | the validator's proof-of-space-time bond plot (#93) | its consensus standing survives without re-plotting |
| `issuer/issuer.key` | the publish-token issuer RSA key | tokens it signed stay verifiable; peers' cached keys don't go stale |
| `chain.cbor` | the committed block history (a single CBOR file) | the replica rejoins at its height, not from genesis |
| `identity.pem` | the node's keypair (NodeID = hash of the pubkey) | its reputation is not transplantable and survives restart |

---

## Role 1 — Client (ephemeral user)

A client keeps nothing: it joins, does the thing, and leaves. The swarm keeps
the data.

| Operation | Command | Expected result | How to verify |
|---|---|---|---|
| Add a local file | `silt add FILE` | prints the Merkle root | `silt ls` lists it; `objects/` has the shards |
| Retrieve locally | `silt get ROOT -o OUT` | writes OUT, re-verifying every shard | `OUT` is bit-identical to the input |
| Inspect | `silt info ROOT` | the stripe map (each shard, stripe, presence) | shard count matches the erasure geometry |
| Private mode | `silt add FILE -mode private` | random per-file key, no dedup | adding the same file twice gives *different* roots |
| Custom geometry | `silt add FILE -k 4 -n 7` | 4-of-7 erasure coding | `info` shows 7 shards/stripe, needs any 4 |
| Publish to a swarm | `silt swarm add FILE -peers ID@ADDR -registry ID@https://…` | scatters to the swarm, registers, prints a `silt:` link + a `siltcare:` care link | fetch from a *different* node returns it bit-perfect |
| Retrieve from a swarm | `silt swarm get LINK -o OUT -peers … -registry …` | assembles from the swarm | works even after the publisher has left / a node died |
| **Unlinkable publish** | `silt swarm add FILE -token-quorum K …` (against validators running `-require-tokens K`) | the registry entry carries a blind-signed token, **no Publisher identity** | the committed entry has no Publisher field; the fee charged your identity but the token doesn't name it |

**The link is the primitive.** A `silt:v1:ROOT:KEY` link retrieves *and*
decrypts. A `siltcare:v1:ROOT:KEY` link (which `swarm add` also prints, and
which a full link degrades to) lets a caretaker repair and audit forever
**without** being able to read the bytes.

---

## Role 2 — Registry operator

The registry maps a root to its file record. In v1 one honest daemon serves it
over key-pinned HTTPS; the chain (Role 4) is the decentralized replacement.

| Operation | Command | Expected result | How to verify |
|---|---|---|---|
| Serve a registry | `silt daemon -serve-registry HOST:PORT …` | prints `registry: serving ID@https://HOST:PORT` — **copy it verbatim** | clients pass that exact `ID@https://…` ref |
| Use a registry | any client/daemon flag `-registry ID@https://…` | publishes/looks up against it | a bare `http://` or unkeyed `https://` is refused (key-pinned) |

---

## Role 3 — Storage / public node operator (daemon)

| Operation | Command / flag | Expected result | How to verify |
|---|---|---|---|
| Run a node | `silt daemon -listen HOST:PORT -store DIR` | prints its `peer: ID@ADDR` bootstrap line | it accepts stored chunks and serves fetches |
| Pledge capacity | `-capacity 2G` | bounds how much it hosts (not staging) | `pledge: used/total` line; refuses stores past the cap |
| Join a swarm | `-bootstrap ID@ADDR[,…]` | learns peers, fills its routing table | prints a bootstrap-complete line |
| Web UI | `-ui HOST:PORT` | a dashboard of what it holds/serves | open it in a browser |
| NAT: lean on a relay | `-relay-via RELAYID@HOST:PORT` | a NATed node becomes reachable through the relay | `relay-via: registered` line |
| NAT: offer relay | `-relay HOST:PORT` | content-blind splice for NATed peers (capped) | two NATed peers exchange data through it |
| NAT: advertise | `-advertise HOST:PORT` | stamps a dialable endpoint on outgoing messages | peers dial it directly / hole-punch to it |
| LAN discovery | `-mdns` (default on) | finds peers on the local network | a second daemon on the LAN is found without `-bootstrap` |
| Caretake content | `-care siltcare:…[,…]` | repairs & audits those files as nodes churn, no decryption | a shard deleted elsewhere is rebuilt from parity |
| Operator takedown | `-denylist FILE` (roots, one per line) | refuses to store/serve those roots | that root stops serving *here*; other operators are unaffected |
| Restart survival | stop, then rerun with the **same** `-store DIR` | reloads objects + proofs; re-announces | its content is discoverable and served again |

Detailed walkthroughs: [`local-test-network.md`](local-test-network.md) (local
swarm + node death) and [`cross-network-runbook.md`](cross-network-runbook.md)
(genuine cross-NAT, now automated by `integration/nat/`).

---

## Role 4 — Trust / validator operator (the M0 surface)

This is where consensus standing is **earned** and publishing stays unlinkable
— the heart of the trilemma, and the surface the QA phase exists to probe. The
commands below are the actually-tested flow (see `e2e/e2e_test.go`
`TestBondEarnedStandingCommitsOverTCP`).

### Earn standing and commit through consensus

```sh
# Validator A: registry + validator + a storage bond. -min-rep 100 means
# standing must be EARNED (no -quorum 0 rubber-stamp). Fast bond audit so
# standing accrues quickly. Name B (a deterministic id) as an attester.
silt daemon -listen 127.0.0.1:7101 -serve-registry 127.0.0.1:7100 -store dA \
  -validator -min-rep 100 -quorum 1 -attesters <ID_B> \
  -bond 8M -bond-audit 1s -capacity 1G

# Validator B: joins A, runs its own bond. Each audits the other, so each
# earns the other's standing in its own ledger.
silt daemon -listen 127.0.0.1:7102 -store dB -bootstrap <A> \
  -validator -min-rep 100 -quorum 1 -bond 8M -bond-audit 1s -capacity 1G

# Publish through consensus: the entry commits only once the bond audits have
# earned real standing on both sides (a few 1s rounds). A success is a genuine
# quorum commit on earned standing — NOT a self-commit.
silt swarm add FILE -peers <A> -registry <regRef>
```

| Operation | Flag(s) | Expected | How to verify |
|---|---|---|---|
| Be a validator | `-validator` | keeps a chain replica, proposes/attests | prints `chain: committed block N` on a commit |
| Seal a bond | `-bond 8M` | plots an identity-bound space-time bond, persists it | `plot/` appears; standing rises after audits |
| Bond audit cadence | `-bond-audit 1s` | how often it challenges peers + refreshes its own standing | standing decays if a peer stops answering |
| Require earned standing | `-min-rep 100` | proposers/attesters must clear the bar | an unbonded node's publish is **refused** |
| Quorum | `-quorum K` | attestations (excluding proposer) to commit | fewer than K → no commit |
| Attesters | `-attesters ID[,…]` | who a proposer gathers attestations from | — |
| **Unlinkable issuance** | `-require-tokens K` | this validator blind-signs publish tokens; the chain accepts only tokened, Publisher-less entries | a committed entry carries a token, no Publisher |
| Training wheels | `-anchors ID[,…] -anchor-quorum A -mature-validators M` | a young network's commit also needs anchor sign-off, until M distinct independents have attested — then it sheds automatically | before maturity an anchorless quorum is refused; after, it commits |
| Trusted mode (opt out of privacy) | `-allow-publisher` | permits a durable Publisher→root record | off by default; only for explicitly trusted deployments |
| **Restart survival** | stop, rerun with the same `-store DIR` | bond plot + issuer key + chain reload | it is a validator again immediately — **no re-plot**, standing intact |

### What the QA phase should exercise here (roadmap #52 field test)

- **Convergence** — several validators, a publish, every replica agreeing on
  the committed history.
- **Fault tolerance** — kill one validator mid-flight; a quorum of the rest
  still commits.
- **Restart-standing** — restart a validator; it rejoins with standing intact
  (the persisted plot), no re-plot delay.
- **The three M0 denials** — see [the red-team brief](reviews/m0-redteam-brief.md)
  and the design doc's §6.

---

## Cross-cutting

| Concern | How |
|---|---|
| Identity | a keyfile in `<store>/identity.pem` (persistent), or `-id-seed N` for scripted/deterministic demos |
| Logging | `-log info` (narrates placements/commits/repairs) or `-debug` (firehose) → `<store>/debug.log` |
| The whole thing in one process | `silt sim run <scenario>` and `silt net demo -nodes N` — deterministic, no sockets |

## Honest boundaries

The trust-plane operations are **built and internally tested**, not yet
independently reviewed. Known limits are recorded in the CHANGELOG (search
"honestly labelled") and design doc §6 — e.g. the publish anonymity set, the
locally-qualified fork-choice weight, and lock-on-attest liveness. Reporting a
*new* gap beyond those is exactly the point of the QA phase.
