package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsDuplicateJobs(t *testing.T) {
	t.Setenv("PATH", os.Getenv("PATH"))
	cfgPath := filepath.Join(t.TempDir(), "dup.toml")
	if err := os.WriteFile(cfgPath, []byte(`
rclone_binary = "sh"
[[jobs]]
name = "notes"
type = "copy"
local = "~/notes"
remote = "nas:notes"
[[jobs]]
name = "notes"
type = "copy"
local = "~/notes2"
remote = "nas:notes2"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "duplicate job name") {
		t.Fatalf("expected duplicate job error, got %v", err)
	}
}

func TestLoadFillsDefaults(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "ok.toml")
	if err := os.WriteFile(cfgPath, []byte(`
rclone_binary = "sh"
[[jobs]]
name = "notes"
type = "copy"
local = "~/notes"
remote = "nas:notes"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LockFile == "" {
		t.Fatal("expected default lock file")
	}
	if got, want := cfg.Jobs[0].Direction, "push"; got != want {
		t.Fatalf("direction = %q want %q", got, want)
	}
}

func TestLoadRejectsMixedFilterStyles(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "bad-filters.toml")
	if err := os.WriteFile(cfgPath, []byte(`
rclone_binary = "sh"
[[jobs]]
name = "notes"
type = "bisync"
local = "~/notes"
remote = "nas:notes"
exclude = ["*.tmp"]
filter_rules = ["- *.bak"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "mixes filter_rules with include/exclude") {
		t.Fatalf("expected mixed filter style error, got %v", err)
	}
}
