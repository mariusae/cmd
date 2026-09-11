package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIsNotePath(t *testing.T) {
	for _, test := range []struct {
		rel  string
		want bool
	}{
		{rel: "daily/2026-09-10.md", want: true},
		{rel: "notes/deep/a.md", want: true},
		{rel: "a.md", want: true},
		{rel: "notes/a.txt"},
		{rel: "assets/caption.md"},
		{rel: "audio-memos/memo.md"},
		{rel: ".reflect/index.md"},
		{rel: "notes/.hidden.md"},
	} {
		if got := isNotePath(test.rel); got != test.want {
			t.Errorf("isNotePath(%q): got %v, want %v", test.rel, got, test.want)
		}
	}
}

func TestChangedPositions(t *testing.T) {
	content := "one\ntwo\nthree\nfour\n"
	diff := strings.Join([]string{
		"@@ -1,0 +2,2 @@",
		"+two",
		"+three",
		"@@ -9 +9,0 @@",
		"-gone",
	}, "\n")
	got := changedPositions(content, diff, false)
	want := []int{4, 8}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// A deletion past the end of the new file anchors nothing.
	if positions := changedPositions(content, "@@ -1 +1,0 @@", false); len(positions) != 1 || positions[0] != 0 {
		t.Errorf("a pure deletion: got %v, want [0]", positions)
	}
	if whole := changedPositions(content, "", true); len(whole) != 4 {
		t.Errorf("a new file: got %v, want every line", whole)
	}
}

// gitGraph builds a graph under git, committing each revision in turn.
func gitGraph(t *testing.T, revisions ...map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, notesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, 0, "init", "-q")
	for sequence, revision := range revisions {
		for rel, content := range revision {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git(t, root, sequence, "add", "-A")
		git(t, root, sequence, "commit", "-q", "-m", "revision")
	}
	return root
}

// gitEpoch dates the first commit of a test graph. Commits are an hour apart,
// so a test never turns on two of them landing in the same second.
var gitEpoch = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func git(t *testing.T, root string, sequence int, args ...string) {
	t.Helper()
	when := gitEpoch.Add(time.Duration(sequence) * time.Hour).Format(time.RFC3339)
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=refl", "GIT_AUTHOR_EMAIL=refl@example.com",
		"GIT_COMMITTER_NAME=refl", "GIT_COMMITTER_EMAIL=refl@example.com",
		"GIT_AUTHOR_DATE="+when, "GIT_COMMITTER_DATE="+when)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func TestTimelineReadsCommittedChangesNewestFirst(t *testing.T) {
	root := gitGraph(t,
		map[string]string{"notes/a.md": "# A\n\nfirst paragraph\n"},
		map[string]string{"notes/a.md": "# A\n\nfirst paragraph\n\nsecond paragraph\n"},
		map[string]string{"notes/b.md": "# B\n\n- a bullet\n  - a nested target\n"},
	)
	changes, err := openGraph(root).timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3", len(changes))
	}
	if changes[0].note.rel != "notes/b.md" {
		t.Errorf("newest change: got %q, want notes/b.md", changes[0].note.rel)
	}
	if changes[1].note.rel != "notes/a.md" || len(changes[1].blocks) != 1 ||
		changes[1].blocks[0].text != "second paragraph" {
		t.Errorf("the block that changed: got %+v", changes[1].blocks)
	}
	if changes[1].blocks[0].line != 5 {
		t.Errorf("block line: got %d, want 5", changes[1].blocks[0].line)
	}
}

func TestTimelineLeadsWithTheWorkingTree(t *testing.T) {
	root := gitGraph(t, map[string]string{"notes/a.md": "# A\n\nfirst paragraph\n"})
	edited := filepath.Join(root, notesDir, "a.md")
	if err := os.WriteFile(edited, []byte("# A\n\nfirst paragraph\n\nuncommitted line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(root, notesDir, "new.md")
	if err := os.WriteFile(fresh, []byte("# New\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	changes, err := openGraph(root).timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3", len(changes))
	}
	var uncommitted []string
	for _, changed := range changes[:2] {
		uncommitted = append(uncommitted, changed.note.rel)
	}
	if uncommitted[0] != "notes/new.md" || uncommitted[1] != "notes/a.md" {
		t.Fatalf("the working tree leads: got %q", uncommitted)
	}
	if len(changes[1].blocks) != 1 || changes[1].blocks[0].text != "uncommitted line" {
		t.Errorf("the edited block: got %+v", changes[1].blocks)
	}
}

func TestTimelineSkipsPrivateNotes(t *testing.T) {
	root := gitGraph(t, map[string]string{
		"notes/secret.md": "---\nprivate: true\n---\n# Secret\n",
		"notes/open.md":   "# Open\n",
	})
	changes, err := openGraph(root).timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range changes {
		if changed.note.rel == "notes/secret.md" {
			t.Fatalf("a private note reached the timeline: %+v", changed)
		}
	}
	if len(changes) != 1 {
		t.Errorf("got %d changes, want 1", len(changes))
	}
}

func TestTimelineNeedsARepository(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/a.md": "# A\n"})
	if _, err := openGraph(root).timeline(10); err == nil {
		t.Error("a graph outside git has no history to read")
	}
}

func TestTimelineReadsAGraphNestedInARepository(t *testing.T) {
	repo := gitGraph(t, map[string]string{"README.md": "# Repository\n"})
	root := filepath.Join(repo, "graph")
	if err := os.MkdirAll(filepath.Join(root, notesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, notesDir, "a.md"), []byte("# A\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, 1, "add", "-A")
	git(t, repo, 1, "commit", "-q", "-m", "the note")

	changes, err := openGraph(root).timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	// The repository's own README is not in the graph, so only the note shows.
	if len(changes) != 1 || changes[0].note.rel != "notes/a.md" {
		t.Fatalf("got %+v", changes)
	}
	if want := filepath.Join(root, notesDir, "a.md"); changes[0].note.path != want {
		t.Errorf("path: got %q, want %q", changes[0].note.path, want)
	}
	if len(changes[0].blocks) != 2 || changes[0].blocks[1].text != "body" {
		t.Errorf("blocks: got %+v", changes[0].blocks)
	}
}

// commitTo adds one more commit to a graph built by gitGraph, an hour after
// the last.
func commitTo(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	count, err := exec.Command("git", "-C", root, "rev-list", "--count", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := strconv.Atoi(strings.TrimSpace(string(count)))
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, sequence, "add", "-A")
	git(t, root, sequence, "commit", "-q", "-m", rel)
}

func TestTimelineIsKeptWhileTheGraphStandsStill(t *testing.T) {
	root := gitGraph(t, map[string]string{"notes/a.md": "# A\n\nfirst\n"})
	g := openGraph(root)

	first, err := g.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	key := g.changeKey
	if key == "" || len(first) != 1 {
		t.Fatalf("got %d changes, key %q", len(first), key)
	}
	// Standing in for the answer proves the second read did not make its own.
	g.changes = []change{{note: note{rel: "kept"}}}
	again, err := g.timeline(10)
	if err != nil || len(again) != 1 || again[0].note.rel != "kept" {
		t.Errorf("an unchanged graph should be read once: got %+v, %v", again, err)
	}
	g.changes = first

	// A new commit is a new answer.
	commitTo(t, root, "notes/b.md", "# B\n")
	after, err := g.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || g.changeKey == key {
		t.Errorf("got %d changes, key %q (was %q)", len(after), g.changeKey, key)
	}

	// So is a note written since.
	key = g.changeKey
	if err := os.WriteFile(filepath.Join(root, notesDir, "a.md"), []byte("# A\n\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.timeline(10); err != nil {
		t.Fatal(err)
	}
	if g.changeKey == key {
		t.Error("a note written since the last commit should be a new answer")
	}
}

func TestTimelineReadsFurtherWhenAskedForMore(t *testing.T) {
	root := gitGraph(t,
		map[string]string{"notes/a.md": "# A\n"},
		map[string]string{"notes/b.md": "# B\n"},
		map[string]string{"notes/c.md": "# C\n"},
	)
	g := openGraph(root)
	if first, err := g.timeline(1); err != nil || len(first) != 1 {
		t.Fatalf("got %d changes, %v", len(first), err)
	}
	// The kept answer went only one deep; asking for more reads again.
	more, err := g.timeline(3)
	if err != nil || len(more) != 3 {
		t.Fatalf("got %d changes, %v", len(more), err)
	}
}
