package main

// The Apex windows.
//
// The main window is the graph's notes, most recently changed first, a page at
// a time. What it is showing is written in its own first line, not held aside
// in this program: Get re-reads that line and does what it says, so a reader
// can edit the window and ask for the result. Backlinks open windows of their
// own, named for the note they are about, and are thrown away by deleting them.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	apexapi "github.com/mariusae/apex/go/apex"
)

const (
	windowPoll = 2 * time.Second
	// syncEvery is how often the graph is synced while the window is open.
	// Notes are written by hand, so the remote is never far behind.
	syncEvery = 5 * time.Minute
	// pageSize is how many notes a window shows at a time. The rest are a
	// Look away, on the `more` row the window ends with.
	pageSize = 10
)

// The views the main window answers to. Its first line names the one it is
// showing, and that line is the whole of the window's state.
const (
	searchVerb   = "Search"
	timelineVerb = "Timeline"
)

var viewVerbs = []string{searchVerb, timelineVerb}

// A row is what one line of a window's body stands for: a note and the line of
// it the row came from, or the `more` row that shows the next page.
type row struct {
	path string
	line int
	more bool
}

// A view is a rendered body and the notes its lines point at.
type view struct {
	text string
	rows []row
}

// A pane is one window this tool wrote: its text, what that text points at,
// and how much of the answer has been asked for.
type pane struct {
	window *apexapi.Window
	// subject is the note a backlinks pane is about, as the graph settled it;
	// asked is how the reader spelled it. Both are empty in the main pane.
	// named says the window has since been called after the note it found.
	subject string
	asked   string
	named   bool
	shown   int
	view    view
	// written is the body as this tool last wrote it. A body that has since
	// been edited is the reader's, and is not overwritten under their hands.
	written string
}

type reflWindow struct {
	graph *graph
	tool  *apexapi.Tool
	now   func() time.Time

	mu   sync.Mutex
	main *pane
	// command is the main window's first line: the view it is showing.
	command string
	panes   map[int]*pane

	refresh chan struct{}
	syncing chan struct{}
}

// openWindow shows the graph in the session until the window is deleted,
// syncing the graph as it goes.
func openWindow(g *graph, query string, getenv func(string) string, now func() time.Time) error {
	tool, err := attachApex(fmt.Sprintf("refl-%d", os.Getpid()), getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = tool.Close()
	}()

	w := &reflWindow{
		graph:   g,
		tool:    tool,
		now:     now,
		command: openingCommand(query),
		panes:   map[int]*pane{},
		refresh: make(chan struct{}, 1),
		syncing: make(chan struct{}, 1),
	}

	// Render while the UI creates the window and marks it live: the render is
	// local work, so this hides it behind those round trips.
	rendered := make(chan view, 1)
	go func() { rendered <- w.render() }()

	window, err := tool.New(windowName(g.root))
	if err != nil {
		return err
	}
	if err := window.SetLive(true); err != nil {
		return err
	}
	w.main = &pane{window: window, shown: pageSize}
	w.panes[window.ID] = w.main
	if err := w.offer(window); err != nil {
		return err
	}
	tool.OnDelete(w.forget)

	serving := make(chan error, 1)
	go func() { serving <- tool.Serve(ctx) }()

	if err := w.write(w.main, <-rendered, true); err != nil {
		return err
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(signals)
	ticker := time.NewTicker(windowPoll)
	defer ticker.Stop()
	syncs := time.NewTicker(syncEvery)
	defer syncs.Stop()

	for {
		asked := false
		select {
		case <-signals:
			return nil
		case err := <-serving:
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("serving the Apex session: %w", err)
		case <-w.syncing:
			w.sync(true)
		case <-syncs.C:
			w.sync(false)
		case <-w.refresh:
			asked = true
		case <-ticker.C:
			open, err := windowOpen(tool, window.ID)
			if err != nil {
				return err
			}
			if !open {
				return nil
			}
		}
		if err := w.update(asked); err != nil {
			return err
		}
	}
}

// openingCommand is the view -a opens on: a search, when a query was given.
func openingCommand(query string) string {
	if query = strings.TrimSpace(query); query == "" {
		return ""
	}
	return searchVerb + " " + query
}

func windowName(root string) string {
	return filepath.Join(root, "-refl")
}

// offer installs the main window's verbs, the plumbing that opens a note from
// a row, and the note-wide verbs the graph's own notes answer to.
func (w *reflWindow) offer(window *apexapi.Window) error {
	rules := []struct {
		rule   apexapi.Rule
		handle func(apexapi.Plumb) bool
	}{
		{apexapi.Rule{Verb: searchVerb, Window: window, Priority: 100}, w.handleSearch},
		{apexapi.Rule{Verb: timelineVerb, Window: window, Priority: 100}, w.handleTimeline},
		{apexapi.Rule{Verb: "Get", Window: window, Priority: 100}, w.handleGet},
		{apexapi.Rule{Verb: "Today", Window: window, Priority: 100}, w.handleToday},
		{apexapi.Rule{Verb: "Sync", Window: window, Priority: 100}, w.handleSync},
		{apexapi.Rule{Window: window, Text: `^.+$`, Priority: 100}, w.handleLook},
		{apexapi.Rule{Verb: "Today", File: w.dailyPattern(), Priority: 100}, w.handleToday},
		{apexapi.Rule{Verb: "Yesterday", File: w.dailyPattern(), Priority: 100}, w.handleStep(-1)},
		{apexapi.Rule{Verb: "Tomorrow", File: w.dailyPattern(), Priority: 100}, w.handleStep(1)},
		// Note and Backlinks belong wherever the notes are: the window, and
		// every note of the graph open in the session. The verb is Note, not
		// New: New is one of Apex's own commands, and those take every B2
		// before a tool's verbs are tried, so no tool can answer to that word.
		{apexapi.Rule{Verb: "Note", Window: window, Priority: 100}, w.handleNote},
		{apexapi.Rule{Verb: "Note", File: w.notePattern(), Priority: 100}, w.handleNote},
		{apexapi.Rule{Verb: "Backlinks", Window: window, Priority: 100}, w.handleBacklinks},
		{apexapi.Rule{Verb: "Backlinks", File: w.notePattern(), Priority: 100}, w.handleBacklinks},
		{apexapi.Rule{Verb: "Sync", File: w.notePattern(), Priority: 100}, w.handleSync},
	}
	for _, offered := range rules {
		if _, err := w.tool.Offer(offered.rule, offered.handle); err != nil {
			return err
		}
	}
	return nil
}

// dailyPattern matches the graph's daily notes by name, which is where
// Yesterday, Today and Tomorrow belong.
func (w *reflWindow) dailyPattern() string {
	return regexp.QuoteMeta(filepath.Join(w.graph.root, dailyDir)+string(filepath.Separator)) +
		`\d{4}-\d{2}-\d{2}\.md`
}

// notePattern matches any note of the graph by name.
func (w *reflWindow) notePattern() string {
	return regexp.QuoteMeta(w.graph.root+string(filepath.Separator)) + `.*\.md`
}

func (w *reflWindow) handleSearch(plumb apexapi.Plumb) bool {
	w.show(openingCommand(plumb.Text))
	return true
}

// handleTimeline puts the window on the graph's recent changes, as refl -t
// prints them.
func (w *reflWindow) handleTimeline(apexapi.Plumb) bool {
	w.show(timelineVerb)
	return true
}

// handleGet re-reads the graph, taking the window's own first line as what to
// show. The line is the state: edit it to `Search whatever` or `Timeline` and
// Get runs that, clear it and Get brings the whole graph back.
func (w *reflWindow) handleGet(apexapi.Plumb) bool {
	text, err := w.main.window.Read()
	if err != nil {
		return false
	}
	w.show(commandOfBody(text))
	return true
}

// commandOfBody reads what a main-window body says it is showing: its first
// line, when that line names a view.
func commandOfBody(text string) string {
	first, _, _ := strings.Cut(text, "\n")
	if verb, args := splitCommand(first); verb != "" {
		if args == "" {
			return verb
		}
		return verb + " " + args
	}
	return ""
}

// splitCommand names the view a line asks for, and its argument. A line that
// names no view is the whole graph, which is also what a note row is — a note
// whose name begins with one of these words is the one place this misreads,
// and Search brings it back.
func splitCommand(line string) (verb, args string) {
	line = strings.TrimSpace(line)
	for _, known := range viewVerbs {
		rest, ok := strings.CutPrefix(line, known)
		if !ok {
			continue
		}
		if rest == "" {
			return known, ""
		}
		if trimmed := strings.TrimLeft(rest, " \t"); len(trimmed) < len(rest) {
			return known, trimmed
		}
	}
	return "", ""
}

// show puts the main window on a view, from its first page.
func (w *reflWindow) show(command string) {
	w.mu.Lock()
	w.command, w.main.shown = command, pageSize
	w.mu.Unlock()
	w.wake()
}

func (w *reflWindow) handleToday(apexapi.Plumb) bool {
	_, err := w.tool.Open(w.graph.dailyPath(w.now()), 0)
	return err == nil
}

// handleStep moves from the daily note a window holds to the one days away.
func (w *reflWindow) handleStep(days int) func(apexapi.Plumb) bool {
	return func(plumb apexapi.Plumb) bool {
		if plumb.Window == nil {
			return false
		}
		name, ok := windowNameOf(w.tool, plumb.Window.ID)
		if !ok {
			return false
		}
		day, err := w.graph.dailyDay(name)
		if err != nil {
			return false
		}
		_, err = w.tool.Open(w.graph.dailyPath(day.AddDate(0, 0, days)), 0)
		return err == nil
	}
}

// handleSync asks for a sync now; the answer lands in +Errors, since a sync
// that changed nothing has nothing to say.
func (w *reflWindow) handleSync(apexapi.Plumb) bool {
	select {
	case w.syncing <- struct{}{}:
	default:
	}
	return true
}

// handleNote writes a note for the title it is given and opens it. The title
// may follow the verb or be swept in a window, so a phrase already written can
// become the note it names.
func (w *reflWindow) handleNote(plumb apexapi.Plumb) bool {
	title := strings.TrimSpace(plumb.Text)
	if title == "" {
		title = w.selectedText(plumb)
	}
	path, err := w.graph.createNote(title, w.now())
	if err != nil {
		_ = w.tool.Errors(w.graph.root, "Note: "+err.Error()+"\n")
		return true
	}
	if _, err := w.tool.Open(path, 0); err != nil {
		return false
	}
	w.show("")
	return true
}

// handleBacklinks opens a window of its own on what links to a note: the one
// named as the verb's argument, the one the window holds, or the one under the
// pointer. It is a throwaway — Del is how it goes.
func (w *reflWindow) handleBacklinks(plumb apexapi.Plumb) bool {
	ref, ok := w.backlinkRef(plumb)
	if !ok {
		return false
	}
	if w.showing(ref) {
		return true // one window per note is enough
	}
	window, err := w.tool.New(backlinksName(w.graph, ref))
	if err != nil {
		return false
	}
	if err := window.SetLive(true); err != nil {
		return false
	}
	if _, err := w.tool.Offer(
		apexapi.Rule{Window: window, Text: `^.+$`, Priority: 100}, w.handleLook,
	); err != nil {
		return false
	}
	w.mu.Lock()
	w.panes[window.ID] = &pane{window: window, subject: ref, asked: ref, shown: pageSize}
	w.mu.Unlock()
	// The answer is read off the whole graph, which is more than a handler
	// has a second for; the loop writes it.
	w.wake()
	return true
}

// showing refreshes the pane already open on a note, if there is one. A note
// asked for under two spellings gets two windows, which is a fair price for
// not resolving the whole graph inside a handler's second.
func (w *reflWindow) showing(ref string) bool {
	w.mu.Lock()
	found := false
	for _, open := range w.panes {
		if open.subject == "" && open.asked == "" {
			continue // the main pane
		}
		if open.subject == ref || open.asked == ref {
			open.shown = pageSize
			found = true
		}
	}
	w.mu.Unlock()
	if found {
		w.wake()
	}
	return found
}

// backlinksName names a backlinks window after the note it is about, so the
// window's own name says what it holds.
func backlinksName(g *graph, ref string) string {
	if rel, ok := g.relative(filepath.Join(g.root, filepath.FromSlash(ref))); ok {
		return filepath.Join(g.root, filepath.FromSlash(rel)) + "+Backlinks"
	}
	return filepath.Join(g.root, ref) + "+Backlinks"
}

func (w *reflWindow) backlinkRef(plumb apexapi.Plumb) (string, bool) {
	if named := strings.TrimSpace(plumb.Text); named != "" {
		return named, true
	}
	if plumb.Window == nil {
		return "", false
	}
	w.mu.Lock()
	showing, known := w.panes[plumb.Window.ID]
	w.mu.Unlock()
	if known {
		at, ok := lookPoint(plumb, showing.window)
		if !ok {
			return "", false
		}
		target, found := rowAt(showing.view, at)
		if !found {
			return "", false
		}
		return w.graph.relative(target.path)
	}
	name, ok := windowNameOf(w.tool, plumb.Window.ID)
	if !ok {
		return "", false
	}
	return w.graph.relative(name)
}

// selectedText is what a window's dot holds, which is how Note reads a title
// swept in a note rather than typed after the verb.
func (w *reflWindow) selectedText(plumb apexapi.Plumb) string {
	if plumb.Window == nil {
		return ""
	}
	q0, q1, ok := plumb.Range()
	if !ok {
		return ""
	}
	text, err := plumb.Window.Read()
	if err != nil {
		return ""
	}
	runes := []rune(text)
	if q0 < 0 || q1 > len(runes) || q0 >= q1 {
		return ""
	}
	return strings.TrimSpace(string(runes[q0:q1]))
}

// handleLook opens the note a row stands for, or shows the next page on the
// `more` row. Rows that stand for nothing — the header, the blank lines
// between hits — are left to the session's own plumbing.
func (w *reflWindow) handleLook(plumb apexapi.Plumb) bool {
	if plumb.Window == nil {
		return false
	}
	w.mu.Lock()
	showing, known := w.panes[plumb.Window.ID]
	w.mu.Unlock()
	if !known {
		return false
	}
	at, ok := lookPoint(plumb, showing.window)
	if !ok {
		return false
	}
	target, ok := rowAt(showing.view, at)
	if !ok {
		return false
	}
	if target.more {
		w.mu.Lock()
		showing.shown += pageSize
		w.mu.Unlock()
		w.wake()
		return true
	}
	_, err := w.tool.Open(target.path, target.line)
	return err == nil
}

// forget drops a pane whose window the reader deleted.
func (w *reflWindow) forget(window *apexapi.Window) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.panes, window.ID)
}

// lookPoint is where a Look happened: what B3 took, else the pointer, else the
// window's dot — which is all a plumb raised from a script carries.
func lookPoint(plumb apexapi.Plumb, window *apexapi.Window) (int, bool) {
	if at, _, ok := plumb.Range(); ok {
		return at, true
	}
	if plumb.At != nil {
		return plumb.At.Q0, true
	}
	if window == nil {
		return 0, false
	}
	at, _, err := window.Selection()
	return at, err == nil
}

// rowAt is the row covering a character offset in the body.
func rowAt(rendered view, at int) (row, bool) {
	if at < 0 {
		return row{}, false
	}
	offset, line := 0, 0
	for _, text := range strings.Split(rendered.text, "\n") {
		if at <= offset+len([]rune(text)) {
			break
		}
		offset += len([]rune(text)) + 1
		line++
	}
	if line >= len(rendered.rows) {
		return row{}, false
	}
	if rendered.rows[line].path == "" && !rendered.rows[line].more {
		return row{}, false
	}
	return rendered.rows[line], true
}

func (w *reflWindow) wake() {
	select {
	case w.refresh <- struct{}{}:
	default:
	}
}

// sync brings the graph and its remote into step. A sync nobody asked for says
// nothing unless it moved something or failed; one that was asked for says so
// either way.
func (w *reflWindow) sync(asked bool) {
	report, err := w.graph.sync()
	switch {
	case err != nil:
		_ = w.tool.Errors(w.graph.root, "Sync: "+err.Error()+"\n")
	case asked && report.quiet():
		_ = w.tool.Errors(w.graph.root, "Sync: already in step\n")
	case !report.quiet():
		_ = w.tool.Errors(w.graph.root, "Sync: "+report.String()+"\n")
	}
}

// update re-renders every window this tool wrote and writes back only what
// changed, so a reader's selection and scroll position survive a refresh. A
// refresh someone asked for is written whatever the window now holds; one the
// clock asked for is not, so a body being edited is left alone.
func (w *reflWindow) update(asked bool) error {
	w.mu.Lock()
	panes := make([]*pane, 0, len(w.panes))
	for _, showing := range w.panes {
		panes = append(panes, showing)
	}
	w.mu.Unlock()
	for _, showing := range panes {
		if err := w.write(showing, w.renderPane(showing), asked); err != nil {
			return err
		}
	}
	return nil
}

// write puts a view in its window. A body the reader has edited is theirs: the
// clock does not overwrite it, and Get is how they ask for what they wrote to
// be run. The edit is figured against the window's own text rather than what
// this tool last wrote, so it is right however the two have drifted.
func (w *reflWindow) write(showing *pane, next view, asked bool) error {
	current, err := showing.window.Read()
	if err != nil {
		return nil // the window is going or gone; the poll will notice
	}
	w.mu.Lock()
	edited := showing.written != "" && current != showing.written
	w.mu.Unlock()
	if edited && !asked {
		return nil
	}
	if from, to, text, changed := minimalEdit(current, next.text); changed {
		if err := showing.window.Replace(from, to, text); err != nil {
			return err
		}
	}
	w.mu.Lock()
	showing.view, showing.written = next, next.text
	w.mu.Unlock()
	return nil
}

// minimalEdit is the one replacement that turns before into after: everything
// they share at each end stays untouched.
func minimalEdit(before, after string) (from, to int, text string, changed bool) {
	if before == after {
		return 0, 0, "", false
	}
	old, next := []rune(before), []rune(after)
	prefix := 0
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(next)-prefix &&
		old[len(old)-1-suffix] == next[len(next)-1-suffix] {
		suffix++
	}
	return prefix, len(old) - suffix, string(next[prefix : len(next)-suffix]), true
}

func windowOpen(tool *apexapi.Tool, id int) (bool, error) {
	windows, err := tool.Windows()
	if err != nil {
		return false, err
	}
	for _, candidate := range windows {
		if candidate.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func windowNameOf(tool *apexapi.Tool, id int) (string, bool) {
	windows, err := tool.Windows()
	if err != nil {
		return "", false
	}
	for _, candidate := range windows {
		if candidate.ID == id {
			return candidate.Name, true
		}
	}
	return "", false
}

// render is the main window's body: whichever view its first line names.
func (w *reflWindow) render() view {
	w.mu.Lock()
	command, shown := w.command, pageSize
	if w.main != nil {
		shown = w.main.shown
	}
	w.mu.Unlock()
	switch verb, args := splitCommand(command); verb {
	case searchVerb:
		if terms := queryTerms(args); len(terms) > 0 {
			return w.renderSearch(args, terms, shown)
		}
	case timelineVerb:
		return w.renderTimeline(shown)
	}
	return w.renderList(shown)
}

// renderTimeline is the graph's recent changes, as refl -t prints them.
func (w *reflWindow) renderTimeline(shown int) view {
	changes, err := w.graph.timeline(shown + 1)
	if err != nil {
		return failed(err)
	}
	page := &body{}
	page.header(timelineVerb)
	if len(changes) == 0 {
		page.line("nothing to show")
		return page.view()
	}
	now := w.now()
	for _, changed := range changes[:min(shown, len(changes))] {
		page.entry(changed.note, changed.blocks, now)
	}
	page.more(len(changes) > shown)
	return page.view()
}

func (w *reflWindow) renderPane(showing *pane) view {
	w.mu.Lock()
	subject, shown, named := showing.subject, showing.shown, showing.named
	w.mu.Unlock()
	if subject == "" {
		return w.render()
	}
	rendered, found := w.renderBacklinks(subject, shown)
	if found.rel == "" {
		return rendered
	}
	w.mu.Lock()
	showing.subject, showing.named = found.rel, true
	w.mu.Unlock()
	// The window was opened under whatever name the reader used; once the
	// graph has settled which note that is, it takes the note's own path.
	if !named && showing.window != nil {
		_ = showing.window.Rename(backlinksName(w.graph, found.rel))
	}
	return rendered
}

func (w *reflWindow) renderList(shown int) view {
	notes, err := w.graph.list()
	if err != nil {
		return failed(err)
	}
	page, now := &body{}, w.now()
	for _, subject := range notes[:min(shown, len(notes))] {
		page.note(subject, now)
	}
	page.more(len(notes) > shown)
	return page.view()
}

func (w *reflWindow) renderSearch(query string, terms []string, shown int) view {
	// One note past the page, so the window knows whether there is more
	// without searching out blocks nobody has asked to see.
	hits, err := w.graph.search(terms, true, bounds{notes: shown + 1})
	if err != nil {
		return failed(err)
	}
	page := &body{}
	page.header(searchVerb + " " + query)
	return w.hits(page, hits, shown)
}

func (w *reflWindow) renderBacklinks(subject string, shown int) (view, noteRef) {
	hits, found, err := w.graph.backlinks(subject, bounds{notes: shown + 1})
	if err != nil {
		return failed(err), noteRef{}
	}
	page := &body{}
	page.header("Backlinks " + displayTitle(found.title))
	return w.hits(page, hits, shown), found
}

// hits writes a page of matched notes, each with the blocks that matched.
func (w *reflWindow) hits(page *body, hits []hit, shown int) view {
	if len(hits) == 0 {
		page.line("nothing to show")
		return page.view()
	}
	now := w.now()
	for _, matched := range hits[:min(shown, len(hits))] {
		page.entry(matched.note, matched.blocks, now)
	}
	page.more(len(hits) > shown)
	return page.view()
}

// A body is a window's text as it is written, each line carrying what it
// stands for.
type body struct {
	out  strings.Builder
	rows []row
}

func (b *body) view() view { return view{text: b.out.String(), rows: b.rows} }

func (b *body) add(text string, at row) {
	b.out.WriteString(text)
	b.out.WriteByte('\n')
	b.rows = append(b.rows, at)
}

// line adds a line that stands for nothing.
func (b *body) line(text string) { b.add(text, row{}) }

// header says what the window is showing, phrased as the command that would
// show it again, so B2 on it re-runs the view.
func (b *body) header(text string) {
	b.line(text)
	b.line("")
}

// note adds a note's own row: its name, then when it last changed.
func (b *body) note(subject note, now time.Time) {
	b.add(displayTitle(subject.title)+" "+formatTime(subject.modTime, now),
		row{path: subject.path})
}

// entry is one note and the blocks shown under it, which is how a search, a
// timeline and a backlinks window all read.
func (b *body) entry(subject note, blocks []blockContext, now time.Time) {
	b.note(subject, now)
	for _, block := range blocks {
		for offset, line := range strings.Split(block.text, "\n") {
			b.add("\t"+line, row{path: subject.path, line: block.line + offset})
		}
	}
	b.line("")
}

// more ends the body with the row that shows the next page, when there is one.
// It says only that there is more, never how much: a view that stops reading
// one note past the page cannot know, and a number that is sometimes the truth
// is worse than none.
func (b *body) more(rest bool) {
	if rest {
		b.add("more", row{more: true})
	}
}

func failed(err error) view {
	return view{text: err.Error() + "\n", rows: []row{{}}}
}
