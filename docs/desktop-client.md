# The Desktop Client (M14)

`silt client` is the end-user face of Silt: a single binary that
**consumes and serves in the same process**. Downloading and
contributing are the same act — the network's health is the sum of its
clients, not a separate class of servers.

## Running it

```sh
silt client \
  -bootstrap <ID>@seed-host:port \   # or -dns-seed a domain
  -registry  <ID>@https://host:port  # to browse/publish (optional)
```

Defaults chosen for a person, not an operator:

- `-store ~/.silt` — identity keyfile, disk store, link book, learned
  peers, all in one place.
- `-capacity 5G` — you contribute 5 GB by default (`-capacity 0` to
  consume only, discouraged).
- `-ui 127.0.0.1:8090` and `-open` — the library opens in your browser
  on start.

On launch it mints or loads its keypair (identity = the key, M10),
opens a capacity-bounded disk store, joins the swarm via discovery
(persisting peers for flagless restarts), announces what it holds, and
serves the local UI.

## What the UI shows

- **Library** — the files you hold a *link* for. This is the honest
  center of the whole design: the chain lists opaque roots the network
  hosts, but a link is a key, and keys arrive out of band (or from a
  resolver like Aslan). So your library is your *link book*, and the
  page tells you plainly how many identifiers the network hosts that
  you have **no key for** ("opaque to you, by design"). Paste a link,
  give it a name for yourself, click **get**.
- **Publish** — drag a file; get back its silt link and care link.
- **Node** — your contribution: pledge used/total, chunks you host for
  others, bytes served, self-estimated network size.
- **Observatory** — aggregate any set of nodes you can reach.

## Why one binary, no Electron

The UI is `go:embed`-ed into the binary and served locally; the only
OS-specific code is three lines that open the default browser
(`open` / `xdg-open` / `rundll32`). So `build.sh` cross-compiles the
same source to Mac (Intel + Apple Silicon), Windows, and Linux
(amd64 + arm64) with plain `GOOS`/`GOARCH` and `CGO_ENABLED=0` — five
self-contained 8–10 MB binaries, nothing to install alongside them.

```sh
./build.sh v0.1     # → dist/silt-v0.1-<os>-<arch>
```

**System-tray wrapping** (a menu-bar icon that starts/stops the client
and links to the UI) is the natural next polish. It's deliberately not
in the core binary: a tray needs a native GUI toolkit (cgo, per-OS),
which would break the "one `go build`, no toolchain" property that
makes the client trivial to ship. Options, in order of effort: (1) a
tiny per-OS launcher (`systray` library) that shells out to this
binary; (2) wrap the same Go core in **Tauri** for a fully native
window while keeping the web UI. Both consume the binary as-is; neither
changes a line of Silt.

## The endgame this reaches

With the client contributing by default, the distinction between "the
network" and "the users" dissolves — every downloader is a host, the
way BitTorrent works, but with erasure coding, encryption at every
level, earned-reputation consensus, and the infrastructure/meaning
split all underneath. That was the whole arc: `silt add file.txt` on
one laptop in M1, to a self-serving desktop app over an encrypted,
audited, chain-governed swarm here in M14.
