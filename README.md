# yggsync

`yggsync` is a small Go wrapper around `rclone` for repeatable endpoint sync jobs.
It keeps the policy in one TOML file so phones, laptops, and timers run the same commands every time.

## What It Does

- runs named sync jobs from one config file
- wraps `rclone bisync`, `copy`, and `sync`
- adds a conservative `retained_copy` mode: upload first, prune local files only after remote confirmation
- takes a lock to stop overlapping timer runs
- supports per-job timeouts
- supports explicit recovery runs with `--resync` and `--force-bisync`
- returns non-zero if any selected job fails

## Build

```bash
go build ./cmd/yggsync
```

Cross-build examples:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/yggsync-linux-amd64 ./cmd/yggsync
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o dist/yggsync-android-arm64 ./cmd/yggsync
```

## Command Line

```bash
yggsync -config ~/.config/ygg_sync.toml
yggsync -jobs notes,camera-roll
yggsync -jobs notes -dry-run
yggsync -jobs notes --resync
yggsync -jobs notes --resync --force-bisync
yggsync -list
yggsync -version
```

Flags:

- `-config`: config file path. Defaults to `~/.config/ygg_sync.toml` or `$YGG_SYNC_CONFIG`.
- `-jobs`: comma-separated job names. Default is all configured jobs.
- `-dry-run`: passes `--dry-run` to `rclone` and disables local pruning.
- `-resync`: forces `--resync` for selected `bisync` jobs.
- `-force-bisync`: adds `--force` during manual bisync recovery.
- `-list`: prints configured job names.
- `-version`: prints the binary version.

## Config File

Start from [`ygg_sync.example.toml`](./ygg_sync.example.toml).

Top-level keys:

- `rclone_binary`: path or command name for `rclone`. Default: `rclone`
- `rclone_config`: path to the `rclone.conf` file. Default: `~/.config/rclone/rclone.conf`
- `default_flags`: flags appended to every `rclone` invocation
- `lock_file`: file used to prevent overlapping runs. Default: `~/.local/state/yggsync.lock`

Each `[[jobs]]` entry defines one named sync flow.

Required per-job keys:

- `name`
- `type`
- `local`
- `remote`

Supported job types:

- `bisync`: two-way sync via `rclone bisync`
- `copy`: one-way copy from local to remote
- `sync`: one-way mirror from local to remote
- `retained_copy`: copy first, then delete eligible local files only after remote confirmation

Optional per-job keys:

- `description`: operator-facing note
- `flags`: extra `rclone` flags for that job
- `include`: plain include globs
- `exclude`: plain exclude globs
- `filter_rules`: raw `rclone --filter` rules for cases that need exact control
- `local_retention_days`: for `retained_copy`
- `keep_latest`: keep newest `N` files matching a glob
- `resync_on_exit`: retry `bisync` with `--resync` when `rclone` exits with one of these codes
- `resync_flags`: extra flags added during that automatic resync retry
- `timeout_seconds`: job-level timeout

Rule: use either `include` and `exclude`, or `filter_rules`. Do not mix them in one job.

## Config Examples

Two-way notes sync:

```toml
[[jobs]]
name = "notes"
type = "bisync"
local = "~/Documents/notes"
remote = "nas:users/alice/notes"
timeout_seconds = 900
resync_on_exit = [7]
resync_flags = ["--resync"]
filter_rules = [
  "- **/.obsidian/**",
  "- **/.trash/**",
  "- **/*.conflict*",
  "- **/[A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_]~[A-Za-z0-9].*",
]
flags = [
  "--create-empty-src-dirs",
  "--resilient",
  "--recover",
  "--conflict-loser", "pathname",
  "--max-delete", "90",
]
```

Upload-first media archive with local retention:

```toml
[[jobs]]
name = "camera-roll"
type = "retained_copy"
local = "~/Pictures/Camera"
remote = "nas:users/alice/media/camera-roll"
local_retention_days = 30
flags = ["--create-empty-src-dirs"]
```

Selective copy with `keep_latest`:

```toml
[[jobs]]
name = "downloads-archive"
type = "copy"
local = "~/Downloads"
remote = "nas:users/alice/downloads"
include = ["session-buddy-export-*", "exports/**"]
exclude = ["*"]

[[jobs.keep_latest]]
glob = "session-buddy-export-*"
keep = 1
```

## Obsidian on SMB

For Obsidian vaults on SMB/NAS storage, use conservative filters.

Recommended:

- exclude `.obsidian/**` if you do not want device-local UI state to sync
- exclude `*.conflict*` so old conflict artifacts do not keep churning
- exclude DOS 8.3 aliases if the SMB backend exposes both the long name and the alias

Example filter rules for those aliases:

```toml
filter_rules = [
  "- [A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_]~[A-Za-z0-9].*",
  "- **/[A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_][A-Za-z0-9_]~[A-Za-z0-9].*",
]
```

That rule is an inference from a real SMB failure mode where directory listings contained bogus names such as `AW5E46~3.MD` alongside the real long filenames.

## Recovery

Normal run:

```bash
yggsync -jobs notes
```

If `bisync` aborts and says it needs a resync:

```bash
yggsync -jobs notes --resync
```

If you are deliberately rebuilding the bisync state after a messy rename/delete wave:

```bash
yggsync -jobs notes --resync --force-bisync
```

Use `-dry-run` first when you are unsure:

```bash
yggsync -jobs notes --resync --force-bisync -dry-run
```

## How yggclient Fits

`yggsync` owns the binary and config schema.
`yggclient` owns endpoint wrappers, timers, Android helpers, and template rendering.

Current split:

- `yggsync`: binary, TOML schema, retention logic, lock behavior
- `yggclient`: install scripts, Android/desktop wrapper scripts, service/timer templates, endpoint-specific templates

On Linux, `yggclient` can auto-render `~/.config/ygg_sync.toml` from its desktop template instead of copying a raw file.
On Android, `yggclient` ships the phone template and the scheduling wrappers around `yggsync`.

## Testing

```bash
go test ./...
./testbench/run.sh
```

## License

Apache-2.0
