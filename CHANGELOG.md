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
