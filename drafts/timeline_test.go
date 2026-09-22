package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func changeTexts(changes []change) []string {
	var texts []string
	for _, changed := range changes {
		for _, block := range changed.blocks {
			texts = append(texts, changed.draft.name+":"+block.text)
		}
	}
	return texts
}

// Under version control the timeline is what changed, which is what the
// history records.
func TestTimelineShowsTheBlocksThatChanged(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	d := openDir(root)

	write(t, root, "one.md", "# One\n\nthe first paragraph\n\nthe second paragraph\n")
	if _, err := d.repository().commit(); err != nil {
		t.Fatal(err)
	}
	write(t, root, "one.md", "# One\n\nthe first paragraph, revised\n\nthe second paragraph\n")
	if _, err := d.repository().commit(); err != nil {
		t.Fatal(err)
	}

	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("no changes")
	}
	if got := changeTexts(changes)[0]; got != "one.md:the first paragraph, revised" {
		t.Errorf("newest change: got %q", got)
	}
}

// What is written but not yet kept is the newest of all.
func TestTimelinePutsTheWorkingTreeFirst(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	d := openDir(root)
	write(t, root, "one.md", "# One\n\ncommitted\n")
	if _, err := d.repository().commit(); err != nil {
		t.Fatal(err)
	}
	write(t, root, "one.md", "# One\n\ncommitted\n\nnot yet kept\n")

	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if got := changeTexts(changes)[0]; got != "one.md:not yet kept" {
		t.Errorf("got %q, want the uncommitted paragraph first", got)
	}
}

// A draft that has never been recorded is shown whole: there is no diff to
// say which part of it is new, because all of it is.
func TestTimelineShowsAnUnrecordedDraftWhole(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	d := openDir(root)
	write(t, root, "one.md", "# One\n")
	if _, err := d.repository().commit(); err != nil {
		t.Fatal(err)
	}
	write(t, root, "two.md", "# Two\n\nbrand new\n")

	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(changeTexts(changes), "|")
	if !strings.Contains(got, "two.md:# Two") || !strings.Contains(got, "two.md:brand new") {
		t.Errorf("got %q", got)
	}
}

// Outside version control the timeline is the files' own times, and each
// draft stands for itself.
func TestTimelineWithoutAnyVersionControl(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md": "# One\n\nbody\n",
		"two.md": "opening line\n\nbody\n",
	}, "one.md", "two.md")
	changes, err := openDir(root).timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(changes))
	}
	if changes[0].draft.name != "two.md" || changes[1].draft.name != "one.md" {
		t.Errorf("order: %s, %s", changes[0].draft.name, changes[1].draft.name)
	}
	if got := changes[1].blocks[0].text; got != "# One" {
		t.Errorf("a draft stands for what it opens with; got %q", got)
	}
}

func TestTimelineHonoursItsLimit(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md": "# One\n", "two.md": "# Two\n", "three.md": "# Three\n",
	}, "one.md", "two.md", "three.md")
	changes, err := openDir(root).timeline(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Errorf("got %d changes, want 2", len(changes))
	}
}

// Reading the timeline walks the history and reads a file per change, which is
// more work than a window has any business doing every couple of seconds. The
// answer is kept while the directory stands still, and dropped when it moves.
func TestTimelineIsRereadWhenTheDirectoryMoves(t *testing.T) {
	root := testDir(t, map[string]string{"one.md": "# One\n"}, "one.md")
	d := openDir(root)
	if _, err := d.timeline(10); err != nil {
		t.Fatal(err)
	}
	key := d.changeKey

	later := time.Date(2026, 9, 21, 9, 0, 0, 0, time.Local)
	write(t, root, "two.md", "# Two\n")
	if err := os.Chtimes(filepath.Join(root, "two.md"), later, later); err != nil {
		t.Fatal(err)
	}
	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if d.changeKey == key {
		t.Error("the timeline was not re-read after the directory moved")
	}
	if len(changes) != 2 {
		t.Errorf("got %d changes, want 2", len(changes))
	}
}

func TestChangedPositions(t *testing.T) {
	content := "one\ntwo\nthree\nfour\n"
	// A hunk naming two lines from the second anchors at both.
	diff := "@@ -2,1 +2,2 @@\n+two\n+three\n"
	got := changedPositions(content, diff, false)
	want := []int{4, 8}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
	// A pure deletion still anchors at the line it left behind.
	if got = changedPositions(content, "@@ -2,1 +1,0 @@\n-two\n", false); len(got) != 1 || got[0] != 0 {
		t.Errorf("deletion: got %v, want [0]", got)
	}
	// A file with no history at all is changed throughout.
	if got = changedPositions(content, "", true); len(got) != 4 {
		t.Errorf("whole: got %v, want four positions", got)
	}
}

// A draft that opens with frontmatter, or with a blank line under it, still
// has an opening: the first thing actually written in it.
func TestOpeningPosition(t *testing.T) {
	for _, test := range []struct {
		content string
		want    string
	}{
		{"# One\n\nbody\n", "# One"},
		{"---\ntitle: One\n---\n\nthe body\n", "the body"},
		{"\n\n\n# One\n", "# One"},
		{"---\ntitle: One\n---\n", ""},
		{"", ""},
	} {
		blocks := prepareBlocks(test.content).blockContextsAt([]int{openingPosition(test.content)}, nil)
		got := ""
		if len(blocks) > 0 {
			got = blocks[0].text
		}
		if got != test.want {
			t.Errorf("opening of %q: got %q, want %q", test.content, got, test.want)
		}
	}
}

// The history knows what was archived; the timeline says so only when the
// archive is being shown.
func TestTimelineLeavesOutTheArchiveUntilItIsAskedFor(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	d := openDir(root)

	write(t, root, "one.md", "# One\n\nthe first paragraph\n")
	if err := os.MkdirAll(d.archiveRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, d.archiveRoot(), "old.md", "# Old\n\nwhat was put away\n")
	if _, err := d.repository().commit(); err != nil {
		t.Fatal(err)
	}

	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range changeTexts(changes) {
		if strings.HasPrefix(got, archiveSubdir) {
			t.Errorf("the archive is in the timeline: %q", got)
		}
	}
	d.includeArchive = true
	if changes, err = d.timeline(10); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, got := range changeTexts(changes) {
		found = found || got == filepath.Join(archiveSubdir, "old.md")+":what was put away"
	}
	if !found {
		t.Errorf("the archive is not in the timeline: %v", changeTexts(changes))
	}
}
