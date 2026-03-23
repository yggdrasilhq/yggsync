package runner

import (
	"context"
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

func TestWorktreeSyncInitializesAndSavesState(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		LockFile:         filepath.Join(dir, "yggsync.lock"),
		WorktreeStateDir: stateDir,
		Jobs: []config.Job{{
			Name:   "vault",
			Type:   "worktree",
			Local:  local,
			Remote: remote,
		}},
	}
	if err := New(cfg, false, "sync", "test").RunJob(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(remote, "note.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "vault.json")); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeSyncDetectsConflicts(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local")
	remote := filepath.Join(dir, "remote")
	stateDir := filepath.Join(dir, "state")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		LockFile:         filepath.Join(dir, "yggsync.lock"),
		WorktreeStateDir: stateDir,
		Jobs: []config.Job{{
			Name:   "vault",
			Type:   "worktree",
			Local:  local,
			Remote: remote,
		}},
	}
	r := New(cfg, false, "sync", "test")
	if err := r.RunJob(context.Background(), "vault"); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(local, "note.md"), []byte("local-change"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "note.md"), []byte("remote-change"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := r.RunJob(context.Background(), "vault")
	if err == nil || !strings.Contains(err.Error(), "worktree conflicts") {
		t.Fatalf("expected worktree conflict, got %v", err)
	}
}
