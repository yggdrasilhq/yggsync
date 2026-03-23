# yggsync

`yggsync` is a native Go sync engine for Yggdrasil endpoints.
It syncs local files directly to local paths or SMB shares without shelling out to `rclone`.

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
- a configured target reference such as `nas:immich/dada/DCIM`

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

### Copy Job

```toml
[[jobs]]
name = "screenshots"
type = "retained_copy"
local = "~/Pictures/Screenshots"
remote = "nas:immich02/alice/desktop/Screenshots"
local_retention_days = 30
```

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

## Direct SMB Use

If you personally use only one live Obsidian instance at a time, opening the vault directly from an SMB mount is still a supported human workflow.
`yggsync` does not forbid that.

The `worktree` mode exists for the cases where you want a safer local-vault workflow without relying on direct live editing over SMB.

## Testing

```bash
go test ./...
```

## License

Apache-2.0
