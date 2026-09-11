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

	path, err := g.createNote("Air Traffic Control", now)
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

	first, err := g.createNote("Taken", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first) != "taken-2.md" {
		t.Errorf("a taken name steps aside: got %q", filepath.Base(first))
	}
	second, err := g.createNote("Taken", now)
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

func TestCreateNoteRefusesAnEmptyTitle(t *testing.T) {
	g := openGraph(testGraph(t, map[string]string{"notes/a.md": "# A\n"}))
	if _, err := g.createNote("   ", time.Now()); err == nil {
		t.Error("a note with no title should be refused")
	}
}

func TestCreateNoteKeepsTheTitleOnOneLine(t *testing.T) {
	g := openGraph(testGraph(t, map[string]string{"notes/a.md": "# A\n"}))
	path, err := g.createNote("First line\nsecond line", time.Now())
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
