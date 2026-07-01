package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yggsync/internal/config"
)

func TestRunJobsReturnsFailures(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		LockFile: filepath.Join(dir, "yggsync.lock"),
		Targets: []config.Target{{
			Name:  "broken",
			Type:  "smb",
			Host:  "127.0.0.1",
			Port:  1,
			Share: "data",
		}},
		Jobs: []config.Job{
			{
				Name:   "ok",
				Type:   "copy",
				Local:  filepath.Join(dir, "src"),
				Remote: filepath.Join(dir, "remote"),
			},
			{
				Name:   "bad",
				Type:   "copy",
				Local:  filepath.Join(dir, "src"),
				Remote: "broken:dest",
			},
		},
	}
	summary := New(cfg, false, "sync", "test").RunJobs(context.Background(), []string{"ok", "bad"})
	if len(summary.Succeeded) != 1 {
		t.Fatalf("expected 1 success, got %+v", summary)
	}
	if _, ok := summary.Failed["bad"]; !ok {
		t.Fatalf("missing failure entry: %#v", summary.Failed)
	}
}

func TestAcquireLockPreventsOverlap(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "yggsync.lock")
	unlock, err := acquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	_, err = acquireLock(lockPath)
	if err == nil {
		t.Fatal("expected overlapping lock to fail")
	}
}

func TestCopyJobCopiesFiles(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(filepath.Join(local, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "notes", "today.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		LockFile: filepath.Join(dir, "yggsync.lock"),
		Jobs: []config.Job{{
			Name:   "copy",
			Type:   "copy",
			Local:  local,
			Remote: remote,
		}},
	}
	if err := New(cfg, false, "sync", "test").RunJob(context.Background(), "copy"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(remote, "notes", "today.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected file contents: %q", got)
	}
}

func TestRetainedCopyDeletesAfterRemoteConfirmed(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(local, "clip.txt")
	if err := os.WriteFile(filePath, []byte("clip"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filePath, old, old); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		LockFile: filepath.Join(dir, "yggsync.lock"),
		Jobs: []config.Job{{
			Name:               "media",
			Type:               "retained_copy",
			Local:              local,
			Remote:             remote,
			LocalRetentionDays: 1,
		}},
	}
	if err := New(cfg, false, "sync", "test").RunJob(context.Background(), "media"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("expected local file to be pruned, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(remote, "clip.txt")); err != nil {
		t.Fatal(err)
	}
}

func worktreeCfg(dir, local, remote string) config.Config {
	return config.Config{
		LockFile:         filepath.Join(dir, "yggsync.lock"),
		WorktreeStateDir: filepath.Join(dir, "state"),
		Jobs: []config.Job{{
			Name:     "vault",
			Type:     "worktree",
			Local:    local,
			Remote:   remote,
			ClientID: "test-client",
		}},
	}
}

func TestWorktreeSyncInitializesAndBuildsLedger(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := worktreeCfg(dir, local, remote)
	if err := New(cfg, false, "sync", "test").RunJob(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	// Local content flows to the hub, and the authoritative ledger appears there.
	if _, err := os.Stat(filepath.Join(remote, "note.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(remote, ".yggsync", "ledger.json")); err != nil {
		t.Fatalf("expected hub ledger: %v", err)
	}
}

// A file changed on only one side propagates cleanly; a rename does not
// manufacture a conflict.
func TestWorktreeRenameIsNotAConflict(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "old.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := worktreeCfg(dir, local, remote)
	r := New(cfg, false, "sync", "test")
	if err := r.RunJob(context.Background(), "vault"); err != nil {
		t.Fatal(err) // initial sync: old.md now on both sides + ledger
	}
	// Rename on the hub: delete old, add new with identical content.
	if err := os.Remove(filepath.Join(remote, "old.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "new.md"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.RunJob(context.Background(), "vault"); err != nil {
		t.Fatalf("rename should not conflict, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "new.md")); err != nil {
		t.Fatalf("renamed file did not propagate to local: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "old.md")); !os.IsNotExist(err) {
		t.Fatalf("old path should be gone locally, err=%v", err)
	}
}

// Disjoint edits on both sides merge cleanly with no conflict.
func TestWorktreeCleanThreeWayMerge(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	base := "line1\nline2\nline3\n"
	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := worktreeCfg(dir, local, remote)
	r := New(cfg, false, "sync", "test")
	if err := r.RunJob(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	// Edit different lines on each side.
	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte("LINE1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "note.md"), []byte("line1\nline2\nLINE3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.RunJob(context.Background(), "vault"); err != nil {
		t.Fatalf("clean merge should not error, got %v", err)
	}
	want := "LINE1\nline2\nLINE3\n"
	for _, side := range []string{local, remote} {
		got, err := os.ReadFile(filepath.Join(side, "note.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s merged=%q want %q", side, got, want)
		}
	}
}

// Overlapping edits cannot merge: the hub version stays live, ours is preserved
// as a .mergefail sidecar, the run reports a conflict, and nothing is lost.
func TestWorktreeConflictQuarantines(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := worktreeCfg(dir, local, remote)
	r := New(cfg, false, "sync", "test")
	if err := r.RunJob(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte("local-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "note.md"), []byte("remote-change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := r.RunJob(context.Background(), "vault")
	if err == nil || !strings.Contains(err.Error(), "worktree conflicts") {
		t.Fatalf("expected conflict, got %v", err)
	}
	// Hub version is live on local now.
	got, _ := os.ReadFile(filepath.Join(local, "note.md"))
	if string(got) != "remote-change\n" {
		t.Fatalf("hub version should be live locally, got %q", got)
	}
	// Our version preserved as a sidecar (no data loss).
	entries, _ := os.ReadDir(local)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "note.md.mergefail.") {
			found = true
			b, _ := os.ReadFile(filepath.Join(local, e.Name()))
			if string(b) != "local-change\n" {
				t.Fatalf("sidecar content=%q want local-change", b)
			}
		}
	}
	if !found {
		t.Fatalf("expected a .mergefail sidecar; dir=%v", entries)
	}
}

// An emptied local tree must not wipe the hub: the mass-delete guard aborts.
func TestWorktreeMassDeleteGuard(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		name := filepath.Join(local, fmt.Sprintf("n%02d.md", i))
		if err := os.WriteFile(name, []byte(fmt.Sprintf("content-%d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := worktreeCfg(dir, local, remote)
	r := New(cfg, false, "sync", "test")
	if err := r.RunJob(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	// Wipe local, then sync: should refuse rather than delete all hub files.
	if err := os.RemoveAll(local); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	err := r.RunJob(context.Background(), "vault")
	if err == nil || !strings.Contains(err.Error(), "refusing to delete") {
		t.Fatalf("expected mass-delete guard to trip, got %v", err)
	}
	// Hub files still intact.
	if _, err := os.Stat(filepath.Join(remote, "n00.md")); err != nil {
		t.Fatalf("hub file should survive guard: %v", err)
	}
}
