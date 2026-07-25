# Changelog

All notable changes to Silt are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and the project aims to
follow [Semantic Versioning](https://semver.org/).

This log is published at [silthq.com/changelog](https://silthq.com/changelog.html).

## [Unreleased]

### Added
- Public website (silthq.com) with brand, docs, operator guide, and downloads.
- Continuous delivery: PR previews, a `staging` environment, and production
  deploys from `main`.

## [0.1.0] — 2026-07-25

The first public cut. Silt is a distributed, encrypted, erasure-coded
storage network whose nodes cannot know what they carry.

### Added
- **Content-addressed storage** — every fragment is named by the SHA-256 of
  its bytes; verification is intrinsic, so hosts are never trusted.
- **Erasure coding** — Reed-Solomon stripes (default any 10 of 16 rebuild the
  file); a repair loop restores redundancy as machines fail.
- **Encryption at every level** — chunks and manifests are both ciphertext; a
  file's share handle is a *link* (`silt:v1:root:key`) whose one-way key
  hierarchy also yields *care links* that grant repair and audit without the
  ability to decrypt.
- **The swarm** — Kademlia routing, provider records, and multi-node fetch over
  a deterministic simulator or real mutual-TLS sockets; identity is a keypair
  and a node's ID is the hash of its public key.
- **Capacity** — nodes pledge a fixed budget (`-capacity 2G`); placement spills
  over as nodes fill, and every node estimates the whole network's size from
  local gossip alone.
- **Proof-of-retrieval audits** — hosts are challenged to prove possession with
  a fresh nonce; those that keep the proof but drop the data are slashed.
- **The registry chain** — an append-only chain kept by the operators; blocks
  commit only with a quorum of attestations from validators whose reputation
  (audits + serving) is earned, not bought.
- **Genesis** — every fresh network is born carrying a founding manifesto in
  block 0, declared identically on every node.
- **Web UI** — an embedded dashboard, publish/fetch pages, and a network
  observatory, served by the daemon.
- **Desktop client** — one binary that consumes and serves at once, keeps a
  link-book library, and runs on macOS, Windows, and Linux.

[Unreleased]: https://github.com/nerolabs/silt/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/nerolabs/silt/releases/tag/v0.1.0
