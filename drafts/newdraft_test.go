package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugForTitle(t *testing.T) {
	for _, test := range []struct{ title, want string }{
		{title: "Meeting Notes", want: "meeting-notes"},
		{title: "Don't Panic!", want: "dont-panic"},
		{title: "日本語ノート", want: "日本語ノート"},
		{title: "🎉🎉🎉", want: "untitled"},
		{title: "", want: "untitled"},
		{title: "CON", want: "con-draft"},
		{title: "  spaced   out  ", want: "spaced-out"},
		{title: "snake_case and-dashes", want: "snake-case-and-dashes"},
		{title: strings.Repeat("a", 80), want: strings.Repeat("a", 60)},
		// A title is slugged as it reads; whether that name is free is the
		// directory's question, not the slug's.
		{title: "Meeting Notes", want: "meeting-notes"},
	} {
		if got := slugForTitle(test.title); got != test.want {
			t.Errorf("slugForTitle(%q): got %q, want %q", test.title, got, test.want)
		}
	}
}

func TestSlugIsIdempotent(t *testing.T) {
	for _, title := range []string{"Meeting Notes", "Don't Panic!", "CON", "日本語ノート"} {
		once := slugForTitle(title)
		if twice := slugForTitle(once); twice != once {
			t.Errorf("slug of %q: %q slugs to %q", title, once, twice)
		}
	}
}

func TestCreateDraft(t *testing.T) {
	d := openDir(t.TempDir())
	path, at, err := d.createDraft("Air Traffic Control")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "air-traffic-control.md" {
		t.Errorf("path: %q", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# Air Traffic Control\n\n" {
		t.Errorf("content: %q", content)
	}
	// Writing begins below the heading, which is the end of the text.
	if at != len([]rune(string(content))) {
		t.Errorf("cursor at %d, want %d", at, len(content))
	}
}

// With nothing to call it, the draft opens with its heading empty and the
// cursor in it: the first thing wanted is what this is about.
func TestCreateDraftWithNoTitle(t *testing.T) {
	d := openDir(t.TempDir())
	path, at, err := d.createDraft("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "untitled.md" {
		t.Errorf("path: %q", path)
	}
	if at != 2 {
		t.Errorf("cursor at %d, want 2 — in the heading", at)
	}
}

func TestCreateDraftNeverOverwrites(t *testing.T) {
	d := openDir(t.TempDir())
	first, _, err := d.createDraft("One")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := d.createDraft("One")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("the second draft took the first one's file")
	}
	if filepath.Base(second) != "one-2.md" {
		t.Errorf("second: %q", second)
	}
}

func TestCreateDraftMakesTheDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nowhere")
	if _, _, err := openDir(root).createDraft("One"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "one.md")); err != nil {
		t.Error(err)
	}
}

func TestCreateNotes(t *testing.T) {
	root := t.TempDir()
	d := openDir(root)
	draft := filepath.Join(root, "one.md")
	write(t, root, "one.md", "# One\n")

	notes, err := d.createNotes(draft, "One")
	if err != nil {
		t.Fatal(err)
	}
	if notes != filepath.Join(root, "one-notes.md") {
		t.Errorf("notes: %q", notes)
	}
	content, err := os.ReadFile(notes)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# Notes on One\n\n" {
		t.Errorf("content: %q", content)
	}

	// Notes already written are opened, never written over.
	write(t, root, "one-notes.md", "# Notes on One\n\nsomething I wrote\n")
	if _, err = d.createNotes(draft, "One"); err != nil {
		t.Fatal(err)
	}
	if content, err = os.ReadFile(notes); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "something I wrote") {
		t.Error("existing notes were overwritten")
	}
}

// Notes about notes are the notes themselves.
func TestCreateNotesOnNotesStaysPut(t *testing.T) {
	root := t.TempDir()
	write(t, root, "one-notes.md", "# Notes on One\n")
	notes, err := openDir(root).createNotes(filepath.Join(root, "one-notes.md"), "One")
	if err != nil {
		t.Fatal(err)
	}
	if notes != filepath.Join(root, "one-notes.md") {
		t.Errorf("got %q", notes)
	}
}

func TestHeadingSafe(t *testing.T) {
	if got := headingSafe("one\ntwo\rthree"); got != "one two three" {
		t.Errorf("got %q", got)
	}
}

// A draft called "Meeting Notes" files as `meeting-notes.md` when that is a
// name of its own, and steps aside when it is where another draft's notes live.
func TestCreateDraftStepsAsideFromANotesName(t *testing.T) {
	root := t.TempDir()
	d := openDir(root)

	path, _, err := d.createDraft("Meeting Notes")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "meeting-notes.md" {
		t.Errorf("with no draft called Meeting: got %q", path)
	}

	other := openDir(t.TempDir())
	if _, _, err = other.createDraft("Meeting"); err != nil {
		t.Fatal(err)
	}
	if path, _, err = other.createDraft("Meeting Notes"); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "meeting-notes-2.md" {
		t.Errorf("beside a draft called Meeting: got %q", path)
	}
}
