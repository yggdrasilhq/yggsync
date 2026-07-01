# Migrating a worktree job to the ledger engine

This is the one-time cutover from the old client-only JSON base (per-file
`worktree_state_dir/<job>.json`, whole-job hard-fail on conflict) to the
hub-authoritative ledger engine (ADR-001). It is written for the field case: a
single Obsidian client (a phone) whose local tree is a stale, read-only replica
and whose hub holds all authoritative content.

## Preconditions

- The hub has **no** `.yggsync/` ledger yet for this job (fresh start).
- You have verified there is no content on the client that is missing from the
  hub. (If unsure, back up the client tree first; the cutover discards it.)
- The new `yggsync` binary is built and available on the client.

## Why a plain sync is the migration

The new engine needs no special "init" mode. On first contact with a hub that
has no ledger, it seeds the ledger from the current hub + client state and
reconciles per path. The safe shapes:

- **empty client, populated hub** -> client pulls everything; ledger records the
  hub state. This is the field case.
- identical client and hub -> ledger records state, no data moves.

The dangerous shape — a client that *used* to hold files, now emptied, syncing
against a hub whose ledger still lists those files with a high client cursor —
cannot occur on a fresh ledger (cursor starts at 0, so a reset client re-pulls).
The mass-delete guard is the backstop if a stale ledger is ever present.

## Procedure

1. **Scope and identify the job** in `~/.config/ygg_sync.toml` on the client:
   - point `local`/`remote` at the single vault subtree you want (e.g.
     `.../obsidian/main`);
   - set a unique `client_id`;
   - ensure `filter_rules` excludes `- **/.yggsync/**` and `- CONFLICTS.md`.

2. **Stop the old scheduled job** so it cannot run mid-cutover (e.g. cancel the
   Termux job id, or remove its scheduler entry).

3. **Remove the stale local base** for this job:
   `rm -f <worktree_state_dir>/<job>.json`. (The hub ledger is the new base.)

4. **Discard the stale client vault** for this job's `local` path (field case:
   the phone copy is a read-only stale replica with no unique content). Back it
   up first if you want a safety copy.

5. **Confirm the hub has no `.yggsync/`** for this remote yet:
   `ls <remote>/.yggsync` should be absent. If a ledger exists from a prior
   experiment, remove it so the seed is clean.

6. **Dry-run** and read the plan:
   `yggsync <job> -config ~/.config/ygg_sync.toml -dry-run`
   Expect a list of `would pull ...` lines equal to the hub file count, no
   `would delete` on the hub, and no conflicts.

7. **Run for real:**
   `yggsync <job> -config ~/.config/ygg_sync.toml`
   The client fills from the hub; `<remote>/.yggsync/{ledger.json,blobs/}`
   appears; exit status is 0.

8. **Verify parity:** the client tree matches the hub (empty directories aside).

9. **Re-enable the scheduled job**, now invoking the new binary.

## Rollback

The cutover only writes to the client (pull) and creates `<remote>/.yggsync/` on
the hub; it does not modify existing hub content on the field path. To roll back:
stop the job, delete `<remote>/.yggsync/`, restore the client backup if you made
one, and reinstate the old binary + `<job>.json`.

## After cutover

Steady state is a scheduled `yggsync <job>`; conflicts (if the client ever edits)
surface as `.mergefail` sidecars + `CONFLICTS.md` + a Termux notification rather
than a silent stall. A future client (laptop) joins by setting its own
`client_id` and running a sync — the cursor guard pulls it up to date without
risking hub deletions.
