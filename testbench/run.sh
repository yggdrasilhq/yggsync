#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
KEEP="${YGG_TESTBENCH_KEEP:-false}"
BIN="$WORKDIR/yggsync"
CFG="$WORKDIR/ygg_sync.toml"
PHONE="$WORKDIR/phone"
LAPTOP="$WORKDIR/laptop"
MEDIA="$WORKDIR/media"
REMOTE_MEDIA="$WORKDIR/remote-media"
LOG="$WORKDIR/report.log"

cleanup() {
  if [[ "$KEEP" == "true" ]]; then
    printf 'Preserving testbench workdir: %s\n' "$WORKDIR"
  else
    rm -rf "$WORKDIR"
  fi
}
trap cleanup EXIT

log() {
  printf '[testbench] %s\n' "$*" | tee -a "$LOG"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    log "missing command: $1"
    exit 1
  }
}

assert_exists() {
  [[ -e "$1" ]] || {
    log "assert failed: expected path $1"
    exit 1
  }
}

assert_missing() {
  [[ ! -e "$1" ]] || {
    log "assert failed: expected path to be absent $1"
    exit 1
  }
}

assert_file_sets_equal() {
  diff -u <(cd "$1" && find . -type f | sort) <(cd "$2" && find . -type f | sort) >>"$LOG" 2>&1 || {
    log "assert failed: file sets differ: $1 vs $2"
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

[[jobs]]
name = "media"
type = "retained_copy"
local = "$MEDIA"
remote = "$REMOTE_MEDIA"
local_retention_days = 1
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

seed_remote_update() {
  mkdir -p "$LAPTOP/journal"
  printf 'laptop-a\n' >"$LAPTOP/journal/desk.md"
}

scenario_initial_sync() {
  log "scenario: initial commit"
  seed_vault
  run_sync -jobs obsidian -worktree-op commit >>"$LOG" 2>&1
  assert_exists "$LAPTOP/notes/today.md"
}

scenario_remote_update() {
  log "scenario: remote update"
  seed_remote_update
  run_sync -jobs obsidian -worktree-op update >>"$LOG" 2>&1
  assert_exists "$PHONE/journal/desk.md"
}

scenario_delete_propagates() {
  log "scenario: commit delete propagation"
  rm -rf "$PHONE/journal"
  run_sync -jobs obsidian -worktree-op commit >>"$LOG" 2>&1
  assert_missing "$LAPTOP/journal/desk.md"
}

scenario_retained_copy_safety() {
  log "scenario: retained copy safety"
  mkdir -p "$MEDIA/day1" "$REMOTE_MEDIA"
  printf 'clip\n' >"$MEDIA/day1/clip.txt"
  run_sync -jobs media >>"$LOG" 2>&1
  assert_exists "$REMOTE_MEDIA/day1/clip.txt"
  touch -d '5 days ago' "$MEDIA/day1/clip.txt"
  run_sync -jobs media >>"$LOG" 2>&1
  assert_missing "$MEDIA/day1/clip.txt"
  assert_exists "$REMOTE_MEDIA/day1/clip.txt"
}

require_cmd go
build_bin
write_cfg
scenario_initial_sync
scenario_remote_update
scenario_delete_propagates
assert_file_sets_equal "$PHONE" "$LAPTOP"
scenario_retained_copy_safety
log "all scenarios passed"
