# Design: cross-network reachability

**Status:** largely built (issue #27). What began as a design checkpoint is now
mostly code: the relay is built and field-proven, and TCP hole-punching is
**proven through a real cone NAT** (falling back to relay on symmetric NAT) in
an automated Docker NAT harness gated in CI. This doc has been reconciled to
that reality; it keeps the rendezvous-vs-NAT-traversal split because they remain
two distinct mechanisms. It covers the two things standing between an impressive
localhost demo and two real people on separate home networks using Silt:
**rendezvous** (how a fresh node finds anyone) and **NAT traversal** (how two
nodes behind home routers actually connect). It also draws the line — sharpened with Andrew,
2026-07-26 — between the *relay capability* (which belongs in the binary)
and *running public infrastructure* (which SiltHQ does not do, but which we
must stand up as throwaway dev scaffolding to build against).

## The problem

Everything in [local-test-network.md](../local-test-network.md) works
because every node is on `127.0.0.1` — mutually reachable by construction.
Move two daemons onto two real home networks and both assumptions break:

1. **They can't find each other.** Discovery today is `-bootstrap ID@host:port`,
   `-dns-seed` TXT records, then peer-exchange from there. All three need a
   *first* reachable peer whose address you already know. On a laptop behind
   a router, you don't have one to hand out.
2. **They can't connect even if they did.** A home node sits behind NAT: it
   has no stable public `host:port`, and inbound TCP to it is dropped by the
   router unless something opened a mapping. Node A dialing node B's LAN
   address (`192.168.1.x`) reaches nothing.

These are two distinct problems and they want two distinct mechanisms.
Conflating them ("just run a public server") is what pushes projects into
owning central infrastructure. Kept separate, most of the work is neutral
protocol code and only a thin, replaceable sliver is "a reachable box."

Prior art we're deliberately learning from: libp2p's stack splits the same
way — **AutoNAT** (am I reachable?), **Circuit Relay v2** (relayed
connections), **DCUtR** (hole-punching to upgrade a relay into a direct
link), plus DHT/mDNS for rendezvous. We adopt the *shape*, not the
dependency.

## Part 1 — Rendezvous (finding a first peer)

A node needs one reachable peer to bootstrap into the DHT. Layers, cheapest
and most neutral first:

| mechanism | reach | who runs it | notes |
|-----------|-------|-------------|-------|
| **mDNS / local discovery** | same LAN | nobody | free win for two nodes in one house; zero infra |
| **`-bootstrap ID@host:port`** | anywhere | the peer itself | already have it; needs a *reachable* target (see Part 2) |
| **`-dns-seed <domain>`** | anywhere | whoever owns the domain | already have it; TXT records list `ID@host:port` seeds |
| **peer exchange** | anywhere | nobody | already have it; grows the address book once you have *one* peer |

The only genuinely missing piece here is **mDNS** (`_silt._udp.local`) —
cheap, no infrastructure, and it makes the two-Macs-in-one-house case work
with zero configuration. Worth doing regardless of the NAT work.

For nodes on *different* networks, rendezvous reduces to: **someone
publishes a `-dns-seed` domain whose TXT records point at reachable
peers.** That is a community act (any domain owner can do it), not a Silt
service. Our job is to keep `-dns-seed` a value you point at — **never a
default baked into the binary.** A hardcoded default seed would quietly make
whoever runs it *the* de-facto rendezvous — exactly the central operator we
refuse to be. See "Neutrality" below.

## Part 2 — NAT traversal (connecting through home routers)

Three techniques, in increasing order of what they buy and what they cost:

### 2a. Self-help: UPnP / NAT-PMP / PCP
Ask the router to open a port mapping for us. When it works (many consumer
routers, opt-in), the node becomes *directly* reachable and needs no relay
at all. Cheap, and it strictly reduces relay load. It fails silently on
carrier-grade NAT and locked-down routers, so it's an optimization, never
the foundation. **Ship it, don't depend on it.**

### 2b. Reachability detection (our AutoNAT)
A node cannot assume it's reachable. Before advertising a direct address it
should *verify* one: ask a peer "dial me back at the address you see me
coming from." If the callback lands, the node is public — advertise the
direct address, done. If it times out, the node is NATed and must fall back
to a relay. This one check decides everything downstream, and it's a few
messages over the existing transport. It also yields the node's
public-facing `host:port` as observed by others (STUN-style) — **now live:**
the relay reports it back in the register-ack, the node reads it via
`relay.Client.Observed()` / `node.ObservedAddr()`, and a peer learns it when
the relay coordinates a hole-punch. Both 2c and 2d need it.

### 2c. Relay (the universal fallback)
When both peers are NATed, neither can accept an inbound connection — but
both can make *outbound* ones. So route through a third node that *is*
reachable:

```
   NATed A ──outbound──►  Relay R  ◄──outbound── NATed B
                    (forwards opaque frames A↔B)
```

- B, being NATed, keeps a standing outbound connection to R and *registers*:
  "reach me via R." (This is just B using R as its advertised address.)
- A wants B, learns "B is at R," dials R, asks R to forward to B's
  registered connection. R splices the two streams.
- **R never decrypts anything.** All Silt swarm traffic is already
  mutual-TLS + ciphertext end to end; the relay moves opaque frames and
  learns only that two node-IDs are talking and roughly how much. That
  metadata exposure is real and goes in the threat model — but a relay is
  no more a content risk than any router on the path.

Relaying is a **node capability**, not special infrastructure: any daemon
that has verified itself reachable (2b) can offer to relay, gated by config
(`-relay` opt-in, with rate/bandwidth caps so it can't be trivially abused).
This is the crucial point for neutrality — "the public node" is just a
daemon running `-relay` somewhere reachable. The *code* is universal; only
the *placement* (a box with a public IP) is special, and anyone can provide
that box.

### 2d. Hole-punching (upgrade relay → direct) — BUILT, proven
A relay works but pays bandwidth for the whole transfer. Once two NATed
peers are talking *through* R, R coordinates a simultaneous-open: both
sides fire outbound SYNs at each other's observed public `host:port` at
the same instant (from the same local port their relay registration used,
via `SO_REUSEPORT`, so the NAT reuses the mapping R observed), and many NATs
(full-cone, restricted-cone) let the crossing SYNs establish a direct path —
after which R drops out, demoted to rendezvous. This is DCUtR's trick. It
fails on symmetric NAT, which stays on the relay. **Implemented and
end-to-end proven:** the punch primitive establishes a direct connection
through a real cone NAT and correctly fails on symmetric (relay fallback),
gated in CI (`nat-holepunch` job over the `integration/nat` Docker harness).
The make-or-break detail the harness surfaced: a real NAT firewall must
*drop* unsolicited inbound (stealth), not RST it, or the first crossing SYN
is refused instead of retransmitted. Code: `adapters/tcpnet/holepunch.go`
(`HolePunch`, `reuseControl`), relay coordination in `adapters/relay`
(`punch` op, `coordinatePunch`, `RequestPunch`, `Observed`). (The live
two-daemon *upgrade* is wired but not yet green end-to-end — blocked on a
provider-discoverability issue in the minimal harness, not the punch itself.)

## Where this lives in the code

Almost all of it is **adapters**, not core — the core stays oblivious to how
bytes reach a peer, exactly as it's oblivious to disk vs. memory today.

| piece | home | new? |
|-------|------|------|
| mDNS local discovery | `adapters/discovery` | new, small |
| reachability check (2b) | `adapters/discovery` or a new `adapters/reachability` | new |
| UPnP/NAT-PMP (2a) | new `adapters/portmap` (build-tagged, no cgo) | new |
| relay transport (2c) | `adapters/tcpnet` as a relayed `Transport`, or a sibling `adapters/relay` | new, the bulk |
| hole-punching (2d) | `adapters/tcpnet/holepunch.go` + relay coordination in `adapters/relay` | **built, proven (cone → direct; symmetric → relay), CI-gated** |
| `-relay`, `-mdns`, `-dns-seed` wiring | `cmd/silt/daemon.go` | flags only |

The relay adapter should satisfy the *same* `ports.Transport` the swarm
already uses, so `distributeFrom` / `fetchColumn` / repair don't know or
care that a given peer is reached through a relay. That keeps the durability
backbone (columns, domains, dispersion) completely untouched.

## Neutrality: the dev node vs. the project

This is the part that needs to be gotten right on purpose, not by accident.

**What SiltHQ is:** a project that publishes source and occasionally cuts
binaries. It runs no network, keeps no denylist, holds no override, operates
no privileged node. That stance is the product's whole credibility.

**The tension:** you cannot *develop* relay/NAT traversal without a
publicly-reachable box to develop against — localhost has no NAT. So we need
public infrastructure to build, before any community exists to provide it.

**The resolution** (three separable things, only the middle one is
delicate):

1. **The relay/NAT *capability* → in the binary.** Neutral by construction
   (content-blind, any node can do it). No governance issue; it's just code.
2. **A public node to develop against → throwaway dev scaffolding.** A
   personal dev deployment — a $5 VPS or a tunnel (Fly.io / Tailscale /
   cloudflared) running `silt daemon -relay` — that Andrew stands up to
   iterate against. It is **not** "the Silt network," not operated by the
   project-as-entity, not privileged in the protocol, and explicitly
   temporary.
3. **Community public peers → the goal.** Many operators run reachable
   `-relay` daemons and publish `-dns-seed` domains; the dev node is torn
   down or becomes just one unremarkable peer among them.

The line is **not** "no one may run a public node" — the network *is* node
operators; a developer running one is simply an operator. The line SiltHQ
protects is "the code-publisher is not a central operator or gatekeeper." A
dev deployment stays on the right side of that line as long as it is:

- **not privileged** — nothing in the protocol treats it specially;
- **not default** — no ID/domain of it is compiled into the binary;
- **not marketed as "official"** — it's dev scaffolding, said plainly;
- **not permanent** — it exists to build against and is replaced by
  community peers.

Concretely, to keep the accident from happening:

- Dev-infra deploy scripts live in a **separate `deploy/` directory or a
  separate repo**, clearly headed *"development/test infrastructure — not
  operated by the Silt project; replaced by community peers."* They are not
  part of the `silt` binary's build.
- **No baked-in seed or relay.** `-dns-seed` and `-bootstrap` stay
  arguments. If we ever ship an *example* seed value, it's in docs as
  "here's a dev seed you can try," never a default the binary reaches for on
  its own.
- The threat model gains a short section: relays and rendezvous see peer
  IPs and traffic timing (metadata), consistent with Silt's existing "not
  anonymous" honesty; relays never see plaintext.

## Open questions (flagged, not solved here)

- **Relay incentives.** Relaying spends bandwidth for someone else's
  transfer. Long-term this should earn credit like serving does (the
  bandwidth-gossip pattern extends to it); short-term, `-relay` is
  altruistic + rate-capped. Don't block the first cut on the economics.
- **Relay abuse.** An open relay invites bandwidth leeching and amplification
  games. Mitigations: per-peer rate/bandwidth caps, reachability-verified
  peers only, opt-in. Enough for dev; revisit for launch.
- **IPv6.** Many home networks now hand out globally-routable IPv6. Where
  both peers have it, there may be *no NAT to traverse* — try a direct v6
  dial before assuming a relay is needed. Cheap latent win.
- **Symmetric NAT.** *Settled, not open.* The harness confirms hole-punching
  won't cross symmetric NAT; those peers stay on the relay (fallback proven).
  Acceptable — kept here only to record the demonstrated boundary.

## Suggested build order (MVP to "two Macs talking")

1. **mDNS** — free two-nodes-in-a-house discovery; smallest, ships alone.
2. **Reachability check (2b)** — the decision everything else hangs on.
3. **Relay (2c)** + `-relay` flag — the universal fallback; the bulk of the
   work; makes cross-network *possible*.
4. **Stand up the dev node** (separate `deploy/`) — the box to test 3 against.
5. **Prove it:** Andrew's Mac ↔ wife's Mac, different networks, meeting
   through the dev relay, publishing and retrieving a file.
6. **Hole-punching (2d)** — **done**: the punch primitive is proven end-to-end
   (cone → direct, symmetric → relay) and CI-gated via the `integration/nat`
   Docker harness. **UPnP (2a)** — the remaining optimization to reduce relay
   dependence further (ask the router to open a port).
7. Community `-dns-seed` guidance in docs; retire/relegate the dev node.

Steps 1–6 are done (hole-punch primitive proven; the live two-daemon upgrade
is wired but not yet green — a provider-discoverability follow-up). 7 is
hand-off to the community. Note: cross-network + relay + hole-punch + restart
are now tested automatically by the Docker harness in CI (`nat-integration`,
`nat-holepunch`), not the old manual two-Mac rig.
