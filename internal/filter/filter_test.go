package filter

import (
	"testing"

	"yggsync/internal/config"
)

func newMatcher(t *testing.T, rules ...string) *Matcher {
	t.Helper()
	m, err := New(config.Job{FilterRules: rules})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// A `**/dir/**` exclusion must catch the directory at the vault root as well as
// nested, which matters once a job is scoped to a subtree (the root .obsidian
// leak that slipped a whole app-config dir into sync).
func TestRootAndNestedDirExclusion(t *testing.T) {
	m := newMatcher(t, "- **/.obsidian/**")
	excluded := []string{
		".obsidian/app.json",
		".obsidian/plugins/x/main.js",
		"sub/.obsidian/workspace.json",
		"a/b/.obsidian/c.json",
	}
	for _, p := range excluded {
		if m.Match(p) {
			t.Errorf("expected %q to be excluded", p)
		}
	}
	included := []string{
		"note.md",
		"src/2026/x.md",
		"xobsidian/y.md",      // not a dotdir
		"a/notobsidian/b.md",  // substring, not the dir
	}
	for _, p := range included {
		if !m.Match(p) {
			t.Errorf("expected %q to be included", p)
		}
	}
}

func TestTrailingDoubleStar(t *testing.T) {
	m := newMatcher(t, "- materials/generated/**")
	if m.Match("materials/generated/x.png") {
		t.Error("expected nested file under materials/generated to be excluded")
	}
	if !m.Match("materials/keep.md") {
		t.Error("expected materials/keep.md to be included")
	}
}

func TestBasenameRuleMatchesAnywhere(t *testing.T) {
	// A rule without a slash matches by basename at any depth (8.3-alias style).
	m := newMatcher(t, "- CONFLICTS.md")
	if m.Match("CONFLICTS.md") || m.Match("src/CONFLICTS.md") {
		t.Error("expected CONFLICTS.md excluded at root and nested")
	}
	if !m.Match("src/notes.md") {
		t.Error("expected unrelated file included")
	}
}
