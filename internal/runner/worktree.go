package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"yggsync/internal/backend"
	"yggsync/internal/config"
	"yggsync/internal/filter"
	"yggsync/internal/ledger"
	"yggsync/internal/merge"
)

// conflictsReport is written at the vault root so quarantined files surface as a
// note inside Obsidian on the client.
const conflictsReport = "CONFLICTS.md"

// runWorktree performs a two-way reconciliation between the local tree and the
// remote hub, using the hub-authoritative ledger as the common ancestor. See
// docs/adr-001-hub-authoritative-ledger.md.
//
// Correctness rests on the per-path three-way decision (base = ledger):
//   - unchanged on one side  -> the other side is authoritative
//   - changed on both        -> diff3 merge, else quarantine as .mergefail
//
// Renames therefore never manufacture conflicts: a moved file is a clean delete
// at the old path plus a clean add at the new path, each resolved independently.
func (r *Runner) runWorktree(ctx context.Context, job config.Job) error {
	local, remote, matcher, err := r.openJobFS(ctx, job)
	if err != nil {
		return err
	}
	defer local.Close()
	defer remote.Close()

	clientID := job.ClientID
	if clientID == "" {
		clientID = defaultClientID()
	}
	now := time.Now().UTC()

	release, err := ledger.AcquireLock(ctx, remote, ledger.Dir, clientID, now, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("job %s: %w", job.Name, err)
	}
	defer release()

	L, err := ledger.Load(ctx, remote, ledger.Dir)
	if err != nil {
		return fmt.Errorf("job %s: load ledger: %w", job.Name, err)
	}

	localSnap, err := scanWorktree(ctx, local, matcher)
	if err != nil {
		return err
	}
	remoteSnap, err := scanWorktree(ctx, remote, matcher)
	if err != nil {
		return err
	}

	cursor := L.Clients[clientID].LastGen
	paths := unionPaths(L.Files, L.Tombstones, localSnap.Files, remoteSnap.Files)

	// Safety pre-pass: refuse a run that would delete a large share of the hub's
	// tracked files (e.g. an emptied or misconfigured local tree). Conflicts
	// should never occur silently; a mass deletion must fail loudly.
	if !r.allowMassDelete {
		if del := plannedRemoteDeletes(paths, L, localSnap, remoteSnap, cursor); massDelete(len(del), len(L.Files)) {
			return fmt.Errorf("job %s: refusing to delete %d of %d hub files in one run "+
				"(local tree looks empty or wrong); rerun with -allow-mass-delete if intended",
				job.Name, len(del), len(L.Files))
		}
	}

	wt := &wtReconciler{
		ctx: ctx, local: local, remote: remote,
		localSnap: localSnap, remoteSnap: remoteSnap,
		files: cloneFiles(L.Files), tombs: cloneTombs(L.Tombstones),
		gen: L.Generation + 1, clientID: clientID, cursor: cursor,
		now: now, dryRun: r.dryRun, noMerge: job.NoMerge,
	}

	for _, p := range paths {
		if err := wt.decide(p); err != nil {
			return fmt.Errorf("job %s: reconcile %s: %w", job.Name, p, err)
		}
	}

	if r.dryRun {
		if len(wt.conflicts) > 0 {
			return conflictError(job.Name, wt.conflicts)
		}
		return nil
	}

	L.Files = wt.files
	L.Tombstones = wt.tombs
	L.Generation = wt.gen
	L.Clients[clientID] = ledger.ClientCursor{LastGen: wt.gen, LastSeen: now.Unix()}
	if err := ledger.Save(ctx, remote, ledger.Dir, L); err != nil {
		return fmt.Errorf("job %s: save ledger: %w", job.Name, err)
	}
	if err := ledger.GCBlobs(ctx, remote, ledger.Dir, L); err != nil {
		log.Printf("job %s: blob gc (non-fatal): %v", job.Name, err)
	}

	if len(wt.conflicts) > 0 {
		writeConflictsReport(ctx, local, remote, job.Name, wt.conflicts, now)
		notifyConflicts(job.Name, wt.conflicts)
		return conflictError(job.Name, wt.conflicts)
	}
	return nil
}

type wtReconciler struct {
	ctx                   context.Context
	local, remote         backend.FS
	localSnap, remoteSnap Snapshot
	files                 map[string]ledger.FileEntry
	tombs                 map[string]ledger.Tombstone
	gen                   int64
	clientID              string
	cursor                int64
	now                   time.Time
	dryRun                bool
	noMerge               bool
	conflicts             []string
}

// decide resolves a single path and performs the resulting I/O.
func (w *wtReconciler) decide(p string) error {
	la, aok := w.localSnap.Files[p]
	lb, bok := w.remoteSnap.Files[p]
	le, lok := w.files[p]
	tomb, tok := w.tombs[p]

	a, b, l := hashOf(la, aok), hashOf(lb, bok), ""
	if lok {
		l = le.Hash
	}

	// Tombstone guard: a recorded deletion must not be resurrected by a client
	// that still holds the deleted version.
	if tok && l == "" {
		switch {
		case a == "" && b == "":
			return nil // both honor the deletion
		case a == tomb.LastHash && b == "":
			return w.deleteLocal(p) // stale client copy -> remove
		case b == tomb.LastHash && a == "":
			return w.deleteRemote(p, tomb.LastHash)
		default:
			// A side holds new content (re-created after deletion): clear the
			// tombstone and fall through to normal resolution.
			delete(w.tombs, p)
		}
	}

	if a == b {
		return w.decideConverged(p, a, l, lok, lb, bok)
	}

	aChanged := a != l
	bChanged := b != l

	switch {
	case !aChanged:
		// Local unchanged from base -> remote (hub) is authoritative.
		if b == "" {
			return w.deleteLocal(p) // remote deleted; ledger entry cleared in helper
		}
		return w.copyToLocal(p, lb)
	case !bChanged:
		// Remote unchanged from base -> local is authoritative, UNLESS this
		// client has not yet received this version (fresh client / reset tree):
		// then pull instead of interpreting local-absence as a deletion.
		if a == "" {
			if lok && le.Gen > w.cursor {
				return w.copyToLocal(p, lb) // client never had it: pull, don't delete hub
			}
			return w.deleteRemote(p, l)
		}
		return w.copyToRemote(p, la)
	default:
		return w.resolveDivergence(p, a, b, l, la, lb, aok, bok)
	}
}

func (w *wtReconciler) decideConverged(p, a, l string, lok bool, lb FileState, bok bool) error {
	if a == "" {
		if lok {
			delete(w.files, p)
		}
		return nil
	}
	if a != l {
		// Same new content on both sides (e.g. identical edit): record it.
		content, err := readAll(w.ctx, w.remote, p)
		if err != nil {
			return err
		}
		return w.record(p, content, lb, "both")
	}
	return nil // fully converged, nothing to do
}

func (w *wtReconciler) resolveDivergence(p, a, b, l string, la, lb FileState, aok, bok bool) error {
	// Delete-vs-edit: preserve the edited side (never lose an edit), flag it.
	if a == "" {
		w.flag(p)
		return w.copyToLocal(p, lb)
	}
	if b == "" {
		w.flag(p)
		return w.copyToRemote(p, la)
	}

	aContent, err := readAll(w.ctx, w.local, p)
	if err != nil {
		return err
	}
	bContent, err := readAll(w.ctx, w.remote, p)
	if err != nil {
		return err
	}

	if !w.noMerge && l != "" && isText(aContent) && isText(bContent) {
		if base, err := ledger.GetBlob(w.ctx, w.remote, ledger.Dir, l); err == nil {
			if res := merge.Merge(string(base), string(aContent), string(bContent)); res.Clean {
				return w.writeBoth(p, []byte(res.Merged), lb) // apply merged text to both replicas
			}
		}
	}

	// Cannot cleanly merge: keep the hub (remote) version live on both replicas
	// and preserve the local version as a .mergefail sidecar. No data is lost.
	w.flag(p)
	sidecar := fmt.Sprintf("%s.mergefail.%s", p, w.now.Format("20060102-150405"))
	if err := w.writeBoth(sidecar, aContent, la); err != nil {
		return err
	}
	return w.copyToLocal(p, lb) // align local to the live hub version
}

// --- primitive operations (each keeps the ledger in step) ----------------

func (w *wtReconciler) copyToLocal(p string, st FileState) error {
	if w.dryRun {
		log.Printf("[dry-run] would pull %s from hub", p)
		return nil
	}
	content, err := readAll(w.ctx, w.remote, p)
	if err != nil {
		return err
	}
	if err := w.local.WriteFile(w.ctx, p, bytes.NewReader(content), os.FileMode(st.Mode), st.ModTime); err != nil {
		return err
	}
	return w.record(p, content, st, "hub")
}

func (w *wtReconciler) copyToRemote(p string, st FileState) error {
	if w.dryRun {
		log.Printf("[dry-run] would push %s to hub", p)
		return nil
	}
	content, err := readAll(w.ctx, w.local, p)
	if err != nil {
		return err
	}
	if err := w.remote.WriteFile(w.ctx, p, bytes.NewReader(content), os.FileMode(st.Mode), st.ModTime); err != nil {
		return err
	}
	return w.record(p, content, st, w.clientID)
}

// writeBoth writes identical content to both replicas and records it.
func (w *wtReconciler) writeBoth(p string, content []byte, st FileState) error {
	if w.dryRun {
		log.Printf("[dry-run] would write %s to both replicas", p)
		return nil
	}
	mode := os.FileMode(st.Mode)
	if mode == 0 {
		mode = 0o644
	}
	mtime := st.ModTime
	if mtime.IsZero() {
		mtime = w.now
	}
	if err := w.local.WriteFile(w.ctx, p, bytes.NewReader(content), mode, mtime); err != nil {
		return err
	}
	if err := w.remote.WriteFile(w.ctx, p, bytes.NewReader(content), mode, mtime); err != nil {
		return err
	}
	return w.record(p, content, FileState{Mode: uint32(mode), ModTime: mtime}, w.clientID)
}

// record stores the base blob and updates the ledger entry for p.
func (w *wtReconciler) record(p string, content []byte, st FileState, by string) error {
	hash := hashBytes(content)
	if !w.dryRun {
		if err := ledger.PutBlob(w.ctx, w.remote, ledger.Dir, hash, content); err != nil {
			return err
		}
	}
	mtime := st.ModTime
	if mtime.IsZero() {
		mtime = w.now
	}
	w.files[p] = ledger.FileEntry{
		Hash: hash, Size: int64(len(content)), ModTime: mtime.Unix(),
		Gen: w.gen, UpdatedBy: by,
	}
	delete(w.tombs, p)
	return nil
}

func (w *wtReconciler) deleteLocal(p string) error {
	if err := w.local.Remove(w.ctx, p); err != nil {
		return err
	}
	w.recordTombstone(p, "hub")
	return nil
}

func (w *wtReconciler) deleteRemote(p, lastHash string) error {
	if err := w.remote.Remove(w.ctx, p); err != nil {
		return err
	}
	_ = lastHash
	w.recordTombstone(p, w.clientID)
	return nil
}

func (w *wtReconciler) recordTombstone(p, by string) {
	last := ""
	if e, ok := w.files[p]; ok {
		last = e.Hash
	}
	delete(w.files, p)
	w.tombs[p] = ledger.Tombstone{Gen: w.gen, LastHash: last, DeletedBy: by, DeletedAt: w.now.Unix()}
}

func (w *wtReconciler) flag(p string) {
	w.conflicts = append(w.conflicts, p)
}

// --- helpers -------------------------------------------------------------

func hashOf(st FileState, ok bool) string {
	if !ok {
		return ""
	}
	return st.Hash
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readAll(ctx context.Context, fs backend.FS, rel string) ([]byte, error) {
	rc, err := fs.OpenReader(ctx, rel)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// isText reports whether content looks like text (no NUL byte in the head).
func isText(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	return !bytes.ContainsRune(b[:n], 0)
}

// scanWorktree scans fs honoring the job matcher, excluding the ledger dir and
// the conflicts report so neither is treated as vault content. The exclusion is
// applied before hashing so the blob store (a full copy of the vault) is never
// re-hashed on every run.
func scanWorktree(ctx context.Context, fs backend.FS, matcher *filter.Matcher) (Snapshot, error) {
	snap := Snapshot{Files: map[string]FileState{}, Dirs: map[string]DirState{}}
	err := fs.Walk(ctx, func(entry backend.Entry) error {
		rel := strings.ReplaceAll(entry.Path, "\\", "/")
		if rel == conflictsReport || rel == ledger.Dir || strings.HasPrefix(rel, ledger.Dir+"/") {
			return nil
		}
		if !matcher.Match(rel) {
			return nil
		}
		if entry.IsDir {
			snap.Dirs[rel] = DirState{Mode: uint32(entry.Mode.Perm())}
			return nil
		}
		sum, err := hashFile(ctx, fs, rel)
		if err != nil {
			return err
		}
		snap.Files[rel] = FileState{
			Size: entry.Size, Mode: uint32(entry.Mode.Perm()),
			ModTime: entry.ModTime.UTC(), Hash: sum,
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func unionPaths(maps ...interface{}) []string {
	set := map[string]struct{}{}
	for _, m := range maps {
		switch v := m.(type) {
		case map[string]ledger.FileEntry:
			for k := range v {
				set[k] = struct{}{}
			}
		case map[string]ledger.Tombstone:
			for k := range v {
				set[k] = struct{}{}
			}
		case map[string]FileState:
			for k := range v {
				set[k] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// plannedRemoteDeletes returns paths that this run would delete from the hub,
// computed cheaply from hashes alone (no content reads) for the safety guard.
func plannedRemoteDeletes(paths []string, L *ledger.Ledger, localSnap, remoteSnap Snapshot, cursor int64) []string {
	var del []string
	for _, p := range paths {
		la, aok := localSnap.Files[p]
		lb, bok := remoteSnap.Files[p]
		le, lok := L.Files[p]
		a, b, l := hashOf(la, aok), hashOf(lb, bok), ""
		if lok {
			l = le.Hash
		}
		if a == b {
			continue
		}
		// Remote authoritative (local unchanged) never deletes the hub.
		if a == l {
			continue
		}
		// Local authoritative deletion of a version this client actually had.
		if b == l && a == "" && !(lok && le.Gen > cursor) {
			del = append(del, p)
		}
	}
	return del
}

func massDelete(deletes, tracked int) bool {
	return deletes > 10 && tracked > 0 && deletes*2 > tracked
}

func cloneFiles(in map[string]ledger.FileEntry) map[string]ledger.FileEntry {
	out := make(map[string]ledger.FileEntry, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTombs(in map[string]ledger.Tombstone) map[string]ledger.Tombstone {
	out := make(map[string]ledger.Tombstone, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func defaultClientID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "client"
}

func writeConflictsReport(ctx context.Context, local, remote backend.FS, job string, conflicts []string, now time.Time) {
	var b strings.Builder
	fmt.Fprintf(&b, "# yggsync conflicts (%s)\n\n", job)
	fmt.Fprintf(&b, "Run at %s. The following paths could not be merged cleanly.\n", now.Format(time.RFC3339))
	b.WriteString("For each, the hub version is live and your version is kept beside it as ")
	b.WriteString("`<name>.mergefail.<timestamp>`. Resolve, then delete the sidecar.\n\n")
	sort.Strings(conflicts)
	for _, p := range conflicts {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	content := []byte(b.String())
	// Best effort on both replicas; failures here must not fail the job.
	_ = local.WriteFile(ctx, conflictsReport, bytes.NewReader(content), 0o644, now)
	_ = remote.WriteFile(ctx, conflictsReport, bytes.NewReader(content), 0o644, now)
}

// notifyConflicts fires a Termux notification when available; a no-op elsewhere.
func notifyConflicts(job string, conflicts []string) {
	bin, err := exec.LookPath("termux-notification")
	if err != nil {
		return
	}
	msg := fmt.Sprintf("%d unmerged file(s) in %s; see CONFLICTS.md", len(conflicts), job)
	cmd := exec.Command(bin, "--title", "yggsync conflict", "--content", msg, "--id", "yggsync-"+job)
	_ = cmd.Run()
}
