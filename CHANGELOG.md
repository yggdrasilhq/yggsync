# Changelog

This file tracks user-visible changes in `yggsync`.

## Unreleased

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
