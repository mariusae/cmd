package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testDir writes a drafts directory, giving each file a distinct modification
// time in the order it is named, oldest first, so a listing's order is the
// test's to choose.
func testDir(t *testing.T, files map[string]string, order ...string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 9, 20, 9, 0, 0, 0, time.Local)
	for index, name := range order {
		at := base.Add(time.Duration(index) * time.Hour)
		if err := os.Chtimes(filepath.Join(root, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDeriveTitle(t *testing.T) {
	for _, test := range []struct{ name, content, want string }{
		{"a.md", "# Foobar\n\nbody\n", "Foobar"},
		{"a.md", "---\ntitle: From Frontmatter\n---\n\n# Ignored\n", "From Frontmatter"},
		{"a.md", "just a first line\n\n# A heading later\n", "A heading later"},
		{"a.md", "no heading at all\n\nsecond paragraph\n", "no heading at all"},
		{"a.md", "---\nid: 7\n---\n\nafter the frontmatter\n", "after the frontmatter"},
		{"a.md", "\n\n   \n", "a"},
		{"a.md", "```\n# not a heading\n```\n\nreal text\n", "```"},
		{"a.md", "# [Linked](http://x) title\n", "Linked title"},
		{"a.md", "# [[wiki|shown]]\n", "shown"},
		{"a.md", "#\n\n# Real\n", "Real"},
	} {
		if got := deriveTitle(test.name, test.content); got != test.want {
			t.Errorf("deriveTitle(%q): got %q, want %q", test.content, got, test.want)
		}
	}
}

func TestListIsNewestFirstAndLeavesOutNotes(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md":       "# One\n",
		"two.md":       "# Two\n",
		"one-notes.md": "# Notes on One\n",
		"-notes.md":    "# A draft called -notes\n",
		".hidden.md":   "# Hidden\n",
		"notes.txt":    "not markdown\n",
	}, "one.md", "-notes.md", "two.md")

	drafts, err := openDir(root).list()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, subject := range drafts {
		got = append(got, subject.name)
	}
	want := []string{"two.md", "-notes.md", "one.md"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestListTitles(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md": "---\ntitle: Titled\n---\n\n# Other\n",
		"two.md": "opening line\n",
	}, "two.md", "one.md")
	drafts, err := openDir(root).list()
	if err != nil {
		t.Fatal(err)
	}
	if drafts[0].title != "Titled" || drafts[1].title != "opening line" {
		t.Errorf("titles: %q, %q", drafts[0].title, drafts[1].title)
	}
}

func TestNotesNaming(t *testing.T) {
	for _, test := range []struct{ path, notes, draft string }{
		{"/d/foo.md", "/d/foo-notes.md", "/d/foo.md"},
		{"/d/foo-notes.md", "/d/foo-notes.md", "/d/foo.md"},
		// `-notes.md` is a draft called `-notes`, not a companion of nothing.
		{"/d/-notes.md", "/d/-notes-notes.md", "/d/-notes.md"},
	} {
		if got := notesFor(test.path); got != test.notes {
			t.Errorf("notesFor(%q): got %q, want %q", test.path, got, test.notes)
		}
		if got := draftOf(test.path); got != test.draft {
			t.Errorf("draftOf(%q): got %q, want %q", test.path, got, test.draft)
		}
	}
}

func TestContains(t *testing.T) {
	d := openDir("/drafts")
	for _, test := range []struct {
		path string
		want bool
	}{
		{"/drafts/a.md", true},
		{"/drafts/a.md+Preview", false},
		{"/drafts/sub/a.md", false},
		// An archived draft is a draft of the directory: it is opened, written
		// and kept like any other, shown or not.
		{"/drafts/archive/a.md", true},
		{"/drafts/archive/sub/a.md", false},
		{"/drafts/.hidden.md", false},
		{"/drafts/a.txt", false},
		{"/elsewhere/a.md", false},
	} {
		if got := d.contains(test.path); got != test.want {
			t.Errorf("contains(%q): got %v, want %v", test.path, got, test.want)
		}
	}
}

func TestFindDir(t *testing.T) {
	home := t.TempDir()
	explicit := t.TempDir()
	env := map[string]string{}
	getenv := func(key string) string { return env[key] }
	homeOf := func() (string, error) { return home, nil }

	if got, err := findDir("", getenv, homeOf); err != nil || got != filepath.Join(home, "drafts") {
		t.Errorf("default: got %q, %v", got, err)
	}
	env["DRAFTS_DIR"] = explicit
	if got, err := findDir("", getenv, homeOf); err != nil || got != explicit {
		t.Errorf("$DRAFTS_DIR: got %q, %v", got, err)
	}
	other := t.TempDir()
	if got, err := findDir(other, getenv, homeOf); err != nil || got != other {
		t.Errorf("-d: got %q, %v", got, err)
	}
}

// A directory that is not there yet is not an error: nothing is created to
// answer a question, and the answer is that there are no drafts.
func TestMissingDirectoryIsEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere")
	root, err := findDir(missing, func(string) string { return "" }, func() (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := openDir(root).list()
	if err != nil || len(drafts) != 0 {
		t.Fatalf("got %d drafts, %v", len(drafts), err)
	}
}

func TestDirectoryThatIsAFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findDir(path, func(string) string { return "" }, func() (string, error) { return "", nil }); err == nil {
		t.Error("a file was accepted as a drafts directory")
	}
}

func TestFormatTime(t *testing.T) {
	now := time.Date(2026, 9, 20, 15, 0, 0, 0, time.Local)
	for _, test := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-2 * time.Hour), "1:00PM"},
		{now.Add(-3 * 24 * time.Hour), "Thu3:00PM"},
		{now.Add(-30 * 24 * time.Hour), "21Aug26"},
	} {
		if got := formatTime(test.at, now); got != test.want {
			t.Errorf("formatTime(%v): got %q, want %q", test.at, got, test.want)
		}
	}
}

// A `-notes.md` with nothing to be the notes of is the only copy of what is
// written in it, and is listed as the draft it is.
func TestOrphanedNotesAreListedAsDrafts(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md":          "# One\n",
		"one-notes.md":    "# Notes on One\n",
		"orphan-notes.md": "# Orphan Notes\n",
	}, "one-notes.md", "one.md", "orphan-notes.md")

	drafts, err := openDir(root).list()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, subject := range drafts {
		got = append(got, subject.name)
	}
	if len(got) != 2 || got[0] != "orphan-notes.md" || got[1] != "one.md" {
		t.Errorf("got %v, want [orphan-notes.md one.md]", got)
	}
}

// archiveDir writes an archive under a drafts directory, dating its files a day
// before testDir dates the drafts: what is put away was worked on before what
// is not.
func archiveDir(t *testing.T, root string, files map[string]string, order ...string) {
	t.Helper()
	archive := filepath.Join(root, archiveSubdir)
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(archive, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 9, 19, 9, 0, 0, 0, time.Local)
	for index, name := range order {
		at := base.Add(time.Duration(index) * time.Hour)
		if err := os.Chtimes(filepath.Join(archive, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func listedNames(t *testing.T, d *dir) []string {
	t.Helper()
	drafts, err := d.list()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, subject := range drafts {
		names = append(names, subject.name)
	}
	return names
}

// An archived draft is one put away: it is not in the listing until the listing
// is asked for it.
func TestListLeavesOutTheArchiveUntilItIsAskedFor(t *testing.T) {
	root := testDir(t, map[string]string{"one.md": "# One\n"}, "one.md")
	archiveDir(t, root, map[string]string{"old.md": "# Old\n"}, "old.md")

	d := openDir(root)
	if got := listedNames(t, d); len(got) != 1 || got[0] != "one.md" {
		t.Errorf("got %v, want [one.md]", got)
	}
	d.includeArchive = true
	got := listedNames(t, d)
	want := []string{"one.md", filepath.Join(archiveSubdir, "old.md")}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A companion belongs to the draft beside it, in the archive as anywhere else —
// and a draft of the same name in each directory is two drafts, not one.
func TestListArchivedNotesBelongToTheArchivedDraft(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md":       "# One\n",
		"one-notes.md": "# Notes on One\n",
	}, "one-notes.md", "one.md")
	archiveDir(t, root, map[string]string{
		"one.md":       "# An older One\n",
		"one-notes.md": "# Notes on the older One\n",
	}, "one-notes.md", "one.md")

	d := openDir(root)
	d.includeArchive = true
	got := listedNames(t, d)
	want := []string{"one.md", filepath.Join(archiveSubdir, "one.md")}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Orphaned notes are still the only copy of what is written in them, wherever
// they are.
func TestListShowsOrphanedArchivedNotes(t *testing.T) {
	root := testDir(t, map[string]string{"one.md": "# One\n"}, "one.md")
	archiveDir(t, root, map[string]string{"gone-notes.md": "# Notes on Gone\n"}, "gone-notes.md")

	d := openDir(root)
	d.includeArchive = true
	got := listedNames(t, d)
	if len(got) != 2 || got[1] != filepath.Join(archiveSubdir, "gone-notes.md") {
		t.Errorf("got %v", got)
	}
}

// A draft says where it is filed, so a listing showing both can say which is
// which.
func TestDraftArchived(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{"one.md", false},
		{filepath.Join(archiveSubdir, "one.md"), true},
		{filepath.Join("sub", "one.md"), false},
	} {
		if got := (draft{name: test.name}).archived(); got != test.want {
			t.Errorf("archived(%q): got %v, want %v", test.name, got, test.want)
		}
	}
}

// A draft's title falls back on the file's own name, not on the path to it.
func TestTitleOfAnArchivedDraftWithoutOne(t *testing.T) {
	if got := deriveTitle(filepath.Join(archiveSubdir, "one.md"), "\n"); got != "one" {
		t.Errorf("got %q, want %q", got, "one")
	}
}
