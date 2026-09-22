package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	apexapi "github.com/mariusae/apex/go/apex"
)

func testWindow(t *testing.T, root string) *draftsWindow {
	t.Helper()
	w := &draftsWindow{
		dir:   openDir(root),
		owner: "drafts-1",
		now:   func() time.Time { return time.Date(2026, 9, 20, 15, 0, 0, 0, time.Local) },
	}
	w.main = &pane{shown: pageSize}
	return w
}

// The window's state is its own text: its first line names the view, and says
// whether the archive is part of it.
func TestParseCommand(t *testing.T) {
	for _, test := range []struct {
		line string
		want viewCommand
	}{
		{"Search chrysalis", viewCommand{verb: "Search", args: "chrysalis"}},
		{"Search", viewCommand{verb: "Search"}},
		{"  Timeline  ", viewCommand{verb: "Timeline"}},
		{"Timeline of events", viewCommand{verb: "Timeline", args: "of events"}},
		{"Some Draft Title 3:00PM", viewCommand{}},
		{"", viewCommand{}},
		// A draft whose title begins with a view's word, without the space
		// that would make it an argument.
		{"Searching for a name", viewCommand{}},
		// The archive is said of whatever view follows it, and of the listing
		// when none does.
		{"IncludeArchive", viewCommand{archive: true}},
		{"IncludeArchive Search chrysalis", viewCommand{verb: "Search", args: "chrysalis", archive: true}},
		{"  IncludeArchive   Timeline  ", viewCommand{verb: "Timeline", archive: true}},
		{"IncludeArchived drafts", viewCommand{}},
		// Said the other way round it is a draft title, not a command.
		{"Search IncludeArchive", viewCommand{verb: "Search", args: "IncludeArchive"}},
	} {
		if got := parseCommand(test.line); got != test.want {
			t.Errorf("parseCommand(%q): got %+v, want %+v", test.line, got, test.want)
		}
	}
}

// A view says itself back in the words that would show it again, which is what
// the window writes as its first line.
func TestViewCommandString(t *testing.T) {
	for _, test := range []struct {
		command viewCommand
		want    string
	}{
		{viewCommand{}, ""},
		{viewCommand{verb: "Timeline"}, "Timeline"},
		{viewCommand{verb: "Search", args: "chrysalis"}, "Search chrysalis"},
		{viewCommand{archive: true}, "IncludeArchive"},
		{viewCommand{verb: "Search", args: "chrysalis", archive: true}, "IncludeArchive Search chrysalis"},
	} {
		if got := test.command.String(); got != test.want {
			t.Errorf("%+v: got %q, want %q", test.command, got, test.want)
		}
		if back := parseCommand(test.want); back != test.command {
			t.Errorf("parseCommand(%q): got %+v, want %+v", test.want, back, test.command)
		}
	}
}

// A search keeps the archive where it was: what is being looked for and where
// it is being looked for are two questions.
func TestSearchingKeepsTheArchive(t *testing.T) {
	archived := viewCommand{verb: "Timeline", archive: true}
	if got := archived.searching("chrysalis"); got != (viewCommand{verb: "Search", args: "chrysalis", archive: true}) {
		t.Errorf("got %+v", got)
	}
	if got := archived.searching("  "); got != (viewCommand{archive: true}) {
		t.Errorf("got %+v", got)
	}
}

func TestCommandOfBody(t *testing.T) {
	for _, test := range []struct {
		body string
		want viewCommand
	}{
		{"Search chrysalis\n\nresults\n", viewCommand{verb: "Search", args: "chrysalis"}},
		{"Timeline\n\nchanges\n", viewCommand{verb: "Timeline"}},
		{"IncludeArchive\n\nA Draft 3:00PM\n", viewCommand{archive: true}},
		{"A Draft 3:00PM\nAnother 2:00PM\n", viewCommand{}},
		{"", viewCommand{}},
	} {
		if got := commandOfBody(test.body); got != test.want {
			t.Errorf("commandOfBody(%q): got %+v, want %+v", test.body, got, test.want)
		}
	}
}

func TestOpeningCommand(t *testing.T) {
	if got := openingCommand("  chrysalis "); got != "Search chrysalis" {
		t.Errorf("got %q", got)
	}
	if got := openingCommand("   "); got != "" {
		t.Errorf("got %q", got)
	}
}

// The listing is the drafts, newest first, one to a line.
func TestRenderList(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md":       "# One\n",
		"two.md":       "# Two\n",
		"one-notes.md": "# Notes on One\n",
	}, "one.md", "one-notes.md", "two.md")
	rendered := testWindow(t, root).renderList(viewCommand{}, pageSize)
	want := "Two 11:00AM\nOne 9:00AM\n"
	if rendered.text != want {
		t.Errorf("got %q, want %q", rendered.text, want)
	}
	if len(rendered.rows) != 2 || filepath.Base(rendered.rows[0].path) != "two.md" {
		t.Errorf("rows: %+v", rendered.rows)
	}
}

// Every view ends on a `more` row while there is more to show, and Look there
// asks for the next page.
func TestRenderListPages(t *testing.T) {
	files := map[string]string{}
	var order []string
	for index := 0; index < pageSize+3; index++ {
		name := string(rune('a'+index)) + ".md"
		files[name] = "# " + strings.ToUpper(string(rune('a'+index))) + "\n"
		order = append(order, name)
	}
	w := testWindow(t, testDir(t, files, order...))
	rendered := w.renderList(viewCommand{}, pageSize)
	lines := strings.Split(strings.TrimSuffix(rendered.text, "\n"), "\n")
	if len(lines) != pageSize+1 {
		t.Fatalf("got %d lines, want %d", len(lines), pageSize+1)
	}
	if lines[len(lines)-1] != "more" {
		t.Errorf("last line: %q", lines[len(lines)-1])
	}
	if !rendered.rows[len(rendered.rows)-1].more {
		t.Error("the last row does not ask for the next page")
	}
	// Asked for, the next page has them all and says no more.
	if rendered = w.renderList(viewCommand{}, pageSize*2); strings.Contains(rendered.text, "more") {
		t.Errorf("a full listing still offers more:\n%s", rendered.text)
	}
}

func TestRenderSearch(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md": "# One\n\nsomething about widgets\n",
	}, "one.md")
	rendered := testWindow(t, root).renderSearch(viewCommand{verb: searchVerb, args: "widgets"}, queryTerms("widgets"), pageSize)
	want := "Search widgets\n\nOne 9:00AM\n\tsomething about widgets\n\n"
	if rendered.text != want {
		t.Errorf("got %q, want %q", rendered.text, want)
	}
	// The block's line points into the draft, so Look on it opens there.
	if got := rendered.rows[3]; filepath.Base(got.path) != "one.md" || got.line != 3 {
		t.Errorf("block row: %+v", got)
	}
}

func TestRenderSearchWithNothingToShow(t *testing.T) {
	root := testDir(t, map[string]string{"one.md": "# One\n"}, "one.md")
	rendered := testWindow(t, root).renderSearch(viewCommand{verb: searchVerb, args: "absent"}, queryTerms("absent"), pageSize)
	if !strings.Contains(rendered.text, "nothing to show") {
		t.Errorf("got %q", rendered.text)
	}
}

func TestRenderAnEmptyDirectory(t *testing.T) {
	w := testWindow(t, t.TempDir())
	if !strings.HasPrefix(w.renderList(viewCommand{}, pageSize).text, "no drafts in ") {
		t.Errorf("got %q", w.renderList(viewCommand{}, pageSize).text)
	}
}

// render reads the window's own first line, which is where the state is.
func TestRenderFollowsTheCommand(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md": "# One\n\nabout widgets\n",
		"two.md": "# Two\n",
	}, "one.md", "two.md")
	w := testWindow(t, root)

	if got := w.render().text; !strings.HasPrefix(got, "Two ") {
		t.Errorf("no command lists the drafts; got %q", got)
	}
	w.command = "Search widgets"
	if got := w.render().text; !strings.HasPrefix(got, "Search widgets\n") {
		t.Errorf("got %q", got)
	}
	// A search with no terms is not a search: it restores the full list.
	w.command = "Search"
	if got := w.render().text; !strings.HasPrefix(got, "Two ") {
		t.Errorf("got %q", got)
	}
	w.command = "Timeline"
	if got := w.render().text; !strings.HasPrefix(got, "Timeline\n") {
		t.Errorf("got %q", got)
	}
}

func TestRowAt(t *testing.T) {
	rendered := view{
		text: "Search widgets\n\nOne 9:00AM\n\tthe block\n\nmore\n",
		rows: []row{
			{}, {},
			{path: "/d/one.md"},
			{path: "/d/one.md", line: 3},
			{},
			{more: true},
		},
	}
	for _, test := range []struct {
		at   int
		path string
		line int
		more bool
		ok   bool
	}{
		{at: 0, ok: false},                             // the header stands for nothing
		{at: 17, path: "/d/one.md", ok: true},          // the draft's own row
		{at: 30, path: "/d/one.md", line: 3, ok: true}, // a block line
		{at: 40, more: true, ok: true},                 // the `more` row
		{at: 1000, ok: false},                          // past the end
		{at: -1, ok: false},
	} {
		got, ok := rowAt(rendered, test.at)
		if ok != test.ok || got.path != test.path || got.line != test.line || got.more != test.more {
			t.Errorf("rowAt(%d): got %+v, %v; want %s:%d more=%v, %v",
				test.at, got, ok, test.path, test.line, test.more, test.ok)
		}
	}
}

// Everything is written back as the smallest edit that does the job, so
// selection and scroll position survive a refresh.
func TestMinimalEdit(t *testing.T) {
	for _, test := range []struct {
		before, after string
		from, to      int
		text          string
		changed       bool
	}{
		{"same", "same", 0, 0, "", false},
		{"A 1:00PM\nB 2:00PM\n", "A 1:01PM\nB 2:00PM\n", 5, 6, "1", true},
		{"B 2:00PM\n", "A 1:00PM\nB 2:00PM\n", 0, 0, "A 1:00PM\n", true},
		{"A\nB\n", "A\n", 2, 4, "", true},
		// Runes, not bytes: an offset apex is given is a character offset.
		{"é one\n", "é two\n", 2, 5, "two", true},
	} {
		from, to, text, changed := minimalEdit(test.before, test.after)
		if from != test.from || to != test.to || text != test.text || changed != test.changed {
			t.Errorf("minimalEdit(%q, %q): got %d, %d, %q, %v", test.before, test.after, from, to, text, changed)
		}
	}
}

// A rule about the directory's files says so by name, and says nothing about
// the windows drafts makes beside them.
func TestFilePattern(t *testing.T) {
	root := t.TempDir()
	w := testWindow(t, root)
	pattern := regexp.MustCompile(`^(?:` + w.filePattern() + `)$`)
	separator := string(filepath.Separator)
	for _, test := range []struct {
		name string
		want bool
	}{
		{root + separator + "one.md", true},
		{root + separator + "one-notes.md", true},
		{root + separator + "one.md+Preview", false},
		{filepath.Join(root, "-drafts"), false},
		{filepath.Join(root, "sub", "one.md"), false},
		{filepath.Join(t.TempDir(), "one.md"), false},
		// An archived draft is a draft, and answers to the same words.
		{filepath.Join(root, "archive", "one.md"), true},
		{filepath.Join(root, "archive", "one.md+Preview"), false},
		{filepath.Join(root, "archive", "sub", "one.md"), false},
	} {
		if got := pattern.MatchString(test.name); got != test.want {
			t.Errorf("filePattern on %q: got %v, want %v", test.name, got, test.want)
		}
	}
}

func TestWindowName(t *testing.T) {
	if got := windowName("/d"); got != filepath.Join("/d", "-drafts") {
		t.Errorf("got %q", got)
	}
}

// Asked for the archive, the window says which of the drafts it is showing are
// put away, and its first line says the archive is in it.
func TestRenderShowsWhereAnArchivedDraftIs(t *testing.T) {
	root := testDir(t, map[string]string{"one.md": "# One\n"}, "one.md")
	archiveDir(t, root, map[string]string{"old.md": "# Old\n"}, "old.md")
	w := testWindow(t, root)

	if got := w.render().text; got != "One 9:00AM\n" {
		t.Errorf("the archive is shown unasked: %q", got)
	}
	w.command = includeArchiveVerb
	want := "IncludeArchive\n\nOne 9:00AM\nOld Sat9:00AM archive\n"
	if got := w.render().text; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The archive is not a view of its own: it survives a search and a timeline,
// because it says where to look rather than what to look for.
func TestIncludeArchiveSurvivesTheViews(t *testing.T) {
	w := testWindow(t, t.TempDir())

	w.handleIncludeArchive(apexapi.Plumb{})
	if got := w.command; got != "IncludeArchive" {
		t.Fatalf("got %q", got)
	}
	w.handleTimeline(apexapi.Plumb{})
	if got := w.command; got != "IncludeArchive Timeline" {
		t.Errorf("got %q", got)
	}
	w.show(w.showing().searching("chrysalis"))
	if got := w.command; got != "IncludeArchive Search chrysalis" {
		t.Errorf("got %q", got)
	}
	// And it is a toggle: asked again, the archive is put away again.
	w.handleIncludeArchive(apexapi.Plumb{})
	if got := w.command; got != "Search chrysalis" {
		t.Errorf("got %q", got)
	}
}
