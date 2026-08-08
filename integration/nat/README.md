# Cross-NAT integration harness (Docker)

An automated, machine-free replacement for the old two-laptop NAT test. It
builds a real multi-NAT "internet" **on one host, in containers** — real
kernel NAT (iptables MASQUERADE), real TLS over real sockets — and asserts
that a file published from behind one NAT comes back bit-perfect when fetched
from behind another, having crossed the public relay.

```
  lanA (internal) ──[ natA: MASQUERADE ]──┐
    └─ nodeA  (NATed, un-dialable)         │
                                           ├─ public ── relay  (+ registry)
  lanB (internal) ──[ natB: MASQUERADE ]──┘
    └─ nodeB  (NATed, un-dialable)
```

`nodeA` and `nodeB` live on separate **`internal`** Docker networks with no
route to each other, each behind its own NAT gateway. Neither can be dialed
from outside, so silt's own reachability probe concludes "NATed" (no
test-only flags) and both lean on the relay — the exact condition that made
NAT↔NAT the one thing the in-process sim and flat-localhost e2e couldn't
cover.

## Run it

```sh
./integration/nat/run.sh          # build, test, tear down; exit 0 = PASS
KEEP=1 ./integration/nat/run.sh   # leave it up afterward to poke at
```

Needs Docker (Desktop on macOS, or the engine on Linux) and a Go toolchain.
The `silt` binary is compiled **on the host** (CGO off → trivial cross-compile
to the container's arch) and copied into a slim image — so the image stays
tiny and there's no ~1 GB Go-build memory spike inside Docker. CI runs this as
the `nat-integration` job on every PR; it is the automation gate that used to
be a manual two-machine step.

## What each piece is

| file | role |
|------|------|
| `docker-compose.yml` | the topology: 3 networks, relay, 2 NAT gateways, 2 NATed nodes |
| `Dockerfile` | slim runtime image (silt binary + iproute2 + iptables), one image for all roles |
| `natgw.sh` | gateway entrypoint: `ip_forward` + `MASQUERADE` for its LAN; blocks inbound |
| `node.sh` | node entrypoint: re-points default route through the gateway, then execs the daemon |
| `run.sh` | the driver: build → bring up → publish from A → fetch from B → assert → tear down |

## Poking at a running topology (`KEEP=1`)

```sh
KEEP=1 ./integration/nat/run.sh
cd integration/nat
docker compose ps                          # the five containers
docker compose logs -f relay               # watch "relay splice" events
docker compose exec nodeB ip route         # default route via 10.30.0.2 (natB)
docker compose exec nodeA silt swarm add /tmp/f.bin -peers "$RELAY_ID@10.10.0.10:4001" -registry "$RELAY_ID@https://10.10.0.10:4003"
docker compose down -v                     # tear down when done
```

## Scenarios (this is the seed; more to come)

- **`./run.sh`** — cross-NAT publish → fetch through the relay (proves the
  relay path; the automatable form of the #65 fetch-under-load test — raise the
  fetch concurrency here to exercise the retry against a real saturated relay).
- **`./loadtest.sh`** — the #65 **fetch-under-load / saturated-relay** field
  test, on its own `-p natload` project + `docker-compose.load.yml` overlay
  (networks 10.70/71/72, image `silt-natload`) so it never collides with a
  concurrent `./run.sh`. Same topology, but it **bandwidth-caps the relay's
  public interface with `tc qdisc htb`** (NET_ADMIN) and fires **N concurrent
  `silt swarm get`** from nodeB through it. Asserts graceful degradation: every
  fetch bit-perfect, none hangs (each `timeout`-capped), splices really crossed.
  `N=<n>`, `RATE=<r>mbit`, `FILE_BYTES=<b>` are tunable. Default `N=12` sits
  under the relay's per-peer splice cap (`PerPeerSessions=16`) and PASSES;
  `N=24 ./loadtest.sh` enters the STRESS zone where the shallow fetch retry
  budget can't outlast sustained saturation and some fetches fail loudly with
  `netget: manifest chunks unreachable` (a real finding — see the PR).
- **`RESTART=1 ./run.sh`** — the #69 test: after the fetch, restart the whole
  swarm (stores persist, in-memory provider records do not) and re-fetch,
  proving each holder reloads its persisted proofs and re-announces its coded
  shards under the right column key.
- **Next:** hole-punching (#27) — set the gateways to full-cone vs symmetric
  conntrack and assert a *direct* A↔B connection forms (bytes bypass the
  relay) for the punchable cases, and falls back to the relay for symmetric.
