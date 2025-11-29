# Agent Notes

## Releases

- Prefer the Forgejo API via `curl` over `tea`.
- List releases:
  ```bash
  curl -H "Authorization: token $GITEA_TOKEN" https://g.gour.top/api/v1/repos/yggdrasil/yggsync/releases | jq
  ```
- Upload asset to an existing release (example: v0.1.0):
  ```bash
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/yggsync-linux-amd64 ./cmd/yggsync
  RELEASE_ID=$(curl -s -H "Authorization: token $GITEA_TOKEN" https://g.gour.top/api/v1/repos/yggdrasil/yggsync/releases | jq 'map(select(.tag_name=="v0.1.0"))[0].id')
  curl -s -X POST \
    -H "Authorization: token $GITEA_TOKEN" \
    -H "Content-Type: multipart/form-data" \
    -F "attachment=@/tmp/yggsync-linux-amd64" \
    "https://g.gour.top/api/v1/repos/yggdrasil/yggsync/releases/${RELEASE_ID}/assets?name=yggsync-linux-amd64"
  ```
- Delete a release (example: v0.1.1):
  ```bash
  RELEASE_ID=$(curl -s -H "Authorization: token $GITEA_TOKEN" https://g.gour.top/api/v1/repos/yggdrasil/yggsync/releases | jq 'map(select(.tag_name=="v0.1.1"))[0].id')
  curl -s -X DELETE -H "Authorization: token $GITEA_TOKEN" "https://g.gour.top/api/v1/repos/yggdrasil/yggsync/releases/${RELEASE_ID}"
  ```
