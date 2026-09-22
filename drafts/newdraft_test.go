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

// A draft opens with its title as a heading, and writing begins below it. With
// nothing to call it, the window opens empty and the first thing typed is the
// title.
func TestOpening(t *testing.T) {
	text, at := opening("Air Traffic Control")
	if text != "# Air Traffic Control\n\n" {
		t.Errorf("text: %q", text)
	}
	if at != len([]rune(text)) {
		t.Errorf("cursor at %d, want %d", at, len([]rune(text)))
	}
	if text, at = opening("  "); text != "" || at != 0 {
		t.Errorf("with no title: %q, %d", text, at)
	}
	if text, _ = opening("one\ntwo"); text != "# one two\n\n" {
		t.Errorf("a title is a line: %q", text)
	}
}

// Nothing is written until Put, and then what was written names the file.
func TestSaveDraft(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{"# Air Traffic Control\n\nthe tower\n", "air-traffic-control.md"},
		{"---\ntitle: From Frontmatter\n---\n\nbody\n", "from-frontmatter.md"},
		{"just a first line\n\nmore\n", "just-a-first-line.md"},
		{"", "untitled.md"},
		{"\n\n   \n", "untitled.md"},
		{"# [Linked](http://x) title\n", "linked-title.md"},
	} {
		d := openDir(t.TempDir())
		path, err := d.saveDraft(test.text)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(path) != test.want {
			t.Errorf("saveDraft(%q): got %q, want %q", test.text, filepath.Base(path), test.want)
		}
		// What was written is what is on disk: nothing is added to it.
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != test.text {
			t.Errorf("content: got %q, want %q", content, test.text)
		}
	}
}

func TestSaveDraftNeverOverwrites(t *testing.T) {
	d := openDir(t.TempDir())
	first, err := d.saveDraft("# One\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.saveDraft("# One\n\na different draft, the same title\n")
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

func TestSaveDraftMakesTheDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nowhere")
	if _, err := openDir(root).saveDraft("# One\n"); err != nil {
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

	path, err := d.saveDraft("# Meeting Notes\n")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "meeting-notes.md" {
		t.Errorf("with no draft called Meeting: got %q", path)
	}

	other := openDir(t.TempDir())
	if _, err = other.saveDraft("# Meeting\n"); err != nil {
		t.Fatal(err)
	}
	if path, err = other.saveDraft("# Meeting Notes\n"); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "meeting-notes-2.md" {
		t.Errorf("beside a draft called Meeting: got %q", path)
	}
}
