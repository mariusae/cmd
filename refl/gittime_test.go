package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStampDatesNotesByTheirLastCommit(t *testing.T) {
	root := gitGraph(t,
		map[string]string{"notes/old.md": "# Old\n"},
		map[string]string{"notes/new.md": "# New\n"},
	)
	// A checkout leaves every file with the same recent time; only git knows
	// which note is actually older.
	flat := time.Now().Add(-time.Minute)
	for _, rel := range []string{"notes/old.md", "notes/new.md"} {
		if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), flat, flat); err != nil {
			t.Fatal(err)
		}
	}

	notes, err := openGraph(root).list()
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}
	if notes[0].rel != "notes/new.md" || notes[1].rel != "notes/old.md" {
		t.Fatalf("order: got %q then %q", notes[0].rel, notes[1].rel)
	}
	if !notes[0].modTime.After(notes[1].modTime) {
		t.Errorf("the newer commit should date the newer note: %v and %v",
			notes[0].modTime, notes[1].modTime)
	}
	if notes[0].modTime.Equal(flat) {
		t.Error("the file's own time should not have been used")
	}
}

func TestStampKeepsTheFileTimeForWhatGitCannotDate(t *testing.T) {
	root := gitGraph(t, map[string]string{"notes/committed.md": "# Committed\n"})
	// An untracked note, and a committed one written since.
	if err := os.WriteFile(filepath.Join(root, notesDir, "fresh.md"), []byte("# Fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(root, notesDir, "committed.md")
	if err := os.WriteFile(edited, []byte("# Committed\n\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(time.Hour)
	if err := os.Chtimes(edited, soon, soon); err != nil {
		t.Fatal(err)
	}

	notes, err := openGraph(root).list()
	if err != nil {
		t.Fatal(err)
	}
	if notes[0].rel != "notes/committed.md" {
		t.Fatalf("a note written since its commit is the newest: got %q", notes[0].rel)
	}
	if notes[0].modTime.Unix() != soon.Unix() {
		t.Errorf("it should keep its file time: got %v, want %v", notes[0].modTime, soon)
	}
	if notes[1].rel != "notes/fresh.md" || notes[1].modTime.IsZero() {
		t.Errorf("an untracked note keeps its file time too: got %+v", notes[1])
	}
}

func TestCommitTimesAreCachedAndCarriedForward(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, ok := os.LookupEnv("HOME"); ok {
		t.Setenv("HOME", t.TempDir()) // os.UserCacheDir reads HOME on macOS
	}
	root := gitGraph(t, map[string]string{"notes/first.md": "# First\n"})

	g := openGraph(root)
	before := g.commitTimes()
	if len(before) != 1 {
		t.Fatalf("got %v, want one note", before)
	}
	cached := openGraph(root).readStamps()
	if len(cached.Times) != 1 || cached.Head == "" {
		t.Fatalf("the cache should hold the answer: %+v", cached)
	}

	// A second commit is carried forward onto what was already known.
	commitTo(t, root, "notes/second.md", "# Second\n")
	after := g.commitTimes()
	if len(after) != 2 {
		t.Fatalf("got %v, want both notes", after)
	}
	if !after["notes/first.md"].Equal(before["notes/first.md"]) {
		t.Error("the untouched note should keep its date")
	}
	if !after["notes/second.md"].After(time.Time{}) {
		t.Error("the new note should be dated")
	}
}

func TestCommitTimesSurviveAGraphOutsideGit(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/a.md": "# A\n"})
	g := openGraph(root)
	if times := g.commitTimes(); times != nil {
		t.Errorf("no repository, no commit times: got %v", times)
	}
	notes, err := g.list()
	if err != nil || len(notes) != 1 {
		t.Fatalf("the graph should still list: got %d notes, %v", len(notes), err)
	}
}
