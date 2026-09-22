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

// Rename files a draft under the title it now carries.
func TestRenameDraft(t *testing.T) {
	root := t.TempDir()
	d := openDir(root)
	write(t, root, "old-title.md", "# A New Title\n\nthe body\n")

	renamed, err := d.renameDraft(filepath.Join(root, "old-title.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "a-new-title.md" {
		t.Errorf("got %q", renamed)
	}
	if exists(filepath.Join(root, "old-title.md")) {
		t.Error("the old name is still there")
	}
	content, err := os.ReadFile(renamed)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# A New Title\n\nthe body\n" {
		t.Errorf("content changed: %q", content)
	}
}

// A draft with no heading is named after its first line, the same reading the
// listing names it by.
func TestRenameDraftByFirstLine(t *testing.T) {
	root := t.TempDir()
	write(t, root, "untitled.md", "the opening line\n\nmore\n")
	renamed, err := openDir(root).renameDraft(filepath.Join(root, "untitled.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "the-opening-line.md" {
		t.Errorf("got %q", renamed)
	}
}

// A name that already fits is left alone, and says so by not moving.
func TestRenameDraftAlreadyFiled(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a-new-title.md")
	write(t, root, "a-new-title.md", "# A New Title\n")
	renamed, err := openDir(root).renameDraft(path)
	if err != nil {
		t.Fatal(err)
	}
	if renamed != path {
		t.Errorf("got %q, want the path unchanged", renamed)
	}
}

// Notes are named after their draft, so they go where it goes.
func TestRenameDraftCarriesItsNotes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "old.md", "# A New Title\n")
	write(t, root, "old-notes.md", "# Notes on Old\n\nwhat I thought\n")

	renamed, err := openDir(root).renameDraft(filepath.Join(root, "old.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "a-new-title.md" {
		t.Fatalf("got %q", renamed)
	}
	content, err := os.ReadFile(filepath.Join(root, "a-new-title-notes.md"))
	if err != nil {
		t.Fatalf("the notes did not come along: %v", err)
	}
	if !strings.Contains(string(content), "what I thought") {
		t.Errorf("notes content: %q", content)
	}
	if exists(filepath.Join(root, "old-notes.md")) {
		t.Error("the old notes are still there")
	}
}

// Nothing is ever overwritten: a name already taken takes the next one.
func TestRenameDraftNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	write(t, root, "taken.md", "# Taken\n\nsomeone else's draft\n")
	write(t, root, "old.md", "# Taken\n\nthe one being renamed\n")

	renamed, err := openDir(root).renameDraft(filepath.Join(root, "old.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "taken-2.md" {
		t.Errorf("got %q", renamed)
	}
	content, err := os.ReadFile(filepath.Join(root, "taken.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "someone else's draft") {
		t.Errorf("the draft that was there was overwritten: %q", content)
	}
}

// A draft and its notes never come apart: a notes name that is taken sends the
// draft looking for the next one, rather than leaving the two under different
// names.
func TestRenameDraftSkipsATakenNotesName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "old.md", "# Taken\n")
	write(t, root, "old-notes.md", "# Notes on Old\n")
	// The draft's new name is free, but the notes name it implies is not.
	write(t, root, "taken-notes.md", "# Someone Else's Notes\n")

	renamed, err := openDir(root).renameDraft(filepath.Join(root, "old.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "taken-2.md" {
		t.Errorf("got %q, want taken-2.md", renamed)
	}
	if !exists(filepath.Join(root, "taken-2-notes.md")) {
		t.Error("the notes did not follow the draft")
	}
	content, err := os.ReadFile(filepath.Join(root, "taken-notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Someone Else's") {
		t.Errorf("the notes that were there were overwritten: %q", content)
	}
}

// A draft retitled to something that reads as another draft's companion steps
// aside, exactly as a new draft would.
func TestRenameDraftStepsAsideFromANotesName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "meeting.md", "# Meeting\n")
	write(t, root, "old.md", "# Meeting Notes\n")

	renamed, err := openDir(root).renameDraft(filepath.Join(root, "old.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "meeting-notes-2.md" {
		t.Errorf("got %q", renamed)
	}
}

// A draft with nothing in it is still a draft, and is filed as untitled.
func TestRenameAnEmptyDraft(t *testing.T) {
	root := t.TempDir()
	write(t, root, "old.md", "")
	renamed, err := openDir(root).renameDraft(filepath.Join(root, "old.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(renamed) != "untitled.md" {
		t.Errorf("got %q", renamed)
	}
}

// Archiving puts a draft away under the name it already had: being done with a
// draft is not retitling it.
func TestArchiveDraft(t *testing.T) {
	root := t.TempDir()
	d := openDir(root)
	write(t, root, "a-title.md", "# A Title\n\nthe body\n")

	archived, err := d.archiveDraft(filepath.Join(root, "a-title.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, archiveSubdir, "a-title.md"); archived != want {
		t.Errorf("got %q, want %q", archived, want)
	}
	if exists(filepath.Join(root, "a-title.md")) {
		t.Error("the draft is still among the drafts")
	}
	content, err := os.ReadFile(archived)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# A Title\n\nthe body\n" {
		t.Errorf("content changed: %q", content)
	}
}

// Notes belong beside their draft, wherever it is.
func TestArchiveDraftCarriesItsNotes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a-title.md", "# A Title\n")
	write(t, root, "a-title-notes.md", "# Notes on A Title\n")

	archived, err := openDir(root).archiveDraft(filepath.Join(root, "a-title.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !exists(notesFor(archived)) {
		t.Error("the notes stayed behind")
	}
	if exists(filepath.Join(root, "a-title-notes.md")) {
		t.Error("the notes are still among the drafts")
	}
}

// A draft already put away is left where it is, and says so by not moving.
func TestArchiveDraftAlreadyArchived(t *testing.T) {
	root := t.TempDir()
	d := openDir(root)
	write(t, root, "a-title.md", "# A Title\n")
	archived, err := d.archiveDraft(filepath.Join(root, "a-title.md"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := d.archiveDraft(archived)
	if err != nil {
		t.Fatal(err)
	}
	if again != archived {
		t.Errorf("got %q, want the path unchanged", again)
	}
}

// Nothing is overwritten: a name already taken in the archive takes the next
// one, and both drafts are still there.
func TestArchiveDraftNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	d := openDir(root)
	if err := os.MkdirAll(d.archiveRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, d.archiveRoot(), "a-title.md", "# The archived one\n")
	write(t, root, "a-title.md", "# The new one\n")

	archived, err := d.archiveDraft(filepath.Join(root, "a-title.md"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(archived) != "a-title-2.md" {
		t.Errorf("got %q", archived)
	}
	kept, err := os.ReadFile(filepath.Join(d.archiveRoot(), "a-title.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != "# The archived one\n" {
		t.Errorf("the archived draft was overwritten: %q", kept)
	}
}

// Renaming is about the name, so an archived draft retitled stays archived.
func TestRenameAnArchivedDraftStaysInTheArchive(t *testing.T) {
	root := t.TempDir()
	d := openDir(root)
	write(t, root, "old-title.md", "# Old Title\n")
	archived, err := d.archiveDraft(filepath.Join(root, "old-title.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archived, []byte("# A New Title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	renamed, err := d.renameDraft(archived)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(d.archiveRoot(), "a-new-title.md"); renamed != want {
		t.Errorf("got %q, want %q", renamed, want)
	}
}
