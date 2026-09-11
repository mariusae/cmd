package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	apexapi "github.com/mariusae/apex/go/apex"
)

func windowGraph(t *testing.T) *reflWindow {
	t.Helper()
	root := testGraph(t, map[string]string{
		"daily/2026-09-10.md": "- priorities:\n  - land chrysalis\n",
		"notes/chrysalis.md":  "# Chrysalis\n\nthe design\n",
	})
	return testWindow(openGraph(root), time.Date(2026, 9, 1, 15, 0, 0, 0, time.Local))
}

// testWindow is a window with no session behind it: the rendering and the row
// bookkeeping are all local, and that is what these tests are about.
func testWindow(g *graph, now time.Time) *reflWindow {
	w := &reflWindow{
		graph:   g,
		now:     func() time.Time { return now },
		panes:   map[int]*pane{},
		refresh: make(chan struct{}, 1),
	}
	w.main = &pane{shown: pageSize}
	w.panes[0] = w.main
	return w
}

func TestRenderListShowsNamesAndTimes(t *testing.T) {
	w := windowGraph(t)
	rendered := w.renderList(w.main.shown)
	lines := strings.Split(strings.TrimSuffix(rendered.text, "\n"), "\n")
	if len(lines) != 2 || len(rendered.rows) != 2 {
		t.Fatalf("got %d lines and %d rows, want 2 of each", len(lines), len(rendered.rows))
	}
	// testGraph stamps in name order, so notes/chrysalis.md is the newest.
	if !strings.HasPrefix(lines[0], "Chrysalis ") || !strings.HasSuffix(lines[0], "1:00PM") {
		t.Errorf("newest row: got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "2026-09-10 ") {
		t.Errorf("second row: got %q", lines[1])
	}
	if !strings.HasSuffix(rendered.rows[0].path, "notes/chrysalis.md") || rendered.rows[0].line != 0 {
		t.Errorf("row target: got %+v", rendered.rows[0])
	}
}

func TestRenderSearchShowsBlocksAnchoredToTheirLines(t *testing.T) {
	w := windowGraph(t)
	w.command = "Search chrysalis"
	rendered := w.render()
	want := strings.Join([]string{
		"Search chrysalis",
		"",
		"Chrysalis 1:00PM",
		"\t# Chrysalis",
		"",
		"2026-09-10 12:00PM",
		"\t- priorities:",
		"\t  - land chrysalis",
		"",
		"",
	}, "\n")
	if rendered.text != want {
		t.Fatalf("body:\n got %q\nwant %q", rendered.text, want)
	}
	daily := rendered.rows[7] // the matching bullet
	if !strings.HasSuffix(daily.path, "daily/2026-09-10.md") || daily.line != 2 {
		t.Errorf("a block row points into the note: got %+v", daily)
	}
	if rendered.rows[0].path != "" || rendered.rows[4].path != "" {
		t.Error("the header and the gaps should point at nothing")
	}
}

func TestRenderSearchSaysWhenNothingMatches(t *testing.T) {
	w := windowGraph(t)
	w.command = "Search nothing at all"
	if !strings.Contains(w.render().text, "nothing to show") {
		t.Errorf("got %q", w.render().text)
	}
}

func TestRowAtFindsTheRowUnderAnOffset(t *testing.T) {
	rendered := view{
		text: "first\nsecond\n\nfourth\n",
		rows: []row{{path: "/a", line: 0}, {path: "/b", line: 3}, {}, {path: "/c", line: 9}},
	}
	for _, test := range []struct {
		name string
		at   int
		want row
		ok   bool
	}{
		{name: "inside the first row", at: 2, want: row{path: "/a"}, ok: true},
		{name: "at the end of the first row", at: 5, want: row{path: "/a"}, ok: true},
		{name: "inside the second row", at: 8, want: row{path: "/b", line: 3}, ok: true},
		{name: "on a blank row", at: 13},
		{name: "past the body", at: 100},
		{name: "before the body", at: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := rowAt(rendered, test.at)
			if ok != test.ok || got != test.want {
				t.Errorf("got (%+v, %v), want (%+v, %v)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestMinimalEdit(t *testing.T) {
	for _, test := range []struct {
		name          string
		before, after string
		from, to      int
		text          string
		changed       bool
	}{
		{name: "nothing moved", before: "same", after: "same"},
		{name: "a middle line", before: "a\nb\nc\n", after: "a\nB\nc\n", from: 2, to: 3, text: "B", changed: true},
		{name: "an insertion at the front", before: "b\n", after: "a\nb\n", from: 0, to: 0, text: "a\n", changed: true},
		{name: "emptied", before: "a\n", after: "", from: 0, to: 2, changed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			from, to, text, changed := minimalEdit(test.before, test.after)
			if from != test.from || to != test.to || text != test.text || changed != test.changed {
				t.Errorf("got (%d, %d, %q, %v), want (%d, %d, %q, %v)",
					from, to, text, changed, test.from, test.to, test.text, test.changed)
			}
		})
	}
}

func TestDailyPatternMatchesDailiesOnly(t *testing.T) {
	w := &reflWindow{graph: openGraph("/graph")}
	pattern := w.dailyPattern()
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "/graph/daily/2026-09-10.md", want: true},
		{name: "/graph/daily/notes.md"},
		{name: "/graph/notes/2026-09-10.md"},
		{name: "/other/daily/2026-09-10.md"},
	} {
		if got := matchesWhole(pattern, test.name); got != test.want {
			t.Errorf("%q against %q: got %v, want %v", test.name, pattern, got, test.want)
		}
	}
}

// matchesWhole is how Apex applies a rule's name pattern: the whole name must
// match, not some part of it.
func matchesWhole(pattern, name string) bool {
	return regexp.MustCompile(`^(?:` + pattern + `)$`).MatchString(name)
}

func pagedWindow(t *testing.T, count int) *reflWindow {
	t.Helper()
	notes := map[string]string{}
	for index := 0; index < count; index++ {
		notes[fmt.Sprintf("notes/note-%02d.md", index)] = fmt.Sprintf("# Note %02d\n", index)
	}
	return testWindow(openGraph(testGraph(t, notes)), time.Date(2026, 9, 10, 15, 0, 0, 0, time.Local))
}

func TestRenderListShowsOnePageAndAMoreRow(t *testing.T) {
	w := pagedWindow(t, 25)
	rendered := w.renderList(w.main.shown)
	lines := strings.Split(strings.TrimSuffix(rendered.text, "\n"), "\n")
	if len(lines) != pageSize+1 {
		t.Fatalf("got %d lines, want a page and a more row:\n%s", len(lines), rendered.text)
	}
	if got := lines[len(lines)-1]; got != "more" {
		t.Errorf("the last row offers the rest: got %q", got)
	}
	last := rendered.rows[len(rendered.rows)-1]
	if !last.more || last.path != "" {
		t.Errorf("the more row stands for no note: got %+v", last)
	}
	// testGraph stamps in name order, so the highest-numbered note is newest.
	if !strings.HasPrefix(lines[0], "Note 24 ") {
		t.Errorf("newest first: got %q", lines[0])
	}
}

func TestRenderListEndsWithoutAMoreRowWhenItFits(t *testing.T) {
	w := pagedWindow(t, pageSize)
	if strings.Contains(w.renderList(w.main.shown).text, "more") {
		t.Error("a graph that fits on a page has nothing more to show")
	}
}

func TestLookOnTheMoreRowShowsTheNextPage(t *testing.T) {
	w := pagedWindow(t, 25)
	w.main.view = w.renderList(w.main.shown)

	at := len([]rune(w.main.view.text[:strings.LastIndex(w.main.view.text, "more")+2]))
	target, ok := rowAt(w.main.view, at)
	if !ok || !target.more {
		t.Fatalf("the more row should be found: got %+v, %v", target, ok)
	}

	if !w.handleLook(apexapi.Plumb{
		Window: &apexapi.Window{ID: 0},
		At:     &apexapi.Span{Q0: at},
	}) {
		t.Fatal("a Look on the more row should be taken")
	}
	if w.main.shown != 2*pageSize {
		t.Errorf("shown: got %d, want %d", w.main.shown, 2*pageSize)
	}
	next := w.renderList(w.main.shown)
	if lines := strings.Count(next.text, "\n"); lines != 2*pageSize+1 {
		t.Errorf("the second page: got %d lines, want %d", lines, 2*pageSize+1)
	}
	if !strings.HasSuffix(next.text, "more\n") {
		t.Errorf("and still offers the rest:\n%s", next.text)
	}
}

func TestShowResetsToTheFirstPage(t *testing.T) {
	w := pagedWindow(t, 25)
	w.main.shown = 100
	w.show("Search note")
	if w.main.shown != pageSize || w.command != "Search note" {
		t.Errorf("got shown %d command %q", w.main.shown, w.command)
	}
}

func TestRenderBacklinks(t *testing.T) {
	w := testWindow(backlinkGraph(t), time.Date(2026, 9, 10, 15, 0, 0, 0, time.Local))
	rendered, found := w.renderBacklinks("Chrysalis", pageSize)
	if found.rel != "notes/chrysalis.md" {
		t.Errorf("the note it settled on: got %q", found.rel)
	}
	if !strings.HasPrefix(rendered.text, "Backlinks Chrysalis\n\n") {
		t.Fatalf("header: got %q", rendered.text)
	}
	if !strings.Contains(rendered.text, "\t  - land [[Chrysalis]]") {
		t.Errorf("the linking block should be shown:\n%s", rendered.text)
	}
	if strings.Contains(rendered.text, "unrelated thing") {
		t.Errorf("a branch that links elsewhere should be dropped:\n%s", rendered.text)
	}
}

func TestRenderBacklinksReportsANoteItCannotFind(t *testing.T) {
	w := testWindow(backlinkGraph(t), time.Now())
	rendered, found := w.renderBacklinks("No Such Note", pageSize)
	if !strings.Contains(rendered.text, "No Such Note") {
		t.Errorf("got %q", rendered.text)
	}
	if found.rel != "" {
		t.Errorf("nothing was settled on: got %q", found.rel)
	}
}

func TestCommandOfBodyReadsTheWindowsOwnState(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "a search says what it is searching", body: "Search chrysalis\n\nChrysalis 1:00PM\n", want: "Search chrysalis"},
		{name: "a phrase survives", body: `Search "air traffic" control` + "\n\n", want: `Search "air traffic" control`},
		{name: "trailing space is a bare Search", body: "Search   \n\n", want: "Search"},
		{name: "the bare word searches for nothing, which is everything", body: "Search\n\n", want: "Search"},
		{name: "a timeline says so", body: "Timeline\n\n2026-09-10 8:33PM\n", want: "Timeline"},
		{name: "a listing names no view", body: "2026-09-10 8:33PM\nApex 4:58PM\n", want: ""},
		{name: "an empty window names no view", body: "", want: ""},
		{name: "a note whose name starts the same is not a view", body: "Searchlight 4:58PM\n", want: ""},
		{name: "nor is this one", body: "Timelines of History 4:58PM\n", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := commandOfBody(test.body); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestRenderFollowsTheWindowsCommand(t *testing.T) {
	w := pagedWindow(t, 3)
	for _, test := range []struct {
		name    string
		command string
		first   string
	}{
		{name: "no command lists the graph", command: "", first: "Note 02 "},
		{name: "a search searches", command: "Search Note 01", first: "Search Note 01"},
		{name: "a bare Search lists the graph", command: "Search", first: "Note 02 "},
		{name: "an unknown line lists the graph", command: "Note 01 3:00PM", first: "Note 02 "},
	} {
		t.Run(test.name, func(t *testing.T) {
			w.command = test.command
			first, _, _ := strings.Cut(w.render().text, "\n")
			if !strings.HasPrefix(first, test.first) {
				t.Errorf("got %q, want it to start %q", first, test.first)
			}
		})
	}
}

func TestBacklinksName(t *testing.T) {
	g := openGraph("/graph")
	for _, test := range []struct{ ref, want string }{
		{ref: "notes/a.md", want: "/graph/notes/a.md+Backlinks"},
		{ref: "Project Atlas", want: "/graph/Project Atlas+Backlinks"},
	} {
		if got := backlinksName(g, test.ref); got != test.want {
			t.Errorf("backlinksName(%q): got %q, want %q", test.ref, got, test.want)
		}
	}
}

func TestRowAtFindsRowsInEachPane(t *testing.T) {
	w := testWindow(openGraph(t.TempDir()), time.Now())
	other := &pane{shown: pageSize, view: view{
		text: "Backlinks X\n\nA 1:00PM\n",
		rows: []row{{}, {}, {path: "/graph/notes/a.md"}},
	}}
	w.panes[7] = other
	if target, ok := rowAt(other.view, 14); !ok || target.path != "/graph/notes/a.md" {
		t.Errorf("got (%+v, %v)", target, ok)
	}
}

func TestRenderTimeline(t *testing.T) {
	root := gitGraph(t,
		map[string]string{"notes/a.md": "# A\n\nfirst paragraph\n"},
		map[string]string{"notes/b.md": "# B\n\n- a bullet\n  - a nested one\n"},
	)
	w := testWindow(openGraph(root), time.Date(2026, 9, 2, 15, 0, 0, 0, time.Local))
	w.command = timelineVerb

	rendered := w.render()
	lines := strings.Split(strings.TrimSuffix(rendered.text, "\n"), "\n")
	if lines[0] != "Timeline" || lines[1] != "" {
		t.Fatalf("header: got %q", rendered.text)
	}
	// Newest commit first, each note with the blocks that changed under it.
	if !strings.HasPrefix(lines[2], "B ") {
		t.Errorf("newest change first: got %q", lines[2])
	}
	if lines[3] != "\t# B" {
		t.Errorf("its first block: got %q", lines[3])
	}
	if !strings.Contains(rendered.text, "\t- a bullet\n\t  - a nested one") {
		t.Errorf("the nested block should be whole:\n%s", rendered.text)
	}
	// Rows point back into the note the change was in.
	for index, line := range lines {
		if line == "\t# B" {
			if got := rendered.rows[index]; !strings.HasSuffix(got.path, "notes/b.md") || got.line != 1 {
				t.Errorf("block row: got %+v", got)
			}
		}
	}
}

func TestRenderTimelinePages(t *testing.T) {
	revisions := make([]map[string]string, 0, 4)
	for index := 0; index < 4; index++ {
		revisions = append(revisions, map[string]string{
			fmt.Sprintf("notes/note-%d.md", index): fmt.Sprintf("# Note %d\n", index),
		})
	}
	w := testWindow(openGraph(gitGraph(t, revisions...)), time.Now())
	w.command = timelineVerb
	w.main.shown = 2

	rendered := w.render()
	if !strings.HasSuffix(rendered.text, "more\n") {
		t.Errorf("two of four changes shown:\n%s", rendered.text)
	}
	if strings.Contains(rendered.text, "Note 1") {
		t.Errorf("the third change is behind the more row:\n%s", rendered.text)
	}
}

func TestRenderTimelineNeedsARepository(t *testing.T) {
	w := testWindow(openGraph(testGraph(t, map[string]string{"notes/a.md": "# A\n"})), time.Now())
	w.command = timelineVerb
	if !strings.Contains(w.render().text, "not a git repository") {
		t.Errorf("got %q", w.render().text)
	}
}
