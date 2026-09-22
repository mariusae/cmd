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
	first := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)

	write(t, root, "one.md", "# One\n\nthe first paragraph\n\nthe second paragraph\n")
	t.Setenv("GIT_AUTHOR_DATE", first.Format(time.RFC3339))
	t.Setenv("GIT_COMMITTER_DATE", first.Format(time.RFC3339))
	if _, err := d.repository().commit(); err != nil {
		t.Fatal(err)
	}
	write(t, root, "one.md", "# One\n\nthe first paragraph, revised\n\nthe second paragraph\n")
	t.Setenv("GIT_AUTHOR_DATE", second.Format(time.RFC3339))
	t.Setenv("GIT_COMMITTER_DATE", second.Format(time.RFC3339))
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

func TestTimelineIgnoresARecordedREADME(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	d := openDir(root)
	write(t, root, readmeName, "# Directory documentation\n")
	write(t, root, "one.md", "# One\n")
	if _, err := d.repository().commit(); err != nil {
		t.Fatal(err)
	}

	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].draft.name != "one.md" {
		t.Errorf("got %v, want only one.md", changeTexts(changes))
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
	if got := changeTexts(changes)[0]; !strings.Contains(got, "not yet kept") {
		t.Errorf("got %q, want the uncommitted writing first", got)
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
	if !strings.Contains(got, "two.md:# Two\n\nbrand new") {
		t.Errorf("got %q", got)
	}
}

// Nearby changed paragraphs become one source excerpt, but each paragraph is
// included whole even where only one of its wrapped lines changed. A distant
// paragraph remains a separate block.
func TestTimelineCoalescesNearbyWholeBlocks(t *testing.T) {
	content := "first line\nfirst changed line\n\n" +
		"second line\nsecond changed line\n\n\n\n" +
		"distant line\ndistant changed line\n"
	diff := "@@ -2 +2 @@\n+first changed line\n" +
		"@@ -5 +5 @@\n+second changed line\n" +
		"@@ -10 +10 @@\n+distant changed line\n"
	changed, ok := changeAt("/drafts", "one.md", content, diff, false, time.Now())
	if !ok {
		t.Fatal("the changes produced no timeline entry")
	}
	if len(changed.blocks) != 2 {
		t.Fatalf("got %d blocks, want 2: %+v", len(changed.blocks), changed.blocks)
	}
	if got, want := changed.blocks[0].text,
		"first line\nfirst changed line\n\nsecond line\nsecond changed line"; got != want {
		t.Errorf("nearby block:\n got %q\nwant %q", got, want)
	}
	if got, want := changed.blocks[1].text, "distant line\ndistant changed line"; got != want {
		t.Errorf("distant block:\n got %q\nwant %q", got, want)
	}
}

// Several saves in the same place and within one writing burst are one
// timeline entry showing the final, complete block rather than intermediate
// versions of it.
func TestTimelineCoalescesNearbyCommits(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	d := openDir(root)
	base := time.Date(2026, 9, 20, 8, 0, 0, 0, time.UTC)
	commit := func(content string, when time.Time) {
		t.Helper()
		write(t, root, "one.md", content)
		t.Setenv("GIT_AUTHOR_DATE", when.Format(time.RFC3339))
		t.Setenv("GIT_COMMITTER_DATE", when.Format(time.RFC3339))
		if _, err := d.repository().commit(); err != nil {
			t.Fatal(err)
		}
	}
	commit("# One\n\nbefore\n", base)
	commit("# One\n\nfirst revision\n", base.Add(time.Hour))
	commit("# One\n\nfinal revision\n", base.Add(time.Hour+3*time.Minute))

	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want the nearby saves and the old creation", len(changes))
	}
	if got := changeTexts(changes)[0]; got != "one.md:final revision" {
		t.Errorf("coalesced change: got %q, want the final complete paragraph", got)
	}
	if !changes[0].when.Equal(base.Add(time.Hour + 3*time.Minute)) {
		t.Errorf("coalesced change has time %v", changes[0].when)
	}
}

// A line inserted above an older update moves that block in the final source.
// Coalescing follows the unchanged block text to its new location rather than
// losing it at its obsolete line number.
func TestTimelineCoalescingAccountsForInsertedLines(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	d := openDir(root)
	first := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	commit := func(content string, when time.Time) {
		t.Helper()
		write(t, root, "one.md", content)
		t.Setenv("GIT_AUTHOR_DATE", when.Format(time.RFC3339))
		t.Setenv("GIT_COMMITTER_DATE", when.Format(time.RFC3339))
		if _, err := d.repository().commit(); err != nil {
			t.Fatal(err)
		}
	}
	commit("second paragraph\n\nthird paragraph\n", first)
	commit("first paragraph\n\nsecond paragraph\n\nthird paragraph\n", first.Add(time.Minute))

	changes, err := d.timeline(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	want := "one.md:first paragraph\n\nsecond paragraph\n\nthird paragraph"
	if got := changeTexts(changes)[0]; got != want {
		t.Errorf("coalesced change:\n got %q\nwant %q", got, want)
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
		found = found || got == filepath.Join(archiveSubdir, "old.md")+":# Old\n\nwhat was put away"
	}
	if !found {
		t.Errorf("the archive is not in the timeline: %v", changeTexts(changes))
	}
}
