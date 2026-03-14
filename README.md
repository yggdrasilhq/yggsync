# yggsync

`yggsync` is a small Go sync orchestrator for phones, laptops, and homelabs.
It wraps `rclone` with a TOML job file so one binary can run a handful of repeatable sync flows without forcing every user to hand-roll shell scripts.

## Why it exists

Most personal sync setups begin as one-off commands.
That works until the jobs multiply: notes, camera roll, screenshots, chat exports, desktop downloads, and long-retention archives.
At that point you need a tool that stays simple but gives you repeatable jobs, safer pruning, and one place to describe intent.

`yggsync` is that layer.

## Capabilities

- single static Go binary
- TOML-defined job catalog
- `bisync`, `copy`, `sync`, and `retained_copy` job types
- optional `keep_latest` rotation rules
- optional one-time `--resync` retry for `bisync` jobs
- `--dry-run`, `--list`, and `--jobs` selection for safe iteration

## Typical uses

- phone notes mirrored to a NAS
- camera roll uploads with local retention
- screenshot archives
- chat export retention
- desktop download subsets copied to a remote

## Build

```bash
go build ./cmd/yggsync
```

Cross-build examples:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/yggsync-linux-amd64 ./cmd/yggsync
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -o dist/yggsync-android-arm64 ./cmd/yggsync
```

## Usage

```bash
yggsync -config ~/.config/ygg_sync.toml
yggsync -jobs notes,camera-roll -dry-run
yggsync -list
yggsync -version
```

## Config

Start from [`ygg_sync.example.toml`](./ygg_sync.example.toml).
Key concepts:

- `rclone_binary`: binary to invoke, default `rclone`
- `rclone_config`: rclone config path
- `default_flags`: flags applied to every rclone invocation
- `[[jobs]]`: named sync jobs with local path, remote path, and type
- `[[jobs.keep_latest]]`: keep newest N files matching a glob

## Boundaries

- `yggsync`: sync engine and config format
- `yggclient`: endpoint wrappers, install helpers, service templates
- `yggdocs`: user guides, recipes, and ecosystem docs

## License

Apache-2.0
