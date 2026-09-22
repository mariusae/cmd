package main

// The Apex windows.
//
// The main window is the drafts, most recently modified first, a page at a
// time. What it is showing is written in its own first line, not held aside in
// this program: Get re-reads that line and does what it says, so a reader can
// edit the window and ask for the result.
//
// Around it are the drafts themselves — ordinary file windows, which is the
// point — carrying the verbs a draft answers to: Notes, Search, New. Their Put
// is taken so that what was written is also kept. Preview they already have
// from apex, which previews a Markdown file better than this could.

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
	// pageSize is how many drafts a window shows at a time. The rest are a
	// Look away, on the `more` row the window ends with.
	pageSize = 10
	// syncEvery is how often the directory is synced while the window is open.
	// Drafts are written by hand, so the remote is never far behind.
	syncEvery = 5 * time.Minute
)

// The views the main window answers to. Its first line names the one it is
// showing, and that line is the whole of the window's state.
const (
	searchVerb   = "Search"
	timelineVerb = "Timeline"
)

var viewVerbs = []string{searchVerb, timelineVerb}

// The words Apex knows that drafts answers to in the directory's own files. A
// rule may take one so long as it says which windows it is about, and a
// handler that refuses hands the word back to Apex.
const (
	newVerb = "New"
	putVerb = "Put"
)

// syncVerb brings the directory and its remote into step now.
const syncVerb = "Sync"

// A row is what one line of a window's body stands for: a draft and the line
// of it the row came from, or the `more` row that shows the next page.
type row struct {
	path string
	line int
	more bool
}

// A view is a rendered body and the drafts its lines point at.
type view struct {
	text string
	rows []row
}

// A pane is the window this tool wrote: its text, what that text points at,
// and how much of the answer has been asked for.
type pane struct {
	window *apexapi.Window
	shown  int
	view   view
	// written is the body as this tool last wrote it. A body that has since
	// been edited is the reader's, and is not overwritten under their hands.
	written string
}

// An unnamed is a draft begun before it was named: the window it is being
// written in, and the rule by which that window's Put settles where it belongs.
//
// It is deliberately not claimed as one of drafts' own windows, though drafts
// made it. A draft on its way to being a file wants what any file window has —
// Undo and Redo while it is written, Put in the tag as soon as there is
// something to save, and Del asking before it throws away what was typed.
type unnamed struct {
	window *apexapi.Window
	rule   apexapi.RuleID
}

type draftsWindow struct {
	dir  *dir
	tool *apexapi.Tool
	now  func() time.Time
	// owner is the name drafts attached under, which is what its own window
	// is owned by and what a rule about it names.
	owner string

	mu   sync.Mutex
	main *pane
	// command is the main window's first line: the view it is showing.
	command string
	// begun are the drafts opened but not yet named, by window.
	begun map[int]*unnamed

	refresh chan struct{}
	// keeping asks for a commit, pushing for a push, syncing for a sync now.
	// All three are coalesced: what is wanted is that the writing ends up
	// recorded and sent, not that each Put has a commit and a push of its own
	// waiting on it.
	keeping chan struct{}
	pushing chan struct{}
	syncing chan struct{}

	// vcsMu is held by anything that writes to the repository, so a commit
	// never runs into a merge. It is not held over the refresh, which only
	// reads: a writer waiting on a sync waits in the background, never at the
	// keyboard.
	vcsMu sync.Mutex
}

// openWindow shows the drafts in the session until the window is deleted.
func openWindow(d *dir, query string, getenv func(string) string, now func() time.Time) error {
	owner := fmt.Sprintf("drafts-%d", os.Getpid())
	tool, err := attachApex(owner, getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = tool.Close()
	}()

	w := &draftsWindow{
		dir:     d,
		tool:    tool,
		now:     now,
		owner:   owner,
		command: openingCommand(query),
		begun:   map[int]*unnamed{},
		refresh: make(chan struct{}, 1),
		keeping: make(chan struct{}, 1),
		pushing: make(chan struct{}, 1),
		syncing: make(chan struct{}, 1),
	}

	// Render while the UI creates the window and marks it live: the render is
	// local work, so this hides it behind those round trips.
	rendered := make(chan view, 1)
	go func() { rendered <- w.render() }()

	window, err := tool.New(windowName(d.root))
	if err != nil {
		return err
	}
	if err := window.SetOwner(true); err != nil {
		return err
	}
	w.main = &pane{window: window, shown: pageSize}
	if err := w.offer(window); err != nil {
		return err
	}

	tool.OnDelete(w.forget)

	serving := make(chan error, 1)
	go func() { serving <- tool.Serve(ctx) }()
	// Keeping and sending are each their own thread of work: a commit must not
	// wait on the refresh loop, and a push — which talks to another machine and
	// may take as long as that machine likes — must not wait on the commit.
	go w.keeper(ctx)
	go w.pusher(ctx)
	go w.syncer(ctx)

	if err := w.write(w.main, <-rendered, true); err != nil {
		return err
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(signals)
	ticker := time.NewTicker(windowPoll)
	defer ticker.Stop()

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
	return filepath.Join(root, "-drafts")
}

// offer installs the main window's verbs, the plumbing that opens a draft from
// a row, and the verbs a draft of this directory answers to.
func (w *draftsWindow) offer(window *apexapi.Window) error {
	rules := []struct {
		rule   apexapi.Rule
		handle func(apexapi.Plumb) bool
	}{
		{apexapi.Rule{Verb: searchVerb, Window: window, Priority: 100}, w.handleSearch},
		{apexapi.Rule{Verb: timelineVerb, Window: window, Priority: 100}, w.handleTimeline},
		{apexapi.Rule{Verb: "Get", Window: window, Priority: 100}, w.handleGet},
		{apexapi.Rule{Verb: newVerb, Window: window, Priority: 100}, w.handleNew},
		{apexapi.Rule{Verb: syncVerb, Window: window, Priority: 100}, w.handleSync},
		// A row is a draft wherever drafts wrote one: the rule is about the
		// windows this drafts owns, so nothing has to guess at their names.
		{apexapi.Rule{Owner: w.ownPattern(), Text: `^.+$`, Priority: 100}, w.handleLook},
		// The draft verbs are about drafts: real files of this directory, not
		// a window some tool made and happened to name like one. Preview is
		// only wanted on the listing, where there is no file for apex's own
		// Preview to be about.
		{apexapi.Rule{Verb: "Preview", Window: window, Priority: 100}, w.handlePreview},
		{apexapi.Rule{Verb: "Notes", Window: window, Priority: 100}, w.handleNotes},
		{apexapi.Rule{Verb: "Notes", File: w.filePattern(), Owner: apexapi.NoOwner, Priority: 100}, w.handleNotes},
		{apexapi.Rule{Verb: searchVerb, File: w.filePattern(), Owner: apexapi.NoOwner, Priority: 100}, w.handleSearch},
		{apexapi.Rule{Verb: newVerb, File: w.filePattern(), Owner: apexapi.NoOwner, Priority: 100}, w.handleNew},
		{apexapi.Rule{Verb: syncVerb, File: w.filePattern(), Owner: apexapi.NoOwner, Priority: 100}, w.handleSync},
		// Put is Apex's own word, taken here for the directory's files so that
		// writing and keeping are the one act. Unlisted: apex puts Put in the
		// tag itself, and a second one in the tools menu would say there were
		// two of them.
		{apexapi.Rule{Verb: putVerb, File: w.filePattern(), Owner: apexapi.NoOwner, Unlisted: true, Priority: 100}, w.handlePut},
	}
	for _, offered := range rules {
		if _, err := w.tool.Offer(offered.rule, offered.handle); err != nil {
			return err
		}
	}
	return nil
}

// ownPattern matches the windows this drafts owns, by the name it attached
// under. Rules about them say so rather than guessing at their names.
func (w *draftsWindow) ownPattern() string { return regexp.QuoteMeta(w.owner) }

// filePattern matches the Markdown files of the drafts directory, and nothing
// below it: the directory is flat, and a preview window — `<draft>+Preview` —
// is not one of its files.
func (w *draftsWindow) filePattern() string {
	separator := string(filepath.Separator)
	return regexp.QuoteMeta(w.dir.root+separator) +
		`[^` + regexp.QuoteMeta(separator) + `]+\.md`
}

// ---- the views -----------------------------------------------------------

// handleSearch searches the directory: for the verb's argument, else for what
// is swept in the window it was run in — so a phrase already written can be
// looked for where else it was said.
func (w *draftsWindow) handleSearch(plumb apexapi.Plumb) bool {
	query := strings.TrimSpace(plumb.Text)
	if query == "" {
		query = w.selectedText(plumb)
	}
	w.show(openingCommand(query))
	return true
}

// handleTimeline puts the window on recent modifications, as drafts -t prints
// them.
func (w *draftsWindow) handleTimeline(apexapi.Plumb) bool {
	w.show(timelineVerb)
	return true
}

// handleGet re-reads the directory, taking the window's own first line as what
// to show. The line is the state: edit it to `Search whatever` or `Timeline`
// and Get runs that, clear it and Get brings every draft back.
func (w *draftsWindow) handleGet(apexapi.Plumb) bool {
	text, err := w.main.window.Read()
	if err != nil {
		return false
	}
	w.show(commandOfBody(text))
	return true
}

// commandOfBody reads what a body says it is showing: its first line, when
// that line names a view.
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
// names no view is the whole listing, which is also what a draft row is — a
// draft whose title begins with one of these words is the one place this
// misreads, and Search brings it back.
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
func (w *draftsWindow) show(command string) {
	w.mu.Lock()
	w.command, w.main.shown = command, pageSize
	w.mu.Unlock()
	w.wake()
}

// ---- the drafts ----------------------------------------------------------

// handleNew begins a draft. Nothing is written yet: a draft's filename comes
// from its title, and the title is not known until something has been written.
// So the draft is begun in a window of its own, and Put there is what names it.
//
// A title may follow the verb or be swept in a window, so a phrase already
// written can become the draft it names; it opens the window as the heading,
// where it is ordinary text that can be rewritten before it is ever a filename.
// With neither, the window opens empty and the first thing typed is the title.
//
// A draft thrown away — Del, and no Put — leaves nothing behind.
func (w *draftsWindow) handleNew(plumb apexapi.Plumb) bool {
	title := strings.TrimSpace(plumb.Text)
	if title == "" {
		title = w.selectedText(plumb)
	}
	window, err := w.tool.New(draftName(w.dir.root))
	if err != nil {
		return false
	}
	// Put is Apex's, and is taken for this one window: the tag offers it as
	// soon as anything is typed, and it means what putDraft says it means.
	pending := &unnamed{window: window}
	rule, err := w.tool.Offer(
		// Unlisted: apex puts Put in the tag itself, as it does for any window
		// with a file behind it, and a second one in the tools menu would say
		// there were two.
		apexapi.Rule{Verb: putVerb, Window: window, Unlisted: true, Priority: 100},
		func(plumb apexapi.Plumb) bool { return w.putDraft(pending, plumb) },
	)
	if err != nil {
		_ = window.Delete()
		return false
	}
	w.mu.Lock()
	pending.rule = rule
	w.begun[window.ID] = pending
	w.mu.Unlock()

	if text, at := opening(title); text != "" {
		// A heading the window will not take is no loss: the draft is open
		// either way, and what is typed in it still names it.
		if err := window.Replace(0, 0, text); err == nil {
			_ = window.Select(at, at)
		}
	}
	return true
}

// draftName is what a draft's window is called until it has a name of its own:
// the directory it will join, and the word that began it.
func draftName(root string) string { return root + "+" + newVerb }

// putDraft is a begun draft's own Put: what has been written names the draft,
// and the draft is written there. The window is then that file's window —
// said to be clean, since what it holds is now what is on disk, and called by
// the file's name, which is what puts the path in its tag. Drafts lets go of it
// at the same time: an ordinary draft window from then on, whose Put is the one
// every draft has, and is committed like any other.
//
// A Put that names a file is the writer saying where this goes, and is Apex's
// alone from the start.
func (w *draftsWindow) putDraft(pending *unnamed, plumb apexapi.Plumb) bool {
	if strings.TrimSpace(plumb.Text) != "" {
		w.release(pending)
		return false
	}
	if err := w.settle(pending); err != nil {
		// Taken and failed: nothing is written under the draft's own name, and
		// the draft is still a draft, to be Put again once the trouble is past.
		return w.complain(putVerb, err)
	}
	w.release(pending)
	// It is a file in the directory now, so it is recorded like one, and the
	// listing should say it is there.
	w.keep()
	w.wake()
	return true
}

// settle makes a begun draft's window the window of the file it holds. The
// name comes last on purpose: until the window has it nothing is watching the
// file, so what drafts wrote there is never taken for someone else's change
// and read back over the writer's hands. A name taken for a draft that then
// could not be settled is given back.
func (w *draftsWindow) settle(pending *unnamed) error {
	text, err := pending.window.Read()
	if err != nil {
		return err
	}
	path, err := w.dir.saveDraft(text)
	if err != nil {
		return err
	}
	if err = pending.window.SetClean(); err == nil {
		err = pending.window.Rename(path)
	}
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// release lets go of a begun draft: its rule is withdrawn, so Put in that
// window means what it means everywhere else.
func (w *draftsWindow) release(pending *unnamed) {
	w.mu.Lock()
	rule := pending.rule
	delete(w.begun, pending.window.ID)
	w.mu.Unlock()
	if rule != 0 {
		_ = w.tool.Withdraw(rule)
	}
}

// forget drops a draft thrown away before it was ever named.
func (w *draftsWindow) forget(window *apexapi.Window) {
	w.mu.Lock()
	pending := w.begun[window.ID]
	w.mu.Unlock()
	if pending != nil {
		w.release(pending)
	}
}

// handleNotes opens the notes beside a draft, making the file if it is not
// there yet. Notes about notes are the notes themselves, so Notes in a notes
// window is where it already is.
func (w *draftsWindow) handleNotes(plumb apexapi.Plumb) bool {
	path, ok := w.subject(plumb)
	if !ok {
		return false
	}
	notes, err := w.dir.createNotes(path, w.titleOf(path))
	if err != nil {
		return w.complain("Notes", err)
	}
	if _, err := w.tool.Open(notes, 0); err != nil {
		return false
	}
	// A notes file made now is a draft's companion, and the listing does not
	// show it; the directory has still moved, and the timeline has.
	w.wake()
	return true
}

// handlePreview shows the draft under the pointer as a page. Apex previews a
// file itself — live, against the buffer, unsaved edits and all — and offers
// the word in the tag of every Markdown window; what it cannot do is preview a
// draft that is only a row in a listing. So the draft is opened, and apex is
// asked for the preview it would have given anyway.
func (w *draftsWindow) handlePreview(plumb apexapi.Plumb) bool {
	path, ok := w.subject(plumb)
	if !ok {
		return false
	}
	window, err := w.tool.Open(path, 0)
	if err != nil {
		return false
	}
	return window.Exec("Preview") == nil
}

// handlePut is the word that keeps what was written. The file is written here,
// as apex would write it, and then recorded: a draft is kept by being written
// down and by being committed, and asking the writer to do the second is
// asking them to lose it.
//
// A Put that names a file is the writer saying where this goes, and is apex's
// alone; a window whose name is not a file of this directory is none of
// drafts' business either.
func (w *draftsWindow) handlePut(plumb apexapi.Plumb) bool {
	if strings.TrimSpace(plumb.Text) != "" || plumb.Window == nil {
		return false
	}
	path, ok := windowNameOf(w.tool, plumb.Window.ID)
	if !ok || !w.dir.contains(path) {
		return false
	}
	text, err := plumb.Window.Read()
	if err != nil {
		return false
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		// Taken and failed: the window is still dirty, and holds everything
		// that was written, to be Put again once the trouble is past.
		return w.complain(putVerb, err)
	}
	// What the window holds is now what is on disk, which is what clean means.
	_ = plumb.Window.SetClean()
	w.keep()
	w.wake()
	return true
}

// handleSync asks for a sync now. The answer lands in +Errors, since a sync
// that changed nothing has nothing to say.
func (w *draftsWindow) handleSync(apexapi.Plumb) bool {
	poke(w.syncing)
	return true
}

// subject is the draft a verb is about: the file the window holds, or the row
// under the pointer in the listing.
func (w *draftsWindow) subject(plumb apexapi.Plumb) (string, bool) {
	if plumb.Window == nil {
		return "", false
	}
	w.mu.Lock()
	main := w.main
	w.mu.Unlock()
	if main != nil && plumb.Window.ID == main.window.ID {
		at, ok := lookPoint(plumb, main.window)
		if !ok {
			return "", false
		}
		target, found := rowAt(main.view, at)
		if !found || target.path == "" {
			return "", false
		}
		return target.path, true
	}
	path, ok := windowNameOf(w.tool, plumb.Window.ID)
	if !ok || !w.dir.contains(path) {
		return "", false
	}
	return path, true
}

// titleOf names a draft for whatever is about to be written about it.
func (w *draftsWindow) titleOf(path string) string {
	content, err := os.ReadFile(draftOf(path))
	if err != nil {
		return strings.TrimSuffix(filepath.Base(draftOf(path)), ".md")
	}
	return deriveTitle(filepath.Base(draftOf(path)), string(content))
}

// selectedText is what a window's dot holds, which is how New and Search read
// a phrase swept in a draft rather than typed after the verb.
func (w *draftsWindow) selectedText(plumb apexapi.Plumb) string {
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

// handleLook opens the draft a row stands for, in a window of its own, or
// shows the next page on the `more` row. Rows that stand for nothing — the
// header, the blank lines between hits — are left to the session's own
// plumbing.
func (w *draftsWindow) handleLook(plumb apexapi.Plumb) bool {
	if plumb.Window == nil {
		return false
	}
	w.mu.Lock()
	main := w.main
	w.mu.Unlock()
	if main == nil || plumb.Window.ID != main.window.ID {
		return false
	}
	at, ok := lookPoint(plumb, main.window)
	if !ok {
		return false
	}
	target, ok := rowAt(main.view, at)
	if !ok {
		return false
	}
	if target.more {
		w.mu.Lock()
		main.shown += pageSize
		w.mu.Unlock()
		w.wake()
		return true
	}
	_, err := w.tool.Open(target.path, target.line)
	return err == nil
}

func (w *draftsWindow) complain(verb string, err error) bool {
	_ = w.tool.Errors(w.dir.root, verb+": "+err.Error()+"\n")
	return true
}

// wake asks the loop for a refresh, and keep asks for what has been written to
// be recorded. Both are asks, not queues: what is wanted is that the thing
// happens soon, not that it happens once per ask.
func (w *draftsWindow) wake() { poke(w.refresh) }
func (w *draftsWindow) keep() { poke(w.keeping) }

func poke(to chan struct{}) {
	select {
	case to <- struct{}{}:
	default:
	}
}

// ---- keeping -------------------------------------------------------------

// keeper records what has been written, once per ask and never two at a time.
// A directory under no version control has nothing to record, and says nothing
// about it: not every drafts directory is a repository, and one that is not is
// not failing at anything.
func (w *draftsWindow) keeper(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.keeping:
		}
		repo := w.dir.repository()
		if repo == nil {
			continue
		}
		w.vcsMu.Lock()
		committed, err := repo.commit()
		w.vcsMu.Unlock()
		switch {
		case err != nil:
			_ = w.tool.Errors(w.dir.root, fmt.Sprintf("Put: %s: %v\n", repo.name(), err))
		case committed > 0:
			// Something was recorded, so there is something to send.
			poke(w.pushing)
		}
	}
}

// pusher hands the history to wherever it belongs, behind the writer's back.
// It is its own thread of work because it talks to another machine: a Put
// waits for the disk, which is fast, and never for a network, which is not.
func (w *draftsWindow) pusher(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.pushing:
		}
		repo := w.dir.repository()
		if repo == nil {
			continue
		}
		w.vcsMu.Lock()
		err := repo.push()
		w.vcsMu.Unlock()
		if err != nil {
			_ = w.tool.Errors(w.dir.root, fmt.Sprintf("Push: %s: %v\n", repo.name(), err))
		}
	}
}

// syncer keeps the directory and its remote in step: every few minutes on its
// own, and at once when Sync asks. It is a thread of work of its own for the
// same reason the pusher is — it talks to another machine, and nothing at the
// keyboard should wait for that.
func (w *draftsWindow) syncer(ctx context.Context) {
	ticker := time.NewTicker(syncEvery)
	defer ticker.Stop()
	for {
		asked := false
		select {
		case <-ctx.Done():
			return
		case <-w.syncing:
			asked = true
		case <-ticker.C:
		}
		w.sync(asked)
	}
}

// sync brings the directory and its remote into step. A sync nobody asked for
// says nothing unless it moved something or failed; one that was asked for
// says so either way.
func (w *draftsWindow) sync(asked bool) {
	repo := w.dir.repository()
	if repo == nil {
		if asked {
			_ = w.tool.Errors(w.dir.root, "Sync: "+w.dir.root+" is under no version control\n")
		}
		return
	}
	w.vcsMu.Lock()
	report, err := repo.sync()
	w.vcsMu.Unlock()
	switch {
	case err != nil:
		_ = w.tool.Errors(w.dir.root, fmt.Sprintf("Sync: %s: %v\n", repo.name(), err))
	case asked && report.quiet():
		_ = w.tool.Errors(w.dir.root, "Sync: already in step\n")
	case !report.quiet():
		_ = w.tool.Errors(w.dir.root, "Sync: "+report.String()+"\n")
	}
	// A sync that took something down changed the directory, and the listing
	// should say so.
	if !report.quiet() {
		w.wake()
	}
}

// ---- writing the windows -------------------------------------------------

// update re-renders the listing, writing back only what changed, so a reader's
// selection and scroll position survive a refresh. A refresh someone asked for
// is written whatever the window now holds; one the clock asked for is not, so
// a body being edited is left alone.
func (w *draftsWindow) update(asked bool) error {
	return w.write(w.main, w.render(), asked)
}

// write puts a view in its window. A body the reader has edited is theirs: the
// clock does not overwrite it, and Get is how they ask for what they wrote to
// be run. The edit is figured against the window's own text rather than what
// this tool last wrote, so it is right however the two have drifted.
func (w *draftsWindow) write(showing *pane, next view, asked bool) error {
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
		// The writing was the point, so the window is not dirty for having
		// been written. What the reader types into it afterwards is theirs,
		// and says so.
		if err := showing.window.SetClean(); err != nil {
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

// ---- rendering -----------------------------------------------------------

// render is the main window's body: whichever view its first line names.
func (w *draftsWindow) render() view {
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

// renderList is every draft, most recently modified first.
func (w *draftsWindow) renderList(shown int) view {
	drafts, err := w.dir.list()
	if err != nil {
		return failed(err)
	}
	page, now := &body{}, w.now()
	if len(drafts) == 0 {
		page.line("no drafts in " + w.dir.root)
		return page.view()
	}
	for _, subject := range drafts[:min(shown, len(drafts))] {
		page.draft(subject, now)
	}
	page.more(len(drafts) > shown)
	return page.view()
}

func (w *draftsWindow) renderSearch(query string, terms []string, shown int) view {
	// One draft past the page, so the window knows whether there is more
	// without searching out blocks nobody has asked to see.
	hits, err := w.dir.search(terms, bounds{drafts: shown + 1})
	if err != nil {
		return failed(err)
	}
	page := &body{}
	page.header(searchVerb + " " + query)
	if len(hits) == 0 {
		page.line("nothing to show")
		return page.view()
	}
	now := w.now()
	for _, matched := range hits[:min(shown, len(hits))] {
		page.entry(matched.draft, matched.blocks, now)
	}
	page.more(len(hits) > shown)
	return page.view()
}

// renderTimeline is recent modifications, as drafts -t prints them.
func (w *draftsWindow) renderTimeline(shown int) view {
	changes, err := w.dir.timeline(shown + 1)
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
		page.entry(changed.draft, changed.blocks, now)
	}
	page.more(len(changes) > shown)
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

// draft adds a draft's own row: what it is called, then when it last moved.
func (b *body) draft(subject draft, now time.Time) {
	b.add(subject.title+" "+formatTime(subject.modTime, now), row{path: subject.path})
}

// entry is one draft and the blocks shown under it, which is how a search and
// a timeline both read.
func (b *body) entry(subject draft, blocks []blockContext, now time.Time) {
	b.draft(subject, now)
	for _, block := range blocks {
		for offset, line := range strings.Split(block.text, "\n") {
			b.add("\t"+line, row{path: subject.path, line: block.line + offset})
		}
	}
	b.line("")
}

// more ends the body with the row that shows the next page, when there is one.
// It says only that there is more, never how much: a view that stops reading
// one draft past the page cannot know, and a number that is sometimes the
// truth is worse than none.
func (b *body) more(rest bool) {
	if rest {
		b.add("more", row{more: true})
	}
}

func failed(err error) view {
	return view{text: err.Error() + "\n", rows: []row{{}}}
}
