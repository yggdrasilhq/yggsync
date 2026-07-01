// Package ledger implements the hub-authoritative sync ledger for worktree
// jobs: an atomic JSON document plus a content-addressed blob store, both
// stored on the remote hub under a ".yggsync" directory that travels with the
// vault. See docs/adr-001-hub-authoritative-ledger.md.
//
// The ledger is deliberately JSON, never SQLite: SQLite locking is unsafe over
// SMB/network filesystems. Writes are atomic via temp-write + rename with a
// retained ".bak".
package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"yggsync/internal/backend"
)

// Format is the ledger schema version.
const Format = 3

// Dir is the ledger directory relative to a job's remote root.
const Dir = ".yggsync"

const (
	ledgerFile = "ledger.json"
	bakFile    = "ledger.bak"
	tmpFile    = "ledger.json.tmp"
	lockFile   = "lock"
	blobsDir   = "blobs"
)

// FileEntry is the authoritative record of one tracked file.
type FileEntry struct {
	Hash      string `json:"hash"`       // sha256 hex of content
	Size      int64  `json:"size"`       // bytes
	ModTime   int64  `json:"mtime"`      // unix seconds (stat-cache hint)
	Gen       int64  `json:"gen"`        // ledger generation at last content change (lineage)
	UpdatedBy string `json:"updated_by"` // client id or "hub"
}

// Tombstone records a deletion so it propagates and is not resurrected.
type Tombstone struct {
	Gen       int64  `json:"gen"`
	LastHash  string `json:"last_hash"`
	DeletedBy string `json:"deleted_by"`
	DeletedAt int64  `json:"deleted_at"` // unix seconds
}

// ClientCursor is a per-client sync bookmark, stored in the hub ledger.
type ClientCursor struct {
	LastGen  int64 `json:"last_gen"`
	LastSeen int64 `json:"last_seen"` // unix seconds
}

// Ledger is the authoritative sync state for one worktree job.
type Ledger struct {
	Format     int                     `json:"format"`
	Generation int64                   `json:"generation"` // monotonic, bumped per successful sync
	Files      map[string]FileEntry    `json:"files"`
	Tombstones map[string]Tombstone    `json:"tombstones"`
	Clients    map[string]ClientCursor `json:"clients"`
}

// New returns an empty ledger.
func New() *Ledger {
	return &Ledger{
		Format:     Format,
		Files:      map[string]FileEntry{},
		Tombstones: map[string]Tombstone{},
		Clients:    map[string]ClientCursor{},
	}
}

func (l *Ledger) ensureMaps() {
	if l.Files == nil {
		l.Files = map[string]FileEntry{}
	}
	if l.Tombstones == nil {
		l.Tombstones = map[string]Tombstone{}
	}
	if l.Clients == nil {
		l.Clients = map[string]ClientCursor{}
	}
	if l.Format == 0 {
		l.Format = Format
	}
}

// Load reads the ledger from dir on fs, falling back to the .bak copy if the
// primary is missing or corrupt. A wholly absent ledger is not an error: an
// empty ledger is returned so callers can treat a fresh hub as first-run.
func Load(ctx context.Context, fs backend.FS, dir string) (*Ledger, error) {
	primary := path.Join(dir, ledgerFile)
	if l, err := readLedger(ctx, fs, primary); err == nil {
		l.ensureMaps()
		return l, nil
	} else if !isNotExist(err) {
		// Primary exists but is unreadable/corrupt: try the backup.
		if l, berr := readLedger(ctx, fs, path.Join(dir, bakFile)); berr == nil {
			l.ensureMaps()
			return l, nil
		}
		return nil, fmt.Errorf("ledger %s unreadable and no valid backup: %w", primary, err)
	}
	// Primary missing: a backup may still exist from an interrupted write.
	if l, err := readLedger(ctx, fs, path.Join(dir, bakFile)); err == nil {
		l.ensureMaps()
		return l, nil
	}
	return New(), nil
}

func readLedger(ctx context.Context, fs backend.FS, rel string) (*Ledger, error) {
	rc, err := fs.OpenReader(ctx, rel)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	var l Ledger
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// Save atomically writes the ledger to dir on fs: write tmp, rotate the current
// ledger to .bak, then rename tmp into place. Because SMB rename does not
// replace an existing target, each rename removes its destination first; the
// retained .bak makes an interrupted write recoverable on the next Load.
func Save(ctx context.Context, fs backend.FS, dir string, l *Ledger) error {
	l.ensureMaps()
	l.Format = Format
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	if err := fs.MkdirAll(ctx, dir, 0o755); err != nil {
		return err
	}
	tmp := path.Join(dir, tmpFile)
	primary := path.Join(dir, ledgerFile)
	bak := path.Join(dir, bakFile)

	if err := fs.WriteFile(ctx, tmp, bytes.NewReader(raw), 0o644, time.Now()); err != nil {
		return err
	}
	// Rotate current -> .bak (best effort; absent primary is fine on first run).
	if _, serr := fs.Stat(ctx, primary); serr == nil {
		_ = fs.Remove(ctx, bak)
		if err := fs.Rename(ctx, primary, bak); err != nil {
			return fmt.Errorf("rotate ledger to backup: %w", err)
		}
	}
	// tmp -> primary (primary now absent, so rename lands cleanly).
	if err := fs.Rename(ctx, tmp, primary); err != nil {
		return fmt.Errorf("commit ledger: %w", err)
	}
	return nil
}

// --- blob store ---------------------------------------------------------

// BlobPath returns the content-addressed path (relative to the job remote root)
// for a blob with the given sha256 hex hash.
func BlobPath(dir, hash string) string {
	if len(hash) < 2 {
		return path.Join(dir, blobsDir, "_", hash)
	}
	return path.Join(dir, blobsDir, hash[:2], hash)
}

// HasBlob reports whether a blob for hash already exists.
func HasBlob(ctx context.Context, fs backend.FS, dir, hash string) bool {
	_, err := fs.Stat(ctx, BlobPath(dir, hash))
	return err == nil
}

// PutBlob stores content under its hash if not already present (idempotent).
func PutBlob(ctx context.Context, fs backend.FS, dir, hash string, content []byte) error {
	if HasBlob(ctx, fs, dir, hash) {
		return nil
	}
	return fs.WriteFile(ctx, BlobPath(dir, hash), bytes.NewReader(content), 0o644, time.Now())
}

// GetBlob reads the blob for hash.
func GetBlob(ctx context.Context, fs backend.FS, dir, hash string) ([]byte, error) {
	rc, err := fs.OpenReader(ctx, BlobPath(dir, hash))
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// GCBlobs removes blobs not referenced by any live file entry in the ledger.
// Errors are returned but callers may treat GC failure as non-fatal.
func GCBlobs(ctx context.Context, fs backend.FS, dir string, l *Ledger) error {
	referenced := make(map[string]struct{}, len(l.Files))
	for _, e := range l.Files {
		referenced[e.Hash] = struct{}{}
	}
	root := path.Join(dir, blobsDir)
	var stale []string
	err := walkBlobs(ctx, fs, root, func(hash, rel string) {
		if _, ok := referenced[hash]; !ok {
			stale = append(stale, rel)
		}
	})
	if err != nil {
		return err
	}
	sort.Strings(stale)
	for _, rel := range stale {
		if err := fs.Remove(ctx, rel); err != nil {
			return err
		}
	}
	return nil
}

// walkBlobs invokes fn(hash, relPath) for each stored blob under root.
func walkBlobs(ctx context.Context, fs backend.FS, root string, fn func(hash, rel string)) error {
	// The blob store is small; a targeted Walk of the whole remote is avoided by
	// opening the store as its own subtree is not possible through FS, so we
	// rely on Walk over the job root and filter by the blobs prefix.
	prefix := root + "/"
	return fs.Walk(ctx, func(e backend.Entry) error {
		if e.IsDir {
			return nil
		}
		if !strings.HasPrefix(e.Path, prefix) {
			return nil
		}
		base := path.Base(e.Path)
		fn(base, e.Path)
		return nil
	})
}

// --- lock ---------------------------------------------------------------

type lockInfo struct {
	Client string `json:"client"`
	At     int64  `json:"at"` // unix seconds
}

// ErrLocked indicates another client holds a fresh lock.
var ErrLocked = errors.New("ledger locked by another client")

// AcquireLock takes the hub lock for clientID, stealing a lock older than
// staleAfter. It returns a release function. Concurrency is expected to be low
// (one client today); the lock mainly guards against overlapping runs.
func AcquireLock(ctx context.Context, fs backend.FS, dir, clientID string, now time.Time, staleAfter time.Duration) (func(), error) {
	if err := fs.MkdirAll(ctx, dir, 0o755); err != nil {
		return nil, err
	}
	rel := path.Join(dir, lockFile)
	if cur, err := readLock(ctx, fs, rel); err == nil {
		age := now.Sub(time.Unix(cur.At, 0))
		if cur.Client != clientID && age < staleAfter {
			return nil, fmt.Errorf("%w: held by %s for %s", ErrLocked, cur.Client, age.Round(time.Second))
		}
	}
	raw, _ := json.Marshal(lockInfo{Client: clientID, At: now.Unix()})
	if err := fs.WriteFile(ctx, rel, bytes.NewReader(raw), 0o644, now); err != nil {
		return nil, err
	}
	return func() { _ = fs.Remove(context.Background(), rel) }, nil
}

func readLock(ctx context.Context, fs backend.FS, rel string) (lockInfo, error) {
	rc, err := fs.OpenReader(ctx, rel)
	if err != nil {
		return lockInfo{}, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return lockInfo{}, err
	}
	var li lockInfo
	if err := json.Unmarshal(raw, &li); err != nil {
		return lockInfo{}, err
	}
	return li, nil
}

func isNotExist(err error) bool {
	return errors.Is(err, os.ErrNotExist) || os.IsNotExist(err)
}
