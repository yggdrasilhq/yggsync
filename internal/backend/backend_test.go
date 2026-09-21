package backend

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// A job root that is itself a symlink (Termux's ~/storage/shared) must be
// traversed INTO, yielding relative child paths and never the root as an
// entry. The old Walk refused to descend (Lstat sees a symlink), which
// silently produced an empty snapshot; one deployed lineage emitted the root
// as a file and failed every run with "read …: is a directory".
func TestLocalWalkTraversesSymlinkedRoot(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(filepath.Join(real, "Signal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "Download"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := []string{"Download/a.pdf", "Signal/signal-2026.backup", "top.txt"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(real, filepath.FromSlash(f)), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "shared")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	fs := &localFS{root: link}
	var got []string
	err := fs.Walk(context.Background(), func(e Entry) error {
		if e.IsDir {
			return nil
		}
		got = append(got, e.Path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(got)
	want := []string{"Download/a.pdf", "Signal/signal-2026.backup", "top.txt"}
	if len(got) != len(want) {
		t.Fatalf("walk yielded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walk yielded %v, want %v", got, want)
		}
	}
}

// An unreadable subtree must not blind the whole diff: the entry is skipped
// with a log line while siblings are still visited. (Previously the first
// walk error aborted the entire job.)
func TestLocalWalkSkipsUnreadableSubtree(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Android", "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Android", "data", "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(dir, "Android", "data")
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0o755)

	fs := &localFS{root: dir}
	var got []string
	err := fs.Walk(context.Background(), func(e Entry) error {
		if !e.IsDir {
			got = append(got, e.Path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk should survive an unreadable subtree, got: %v", err)
	}
	if len(got) != 1 || got[0] != "visible.txt" {
		t.Fatalf("walk yielded %v, want [visible.txt]", got)
	}
}

// A missing or non-directory root fails loudly instead of reporting an empty
// snapshot and exit code 0.
func TestLocalWalkFailsOnMissingRoot(t *testing.T) {
	fs := &localFS{root: filepath.Join(t.TempDir(), "absent")}
	if err := fs.Walk(context.Background(), func(Entry) error { return nil }); err == nil {
		t.Fatal("walk on missing root should fail")
	}
	fs = &localFS{root: filepath.Join(t.TempDir(), "file")}
	if err := os.WriteFile(fs.root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.Walk(context.Background(), func(Entry) error { return nil }); err == nil {
		t.Fatal("walk on file root should fail")
	}
}
