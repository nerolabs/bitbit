# Deploying silthq.com

The website (`website/`) is static — plain HTML/CSS, no build step beyond
regenerating the changelog page from `CHANGELOG.md`. This document is the
runbook for publishing it with three environments and a public changelog.

## Environments

| Tier | Trigger | URL |
|------|---------|-----|
| **production** | push to `main` | `https://silthq.com` |
| **staging** | push to `staging` | `https://staging--silt.netlify.app` (or `staging.silthq.com`) |
| **dev / preview** | any pull request | a unique preview URL, auto-posted on the PR |

## Host: Netlify (recommended for DNS-at-Namecheap)

Because the domain is registered at Namecheap and you want production at
the **bare apex** `silthq.com`, Netlify is the cleanest fit: it supports
external DNS with a plain apex `A` record, and gives branch deploys +
deploy previews out of the box. (Cloudflare Pages is faster but requires
moving the nameservers to Cloudflare — see the alternative below.)

### One-time setup

1. **Create the site.** In Netlify, "Add new site → Import from Git" and
   pick `nerolabs/silt`. Netlify reads `netlify.toml` automatically:
   build command `python3 scripts/gen_changelog.py`, publish dir
   `website`. Set the **production branch** to `main`, and enable
   **branch deploys** (for `staging`) and **deploy previews** (for PRs).

2. **Point Namecheap DNS at Netlify.** In Namecheap → Domain List →
   silthq.com → Advanced DNS, set these records (delete the default
   parking/redirect records first):

   | Type | Host | Value | TTL |
   |------|------|-------|-----|
   | `A` | `@` | `75.2.60.5` | Automatic |
   | `CNAME` | `www` | `<your-site>.netlify.app.` | Automatic |
   | `CNAME` | `staging` | `staging--<your-site>.netlify.app.` | Automatic *(optional)* |

   Then in Netlify → Domain management, add `silthq.com` as the primary
   custom domain (Netlify provisions TLS automatically). Add
   `staging.silthq.com` to the staging branch deploy if you want the
   friendly staging hostname.

3. **That's it.** Every push to `main` deploys production; every PR gets
   a preview; `staging` gets its own build. The changelog page rebuilds
   on every deploy, so it can't go stale.

### Alternative: Cloudflare Pages (fastest, needs nameserver move)

If you're willing to move DNS to Cloudflare (registration stays at
Namecheap — you only change the two nameservers in Namecheap to the ones
Cloudflare gives you): create a Pages project from the repo, set build
command `python3 scripts/gen_changelog.py` and output dir `website`,
production branch `main`. Add `silthq.com` and `staging.silthq.com` as
custom domains — Cloudflare handles the apex via CNAME flattening. Pages
gives the same production / preview / branch model.

## CI gates (`.github/workflows/ci.yml`)

Runs on every push and PR: `go vet`, `gofmt`, the full `go test ./...`
suite, a check that `changelog.html` is in sync with `CHANGELOG.md`, and
a website link-check. Make these **required status checks** on `main`
and `staging` (Settings → Branches → protection rules) so nothing merges
red, and neither environment can deploy a broken build.

## Releases (`.github/workflows/release.yml`)

Dormant until you cut one. When ready:

```sh
# 1. move items from "Unreleased" into a dated version in CHANGELOG.md
# 2. tag and push
git tag v0.1.0 && git push origin v0.1.0
```

The workflow builds the macOS / Windows / Linux binaries (`build.sh`) and
publishes a GitHub Release with notes from `CHANGELOG.md`. Only then do
the site's download links resolve to real files — until then they point
at the build-from-source instructions on purpose.

## The changelog

`CHANGELOG.md` is the single source of truth (Keep a Changelog +
SemVer). `scripts/gen_changelog.py` renders it into
`website/changelog.html` at build time, published at
[silthq.com/changelog](https://silthq.com/changelog.html). To update:
edit `CHANGELOG.md`, run the script locally to preview, commit both.
