package main

// The window: one note at a time, written to its file as it is typed, the
// notes walked with Prev and Next. The window's label says where it is
// among them and what its keyword makes of it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	apexapi "github.com/mariusae/apex/go/apex"
)

const (
	// settle is how long typing pauses before the note is saved and its
	// keyword does its work.
	settle = 300 * time.Millisecond
	// defaultExpire is how long a note untouched stays: anti.expire says
	// otherwise, 0 for ever.
	defaultExpire = 14 * 24 * time.Hour
)

// The verbs in the window's tag; Check and Restore are in its tools menu.
var tagVerbs = []string{"Prev", "Next", "New", "Void", "Export"}

type pad struct {
	t *apexapi.Tool
	w *apexapi.Window
	s store

	mu    sync.Mutex
	cur   string         // the note shown
	shown string         // its text as last read or written
	seen  map[string]int // its finished commands, so each is done once
	label string         // the label as last set
	timer *timer
	paste *paster

	// edits counts what others type, so that a change worked out from
	// one reading is not made over newer typing
	edits   atomic.Int64
	changed chan struct{}
	gone    chan struct{}
}

// serve shows the newest note in w (made or adopted) and keeps it until
// the window goes or ctx is done.
func serve(ctx context.Context, t *apexapi.Tool, w *apexapi.Window, s store, fresh bool) error {
	p := &pad{t: t, w: w, s: s, changed: make(chan struct{}, 1), gone: make(chan struct{})}
	if err := w.SetOwner(true); err != nil {
		return err
	}
	if err := w.SetLive(true); err != nil {
		return err
	}
	if err := w.SetTag(strings.Join(tagVerbs, " ")); err != nil {
		return err
	}
	if err := p.offer(); err != nil {
		return err
	}
	p.expire(time.Now())
	notes, err := s.notes()
	if err != nil {
		return err
	}
	path := ""
	if len(notes) > 0 && !fresh {
		path = notes[len(notes)-1]
	} else if path, err = s.fresh(); err != nil {
		return err
	}
	p.mu.Lock()
	err = p.load(path)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	if err := w.Watch(func(apexapi.Edit) { p.edits.Add(1); p.poke() }); err != nil {
		return err
	}
	var once sync.Once
	t.OnDelete(func(x *apexapi.Window) {
		if x.ID == w.ID {
			once.Do(func() { close(p.gone) })
		}
	})

	serving := make(chan error, 1)
	go func() { serving <- t.Serve(ctx) }()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	var due <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			p.mu.Lock()
			p.flush()
			p.mu.Unlock()
			return nil
		case <-p.gone:
			p.mu.Lock()
			if strings.TrimSpace(p.shown) == "" {
				_ = os.Remove(p.cur)
			}
			p.mu.Unlock()
			return nil
		case err := <-serving:
			if err == nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		case <-p.changed:
			due = time.After(settle)
		case <-due:
			due = nil
			p.update()
		case now := <-tick.C:
			p.tick(now)
		case now := <-hourly.C:
			p.expire(now)
		}
	}
}

// poke says the note has changed; the work waits for typing to settle.
func (p *pad) poke() {
	select {
	case p.changed <- struct{}{}:
	default:
	}
}

// offer installs the verbs, and B3 on a checkbox to tick it.
func (p *pad) offer() error {
	verbs := map[string]func(apexapi.Plumb) bool{
		"Prev":    p.prev,
		"Next":    p.next,
		"New":     p.newNote,
		"Front":   p.toFront,
		"Promote": p.promote,
		"Void":    p.void,
		"Restore": p.restore,
		"Export":  p.export,
		"Check":   p.check,
	}
	for _, v := range []string{"Prev", "Next", "New", "Front", "Promote", "Void", "Restore", "Export", "Check"} {
		if _, err := p.t.Offer(apexapi.Rule{Verb: v, Window: p.w, Priority: 100}, verbs[v]); err != nil {
			return err
		}
	}
	_, err := p.t.Offer(apexapi.Rule{Text: `(?s).*`, Window: p.w, Priority: 100}, p.tickBox)
	return err
}

// ---- notes in and out ---------------------------------------------------------

// load shows the note at path.
func (p *pad) load(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(b)
	if err := p.w.Replace(0, apexapi.End, text); err != nil {
		return err
	}
	if err := p.w.Rename(path); err != nil {
		return err
	}
	p.cur, p.shown, p.seen = path, text, commands(text)
	_ = p.w.Select(0, 0)
	_ = p.w.Show(0)
	p.relabel(time.Now())
	return nil
}

// flush writes what the window holds to the note's file.
func (p *pad) flush() string {
	text, err := p.w.Read()
	if err != nil {
		return p.shown
	}
	p.save(text)
	return text
}

func (p *pad) save(text string) {
	if text == p.shown {
		return
	}
	if err := os.WriteFile(p.cur, []byte(text), 0o600); err != nil {
		p.say("%s: %v", p.cur, err)
		return
	}
	p.shown = text
}

// leave is done before another note is shown: what was typed is kept, and
// a note with nothing in it goes.
func (p *pad) leave() {
	if strings.TrimSpace(p.flush()) == "" {
		_ = os.Remove(p.cur)
	}
}

// update saves the note and does what its keyword and commands ask.
func (p *pad) update() {
	p.mu.Lock()
	defer p.mu.Unlock()
	before := p.edits.Load()
	text, err := p.w.Read()
	if err != nil {
		return
	}
	cursor := -1
	if q0, q1, err := p.w.Selection(); err == nil && q0 == q1 {
		cursor = q0
	}
	edits, out, at := plan(text, cursor, p.setting("anti.checked", "stay"))
	if len(edits) > 0 {
		if p.edits.Load() != before {
			// typed meanwhile: worked out again once it settles
			p.poke()
			return
		}
		for _, e := range edits {
			if err := p.w.Replace(e.q0, e.q1, e.text); err != nil {
				return
			}
		}
		if at >= 0 {
			_ = p.w.Select(at, at)
		}
		text = out
	}
	p.save(text)
	for _, c := range newCommands(p.seen, text) {
		p.command(c)
	}
	p.seen = commands(text)
	p.relabel(time.Now())
}

// command does a finished "timer" or "paste" line.
func (p *pad) command(line string) {
	if strings.HasPrefix(strings.ToLower(line), "paste") {
		if p.paste != nil {
			p.paste = nil
			return
		}
		clip, err := clipboard()
		if err != nil {
			p.say("paste: %v", err)
			return
		}
		p.paste = &paster{delim: pasteCommand(line), last: clip}
		return
	}
	t, err := timerCommand(p.timer, line, time.Now())
	if err != nil {
		p.say("%v", err)
		return
	}
	p.timer = t
}

// tick moves the timer on and pastes what was copied.
func (p *pad) tick(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		if over, done := p.timer.tick(now); over != "" {
			_ = p.w.Notify()
			p.say("%s", over)
			if done {
				p.timer = nil
			}
		}
	}
	if p.paste != nil {
		if clip, err := clipboard(); err == nil && clip != p.paste.last && strings.TrimSpace(clip) != "" {
			p.paste.last = clip
			text := p.flush()
			if err := p.w.Append(p.paste.addition(text, clip)); err == nil {
				p.paste.n++
				p.flush()
			}
		}
	}
	p.relabel(now)
}

// relabel says where the note is among them, and what is going on.
func (p *pad) relabel(now time.Time) {
	parts := []string{"anti"}
	if notes, err := p.s.notes(); err == nil {
		for i, n := range notes {
			if n == p.cur {
				parts[0] = fmt.Sprintf("anti %d/%d", i+1, len(notes))
			}
		}
	}
	lines := strings.Split(p.shown, "\n")
	if kw, _ := keywordOf(lines[0]); kw != "" {
		parts = append(parts, summary(kw, lines))
	}
	if p.timer != nil {
		parts = append(parts, p.timer.show(now))
	}
	if p.paste != nil {
		parts = append(parts, "pasting")
	}
	label := strings.Join(parts, "  ")
	if label != p.label {
		if p.w.SetLabel(label) == nil {
			p.label = label
		}
	}
}

// expire throws away the notes left alone longer than anti.expire.
func (p *pad) expire(now time.Time) {
	age, err := parseAge(p.setting("anti.expire", ""))
	if err != nil {
		p.say("anti.expire: %v", err)
		return
	}
	if age == 0 {
		return
	}
	p.mu.Lock()
	keep := p.cur
	p.mu.Unlock()
	if _, err := p.s.expire(age, keep, now); err != nil {
		p.say("expiring: %v", err)
	}
}

// parseAge reads a duration that may be in days: "14d", "36h", "0".
func parseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return defaultExpire, nil
	case s == "0":
		return 0, nil
	case strings.HasSuffix(s, "d"):
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q: a number of days (14d), or of hours (36h), or 0", s)
		}
		return time.Duration(n * 24 * float64(time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q: a number of days (14d), or of hours (36h), or 0", s)
	}
	return d, nil
}

func (p *pad) setting(key, def string) string {
	if v, ok, err := p.t.Setting(key); err == nil && ok {
		return v
	}
	return def
}

// say puts a line in the errors window of anti's directory.
func (p *pad) say(format string, args ...any) {
	_ = p.t.Errors(p.s.dir, "anti: "+fmt.Sprintf(format, args...)+"\n")
}

// ---- the verbs ------------------------------------------------------------------

// walk shows the note `by` away from this one (-1 the one before), or
// with by 0 the front; past the front is a new note.
func (p *pad) walk(by int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	notes, err := p.s.notes()
	if err != nil {
		p.say("%v", err)
		return true
	}
	i := len(notes) - 1
	for k, n := range notes {
		if n == p.cur {
			i = k
		}
	}
	target := ""
	switch {
	case by == 0 && len(notes) > 0:
		target = notes[len(notes)-1]
	case by == 0:
	case i+by < 0:
		return true
	case i+by < len(notes):
		target = notes[i+by]
	}
	if target == p.cur {
		return true
	}
	if target == "" && strings.TrimSpace(p.flush()) == "" {
		// already at a new note
		return true
	}
	p.leave()
	if target == "" {
		if target, err = p.s.fresh(); err != nil {
			p.say("%v", err)
			return true
		}
	}
	if err := p.load(target); err != nil {
		p.say("%v", err)
	}
	return true
}

func (p *pad) prev(apexapi.Plumb) bool    { return p.walk(-1) }
func (p *pad) next(apexapi.Plumb) bool    { return p.walk(+1) }
func (p *pad) toFront(apexapi.Plumb) bool { return p.walk(0) }

func (p *pad) newNote(apexapi.Plumb) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	notes, _ := p.s.notes()
	if len(notes) > 0 && notes[len(notes)-1] == p.cur && strings.TrimSpace(p.flush()) == "" {
		return true
	}
	p.leave()
	path, err := p.s.fresh()
	if err == nil {
		err = p.load(path)
	}
	if err != nil {
		p.say("%v", err)
	}
	return true
}

func (p *pad) promote(apexapi.Plumb) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flush()
	path, err := p.s.promote(p.cur)
	if err != nil {
		p.say("%v", err)
		return true
	}
	p.cur = path
	_ = p.w.Rename(path)
	p.relabel(time.Now())
	return true
}

// void throws the note away, and shows the one before it, or after it.
func (p *pad) void(apexapi.Plumb) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flush()
	notes, _ := p.s.notes()
	target := ""
	for k, n := range notes {
		if n != p.cur {
			continue
		}
		if k > 0 {
			target = notes[k-1]
		} else if k+1 < len(notes) {
			target = notes[k+1]
		}
	}
	if err := p.s.throw(p.cur, time.Now()); err != nil {
		p.say("%v", err)
		return true
	}
	var err error
	if target == "" {
		target, err = p.s.fresh()
	}
	if err == nil {
		err = p.load(target)
	}
	if err != nil {
		p.say("%v", err)
	}
	return true
}

func (p *pad) restore(apexapi.Plumb) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.leave()
	path, err := p.s.restore()
	if err != nil {
		p.say("Restore: %v", err)
		path = p.cur
		if _, err := os.Stat(path); err != nil {
			path, err = p.s.fresh()
			if err != nil {
				return true
			}
		}
	}
	if err := p.load(path); err != nil {
		p.say("%v", err)
	}
	return true
}

// export writes the note out for keeping, as FILE, into DIR, or into
// anti.export (~/Documents unless it says), named by its title, and opens
// what it wrote.
func (p *pad) export(pl apexapi.Plumb) bool {
	p.mu.Lock()
	text := p.flush()
	cur := p.cur
	p.mu.Unlock()
	home, _ := os.UserHomeDir()
	dest := strings.TrimSpace(pl.Text)
	if dest == "" {
		dest = p.setting("anti.export", filepath.Join(home, "Documents"))
	}
	path, err := exportNote(text, cur, dest, home)
	if err != nil {
		p.say("Export: %v", err)
		return true
	}
	if _, err := p.t.Open(path, 0); err != nil {
		p.say("Export: %v", err)
	}
	return true
}

// exportNote writes a note's text, its keyword line left out, to dest: a
// file, or a directory to put it in under its title.
func exportNote(text, cur, dest, home string) (string, error) {
	if dest == "~" || strings.HasPrefix(dest, "~/") {
		dest = filepath.Join(home, dest[1:])
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(home, dest)
	}
	lines := strings.Split(text, "\n")
	title := noteTitle(lines)
	if kw, _ := keywordOf(lines[0]); kw != "" {
		lines = lines[1:]
	}
	body := strings.TrimLeft(strings.Join(lines, "\n"), "\n")
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	path := dest
	if fi, err := os.Stat(dest); err == nil && fi.IsDir() || strings.HasSuffix(dest, "/") {
		name := fileName(title)
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(cur), ".txt")
		}
		path = filepath.Join(dest, name+".txt")
		for k := 2; ; k++ {
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				break
			}
			path = filepath.Join(dest, fmt.Sprintf("%s %d.txt", name, k))
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte(body), 0o644)
}

// fileName makes a title a file's name.
func fileName(title string) string {
	r := strings.NewReplacer("/", "-", ":", "-", "\\", "-", "\x00", "")
	name := strings.TrimSpace(r.Replace(title))
	name = strings.TrimLeft(name, ".")
	if rs := []rune(name); len(rs) > 80 {
		name = strings.TrimSpace(string(rs[:80]))
	}
	return name
}

// check ticks the item the cursor is on, or unticks it.
func (p *pad) check(pl apexapi.Plumb) bool {
	q0, _, ok := pl.Range()
	if pl.At != nil {
		q0, ok = pl.At.Q0, true
	}
	if !ok {
		return true
	}
	p.tickAt(q0, false)
	return true
}

// tickBox is B3 in the window: on a checkbox it ticks it; anywhere else it
// is Look, as ever.
func (p *pad) tickBox(pl apexapi.Plumb) bool {
	if pl.At == nil {
		return false
	}
	return p.tickAt(pl.At.Q0, true)
}

// tickAt ticks the checkbox of the line at q0, or with onBox only when q0
// is on the box itself. Then a ticked item stays, goes to the bottom, or
// goes, as anti.checked says.
func (p *pad) tickAt(q0 int, onBox bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	text := p.flush()
	lines := strings.Split(text, "\n")
	l, c := lineCol(lines, q0)
	if l < 0 {
		return false
	}
	m := boxRE.FindStringSubmatchIndex(lines[l])
	if m == nil {
		if onBox {
			return false
		}
		if kw, _ := keywordOf(lines[0]); kw != "list" || l == 0 || strings.TrimSpace(lines[l]) == "" {
			return true
		}
		lines[l] = boxed(lines[l])
		m = boxRE.FindStringSubmatchIndex(lines[l])
	}
	if onBox {
		// the box: from its "[" to its "]", by characters
		open := len([]rune(lines[l][:strings.Index(lines[l], "[")]))
		close := len([]rune(lines[l][:strings.Index(lines[l], "]")]))
		if c < open || c > close {
			return false
		}
	}
	lines[l] = toggle(lines[l])
	start := offsetOf(lines, l)
	switch policy := p.setting("anti.checked", "stay"); {
	case policy != "stay" && isChecked(lines[l]):
		item := lines[l]
		lines = append(lines[:l], lines[l+1:]...)
		if policy == "bottom" {
			end := len(lines)
			for end > 1 && strings.TrimSpace(lines[end-1]) == "" {
				end--
			}
			tail := append([]string{}, lines[end:]...)
			lines = append(append(lines[:end], item), tail...)
		}
		out := strings.Join(lines, "\n")
		if p.w.Replace(0, apexapi.End, out) == nil {
			_ = p.w.Select(start, start)
		}
	default:
		old := strings.Split(text, "\n")[l]
		_ = p.w.Replace(start, start+len([]rune(old)), lines[l])
	}
	p.flush()
	p.relabel(time.Now())
	return true
}
