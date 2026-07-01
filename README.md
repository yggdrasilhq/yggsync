# yggsync

`yggsync` is a small native sync engine for Yggdrasil endpoints.
It copies files to SMB shares or plain local paths without shelling out to `rclone`.

This README is the operator manual for editing and running `~/.config/ygg_sync.toml`.

## Overview

`yggsync` has two modes:

- file jobs for screenshots, media, archives, and backups
- worktree jobs for a local working copy against a central repository, especially Obsidian

That split is deliberate. Generic two-way sync is the wrong model for a live vault on SMB.

```mermaid
flowchart TD
    A[yggsync run] --> B[File job]
    A --> C[Worktree job]
    B --> D[copy]
    B --> E[sync]
    B --> F[retained_copy]
    D --> G[SMB target or local path]
    E --> G
    F --> G
    C --> H[Local worktree]
    C --> I[Central repository]
    C --> J[State file]
```

## Concepts

### Job Types

- `copy`: copy local files to the destination
- `sync`: mirror local files to the destination and delete remote files missing locally
- `retained_copy`: copy first, then prune eligible local files only after remote confirmation
- `worktree`: local working copy against a central repository with explicit state tracking

`bisync` is accepted as a legacy alias for `worktree`.

### Target Types

The remote side can be either:

- a named target from `[[targets]]`
- a plain absolute local path

Today `yggsync` supports:

- `type = "smb"`
- `type = "local"`

Remote values work like this:

- `remote = "nas:immich/alice/DCIM"` means target `nas`, relative path `immich/alice/DCIM`
- `remote = "/mnt/nas/data/archive"` means use that mounted local path directly

### Worktree Model

`worktree` reconciles a local working copy against a central hub using a
**hub-authoritative ledger** as the common ancestor. The ledger, a
content-addressed blob store, tombstones, and per-client cursors all live under
`<remote>/.yggsync/` so history travels with the vault and survives a client
reset. See `docs/adr-001-hub-authoritative-ledger.md` for the full design.

Each path is resolved three-way against the ledger:

- changed on only one side -> the other side is authoritative (copy or delete)
- a rename is a clean delete at the old path plus an add at the new path — it is
  matched by content and is **never** reported as a conflict
- changed on both sides -> a line-based `diff3` merge is attempted

When a genuine conflict cannot be merged cleanly, the hub version stays live and
your version is preserved beside it as `<name>.mergefail.<UTC-timestamp>`. The
run still exits non-zero, writes a `CONFLICTS.md` at the vault root, and fires a
Termux notification — but one unmergeable file never blocks the rest of the
vault. No `.conflictN`-style silent overwrites, and nothing is lost.

Safety:

- A run that would delete a large share of hub files (e.g. an emptied or
  misconfigured local tree) aborts unless you pass `-allow-mass-delete`.
- `-dry-run` reports the planned actions without touching either replica.

```mermaid
flowchart LR
    L[(.yggsync ledger<br/>on hub)] --- A
    L --- C
    A[Local worktree] -->|per-path 3-way| E{changed<br/>where?}
    C[Central hub] -->|per-path 3-way| E
    E -->|one side| F[copy / delete]
    E -->|both, mergeable| G[diff3 merge -> both]
    E -->|both, conflict| H[.mergefail sidecar<br/>+ CONFLICTS.md]
```

## What You Edit

For most setups, you usually change only these fields.

### Top-Level Keys

Normally leave these alone:

- `lock_file`
- `worktree_state_dir`

### In `[[targets]]`

Usually edit:

- `host`
- `share`
- `username` or `username_env`
- `password_env` or `password`
- `base_path` if all jobs live under one common subtree
- `path` for `type = "local"`

Prefer `password_env` over `password` for steady-state use.

### In `[[jobs]]`

Usually edit:

- `local`
- `remote`
- `local_retention_days` for `retained_copy`
- `filter_rules`, `include`, or `exclude`
- `timeout_seconds` for very large jobs

Rules:

- do not mix `filter_rules` with `include` or `exclude` in the same job
- `retained_copy` without `local_retention_days` is usually a mistake
- use `worktree` for local-vault-to-central-repository sync
- use `sync` only when remote deletions are intended

## First Run

The safest first run is small and explicit.

1. Put credentials in the environment if the target uses `password_env`.
2. List jobs.
3. Dry-run one small file job.
4. Dry-run the worktree job to see the planned pulls/pushes before committing.
5. Only after that, let scheduled jobs run normally.

Example:

```bash
export SAMBA_PASSWORD='your-password'
yggsync -config ~/.config/ygg_sync.toml -list
yggsync -config ~/.config/ygg_sync.toml -jobs screenshots -dry-run
yggsync obsidian -config ~/.config/ygg_sync.toml -dry-run
```

A worktree job needs no explicit initialization mode. On first contact with a
fresh hub (no `.yggsync` ledger) it seeds the ledger and reconciles: an empty
local pulls everything, an empty hub receives everything, and identical trees
just record state. A reset client safely re-pulls from the hub ledger rather
than proposing to delete hub files.

## Normal Operation

### File Jobs

For ordinary file jobs, the normal pattern is:

1. render or edit the config once
2. dry-run
3. let `yggclient` or your own scheduler call `yggsync`

### Worktree Jobs

Run the job on a schedule; it reconciles automatically against the hub ledger.
`-dry-run` previews actions; `-allow-mass-delete` is required only for an
intentional bulk deletion (e.g. deliberately clearing the hub from an emptied
local tree).

On first initialization:

- if one side is empty, `yggsync` can initialize from the populated side
- if both sides are populated but differ, `yggsync` stops and requires explicit `update` or `commit`

## Config Patterns

### SMB Target

```toml
[[targets]]
name = "nas"
type = "smb"
host = "nas.internal"
share = "data"
username = "smb-login"
password_env = "SAMBA_PASSWORD"
```

Optional fields:

- `port`
- `base_path`
- `domain`
- `username_env`
- `password`

### Local Target

```toml
[[targets]]
name = "mounted"
type = "local"
path = "/mnt/nas/data"
```

This is useful when a laptop already uses a mounted NAS path and you do not want `yggsync` opening its own SMB session.

### Android Media Example

```toml
[[jobs]]
name = "screenshots"
type = "retained_copy"
local = "~/storage/shared/Pictures/Screenshots"
remote = "nas:immich/path-user/android/Screenshots"
local_retention_days = 31
```

### Laptop Mounted-Share Example

```toml
[[jobs]]
name = "screenshots"
type = "copy"
local = "~/Pictures/Screenshots"
remote = "/mnt/nas/data/immich/path-user/desktop/Screenshots"
```

Use this only if the destination is a real mount point.
Guard scheduled runs so a missing mount does not turn into writes into an empty local directory.

### Obsidian Worktree Example

```toml
[[jobs]]
name = "obsidian"
type = "worktree"
client_id = "phone"                        # identity in the hub ledger
local = "~/Documents/obsidian/main"        # scope to a single vault
remote = "nas:smbfs/path-user/obsidian/main"
filter_rules = [
  "- **/.yggsync/**",                      # never sync the ledger itself
  "- CONFLICTS.md",                        # generated conflict report
  "- **/.obsidian/**",
  "- **/.trash/**",
  "- **/*.conflict*",
]
```

`client_id` should be unique per device sharing a hub. `local`/`remote` can
point at a single vault subtree to sync just that vault. The `.yggsync/`
exclusion is enforced in code as well, but keep it in the filter so the ledger
is never scanned as content.

For SMB shares that expose DOS 8.3 aliases, exclude them too:

```toml
filter_rules = [
  "- **/.obsidian/**",
  "- **/.trash/**",
  "- **/*.conflict*",
  "- [A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_]~[A-Za-z0-9].*",
  "- **/[A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_]~[A-Za-z0-9].*",
]
```

## Filters

`yggsync` supports:

- `include`
- `exclude`
- `filter_rules`

`filter_rules` are order-sensitive and support:

- `+ pattern`
- `- pattern`

Patterns are glob-style and support:

- `*`
- `**`
- `?`
- character classes like `[A-Za-z0-9_]`

Use `filter_rules` when simple include/exclude is not expressive enough.

## Troubleshooting

### `unknown job`

The job name in `-jobs` does not match any configured `[[jobs]].name`.
Run:

```bash
yggsync -config ~/.config/ygg_sync.toml -list
```

### `initial worktree is ambiguous`

Both local and remote already contain data and there is no saved state yet.
Choose one explicitly:

- `-worktree-op update`
- `-worktree-op commit`

### `remote changed since last state`

You tried `commit` after the remote changed independently.
Run `sync` or `update` first, then commit again if needed.

### Obsidian conflict churn or weird alias names

Usually this means one of these:

- multiple live writers touched the same vault
- `.obsidian` or other high-churn paths were not filtered
- the SMB server exposed DOS alias names and they were not filtered

### Mounted NAS path wrote to the wrong place

This usually means the mount was absent and the path resolved locally.
Guard scheduled runs with a mount check such as:

```ini
[Unit]
ConditionPathIsMountPoint=/mnt/nas/data
```

## CLI Reference

Common commands:

```bash
yggsync -config ~/.config/ygg_sync.toml
yggsync -jobs dcim,screenshots
yggsync -jobs notes -worktree-op update
yggsync -jobs notes -worktree-op commit
yggsync -jobs notes -worktree-op sync
yggsync -jobs notes -dry-run
yggsync -list
yggsync -version
```

Flags:

- `-config`: config file path. Defaults to `~/.config/ygg_sync.toml` or `$YGG_SYNC_CONFIG`
- `-jobs`: comma-separated job names. Default is all jobs
- `-dry-run`: simulate file operations
- `-worktree-op`: `sync`, `update`, or `commit`
- `-list`: print configured job names
- `-version`: print the binary version

Legacy compatibility:

- `--resync` and `--force-bisync` are accepted as no-op compatibility flags while old wrappers are migrated

## Build

```bash
go build ./cmd/yggsync
```

Cross-build examples:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/yggsync-linux-amd64 ./cmd/yggsync
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o dist/yggsync-android-arm64 ./cmd/yggsync
```
