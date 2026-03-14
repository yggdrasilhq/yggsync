# AGENTS

## Mission

Maintain `yggsync` as a small, auditable sync engine for the Yggdrasil ecosystem.
Keep the binary generic. Keep private infrastructure in user config files, not in tracked examples.

## Scope

This repository owns:
- the Go CLI
- the TOML config schema
- release build conventions
- conservative sync semantics around retention and remote confirmation

This repository does not own:
- endpoint install workflows (`yggclient`)
- ISO/build logic (`yggdrasil`)
- long-form documentation (`yggdocs`)

## Release Notes

Prefer the Forgejo API via `curl` for release automation.
Public release assets should live under `yggdrasilhq/yggsync`.

Examples:

```bash
curl -H "Authorization: token $GITEA_TOKEN" \
  https://g.gour.top/api/v1/repos/yggdrasilhq/yggsync/releases | jq
```

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/yggsync-linux-amd64 ./cmd/yggsync
RELEASE_ID=$(curl -s -H "Authorization: token $GITEA_TOKEN" \
  https://g.gour.top/api/v1/repos/yggdrasilhq/yggsync/releases | \
  jq 'map(select(.tag_name=="v0.1.3"))[0].id')
curl -s -X POST \
  -H "Authorization: token $GITEA_TOKEN" \
  -H "Content-Type: multipart/form-data" \
  -F "attachment=@/tmp/yggsync-linux-amd64" \
  "https://g.gour.top/api/v1/repos/yggdrasilhq/yggsync/releases/${RELEASE_ID}/assets?name=yggsync-linux-amd64"
```

## Guardrails

- Do not commit private remotes, usernames, domains, or hostnames.
- Keep the sample config realistic but generic.
- Prefer additive features; this tool should stay small enough to fully read in one sitting.
- Any pruning behavior must remain conservative and dry-run friendly.
