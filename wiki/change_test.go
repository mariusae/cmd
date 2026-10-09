package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testPatch = `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 package a
--- not a header
+++ not one either
-old
+new
diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1 +0,0 @@
-package gone
diff --git a/old name.go b/new name.go
similarity index 100%
rename from old name.go
rename to new name.go
diff --git a/img.png b/img.png
new file mode 100644
Binary files /dev/null and b/img.png differ
`

func TestSplitPatch(t *testing.T) {
	var got []string
	for _, p := range splitPatch(testPatch) {
		got = append(got, p.Path)
		if p.Path == "a.go" && (p.Added != 2 || p.Removed != 2) {
			t.Errorf("a.go +%d -%d", p.Added, p.Removed)
		}
	}
	if want := []string{"a.go", "gone.go", "new name.go", "img.png"}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %q, want %q", got, want)
	}
}

func TestOrderStack(t *testing.T) {
	got, err := orderStack("s", []commit{{Hash: "c", Parent: "b"}, {Hash: "a", Parent: "base"}, {Hash: "b", Parent: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for _, c := range got {
		hashes = append(hashes, c.Hash)
	}
	if !reflect.DeepEqual(hashes, []string{"a", "b", "c"}) {
		t.Errorf("order = %v", hashes)
	}
	for _, bad := range [][]commit{
		nil,
		{{Hash: "a", Parent: "x"}, {Hash: "b", Parent: "y"}},
		{{Hash: "a", Parent: "x"}, {Hash: "b", Parent: "a"}, {Hash: "c", Parent: "a"}},
	} {
		if _, err := orderStack("s", bad); err == nil {
			t.Errorf("orderStack(%v) succeeded", bad)
		}
	}
}

func TestGitRange(t *testing.T) {
	for _, tc := range []struct {
		spec   string
		single bool
		want   []string
	}{
		{"HEAD~2", false, []string{"HEAD~2..HEAD"}},
		{"HEAD^", false, []string{"HEAD^..HEAD"}},
		{"main..topic", false, []string{"main..topic"}},
		{"HEAD^!", false, []string{"HEAD^!"}},
		{"topic ^main", false, []string{"topic", "^main"}},
		{"HEAD~2", true, []string{"HEAD~2^!"}},
	} {
		if got := gitRange(tc.spec, tc.single); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("gitRange(%q, %v) = %q, want %q", tc.spec, tc.single, got, tc.want)
		}
	}
}

func TestSpecName(t *testing.T) {
	for spec, want := range map[string]string{
		"HEAD~2":      "HEAD~2",
		"D12345":      "D12345",
		".^::.":       ".^-.",
		"main..topic": "main..topic",
		"draft() & .": "draft-.",
		"a/b":         "a-b",
		"...":         "...",
		"()":          "diff",
	} {
		if got := specName(spec); got != want {
			t.Errorf("specName(%q) = %q, want %q", spec, got, want)
		}
	}
}

func runIn(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HGPLAIN=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@x", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@x")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// commitFiles writes files and commits them with message.
func commitFiles(t *testing.T, dir, vcs, message string, files map[string]string) {
	t.Helper()
	writeTree(t, dir, files)
	if vcs == "git" {
		runIn(t, dir, "git", "add", "-A")
		runIn(t, dir, "git", "commit", "-q", "-m", message)
	} else {
		runIn(t, dir, "sl", "addremove", "-q")
		runIn(t, dir, "sl", "commit", "-q", "-u", "T <t@x>", "-m", message)
	}
}

func TestResolveGit(t *testing.T) {
	dir := t.TempDir()
	runIn(t, dir, "git", "init", "-q")
	commitFiles(t, dir, "git", "one", map[string]string{"a.go": "package a\n"})
	commitFiles(t, dir, "git", "two\n\nThe body.", map[string]string{"b.go": "package a\n"})
	commitFiles(t, dir, "git", "three", map[string]string{"a.go": "package a // three\n"})
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	c, root, err := resolveChange(filepath.Join(dir, "sub"), "HEAD~2", false)
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := filepath.EvalSymlinks(dir); root != real && root != dir {
		t.Errorf("root = %s", root)
	}
	if len(c.Commits) != 2 || c.Commits[0].Message != "two\n\nThe body." || c.Commits[1].Message != "three" || c.Commits[0].Author != "T <t@x>" {
		t.Fatalf("commits = %+v", c.Commits)
	}
	if !c.atHead || c.Head != c.Commits[1].Hash || c.Base == "" {
		t.Errorf("change = %+v", c)
	}
	if got := c.fileHashes(); len(got) != 2 || got["a.go"] == "" || got["b.go"] == "" {
		t.Errorf("files = %v", got)
	}

	// A single commit, the root, which is not checked out.
	c, _, err = resolveChange(dir, "HEAD~2", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Commits) != 1 || c.Commits[0].Message != "one" || c.Base != "" || c.atHead || c.baseRev() != gitEmptyTree {
		t.Errorf("change = %+v", c)
	}
	if len(c.parts) != 1 || c.parts[0].Path != "a.go" {
		t.Errorf("parts = %+v", c.parts)
	}

	if _, _, err := resolveChange(dir, "HEAD", false); err == nil {
		t.Error("an empty revset succeeded")
	}
	if _, _, err := resolveChange(dir, "nonesuch", true); err == nil {
		t.Error("a bad commit succeeded")
	}
}

func TestResolveSapling(t *testing.T) {
	if _, err := exec.LookPath("sl"); err != nil {
		t.Skip("no sl")
	}
	dir := filepath.Join(t.TempDir(), "r")
	runIn(t, filepath.Dir(dir), "sl", "init", "-q", "--git", "r")
	commitFiles(t, dir, "sl", "one", map[string]string{"a.go": "package a\n"})
	commitFiles(t, dir, "sl", "two", map[string]string{"b.go": "package a\n"})
	commitFiles(t, dir, "sl", "three", map[string]string{"a.go": "package a // three\n"})

	c, _, err := resolveChange(dir, ".^::.", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Commits) != 2 || c.Commits[0].Message != "two" || !c.atHead || c.Base == "" || len(c.parts) != 2 {
		t.Errorf("change = %+v", c)
	}
	c, _, err = resolveChange(dir, "first(all())", true)
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != "" || c.atHead || len(c.parts) != 1 || c.parts[0].Path != "a.go" {
		t.Errorf("change = %+v", c)
	}
	if _, _, err := resolveChange(dir, "all()", true); err == nil {
		t.Error("many commits taken for one")
	}
}

func TestBuildChange(t *testing.T) {
	source := t.TempDir()
	dest := filepath.Join(t.TempDir(), "proj@D1")
	writeTree(t, source, map[string]string{"a.go": "package a\n", "b.go": "package a\n", "c.go": "package a\n"})
	c := &change{
		VCS: "git", Spec: "D1", Base: "b0", Head: "h1", atHead: true,
		Commits: []commit{{Hash: "h1", Parent: "b0", Message: "Make a better."}},
		parts:   splitPatch(testPatch)[:2],
	}
	f := &fakeHarness{outline: []page{
		{Slug: "walkthrough", Title: "The Walk", Parent: "background", Description: "Walk.", Files: []string{"a.go"}},
		{Slug: "background", Title: "Background", Description: "How c works.", Files: []string{"c.go"}},
		{Slug: "deletion", Title: "Deletion", Description: "Of gone.go.", Files: []string{"gone.go"}},
	}}
	b := &builder{source: source, dest: dest, harness: f, jobs: 2, change: c, log: io.Discard}
	if _, err := b.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.plans) != 1 || !strings.Contains(f.plans[0], "Make a better.") || !strings.Contains(f.plans[0], "+++ /dev/null") {
		t.Errorf("plan prompt lacks the change:\n%s", f.plans)
	}
	s, err := loadState(dest)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, p := range s.Pages {
		slugs = append(slugs, p.Slug+"<"+p.Parent)
	}
	if want := []string{"overview<", "walkthrough<", "background<", "deletion<"}; !reflect.DeepEqual(slugs, want) {
		t.Errorf("outline = %v, want %v", slugs, want)
	}
	if s.Name != "proj@D1" || s.Change == nil || s.Change.Head != "h1" || s.Revision != "h1" {
		t.Errorf("state = %+v", s)
	}
	if !reflect.DeepEqual(s.page("overview").Files, []string{"a.go", "b.go", "gone.go"}) {
		t.Errorf("overview files = %v", s.page("overview").Files)
	}
	if p := f.pages["walkthrough"]; !strings.Contains(p, "Foundations") || !strings.Contains(p, "--- a/gone.go") {
		t.Errorf("walkthrough prompt lacks its guide or the whole patch:\n%s", p)
	}
	if p := f.pages["background"]; strings.Contains(p, "<patch>") || !strings.Contains(p, "checked out at the change") {
		t.Errorf("background prompt:\n%s", p)
	}
	if p := f.pages["deletion"]; !strings.Contains(p, "+++ /dev/null") || strings.Contains(p, "+++ b/a.go") {
		t.Errorf("deletion prompt should quote only gone.go:\n%s", p)
	}
	// Unchanged files the pages cite are linked, though the change does not
	// touch them.
	if html, _ := os.ReadFile(filepath.Join(dest, "html", "background.html")); !strings.Contains(string(html), `href="`) || !strings.Contains(string(html), `b.go#L3`) {
		t.Errorf("background.html does not link b.go")
	}

	// The same change again: nothing is asked.
	f.reset()
	if _, err := b.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.plans) != 0 || len(f.pages) != 0 {
		t.Errorf("unchanged change: plans %d, pages %v", len(f.plans), f.written())
	}

	// The change is revised in gone.go: the fixed pages and the one on
	// gone.go are written again, from their last versions.
	f.reset()
	revised := *c
	revised.Head = "h2"
	revised.parts = splitPatch(strings.Replace(testPatch, "-package gone", "-package gone // x", 1))[:2]
	b.change = &revised
	if _, err := b.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.plans) != 1 || !strings.Contains(f.plans[0], "earlier version of the change") {
		t.Errorf("revision plan: %v", f.plans)
	}
	if want := []string{"deletion", "overview", "walkthrough"}; !reflect.DeepEqual(f.written(), want) {
		t.Errorf("rewrote %v, want %v", f.written(), want)
	}
	if p := f.pages["walkthrough"]; !strings.Contains(p, "<previous-page>") || !strings.Contains(p, "may have been revised") {
		t.Errorf("walkthrough prompt lacks its previous version")
	}

	// Only the message changes: the fixed pages are written again.
	f.reset()
	retold := revised
	retold.Commits = []commit{{Hash: "h2", Parent: "b0", Message: "Make a much better."}}
	b.change = &retold
	if _, err := b.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.plans) != 1 || !strings.Contains(f.plans[0], "commit messages have changed") {
		t.Errorf("retold plan: %v", f.plans)
	}
	if want := []string{"overview", "walkthrough"}; !reflect.DeepEqual(f.written(), want) {
		t.Errorf("rewrote %v, want %v", f.written(), want)
	}
}

func TestClaudeArgs(t *testing.T) {
	args := strings.Join(claudeHarness{commands: []string{"git show"}}.args(planSchema), " ")
	if !strings.Contains(args, "--tools Read,Glob,Grep,Bash ") || !strings.Contains(args, "--allowedTools Read,Glob,Grep,Bash(git show:*) ") {
		t.Errorf("args = %s", args)
	}
	args = strings.Join(claudeHarness{}.args(planSchema), " ")
	if strings.Contains(args, "Bash") {
		t.Errorf("args = %s", args)
	}
}
