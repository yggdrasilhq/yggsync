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
lock_file = "$WORKDIR/yggsync.lock"
worktree_state_dir = "$WORKDIR/worktrees"

[[jobs]]
name = "obsidian"
type = "worktree"
local = "$PHONE"
remote = "$LAPTOP"
exclude = ["**/.obsidian/**", "**/.trash/**"]
timeout_seconds = 300
EOF
}

build_bin() {
  log "building yggsync"
  (cd "$ROOT" && go build -o "$BIN" ./cmd/yggsync)
}

run_sync() {
  "$BIN" -config "$CFG" "$@"
}

seed_vault() {
  mkdir -p "$PHONE/notes"
  printf 'phone-a\n' >"$PHONE/notes/today.md"
}

require_cmd go
build_bin
write_cfg

log "scenario: initial sync"
seed_vault
run_sync -jobs obsidian -worktree-op commit >>"$LOG" 2>&1

log "scenario: conflicting edits"
printf 'phone-b\n' >"$PHONE/notes/today.md"
printf 'laptop-b\n' >"$LAPTOP/notes/today.md"
set +e
run_sync -jobs obsidian -worktree-op sync >>"$LOG" 2>&1
status=$?
set -e
log "conflict sync exit status: $status"

printf '\nPHONE\n' | tee -a "$LOG"
find "$PHONE" -maxdepth 3 -print | sort | tee -a "$LOG"
printf '\nLAPTOP\n' | tee -a "$LOG"
find "$LAPTOP" -maxdepth 3 -print | sort | tee -a "$LOG"

if [[ "$status" -eq 0 ]]; then
  log "expected an explicit conflict, but sync exited successfully"
  exit 1
fi

log "conflict behavior reproduced as expected"
