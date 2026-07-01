package ledger

import (
	"context"
	"testing"
	"time"

	"yggsync/internal/backend"
	"yggsync/internal/config"
)

func tempFS(t *testing.T) backend.FS {
	t.Helper()
	fs, err := backend.Open(config.Config{}, t.TempDir())
	if err != nil {
		t.Fatalf("open fs: %v", err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	fs := tempFS(t)
	l, err := Load(context.Background(), fs, Dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(l.Files) != 0 || l.Format != Format {
		t.Fatalf("expected empty ledger, got %+v", l)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	fs := tempFS(t)
	l := New()
	l.Generation = 5
	l.Files["src/a.md"] = FileEntry{Hash: "h1", Size: 3, ModTime: 100, Gen: 5, UpdatedBy: "hub"}
	l.Tombstones["src/old.md"] = Tombstone{Gen: 4, LastHash: "h0", DeletedBy: "hub", DeletedAt: 99}
	l.Clients["phone"] = ClientCursor{LastGen: 5, LastSeen: 101}
	if err := Save(ctx, fs, Dir, l); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(ctx, fs, Dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Generation != 5 || got.Files["src/a.md"].Hash != "h1" ||
		got.Tombstones["src/old.md"].Gen != 4 || got.Clients["phone"].LastGen != 5 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestSaveRotatesBackup(t *testing.T) {
	ctx := context.Background()
	fs := tempFS(t)
	first := New()
	first.Generation = 1
	if err := Save(ctx, fs, Dir, first); err != nil {
		t.Fatalf("save1: %v", err)
	}
	second := New()
	second.Generation = 2
	if err := Save(ctx, fs, Dir, second); err != nil {
		t.Fatalf("save2: %v", err)
	}
	// Primary holds gen 2; .bak holds gen 1.
	if l, err := readLedger(ctx, fs, Dir+"/"+ledgerFile); err != nil || l.Generation != 2 {
		t.Fatalf("primary gen: %+v err=%v", l, err)
	}
	if l, err := readLedger(ctx, fs, Dir+"/"+bakFile); err != nil || l.Generation != 1 {
		t.Fatalf("backup gen: %+v err=%v", l, err)
	}
}

func TestLoadFallsBackToBackupWhenPrimaryMissing(t *testing.T) {
	ctx := context.Background()
	fs := tempFS(t)
	first := New()
	first.Generation = 1
	if err := Save(ctx, fs, Dir, first); err != nil {
		t.Fatalf("save1: %v", err)
	}
	second := New()
	second.Generation = 2
	if err := Save(ctx, fs, Dir, second); err != nil {
		t.Fatalf("save2: %v", err)
	}
	// Simulate a crash after rotate, before commit: primary gone, .bak present.
	if err := fs.Remove(ctx, Dir+"/"+ledgerFile); err != nil {
		t.Fatalf("remove primary: %v", err)
	}
	l, err := Load(ctx, fs, Dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if l.Generation != 1 {
		t.Fatalf("expected fallback to backup gen 1, got %d", l.Generation)
	}
}

func TestBlobStore(t *testing.T) {
	ctx := context.Background()
	fs := tempFS(t)
	content := []byte("hello ledger blob")
	hash := "abc123def456"
	if HasBlob(ctx, fs, Dir, hash) {
		t.Fatal("blob should not exist yet")
	}
	if err := PutBlob(ctx, fs, Dir, hash, content); err != nil {
		t.Fatalf("put: %v", err)
	}
	if !HasBlob(ctx, fs, Dir, hash) {
		t.Fatal("blob should exist after put")
	}
	got, err := GetBlob(ctx, fs, Dir, hash)
	if err != nil || string(got) != string(content) {
		t.Fatalf("get: %q err=%v", got, err)
	}
	// GC keeps referenced, drops unreferenced.
	l := New()
	l.Files["x"] = FileEntry{Hash: "kept"}
	_ = PutBlob(ctx, fs, Dir, "kept", []byte("keep me"))
	if err := GCBlobs(ctx, fs, Dir, l); err != nil {
		t.Fatalf("gc: %v", err)
	}
	if HasBlob(ctx, fs, Dir, hash) {
		t.Fatal("unreferenced blob should be gone")
	}
	if !HasBlob(ctx, fs, Dir, "kept") {
		t.Fatal("referenced blob should survive")
	}
}

func TestLock(t *testing.T) {
	ctx := context.Background()
	fs := tempFS(t)
	now := time.Unix(1_000_000, 0)
	rel, err := AcquireLock(ctx, fs, Dir, "phone", now, 10*time.Minute)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// A different client cannot take a fresh lock.
	if _, err := AcquireLock(ctx, fs, Dir, "laptop", now.Add(time.Minute), 10*time.Minute); err == nil {
		t.Fatal("expected lock contention")
	}
	// The same client may re-acquire.
	if _, err := AcquireLock(ctx, fs, Dir, "phone", now.Add(time.Minute), 10*time.Minute); err != nil {
		t.Fatalf("same-client reacquire: %v", err)
	}
	// A stale lock can be stolen.
	if _, err := AcquireLock(ctx, fs, Dir, "laptop", now.Add(20*time.Minute), 10*time.Minute); err != nil {
		t.Fatalf("steal stale: %v", err)
	}
	rel()
}
