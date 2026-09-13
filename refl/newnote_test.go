package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The cases Reflect's own documentation pins (docs/readable-filenames.md).
func TestSlugForTitle(t *testing.T) {
	for _, test := range []struct{ title, want string }{
		{title: "Meeting Notes", want: "meeting-notes"},
		{title: "Don't Panic!", want: "dont-panic"},
		{title: "日本語ノート", want: "日本語ノート"},
		{title: "🎉🎉🎉", want: "untitled"},
		{title: "CON", want: "con-note"},
		{title: "  spaced   out  ", want: "spaced-out"},
		{title: "snake_case and-dashes", want: "snake-case-and-dashes"},
		{title: "Air Traffic Control", want: "air-traffic-control"},
		{title: strings.Repeat("a", 80), want: strings.Repeat("a", 60)},
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

// reflectNoteID is Reflect's own test for a note identity it will honour
// (packages/core/src/graph/note-management.ts).
var reflectNoteID = regexp.MustCompile(`^[0-7][0-9a-hjkmnp-tv-z]{25}$`)

func TestNewNoteIDIsACanonicalULID(t *testing.T) {
	seen := map[string]bool{}
	for attempt := 0; attempt < 100; attempt++ {
		id, err := newNoteID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !reflectNoteID.MatchString(id) {
			t.Fatalf("got %q, which Reflect would not accept as an id", id)
		}
		if seen[id] {
			t.Fatalf("%q was minted twice", id)
		}
		seen[id] = true
	}
}

func TestNewNoteIDsSortByTime(t *testing.T) {
	earlier, err := newNoteID(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	later, err := newNoteID(time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// A ULID leads with its timestamp, so the text order is the time order.
	if earlier[:10] >= later[:10] {
		t.Errorf("%q should sort before %q", earlier, later)
	}
}

func TestCreateNote(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/taken.md": "# Taken\n"})
	g := openGraph(root)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.Local)

	path, at, err := g.createNote("Air Traffic Control", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, notesDir, "air-traffic-control.md"); path != want {
		t.Fatalf("path: got %q, want %q", path, want)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A note that has its title is ready for its body, which is the end.
	if at != len([]rune(string(content))) {
		t.Errorf("writing begins at %d, of %d", at, len([]rune(string(content))))
	}
	split := splitFrontmatter(string(content))
	if !strings.Contains(split.raw, "id: ") {
		t.Errorf("frontmatter: got %q", split.raw)
	}
	if got := strings.TrimSpace(split.body); got != "# Air Traffic Control" {
		t.Errorf("body: got %q", got)
	}
	// The note reads back as the graph sees any other.
	if title := deriveTitle("notes/air-traffic-control.md", "", split.body); title != "Air Traffic Control" {
		t.Errorf("title: got %q", title)
	}
}

func TestCreateNoteNeverOverwrites(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/taken.md": "# Taken\n"})
	g := openGraph(root)
	now := time.Now()

	first, _, err := g.createNote("Taken", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "taken-2.md" {
		t.Errorf("a taken name steps aside: got %q", filepath.Base(first))
	}
	second, _, err := g.createNote("Taken", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(second) != "taken-3.md" {
		t.Errorf("and steps aside again: got %q", filepath.Base(second))
	}
	if content, err := os.ReadFile(filepath.Join(root, notesDir, "taken.md")); err != nil ||
		string(content) != "# Taken\n" {
		t.Errorf("the note already there is untouched: got %q, %v", content, err)
	}
}

// A note with no title is not written by createNote at all: it is begun in a
// draft window, and saveDraft writes it where it belongs when it is Put.
func TestSaveDraft(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/a.md": "# A\n"})
	g := openGraph(root)

	path, saved, err := g.saveDraft("# Air Traffic Control\n\nthe body\n", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, notesDir, "air-traffic-control.md"); path != want {
		t.Fatalf("path: got %q, want %q", path, want)
	}
	// The note is written there, and is what the window is then brought to.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != saved {
		t.Errorf("the file holds %q, the window %q", content, saved)
	}
	split := splitFrontmatter(saved)
	if !strings.Contains(split.raw, "id: ") {
		t.Errorf("frontmatter: got %q", split.raw)
	}
	if got := strings.TrimSpace(split.body); got != "# Air Traffic Control\n\nthe body" {
		t.Errorf("body: got %q", got)
	}
	// It reads back as the graph sees any other note.
	if title := deriveTitle("notes/air-traffic-control.md", "", split.body); title != "Air Traffic Control" {
		t.Errorf("title: got %q", title)
	}
}

// Two drafts saved at once are two notes, not one written over twice.
func TestSaveDraftNeverOverwrites(t *testing.T) {
	g := openGraph(testGraph(t, map[string]string{"notes/a.md": "# A\n"}))
	first, _, err := g.saveDraft("", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := g.saveDraft("", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "untitled.md" || filepath.Base(second) != "untitled-2.md" {
		t.Errorf("got %q and %q", filepath.Base(first), filepath.Base(second))
	}
}

func TestDraftTitle(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
		want string
	}{
		{"frontmatter names it", "---\ntitle: Named\n---\n\n# Other\n", "Named"},
		{"else the first heading", "# Air Traffic Control\n\nbody\n", "Air Traffic Control"},
		{"a heading below a line still counts", "a line\n\n# Heading\n", "Heading"},
		{"else the first line written", "the tower is a room\n\nmore\n", "the tower is a room"},
		{"and nothing is nothing", "\n\n   \n", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := draftTitle(test.text); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestWithNoteID(t *testing.T) {
	const id = "01m2bfvc1k3b49v5f9q0pzh37f"
	for _, test := range []struct {
		name string
		text string
		want string
	}{
		{"no frontmatter gets some", "# A\n", "---\nid: " + id + "\n---\n\n# A\n"},
		{"leading blank lines are not kept", "\n# A\n", "---\nid: " + id + "\n---\n\n# A\n"},
		{
			"the writer's own frontmatter is kept",
			"---\ntitle: A\n---\n\nbody\n",
			"---\nid: " + id + "\ntitle: A\n---\n\nbody\n",
		},
		{"an id already there stands", "---\nid: other\n---\n\nbody\n", "---\nid: other\n---\n\nbody\n"},
		{"even one YAML reads as a number", "---\nid: 1234\n---\n\nbody\n", "---\nid: 1234\n---\n\nbody\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := withNoteID(test.text, id); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestCreateNoteKeepsTheTitleOnOneLine(t *testing.T) {
	g := openGraph(testGraph(t, map[string]string{"notes/a.md": "# A\n"}))
	path, _, err := g.createNote("First line\nsecond line", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(splitFrontmatter(string(content)).body); got != "# First line second line" {
		t.Errorf("got %q", got)
	}
}
