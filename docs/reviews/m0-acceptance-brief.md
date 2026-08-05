# M0 acceptance brief — does it work the way it says?

> **You are a fresh developer/operator**, not an attacker. You read the website
> and the docs, you liked the pitch, and now you are going to actually *use*
> this thing. Your job is to find every place where **reality diverges from the
> claims** — a documented flow that errors, a promise that doesn't hold when you
> exercise it, a getting-started that doesn't get you started, a step that only
> the authors could know. Good faith throughout: you *want* it to work; you
> report honestly where it doesn't.

## What you get

Only the **public surface** — the same as a real newcomer. Start from these
two; everything you need is reachable from them:

- **the repository — <https://github.com/nerolabs/silt>** — clone, build, run,
  test;
- **the website — <https://silthq.com>** — and anything it links.

Nothing from the builders. **The public docs are the interface under test.** If
you cannot do something because it isn't documented, that is a finding, not a
question to ask.

> **This brief is the WHAT and the WHY only** — the cases to test and what
> "pass" means. **Every HOW — every command, flag, and step — you get from the
> public docs**, discovered the way any newcomer would. This brief deliberately
> does not tell you how to run anything, or which doc to open. If a case can't
> be accomplished from the public docs, that undocumented gap is your single
> most important finding.

## The claims to verify

silt makes the following promises (in its tenets and on its website). Confirm
each is actually stated in the public materials, then hold the system to it *by
doing it*:

- **"The link is the primitive."** A `silt:` link is all a user needs to
  retrieve and decrypt; a `siltcare:` link repairs/audits without decrypting.
- **Content-addressed, verified on every read** — corruption is caught, not
  served.
- **Erasure-coded durability** — a file survives losing nodes.
- **NAT traversal** — two nodes behind home routers can still exchange data.
- **Earned standing** — a validator must prove real held storage over time to
  write to the chain; a node that proves nothing cannot. Standing is the
  **composition** (disk × address-diversity × time × served-demand — `C_honest`
  in `docs/design/m0.md`), not the bond alone.
- **Unlinkable publish** — a published entry carries no durable Publisher
  identity by default.
- **Per-hash takedown** — an operator can refuse a specific root; there is no
  global switch.
- **Survives restart** — a node's stored content, its bond standing, and its
  issuer identity all come back after a restart.

## The flows to run (as an operator, from the public docs only)

For each: follow the documented steps, record what actually happens, and mark
`works` / `gap` / `broken`.

1. **Build & first run** — from a clean clone, get a node running.
2. **Publish → fetch** — add a file, get a link, fetch it back bit-perfect from
   a *different* node/process.
3. **Care link** — repair/audit a file with a `siltcare:` link; confirm you
   cannot read its contents.
4. **Become a validator** — run a node that earns standing (the composition —
   disk × address-diversity × time × served-demand, `C_honest` in
   `docs/design/m0.md` — not the bond alone) by proving real held storage over
   time, and helps commit a publish through consensus on the *earned-standing*
   path (not a trusted rubber-stamp where any node can write).
5. **Multi-validator convergence** — stand up several validators; publish; every
   replica ends up agreeing on the committed history.
6. **Fault tolerance** — kill one validator mid-flight; a quorum of the rest
   still commits.
7. **Restart survival** — restart a validator; its standing comes back
   immediately, without redoing the expensive one-time bond setup, and the
   tokens it issued stay valid. Restart a storage node; its content is still
   discoverable and served.
8. **Takedown** — deny a specific root on one operator; confirm it stops serving
   there and that nothing removed it *everywhere*.
9. **Cross-NAT** — run two nodes behind (simulated) NATs and move a file between
   them.

Flows 5–7 are the multi-machine field test the project owes itself (roadmap
#52): you performing them from the docs *is* that field test, done independently.

## Rules of engagement

- **Docs only.** If a flow needs a flag, a sequence, or a concept that the
  public docs don't teach, the finding is "undocumented", not "I asked and
  learned X."
- **Claim vs. reality.** For each gap, name the claim (quote the doc/site), the
  documented behavior, and what you observed instead.
- **Reproducible.** Exact commands and outputs, so a builder can re-run it.
- **Separate "doesn't work" from "hard to use."** Both are useful; label which.

## What to hand back

A single markdown file. Lead with a **per-flow verdict table** (`works` /
`gap` / `broken` for each of the flows above), then one section per finding:

```
### <short title>
- flow:            which flow above (or "setup"/"docs")
- claim:           what the docs/site promise (quote it)
- documented step: what the docs told you to do
- observed:        what actually happened (commands + output)
- verdict:         works | gap | broken
- severity:        blocker | major | minor | cosmetic
- repro:           exact commands
- suggested fix:   optional (doc fix, or code fix)
```

End with one plain sentence: **can a newcomer, from the public docs alone, get
silt doing what it advertises?** That sentence is the acceptance verdict.
