# ADR-001: Hub-authoritative ledger + 3-way merge for worktree jobs

Status: accepted (design), implementation in progress.

## Context

The `worktree` job type is a two-way sync between a local tree and a remote
(SMB) tree, reconciled through a single JSON "base" snapshot (the last common
state) stored only on the client. This worked but had three structural faults
that produced a silent 10-week sync outage in the field:

1. **Path-identity only, no move detection.** A file is keyed purely by its
   path string. A rename on one side is seen as delete-at-old + create-at-new.
   If the other side still holds the old path unchanged, the pair is flagged a
   *conflict* — even though nothing genuinely diverged. A bulk reorganization
   (mass rename/consolidation) on one side therefore manufactures a wall of
   false conflicts.
2. **Whole-job hard fail on any conflict.** One unresolved path aborts the
   entire job, so *nothing* syncs in either direction until it is cleared —
   with no partial progress.
3. **No durability and no visibility.** The base lives only on the client, so a
   client reset loses the common ancestor (risking resurrection of deleted
   files). Failures are silent — the field outage ran every 3h for ~10 weeks
   with no signal.

The base-snapshot / common-ancestor model itself is sound (it is a real
three-way merge). The faults are in *identity*, *conflict handling*,
*durability*, and *observability* — not in the merge premise.

## Decision

### Topology: hub-authoritative, not peer-replicated

The NAS is a natural hub; clients (phone today, more later) are spokes. This is
**not** a consensus problem (no need for the blockchain/replicated-log model).
The authority is the hub. We use the ZFS-journal shape instead: an
authoritative log co-located with the data it protects.

- **Authoritative ledger lives on the remote (hub), inside the vault:**
  `<remote-root>/.yggsync/` — travels with the data; survives client reset.
- **Client keeps only a disposable local cache** (stat+hash cache + its sync
  cursor). If lost, it is rebuilt from the hub ledger + one local scan. Never a
  source of truth.

Per-client cursors live *inside* the hub ledger, so multi-client is a schema
entry, not a redesign.

Constraint: the authoritative ledger is **JSON, never SQLite** — SQLite's
locking is unsafe over SMB/network filesystems. Writes are atomic via
temp-write → rename, with a retained `.bak`. (SQLite, if ever wanted, may only
live on the disposable client-cache side.)

### Ledger layout (on the remote, under `<root>/.yggsync/`)

```
ledger.json         # authoritative: per-file state, tombstones, client cursors
ledger.bak          # previous good copy
oplog.ndjson        # append-only audit trail: what each run did (compacted periodically)
blobs/ab/cdef...    # content-addressed base blobs (git-object style), for 3-way merge
lock                # client-id + heartbeat; stale-steal after a timeout
```

`ledger.json` schema (v3):

```jsonc
{
  "version": 3,                       // ledger generation, bumped per successful sync
  "files": {
    "src/2026/Raya.md": {
      "hash": "sha256:…", "size": 287, "mtime": 1719…,
      "gen": 7,                       // bumped on each content change → lineage
      "updated_by": "nas" | "phone-abc"
    }
  },
  "tombstones": {                     // deletes are first-class → propagate, never resurrect
    "src/2026/raya.md": { "gen": 6, "last_hash": "sha256:…", "deleted_by": "nas" }
  },
  "clients": { "phone-abc": { "last_gen": 42, "last_seen": … } }
}
```

The `.yggsync/` directory is excluded from sync (`- **/.yggsync/**`).

### Identity & move detection

Entries are path-keyed, but diffing does **content-hash move detection**: when a
path disappears and a file with the same hash appears at a new path, it is
recorded as a *move* (update the entry's path in place), not delete+create. A
mass reorg becomes a set of clean moves with zero conflicts.

The ledger doubles as the hub's stat-cache: out-of-band remote edits are still
caught, but only files whose size/mtime differ from the ledger are re-hashed —
so a run does not re-SHA256 the whole tree.

### Conflict handling: 3-way merge, then `.mergefail`

A conflict is only a *genuine* one when the same file's content changed on both
replicas to divergent states, neither descending from the ledger's recorded
`gen`. For those:

- **Text note + base blob available → `diff3` 3-way merge.**
  - Clean (non-overlapping hunks): apply the merged result to both sides, bump
    `gen`, continue silently.
  - Overlapping hunks: keep one side live, write the other side as
    `<name>.mergefail.<UTC-timestamp>` beside it, **keep syncing everything
    else**, exit non-zero, and signal (see below). No data lost, vault never
    blocked on one file.
- **Binary / no base blob → straight to `.mergefail.<timestamp>`** (cannot
  merge).

This replaces the old whole-job hard-fail. Fail-fast is preserved in spirit —
the run still exits non-zero and alerts — but a single file never blocks the
rest of the vault (the exact 10-week trap).

### Observability

On any conflict or job failure:
- `termux-notification` (Termux:API is present on the client), and
- write `<root>/CONFLICTS.md` at the vault root listing quarantined files — so
  the failure surfaces as a note *inside Obsidian on the phone*.

### Distribution

Cross-compile on a real host; the client only ever downloads a prebuilt binary
(`GOOS=android GOARCH=arm64 CGO_ENABLED=0`). The client never builds. Binary
auto-update (compare installed `-version` to latest release, checksum-verify,
gated on unmetered wifi + charging) lives in `yggclient`, not here.

## Migration (first cutover)

The field client (phone) is a stale checkout with **zero local edits** since its
initial checkout; the hub holds all real content (verified: no unique prose on
the client). So the first cutover is a one-way catch-up, not a merge:

1. Back up the client vault (cheap; already archived).
2. Initialize the hub ledger from the current hub state (`-worktree-op` init /
   ledger-init), populating `blobs/` for current versions.
3. Reset the client vault and let it pull clean from the hub against the fresh
   ledger.

After cutover both sides are identical and the ledger's common-ancestor is
exact, so the false-conflict class cannot recur.

## Deployment architecture (field)

In the field this engine ships as `yggsync-core`: the worktree sync engine. A
separate orchestrator binary (`yggsync`, Android/Termux-specific) owns device
policy — battery/temperature/network profiles, scheduling, notifications — and
delegates worktree jobs to `yggsync-core` (its `-core-bin`). The two share one
`ygg_sync.toml`. This ADR governs the core; the orchestrator is out of scope.
The core's own `internal/gate` (`[gate]`, `-reason`) is therefore usually
redundant in that deployment and stays dormant (default `manual` bypasses it);
it exists for standalone/other-host use.

Consequence for scoping: when the orchestrator points a worktree job at a vault
subtree (e.g. `.../obsidian/main`), any dotdir that was nested under the old
root becomes root-level. Exclusion rules must match the root form — see the
`**/dir/**` root-match fix (v0.3.1) — or a whole app-config dir like `.obsidian`
leaks into sync.

## Consequences

- New `internal/merge` (pure-Go diff3) and `internal/ledger` packages.
- `backend.FS` gains atomic-replace support (temp + `Rename`) for ledger writes.
- `worktree` job config gains ledger/merge options; `WorktreeStateDir` becomes
  the client cache location; the authoritative ledger is remote.
- Full re-scan cost drops (stat-cache skip); wall-clock and battery improve once
  jobs are scoped (the Obsidian job re-scoped to the `main/` subtree only).
