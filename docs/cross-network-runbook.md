# Runbook: proving cross-network publish + fetch (two Macs, two networks)

> **Now largely automated.** The `integration/nat` Docker harness stands up two
> genuinely-NATed daemons + a relay in real container networks (real kernel NAT)
> and proves cross-NAT publish→fetch, restart survival, and hole-punch (cone →
> direct, symmetric → relay) — gated in CI (`nat-integration`, `nat-holepunch`).
> This manual two-Mac runbook is the optional hands-on version; it is no longer
> the gate.

This is #27's step 5 — the moment the localhost story becomes a
person-to-person one: **two machines behind two different home routers
publish and retrieve through a relay, with no port forwarding and no
shared LAN.** CI already proves the relay path end-to-end (including
both-peers-NATed) by modeling "NATed" as "accepts no inbound
connections"; this runbook confirms it against real routers, real NAT
timeouts, and a real WAN.

Cast:

| machine | where | role |
|---------|-------|------|
| **dev node** | any small VPS with a public IP | ordinary daemon with `-relay` on; also hosts the registry for the test. Stand it up with [deploy/dev-relay.sh](../deploy/README.md) — throwaway, not project infrastructure |
| **Mac A** | home network 1 | publishes a file |
| **Mac B** | home network 2 (phone hotspot works) | fetches it |

Everything below assumes the silt binary is built on all three machines
(§0) and the dev node is running and prints its identity as
`peer: <ID>@…`. Call that ID `$DEV`, the VPS's public IP `$IP`.

## 0. Build silt on every machine

silt is pure Go — no cgo, no system libraries — so the build needs only
a Go toolchain and the source. Two flavours of Mac show up in practice:
one with nothing installed, one carrying a stale binary from an earlier
round. Both end at the same `./silt`.

**Prerequisite (both):** Go **1.26.5 or newer** (the version pinned in
`go.mod`). Install the official pkg for your CPU (`arm64` for Apple
silicon, `amd64` for Intel) from <https://go.dev/dl/>, or `brew install
go`, then open a fresh terminal and confirm:

```sh
go version   # must report go1.26.5 or newer
```

### 0a. A barebones Mac (fresh install)

Because the build is pure Go, you can skip Xcode Command Line Tools
entirely by fetching the source as a zip instead of cloning:

```sh
cd ~ && curl -L -o silt.zip https://github.com/nerolabs/silt/archive/refs/heads/main.zip
unzip silt.zip && mv silt-main silt && cd silt
CGO_ENABLED=0 go build -o silt ./cmd/silt
```

(Prefer git? `git clone https://github.com/nerolabs/silt.git ~/silt`
works too — the first `git` invocation will offer to install the Xcode
CLT; accept it.)

### 0b. A Mac carrying an old binary

If it already has the checkout, update and rebuild in place:

```sh
cd ~/silt                        # wherever the repo lives
git pull --ff-only origin main
CGO_ENABLED=0 go build -o silt ./cmd/silt
```

If it has only a loose binary and no source, build fresh per §0a and
discard the old one. Either way, **make sure you run the binary you just
built**, not a stale copy still on `$PATH`:

```sh
which silt                       # a global hit here means: call ./silt explicitly
./silt -h | grep -c relay-via    # the current binary knows -relay-via; a stale one won't
```

### 0c. Version-match gate

All three nodes must run the same commit — a mismatched wire format or
relay-gossip shape will fail in confusing ways. Confirm each machine
reports the same `HEAD` before starting:

```sh
git log --oneline -1
```

## 1. Dev node (once)

```sh
# on the VPS — deploy/dev-relay.sh does this, plus -serve-registry for the test:
./silt daemon -listen 0.0.0.0:4001 -relay 0.0.0.0:4002 \
  -advertise "$IP:4001" \
  -serve-registry 0.0.0.0:4003 \
  -store ~/.silt-dev-relay -capacity 2G -mdns=false -debug
```

`-advertise` matters: a wildcard bind (`0.0.0.0`) is not a dialable
address, so the daemon never stamps it on outgoing messages — without
the flag, peers could bootstrap *to* the box but gossip would never
carry a usable address *for* it.

Note the `registry:` line it prints (`$DEV@https://$IP:4003`) — both
Macs will use it. Open TCP 4001–4003 in the provider firewall.

## 2. Mac A — daemon, NATed, leaning on the relay

```sh
./silt daemon -store ~/.silt-a -listen 0.0.0.0:4001 \
  -bootstrap "$DEV@$IP:4001" \
  -relay-via "$DEV@$IP:4002" \
  -registry "$DEV@https://$IP:4003" \
  -ui 127.0.0.1:8081 -debug
```

Watch for, in order:

1. `bootstrapped (…)`
2. `reachability: no peer could dial back — NATed; leaning on the -relay-via relay`
   — if instead you see `reachability: public`, your router has UPnP or
   you have a public IPv6 route; the relay is skipped because it isn't
   needed, which is also a pass (just a less interesting one).
3. `relay-via: registered — peers reach us at relay:$DEV@$IP:4002`

> **Flagless variant:** `-relay-via` is now optional. The dev box
> gossips its relay service on every envelope, so a NATed daemon that
> bootstraps to it discovers the relay on its own — watch for
> `relay: discovered $DEV@$IP:4002 via gossip — leaning on it`. Run the
> first pass with the explicit flag (deterministic), then repeat Mac A's
> start without it to prove discovery.

## 3. Publish on Mac A

Use the UI (`http://127.0.0.1:8081`, drag a file onto Publish) or the CLI:

```sh
./silt swarm add photo.jpg \
  -peers "$DEV@$IP:4001" \
  -registry "$DEV@https://$IP:4003"
```

Copy the printed `silt:v1:…` link. For an honest test pick a file of a
few MB — enough stripes to spread.

## 4. Mac B — fetch from the other network

```sh
./silt swarm get 'silt:v1:…' -o fetched.jpg \
  -peers "$DEV@$IP:4001" \
  -registry "$DEV@https://$IP:4003"
```

Then verify bit-perfection against the original (compare checksums out
of band):

```sh
shasum -a 256 fetched.jpg   # must equal Mac A's shasum of photo.jpg
```

## What this proves — and the interesting part

With `-capacity` small on the dev node relative to the file, shards
live (also) on Mac A, so Mac B's fetch must reach **through the relay
into Mac A's NATed daemon** — the canonical NATed↔NATed exchange. Check
it actually happened, on the dev node:

```sh
grep "relay splice" ~/.silt-dev-relay/debug.log
```

and on Mac A: `grep "relay" ~/.silt-a/debug.log` should show the
registration and inbound relayed fetches serving chunks.

## When it fails

Every box ran with `-debug`, so the artifact exists. Collect all three
`debug.log` files; the failure narrates itself:

| symptom | log line to look for | usual cause |
|---------|----------------------|-------------|
| Mac A never registers | `relay registration lost` / `relay dial` on A | VPS port 4002 closed, wrong `$DEV` |
| B can't reach A | `relay refused: target not registered` on B | A's registration dropped (NAT killed the control conn — the pings should prevent this; this is exactly the field data we want) |
| fetch hangs | `request timeout` lines on B | shards unreachable — check which peer's address B learned |
| registry errors | `registry ref …` on either Mac | missing `$DEV@` prefix on the https registry ref |

File whatever you find on #27 with the logs attached.

## After it passes

- Record the result on #27 (this closes steps 4–5 of the build order).
- Tear the dev node down, or leave it as one unremarkable community
  peer — but never let it become load-bearing (deploy/README.md).
- Next per the design: UPnP and hole-punching to shed relay load, and
  community `-dns-seed` guidance so rendezvous stops needing a
  hand-typed address.
