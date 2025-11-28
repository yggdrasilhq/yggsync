# yggsync

Lightweight sync orchestrator for Termux/Android and small homelabs. It wraps `rclone` with a TOML config so you can run multiple jobs (bisync, push with retention, keep-latest rotation) from a single statically linked binary.

## Features
- Single binary (pure Go, CGO disabled) – easy to drop into Termux.
- Jobs defined in `~/.config/ygg_sync.toml`.
- Job types:
  - `bisync` with automatic one-time `--resync` retry on specific exit codes.
  - `copy`/`sync` push jobs.
  - `retained_copy` jobs that prune old local files before uploading.
  - `keep_latest` rules to retain only the newest N files matching globs (e.g., Signal backups).
- Per-job include/exclude globs and extra flags, plus global default flags.
- `--dry-run` applies to both local pruning and rclone calls.

## Build
```bash
git clone https://github.com/<you>/yggsync
cd yggsync
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/yggsync      # Termux on ARM64
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/yggsync    # Bionic build
```
The resulting `yggsync` binary can be copied to your device (e.g., `~/git/ygg_client/android/bin/yggsync`).

## Usage
```bash
yggsync -config ~/.config/ygg_sync.toml              # run all jobs
yggsync -jobs obsidian,dcim -dry-run                 # select jobs, no changes
yggsync -list                                        # list job names
```

## Config (TOML)
See `ygg_sync.example.toml` for a full template. Key fields:
- `rclone_binary` (default `rclone`)
- `rclone_config` (default `~/.config/rclone/rclone.conf`)
- `default_flags` applied to every rclone run
- `[[jobs]]` entries with:
  - `name`, `type` (`bisync`|`copy`|`sync`|`retained_copy`)
  - `local`, `remote`
  - optional `flags`, `include`, `exclude`
  - `local_retention_days` (for `retained_copy`)
  - `keep_latest` rules: `[[jobs.keep_latest]] glob="Signal*.backup*" keep=2`
  - `resync_on_exit` (exit codes that trigger `--resync` retry for bisync)
  - `resync_flags` (extra flags for the retry)

## Notes
- The tool sets `RCLONE_CONFIG` env var based on `rclone_config`.
- Logs are printed to stdout/stderr; redirect as needed.
- Keep retention rules conservative until you trust the config. Use `--dry-run` first.
