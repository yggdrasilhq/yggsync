# yggsync

`yggsync` is a native Go sync engine for Yggdrasil endpoints.
It syncs local files directly to local paths or SMB shares without shelling out to `rclone`.

This document is written as the operator manual.
If you are editing `~/.config/ygg_sync.toml` yourself, start here.

## Model

`yggsync` has two distinct modes:

- file sync jobs for Android media, screenshots, backups, and workstation archives
- worktree jobs for a central-repository workflow, especially Obsidian vaults

That split is deliberate.
Generic two-way sync is the wrong tool for a live vault on SMB.
`yggsync` therefore treats Obsidian-like repos as a working copy against a central repository, not as a magic multi-writer folder.

## Job Types

- `copy`: copy local files to the destination
- `sync`: mirror local files to the destination and delete remote files removed locally
- `retained_copy`: copy first, then prune eligible local files only after remote confirmation
- `worktree`: local working copy against a central repository with explicit state tracking

`bisync` is accepted as a legacy alias for `worktree`.

## Targets

The remote side can be:

- a plain local path
- a configured target reference such as `nas:immich/path-user/DCIM`

Targets are defined in `[[targets]]`.
Today `yggsync` supports:

- `type = "smb"`
- `type = "local"`

## Build

```bash
go build ./cmd/yggsync
```

Cross-build examples:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/yggsync-linux-amd64 ./cmd/yggsync
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o dist/yggsync-android-arm64 ./cmd/yggsync
```

## CLI

```bash
yggsync -config ~/.config/ygg_sync.toml
yggsync -jobs dcim,screenshots
yggsync -jobs notes -worktree-op sync
yggsync -jobs notes -worktree-op update
yggsync -jobs notes -worktree-op commit
yggsync -jobs notes -dry-run
yggsync -list
yggsync -version
```

Flags:

- `-config`: config file path. Defaults to `~/.config/ygg_sync.toml` or `$YGG_SYNC_CONFIG`
- `-jobs`: comma-separated list of job names. Default is all jobs
- `-dry-run`: simulate file operations
- `-worktree-op`: `sync`, `update`, or `commit` for `worktree` jobs
- `-list`: print configured job names
- `-version`: print the binary version

Legacy compatibility:

- `--resync` and `--force-bisync` are accepted as no-op compatibility flags while old wrappers are migrated

## Config

Start from [`ygg_sync.example.toml`](./ygg_sync.example.toml).

### What You Normally Change

For a fresh setup, the fields you usually need to edit are:

- in `[[targets]]`:
  - `host`
  - `share`
  - `username` or `username_env`
  - `password_env` or `password`
  - `base_path` only if all jobs live under a common SMB subdirectory
- in `[[jobs]]`:
  - `local`
  - `remote`
  - `local_retention_days` for `retained_copy`
  - `filter_rules`, `include`, or `exclude`
  - `timeout_seconds` for very large jobs

Fields most users should leave alone:

- `lock_file`
- `worktree_state_dir`
- `port` unless your SMB server is not on the default port
- `domain` unless your SMB server actually requires it

### Config Reference

Top-level keys:

- `lock_file`: lock path used to prevent overlapping runs
- `worktree_state_dir`: where worktree state JSON files are stored
- `[[targets]]`: named remote targets
- `[[jobs]]`: named sync jobs

### SMB Target

```toml
[[targets]]
name = "nas"
type = "smb"
host = "nas.lan"
share = "data"
username = "alice"
password_env = "SAMBA_PASSWORD"
```

You can also use:

- `port`
- `base_path`
- `domain`
- `username_env`
- `password`
- `path` for `type = "local"`

Meaning of the main target fields:

- `name`: short reference used by jobs such as `nas:immich/alice/DCIM`
- `type`: `smb` or `local`
- `host`: SMB server hostname or IP
- `share`: SMB share name, for example `data`
- `base_path`: optional prefix inserted before every job remote path on that target
- `username`: literal SMB login name
- `username_env`: environment variable holding the SMB login name
- `password`: literal SMB password. Prefer `password_env` outside one-off local testing
- `password_env`: environment variable holding the SMB password
- `domain`: optional SMB domain/workgroup value
- `path`: root path for `type = "local"`

Remote paths work like this:

- `remote = "nas:immich/alice/DCIM"` means:
  - target `nas`
  - inside that target, sync the relative path `immich/alice/DCIM`
- `remote = "/srv/archive"` means:
  - no named target
  - treat the remote side as a plain local path

### Copy Job

```toml
[[jobs]]
name = "screenshots"
type = "retained_copy"
local = "~/Pictures/Screenshots"
remote = "nas:immich02/alice/desktop/Screenshots"
local_retention_days = 30
```

### Job Fields

Fields accepted on most jobs:

- `name`: required unique job name
- `description`: optional human note
- `type`: `copy`, `sync`, `retained_copy`, or `worktree`
- `local`: local source or worktree path
- `remote`: destination path or target reference
- `timeout_seconds`: optional execution timeout
- `include`: allowlist glob patterns
- `exclude`: denylist glob patterns
- `filter_rules`: ordered `+` and `-` rules when simple include/exclude is not enough
- `state_file`: explicit path for a `worktree` state file
- `local_retention_days`: required for `retained_copy`
- `[[jobs.keep_latest]]`: keep only the newest matching files after upload

Rules:

- do not mix `filter_rules` with `include` or `exclude` in the same job
- `retained_copy` without `local_retention_days` is usually a configuration mistake
- `worktree` is the correct type for Obsidian local-vault to central-repository sync
- `sync` is destructive on the remote side and should be used only when remote deletions are intended

### Worktree Job

```toml
[[jobs]]
name = "notes"
type = "worktree"
local = "~/Documents/notes"
remote = "nas:smbfs/alice/notes"
filter_rules = [
  "- **/.obsidian/**",
  "- **/.trash/**",
  "- **/*.conflict*",
]
```

## Filters

`yggsync` supports:

- `include`
- `exclude`
- `filter_rules`

Do not mix `filter_rules` with `include` or `exclude` in one job.

`filter_rules` are order-sensitive and currently support the native subset:

- `+ pattern`
- `- pattern`

Patterns are glob-style and support:

- `*`
- `**`
- `?`
- character classes like `[A-Za-z0-9_]`

For SMB shares that expose DOS 8.3 aliases, exclude them explicitly:

```toml
filter_rules = [
  "- [A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_]~[A-Za-z0-9].*",
  "- **/[A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_]~[A-Za-z0-9].*",
]
```

## Worktree Semantics

`worktree` is the safer model for local Obsidian vaults against a central SMB repository.

- `sync`: merge non-conflicting local and remote changes using the last saved state
- `update`: pull remote changes into the local worktree
- `commit`: push local changes only if the remote has not changed since the last saved state

On first initialization:

- if one side is empty, `yggsync` can initialize from the populated side
- if both sides are already populated but differ, `yggsync` stops and requires an explicit `-worktree-op update` or `-worktree-op commit`

If both sides changed the same path, `yggsync` fails with an explicit conflict error.
It does not create `.conflictN` files on your behalf.

This is closer to an `SVN` working-copy model than to a generic filesystem `bisync`.

### Which Worktree Command To Run

Use these rules:

- `update`: use when the NAS copy is the source of truth and you want to refresh the local vault
- `commit`: use when the local vault is the source of truth and you want to push it to the NAS
- `sync`: use only after a state file already exists and you want normal day-to-day non-conflicting merges

Typical first-run cases:

1. Remote vault already exists, local is empty or disposable:
   ```bash
   yggsync -config ~/.config/ygg_sync.toml -jobs obsidian -worktree-op update
   ```
2. Local vault already exists, remote is empty or disposable:
   ```bash
   yggsync -config ~/.config/ygg_sync.toml -jobs obsidian -worktree-op commit
   ```
3. Both sides already contain different data:
   - stop and decide which side is authoritative
   - keep a manual backup of the side you are about to overwrite
   - then run either `update` or `commit`

Day-to-day examples:

```bash
# Show jobs
yggsync -config ~/.config/ygg_sync.toml -list

# Safe preview
yggsync -config ~/.config/ygg_sync.toml -jobs screenshots -dry-run

# Pull NAS changes into a local Obsidian vault
yggsync -config ~/.config/ygg_sync.toml -jobs obsidian -worktree-op update

# Push a local Obsidian vault back to the NAS
yggsync -config ~/.config/ygg_sync.toml -jobs obsidian -worktree-op commit

# Merge non-conflicting changes after the worktree is already initialized
yggsync -config ~/.config/ygg_sync.toml -jobs obsidian -worktree-op sync
```

## Direct SMB Use

If you personally use only one live Obsidian instance at a time, opening the vault directly from an SMB mount is still a supported human workflow.
`yggsync` does not forbid that.

The `worktree` mode exists for the cases where you want a safer local-vault workflow without relying on direct live editing over SMB.

## Setup Checklist

Before first real use:

1. Copy or render `~/.config/ygg_sync.toml`
2. Edit the SMB target credentials and the job paths you actually use
3. Export the SMB password if you use `password_env`
4. Run `yggsync -config ~/.config/ygg_sync.toml -list`
5. Run a small `-dry-run` job first
6. For Obsidian `worktree`, choose `update` or `commit` explicitly for the first initialization
7. Only after that, use normal scheduled runs

## Testing

```bash
go test ./...
```

## License

Apache-2.0
