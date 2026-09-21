# Changelog

This file tracks user-visible changes in `yggsync`.

## Unreleased

## v0.4.1

- Fix `copy`/`sync`/`retained_copy` jobs whose local root is a symlink
  (Termux's `~/storage/shared`). The local walk used Lstat-based traversal,
  which refuses to descend into a symlinked root: one deployed lineage
  offered the root itself as a file and failed every run with
  `read …/storage/shared: is a directory` (this is the "yggsync bulk
  stopped" notification seen on every completed bulk run); the generic
  engine silently produced an empty snapshot instead. The root is now
  resolved through symlinks before walking, and the root itself is never
  offered as an entry.
- Unreadable subtrees (e.g. `Android/data` under Android 11+ scoped storage)
  no longer abort a local walk with a failing job: the entry is skipped with
  a `walk: skipping …` log line while siblings are still visited. A job
  source root that is missing, unreadable, or not a directory fails the run
  loudly instead of masquerading as "nothing to do"; a missing destination
  root remains legitimately empty until the first copy creates it.
- Add the device-run surface the Android wrappers use: `yggsync run
  -profile <name>` gates against `[profiles.<name>]` in the device-runtime
  TOML (`min_battery`, `max_battery_temp_c`), defaults to the non-worktree
  jobs when no `-jobs` list is given, logs `profile=<name> reason=<why>`
  lines, and posts a `yggsync <name> stopped` Termux notification when a
  profile run fails and the profile sets `notify = true`. Scheduled-run
  skips are log-only so routine battery gating stays out of the
  notification shade; interactive (`reason=manual`) skips still notify.
  Cellular byte caps in the runtime TOML are accepted but not enforced —
  scheduled jobs are kept off metered networks by the Termux job scheduler.
- Add `yggsync android install-jobs`: regenerates
  `~/.local/state/yggsync/jobs/run-{obsidian,bulk}.sh` and registers them
  with `termux-job-scheduler` (jobs 101/102, unmetered, persisted), so
  Termux:Boot can re-assert the schedule at every power-on. Reconstructs the
  surface of the phone-only v0.4.0 fork, whose source was never pushed;
  this release supersedes it.
- A named single-job invocation (`yggsync obsidian -reason scheduled`) picks
  up gating and notification policy from `[profiles.<job-name>]` when
  present.
- Termux notifications never block a run: the notifier is started detached
  with a 10s timeout. A hung Termux:API app previously wedged whole runs —
  on one phone a run held the sync lock for days while blocked on a
  notification child, so every later run bounced with "already running"
  and no backup ran at all.

## v0.3.1

- Fix filter matching so a `**/dir/**` rule also excludes the directory at the
  vault root, not only nested copies. Scoping a worktree job to a subtree turned
  the vault's `.obsidian/` into a root-level dir that `**/.obsidian/**` failed to
  match, leaking the whole app-config directory into sync. Added filter tests.

## v0.3.0

- Worktree sync is being reworked to a hub-authoritative ledger with content
  based move detection and a diff3 three-way merge, replacing the whole-job
  hard-fail on conflict. See `docs/adr-001-hub-authoritative-ledger.md`.
- Add `internal/merge`: a dependency-free line-based three-way merge. Clean
  merges apply automatically; genuinely divergent hunks are reported so callers
  can preserve both sides via a `.mergefail.<timestamp>` sidecar instead of
  blocking the whole job.
- Add `internal/ledger`: the hub-authoritative sync state — an atomic JSON
  ledger (temp+rename, retained `.bak`) with a content-addressed blob store,
  tombstones, and per-client cursors, stored under `<remote>/.yggsync/`.
- Rewrite the `worktree` job to reconcile against the ledger (base = common
  ancestor) with content-hash move handling: a rename is a clean delete+add and
  never a conflict. Divergent files are diff3-merged when clean, else quarantined
  as `.mergefail` sidecars with a `CONFLICTS.md` report and a Termux notification.
  New job fields: `client_id`, `no_merge`.
- Add a mass-delete safety guard: a run that would delete a large share of hub
  files (e.g. an emptied local tree) aborts unless `-allow-mass-delete` is given.
- Fix `-dry-run` for worktree jobs: it now reports planned actions without
  touching either replica or the ledger.
- Job names may be passed as positional args (`yggsync obsidian`) in addition to
  `-jobs`.
- Add an optional device gate (`internal/gate`, `[gate]` config or a `-runtime`
  TOML): scheduled runs (`-reason` other than `manual`) skip with a Termux
  notification when the battery is below a threshold and not charging, or the
  battery is too hot. Portable no-op where `termux-battery-status` is absent.
