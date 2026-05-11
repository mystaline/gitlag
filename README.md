# gitlag

See which branches are out of sync, and scan open pull requests — all without cloning a single repo.

No disk cache. No shallow-clone bugs. Just Gitea's API doing the heavy lifting.

## What it does

| Command          | What you get                                                                 |
| ---------------- | ---------------------------------------------------------------------------- |
| `gitlag compare` | Per-repo table of commits ahead/behind between branches                      |
| `gitlag pr`      | All open PRs across your org, who wrote them, and where they're trying to go |
| `gitlag show`    | Deep dive into one repo's branch divergence                                  |

## Install

One command. Drops the binary into `~/go/bin`:

```bash
go install ./cmd/gitlag
```

Make sure `~/go/bin` is on your `$PATH` — add this to your `.bashrc` or `.zshrc` if it isn't already:

```bash
export PATH=$HOME/go/bin:$PATH
```

Or build locally:

```bash
make build          # → bin/gitlag
go build -o /wherever/you/want/gitlag ./cmd/gitlag
```

## Config

Drop this at `~/.gitlag.yaml`:

```yaml
gitea:
  url: https://gitea.example.com
  token: ${GITEA_TOKEN}

  repos:
    - org: my-org
      include:
        - service-*
      exclude:
        - "*-legacy"
```

## Commands

### `compare` — who's ahead, who's behind

Pick a source branch, pick one or more targets, and see the gap:

```bash
gitlag compare -s staging --targets dev
```

```
  staging → dev
┌──────────────────────────────┬─────────┬──────────┐
│ REPOSITORY                   │ DEV     │   TIME   │
├──────────────────────────────┼─────────┼──────────┤
│ backend-api                  │ ↑ 51A ↓ 11B │  6.1s   │
│ shared-utils                 │ ↑ 51A ↓ 6B  │  5.7s   │
│ frontend-app                 │ ✔ sync  │  1.0s   │
└──────────────────────────────┴─────────┴──────────┘
```

**↑ source ahead** means the source branch has commits the target doesn't. **↓ source behind** means the target has moved on without the source. All counts come straight from Gitea's compare API — zero cloning, always accurate.

Compare against multiple targets at once:

```bash
gitlag compare -s staging --targets dev,main,release
```

### `pr` — what's waiting for review

See every open PR across every repo, who filed it, and where it's headed:

```bash
gitlag pr
```

```
┌──────────────────────────────┬──────────────────────────────────────┬──────────────────────────────┬──────────┬────────────┐
│ REPOSITORY                   │ TITLE                                │ HEAD → BASE                  │ AUTHOR   │ DATE       │
├──────────────────────────────┼──────────────────────────────────────┼──────────────────────────────┼──────────┼────────────┤
│ backend-api                  │ fix: handle empty response on timeout │ fix/timeout → staging        │ alice    │ 2026-05-11 │
│ frontend-app                 │ feature: add user settings panel      │ feat/settings → dev           │ bob      │ 2026-05-10 │
│ shared-utils                 │ chore: bump dependencies              │ deps/upgrade → main           │ carol    │ 2026-05-08 │
└──────────────────────────────┴──────────────────────────────────────┴──────────────────────────────┴──────────┴────────────┘
```

No divergence counts here — the PR already tells you what's inside.

### `show` — single repo detail

How every branch stacks up against a chosen source:

```bash
gitlag show -r backend-api -s staging
```

```
Repository: backend-api
Branch: staging
Parent: main

Each branch compared to staging:
  BRANCH                         DIVERGENCE
  dev                   ↑ 88 ahead of source   ↓ 1 behind source   2026-04-23
  feat/user-settings    ↑ 157 ahead of source                       2026-03-31
  hotfix/login-timeout  ↓ 12 behind source                          2026-05-11
  main                  ↓ 51 behind source                          2026-01-15
  release               ✓ synced                                    2026-02-10
```

Every line reads like plain English — `↑ ahead of source` means that branch has commits staging doesn't, `↓ behind source` means staging has moved past it.

### Flags

| Flag            | Scope        | What it does                                   |
| --------------- | ------------ | ---------------------------------------------- |
| `--config`      | global       | Path to config file (default `~/.gitlag.yaml`) |
| `--format`      | global       | `table` (default) or `json`                    |
| `-s, --source`  | compare/show | Which branch you're comparing _from_           |
| `-t, --targets` | compare      | Comma-separated target branches                |
| `-r, --repo`    | show         | Single repository name                         |

## How it works

No local clones. No disk sprawl. No shallow-clone bugs inflating your counts.

Every count comes from the Gitea compare API — the server has the full commit history, so the numbers are always right. Ghost detection (squash merges, identical trees) is available as a future opt-in for repos that look ahead but are actually caught up.

## Parent branch detection

gitlag figures out parent branches automatically:

1. **Git upstream** — if the branch already tracks something, use that
2. **Config mapping** — `branch_parents` in your config (glob-friendly)
3. **Naming convention**:
   - `feature/*` → `dev`
   - `fix/*` → `staging`
   - `hotfix/*` → `main`
   - `dev*` → `staging`
   - `staging` → `main`
4. **Default** → `main`

## License

MIT
