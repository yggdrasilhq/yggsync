package merge

import "testing"

func TestMergeShortCircuits(t *testing.T) {
	cases := []struct {
		name, base, a, b, want string
	}{
		{"a==b", "x\n", "y\n", "y\n", "y\n"},
		{"a==base takes b", "x\n", "x\n", "z\n", "z\n"},
		{"b==base takes a", "x\n", "z\n", "x\n", "z\n"},
		{"all equal", "x\n", "x\n", "x\n", "x\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Merge(c.base, c.a, c.b)
			if !r.Clean {
				t.Fatalf("expected clean, got conflicts=%d", r.Conflicts)
			}
			if r.Merged != c.want {
				t.Fatalf("merged=%q want %q", r.Merged, c.want)
			}
		})
	}
}

func TestMergeDisjointChangesClean(t *testing.T) {
	base := "one\ntwo\nthree\nfour\nfive\n"
	// ours edits line 1, theirs edits line 5 — disjoint, should merge cleanly.
	ours := "ONE\ntwo\nthree\nfour\nfive\n"
	theirs := "one\ntwo\nthree\nfour\nFIVE\n"
	r := Merge(base, ours, theirs)
	if !r.Clean {
		t.Fatalf("expected clean merge, got conflicts=%d merged=%q", r.Conflicts, r.Merged)
	}
	want := "ONE\ntwo\nthree\nfour\nFIVE\n"
	if r.Merged != want {
		t.Fatalf("merged=%q want %q", r.Merged, want)
	}
}

func TestMergeInsertionsBothSidesClean(t *testing.T) {
	base := "a\nb\nc\n"
	ours := "a\nINS-OURS\nb\nc\n"    // insert after a
	theirs := "a\nb\nc\nINS-THEIRS\n" // append at end
	r := Merge(base, ours, theirs)
	if !r.Clean {
		t.Fatalf("expected clean, got conflicts=%d merged=%q", r.Conflicts, r.Merged)
	}
	want := "a\nINS-OURS\nb\nc\nINS-THEIRS\n"
	if r.Merged != want {
		t.Fatalf("merged=%q want %q", r.Merged, want)
	}
}

func TestMergeSameChangeBothSidesClean(t *testing.T) {
	base := "a\nb\nc\n"
	edit := "a\nB-EDIT\nc\n"
	r := Merge(base, edit, edit)
	if !r.Clean || r.Merged != edit {
		t.Fatalf("expected clean identical change, got clean=%v merged=%q", r.Clean, r.Merged)
	}
}

func TestMergeConflictOnSameLine(t *testing.T) {
	base := "a\nb\nc\n"
	ours := "a\nOURS\nc\n"
	theirs := "a\nTHEIRS\nc\n"
	r := Merge(base, ours, theirs)
	if r.Clean {
		t.Fatalf("expected conflict, got clean merge %q", r.Merged)
	}
	if r.Conflicts != 1 {
		t.Fatalf("expected 1 conflict hunk, got %d", r.Conflicts)
	}
	// Both variants must be preserved in the reported rendering.
	if !contains(r.Merged, "OURS") || !contains(r.Merged, "THEIRS") {
		t.Fatalf("conflict rendering lost a side: %q", r.Merged)
	}
}

func TestMergeNoTrailingNewline(t *testing.T) {
	base := "a\nb"
	ours := "a\nb\nc" // append a line, no trailing newline
	theirs := "a\nb"
	r := Merge(base, ours, theirs)
	if !r.Clean || r.Merged != "a\nb\nc" {
		t.Fatalf("clean=%v merged=%q", r.Clean, r.Merged)
	}
}

func TestMergeDeletionOneSideClean(t *testing.T) {
	base := "a\nb\nc\n"
	ours := "a\nc\n" // delete b
	theirs := "a\nb\nc\n"
	r := Merge(base, ours, theirs)
	if !r.Clean || r.Merged != "a\nc\n" {
		t.Fatalf("clean=%v merged=%q", r.Clean, r.Merged)
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
