package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testGraph writes a graph of notes and returns its root. Each note is given a
// distinct modification time, oldest first, so ordering is testable.
func testGraph(t *testing.T, notes map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{dailyDir, notesDir, "assets", "templates"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	when := time.Date(2026, 9, 1, 12, 0, 0, 0, time.Local)
	for _, rel := range sortedKeys(notes) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(notes[rel]), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
		when = when.Add(time.Hour)
	}
	return root
}

func sortedKeys(from map[string]string) []string {
	keys := make([]string, 0, len(from))
	for key := range from {
		keys = append(keys, key)
	}
	for index := 1; index < len(keys); index++ {
		for at := index; at > 0 && keys[at] < keys[at-1]; at-- {
			keys[at], keys[at-1] = keys[at-1], keys[at]
		}
	}
	return keys
}

func TestFindGraphPrefersTheExplicitDirectory(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/a.md": "# A\n"})
	other := t.TempDir()
	got, err := findGraph(root,
		func(string) string { return other },
		func() (string, error) { return other, nil },
		func() (string, error) { return other, nil })
	if err != nil || got != root {
		t.Fatalf("got (%q, %v), want %q", got, err, root)
	}
}

func TestFindGraphFallsBackThroughEnvironmentThenWorkingDirectory(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/a.md": "# A\n"})
	home := t.TempDir()

	got, err := findGraph("",
		func(key string) string {
			if key == "REFLECT_GRAPH" {
				return root
			}
			return ""
		},
		func() (string, error) { return home, nil },
		func() (string, error) { return home, nil })
	if err != nil || got != root {
		t.Fatalf("from the environment: got (%q, %v), want %q", got, err, root)
	}

	inside := filepath.Join(root, notesDir)
	got, err = findGraph("",
		func(string) string { return "" },
		func() (string, error) { return inside, nil },
		func() (string, error) { return home, nil })
	if err != nil || got != root {
		t.Fatalf("from the working directory: got (%q, %v), want %q", got, err, root)
	}
}

func TestFindGraphRefusesADirectoryThatIsNotOne(t *testing.T) {
	plain := t.TempDir()
	if _, err := findGraph(plain, os.Getenv, os.Getwd, os.UserHomeDir); err == nil {
		t.Error("a directory with no graph markers should be refused")
	}
}

func TestWalkSkipsReservedAndHiddenTrees(t *testing.T) {
	root := testGraph(t, map[string]string{
		"daily/2026-09-10.md":    "- a note\n",
		"notes/keep.md":          "# Keep\n",
		"notes/deep/nested.md":   "# Nested\n",
		"assets/caption.md":      "# Caption\n",
		"templates/daily.md":     "# Template\n",
		"audio-memos/memo.md":    "# Memo\n",
		".reflect/internal.md":   "# Internal\n",
		"notes/not-markdown.txt": "text\n",
	})
	files, err := openGraph(root).walk()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, file := range files {
		got = append(got, file.rel)
	}
	want := []string{"daily/2026-09-10.md", "notes/deep/nested.md", "notes/keep.md"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestListOrdersByRecencyAndHidesPrivateNotes(t *testing.T) {
	root := testGraph(t, map[string]string{
		"notes/first.md":  "# First\n",
		"notes/second.md": "# Second\n",
		"notes/secret.md": "---\nprivate: true\n---\n# Secret\n",
	})
	// testGraph stamps in name order, so second.md is the newest.
	notes, err := openGraph(root).list()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, subject := range notes {
		got = append(got, subject.title)
	}
	want := []string{"Second", "First"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeriveTitle(t *testing.T) {
	for _, test := range []struct {
		name             string
		rel              string
		frontmatterTitle string
		body             string
		want             string
	}{
		{name: "frontmatter wins", rel: "notes/a.md", frontmatterTitle: "Authored", body: "# Heading\n", want: "Authored"},
		{name: "then the first H1", rel: "notes/a.md", body: "intro\n\n# Heading\n", want: "Heading"},
		{name: "closing hashes are dropped", rel: "notes/a.md", body: "# Heading #\n", want: "Heading"},
		{name: "an H1 in a code fence is not a title", rel: "notes/a.md", body: "```\n# Fenced\n```\n\n# Real\n", want: "Real"},
		{name: "an empty H1 is skipped", rel: "notes/a.md", body: "#\n\n# Real\n", want: "Real"},
		{name: "a deeper heading is not a title", rel: "notes/a.md", body: "## Section\n", want: "a"},
		{name: "then the daily date", rel: "daily/2026-09-10.md", body: "- a bullet\n", want: "2026-09-10"},
		{name: "then the file's own name", rel: "notes/some-note.md", body: "- a bullet\n", want: "some-note"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := deriveTitle(test.rel, test.frontmatterTitle, test.body); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestDailyDate(t *testing.T) {
	for _, test := range []struct{ rel, want string }{
		{rel: "daily/2026-09-10.md", want: "2026-09-10"},
		{rel: "daily/2026-02-31.md"},
		{rel: "daily/not-a-date.md"},
		{rel: "notes/2026-09-10.md"},
		{rel: "daily/deep/2026-09-10.md"},
	} {
		if got := dailyDate(test.rel); got != test.want {
			t.Errorf("dailyDate(%q): got %q, want %q", test.rel, got, test.want)
		}
	}
}

func TestDisplayTitle(t *testing.T) {
	for _, test := range []struct{ title, want string }{
		{title: "Plain Note", want: "Plain Note"},
		{title: "Charlotte MacCaw // Mum", want: "Charlotte MacCaw"},
		{title: "Meeting with [[Ada Lovelace|Ada]]", want: "Meeting with Ada"},
		{title: "About [[Chrysalis]]", want: "About Chrysalis"},
		{title: "Read [the paper](https://example.com)", want: "Read the paper"},
		{title: "https://example.com/a", want: "https://example.com/a"},
		{title: "// Mum", want: "Mum"},
	} {
		if got := displayTitle(test.title); got != test.want {
			t.Errorf("displayTitle(%q): got %q, want %q", test.title, got, test.want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	now := time.Date(2026, 9, 10, 15, 4, 0, 0, time.Local)
	for _, test := range []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "today", at: now.Add(-2 * time.Hour), want: "1:04PM"},
		{name: "this week", at: now.Add(-48 * time.Hour), want: "Tue3:04PM"},
		{name: "older", at: now.Add(-30 * 24 * time.Hour), want: "11Aug26"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := formatTime(test.at, now); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestDisplayPath(t *testing.T) {
	if got := displayPath("/graph/notes/a.md", "/graph"); got != "notes/a.md" {
		t.Errorf("under the working directory: got %q", got)
	}
	if got := displayPath("/graph/notes/a.md", "/elsewhere"); got != "/graph/notes/a.md" {
		t.Errorf("outside the working directory: got %q", got)
	}
	if got := displayPath("/graph/notes/a.md", ""); got != "/graph/notes/a.md" {
		t.Errorf("with no working directory: got %q", got)
	}
}

func TestDailyPathAndDay(t *testing.T) {
	g := openGraph("/graph")
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.Local)
	path := g.dailyPath(day)
	if want := filepath.Join("/graph", dailyDir, "2026-09-10.md"); path != want {
		t.Fatalf("dailyPath: got %q, want %q", path, want)
	}
	got, err := g.dailyDay(path)
	if err != nil || !got.Equal(day) {
		t.Errorf("dailyDay: got (%v, %v), want %v", got, err, day)
	}
	if _, err := g.dailyDay(filepath.Join("/graph", notesDir, "a.md")); err == nil {
		t.Error("a note that is not a daily should have no day")
	}
}
