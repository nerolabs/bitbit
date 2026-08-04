# M0 fix — unlinkable token issuance, D3 (red-team F4)

> **Status: design doc, ahead of code (the design turn).** The external M0
> red-team broke the Privacy corner: the blind-signature crypto is sound, but it
> is invoked over a **non-anonymous transport with a per-request durable-account
> debit**, so a colluding validator minority de-anonymizes the publisher at token
> acquisition. This doc specifies D3 — the issuance-mixing layer that
> `gate4-m0-mechanism.md` §3c/§4 promised and never shipped.
>
> Report: `docs/reviews/M0-REDTEAM-REPORT.md` §4 (F4). **Independent of the bond
> fix** (`m0-sybil-bond.md`) and the consensus fix (`m0-consensus.md`) — can ship
> in parallel.

## 1. What broke, precisely

Two of three privacy layers hold; the third was never built.

- **Layer 1 — Publisher-less ledger (holds).** The default entry carries no
  `Publisher`; the chain refuses one unless `AllowPublisher` (`chain.go`
  `ErrPublisherEntry`). The blind token (`core/blindtoken`, RSA-FDH) proves "a
  quorum authorized *a* publish" without revealing *which* requester — it hides
  the **serial**, correctly.
- **Layer 2 — ephemeral publish announce (holds).** A publish announces from a
  fresh throwaway identity (`joinSwarm` → `identity.Generate`, `daemon.go:749`),
  marked `SetEphemeral(true)` so peers don't route-table it.
- **Layer 3 — issuance mixing (BROKEN, never shipped).** `AcquireToken`
  (`core/node/tokenrole.go:92`) requests blind signatures **directly from the
  bonded identity's own transport**, in real time. The issuer handler
  `answerTokenRequest` (`tokenrole.go:68`) receives the requester NodeID `from`
  and calls `ChargePublish(from)` (`credit.go:235`), **debiting the durable
  standing account**.

The break is a **two-link de-anonymization** available to a colluding issuer
minority (or a network observer):

1. **Network link.** The blinded request arrives over the requester's own TLS
   transport (`tcpnet`, NodeID = hash of its cert): the issuer sees the requester
   IP + the near-simultaneous ephemeral publish → correlates by IP + timing. No
   relay, no mix, no epoch batching, no rotation.
2. **Ledger link.** `ChargePublish(from)` debits the durable account at request
   time — a second, independent, on-ledger link from the standing key to the
   publish, regardless of the network path.

Because there is **no epoch batching and no enforced canonical validator set**,
the anonymity set is a **singleton** — the one identity that asked at 14:03:07
from that IP — not the "narrowed set" the CHANGELOG claims. Sound crypto over a
non-anonymous transport = no unlinkability.

## 2. The fix — D3, four parts

The blind-signature crypto stays. Everything that de-anonymizes is *around* it:
the transport, the timing, the validator-subset choice, and the fee. Fix all four.

### 2a. Route issuance over the content-blind relay, from an ephemeral identity

Reuse the Gate-3 relay (`adapters/relay`) — the same content-blind splice that
already carries publish/fetch traffic. The token request goes
`ephemeral → relay → issuer` via `relay.DialThrough` (`client.go:21`):

- The issuer's transport sees the **relay**, not the requester's IP.
- The requester dials from a **fresh ephemeral identity** (the Layer-2 pattern),
  so even the relay + issuer see only a throwaway NodeID for the request.
- The relay learns only `(ephemeral, issuer)` metadata and opaque TLS bytes — it
  cannot read the blinded serial, and the ephemeral ID ties to nothing durable.

This severs the **network link**: there is no durable IP for the issuer to
correlate. It forces the fee off the `from` path (§2d), because `from` is now
ephemeral and un-chargeable.

### 2b. Epoch-batch issuance

Add an **epoch scheduler** (new, e.g. `core/publishtoken/epoch.go`): requests are
buffered and issued at discrete epoch boundaries, so the anonymity set becomes
**"every identity that requested a token this epoch,"** not the one who asked at a
timestamp. This severs the **timing link** that survives the relay (a relay alone
still leaks request time). Epoch length is a free variable trading latency for
anonymity-set size — state the target set size (§5).

### 2c. Canonical validator set

`AcquireToken` today takes an arbitrary `validators []NodeID` list (`tokenrole.go:92`).
A colluding minority narrows the set by *which* subset a given publisher happened
to ask. Fix: **every publisher requests from the same canonical k-of-N set**
(derived from chain state, e.g. the top-bonded validators at the epoch), so the
subset choice leaks nothing. This closes the subset-correlation side channel and
makes "k-of-N distinct qualified validators" a property any observer computes the
same way.

### 2d. Decouple the fee from the request (the crux)

The ledger link is independent of the network path, so the relay + epoch batching
do **not** fix it alone. `ChargePublish(from)` must leave the per-request path.

**Design: prepaid, aggregate publish credits.** The durable identity tops up a
**publish-credit balance in bulk**, in a transaction decoupled *in time and
network path* from any individual publish. At issuance, the ephemeral requester
presents a **blinded credit** proving "*a* valid prepaid credit exists" without
revealing which durable identity minted it — the issuer verifies the credit, not
a live debit against `from`. The standing spend happens at top-up time (linkable
to the durable key, but not to any specific later publish); the publish itself
debits nothing traceable.

This is a **blinded-voucher / Chaumian e-cash** shape layered on the existing
RSA-FDH machinery: mint blinded credits at top-up (debit standing in bulk), spend
one blindly at publish. **Adopt, don't invent (B8):** pick a published compact
e-cash / blind-voucher scheme with double-spend prevention (the issuer's existing
`spent` set generalizes to credit serials) rather than rolling a bespoke protocol.
Flag the exact scheme as the one real crypto sub-problem in D3.

## 3. The composition, after the fix

```
top-up (rare, linkable to durable key, decoupled from any publish):
    durable standing ── debit in bulk ──▶ mint {blinded publish credits}

publish (per file, unlinkable):
    ephemeral id ──relay──▶ canonical k-of-N issuer set
        request:  blind(serial)  +  blinded credit (proves prepaid, hides minter)
        issued at epoch boundary, batched with all other requests this epoch
    ◀── k blind sigs ── unblind ── tokened, Publisher-less entry (Layer 1)

observer sees: an ephemeral id, via relay, at an epoch tick, from the canonical
    set, spending a credit that ties to no specific durable key.
    anonymity set = every requester in the epoch.
```

Layers 1 and 2 are unchanged and still hold; D3 adds the relay hop, the epoch
scheduler, the canonical set, and the credit decoupling.

## 4. Schema + persistence touch

- **Publish-credit record** (blinded voucher serials + issuer spent-set) — new
  state; if credits are recorded/enforced on-chain, the entry/credit record rides
  `Block.Version` (#98). If credits are issuer-local (like the current blind-token
  `spent` map), no block-schema change — but then issuer-key persistence and
  on-chain issuer registration (`gate4-m0-mechanism.md` §3d) must land so the
  canonical set is verifiable, not a single-host secret.
- **Epoch scheduler** state must survive restart (buffered requests, current
  epoch) or degrade safely (drop-and-retry next epoch).
- **Canonical validator set** derives from chain state — no new persistence, but a
  deterministic selection rule every node computes identically.

## 5. Falsifiable denial + regression (invert the PoC)

**Denial (V3):** given the full ledger, the validators' issuance logs, and network
traces across machines, the adversary's link accuracy from a target root to its
standing key is **≤ chance within the epoch anonymity set**.

Invert the red-team PoC as regression (assert the link FAILS):

| Red-team PoC (asserts BROKEN) | Regression (assert DENIED) |
|---|---|
| `sim/redteam_privacy_issuance_test.go::TestRedteamIssuanceLayerLinksPublishToStanding` | issuer logs + traces no longer link publish→standing better than `1/|epoch set|` |

Add probes per layer: (a) the tokened entry carries no `Publisher`; (b) the
request arrives via relay from an ephemeral id, not the durable transport; (c)
issuance is epoch-batched (assert ≥ target set size before issue); (d) no
`ChargePublish` fires on the per-request path — the fee is a prior bulk top-up.

## 6. Open risks to hand the red-team

- Does epoch batching hold under a **low-traffic** network where an epoch's set is
  1–2 requesters? (Set a minimum-set-size floor, or delay issuance until met —
  and document the availability/anonymity trade-off honestly.)
- Can a colluding relay + issuer minority still correlate by **byte-count / packet
  timing** through the splice? (The relay caps and pads are worth measuring.)
- Does the blinded-credit scheme leak a link at **top-up** (bulk debit size,
  timing) that narrows later publishes? Bound the top-up→publish correlation.
- Canonical-set churn: if the set changes between top-up and spend, does a credit
  minted for an old set still verify without leaking the minter?

## 7. Build sequencing (code is the next turn)

1. Route `AcquireToken` over `relay.DialThrough` from an ephemeral identity; issuer
   handler accepts relay-forwarded requests.
2. Epoch scheduler + minimum-set-size floor.
3. Canonical validator-set derivation from chain state; enforce at `AcquireToken`.
4. Blinded publish-credit mint/spend (adopt a published e-cash scheme); move the
   debit to bulk top-up; retire the per-request `ChargePublish(from)`.
5. Invert the red-team PoC; add per-layer probes; multi-machine trace test.
6. Whole-suite pass (`-race` on `core/node` + `core/blindtoken` + sim privacy).
