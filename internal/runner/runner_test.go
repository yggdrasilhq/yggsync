package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yggsync/internal/config"
)

func TestRunJobsReturnsFailures(t *testing.T) {
	dir := t.TempDir()
	okScript := filepath.Join(dir, "fake-rclone-ok.sh")
	if err := os.WriteFile(okScript, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failScript := filepath.Join(dir, "fake-rclone-fail.sh")
	if err := os.WriteFile(failScript, []byte("#!/bin/sh\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		RcloneConfig: filepath.Join(dir, "rclone.conf"),
		LockFile:     filepath.Join(dir, "yggsync.lock"),
		Jobs: []config.Job{
			{
				Name:   "ok",
				Type:   "copy",
				Local:  dir,
				Remote: "remote:ok",
			},
			{
				Name:   "bad",
				Type:   "copy",
				Local:  dir,
				Remote: "remote:bad",
			},
		},
	}
	r := New(cfg, false, "test")
	r.cfg.RcloneBinary = okScript
	summary := r.RunJobs(context.Background(), []string{"ok"})
	if len(summary.Succeeded) != 1 || len(summary.Failed) != 0 {
		t.Fatalf("unexpected ok summary: %+v", summary)
	}

	r.cfg.RcloneBinary = failScript
	summary = r.RunJobs(context.Background(), []string{"bad"})
	if len(summary.Failed) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(summary.Failed))
	}
	if _, ok := summary.Failed["bad"]; !ok {
		t.Fatalf("missing failure entry for bad job: %#v", summary.Failed)
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

func TestRunJobTimeout(t *testing.T) {
	dir := t.TempDir()
	slowScript := filepath.Join(dir, "fake-rclone-slow.sh")
	if err := os.WriteFile(slowScript, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		RcloneBinary: slowScript,
		RcloneConfig: filepath.Join(dir, "rclone.conf"),
		LockFile:     filepath.Join(dir, "yggsync.lock"),
		Jobs: []config.Job{
			{
				Name:           "slow",
				Type:           "copy",
				Local:          dir,
				Remote:         "remote:slow",
				TimeoutSeconds: 1,
			},
		},
	}
	_ = os.WriteFile(cfg.RcloneConfig, []byte(""), 0o644)

	err := New(cfg, false, "test").RunJob(context.Background(), "slow")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatal("expected timeout error")
	}
}
