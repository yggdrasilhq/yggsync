#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
KEEP="${YGG_TESTBENCH_KEEP:-false}"
BIN="$WORKDIR/yggsync"
CFG="$WORKDIR/ygg_sync.toml"
PHONE="$WORKDIR/phone"
LAPTOP="$WORKDIR/laptop"
LOG="$WORKDIR/limitations.log"

cleanup() {
  if [[ "$KEEP" == "true" ]]; then
    printf 'Preserving limitation workdir: %s\n' "$WORKDIR"
  else
    rm -rf "$WORKDIR"
  fi
}
trap cleanup EXIT

log() {
  printf '[limitations] %s\n' "$*" | tee -a "$LOG"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    log "missing command: $1"
    exit 1
  }
}

write_cfg() {
  cat >"$CFG" <<EOF
rclone_binary = "rclone"
rclone_config = "$WORKDIR/rclone.conf"
lock_file = "$WORKDIR/yggsync.lock"
default_flags = ["--use-json-log", "--stats=120s", "--transfers=2", "--checkers=4"]

[[jobs]]
name = "obsidian"
type = "bisync"
local = "$PHONE"
remote = "$LAPTOP"
resync_on_exit = [7]
resync_flags = ["--resync"]
flags = [
  "--create-empty-src-dirs",
  "--resilient",
  "--recover",
  "--conflict-loser",
  "pathname"
]
exclude = ["**/.obsidian/**", "**/.trash/**"]
timeout_seconds = 300
EOF
  : >"$WORKDIR/rclone.conf"
}

build_bin() {
  log "building yggsync"
  (cd "$ROOT" && go build -o "$BIN" ./cmd/yggsync)
}

run_sync() {
  "$BIN" -config "$CFG" "$@"
}

seed_vault() {
  mkdir -p "$PHONE/notes" "$LAPTOP/journal"
  printf 'phone-a\n' >"$PHONE/notes/today.md"
  printf 'laptop-a\n' >"$LAPTOP/journal/desk.md"
}

require_cmd go
require_cmd rclone
build_bin
write_cfg

log "scenario: initial sync"
seed_vault
run_sync --resync -jobs obsidian >>"$LOG" 2>&1

log "scenario: rename stress"
mv "$PHONE/notes" "$PHONE/notes-renamed"
set +e
run_sync -jobs obsidian >>"$LOG" 2>&1
status=$?
set -e
log "plain rename run exit status: $status"

log "scenario: forced recovery"
set +e
run_sync --resync --force-bisync -jobs obsidian >>"$LOG" 2>&1
status=$?
set -e
log "forced recovery exit status: $status"

printf '\nPHONE\n' | tee -a "$LOG"
find "$PHONE" -maxdepth 3 -print | sort | tee -a "$LOG"
printf '\nLAPTOP\n' | tee -a "$LOG"
find "$LAPTOP" -maxdepth 3 -print | sort | tee -a "$LOG"

if [[ -e "$LAPTOP/notes" || -e "$PHONE/notes" ]]; then
  log "rename limitation reproduced: old path still exists after recovery"
  exit 1
fi

log "rename limitation not reproduced in this environment"
