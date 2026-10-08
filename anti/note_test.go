package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKeywords(t *testing.T) {
	for _, c := range []struct{ first, kw, title string }{
		{"math", "math", ""},
		{"  Math: Budget ", "math", "Budget"},
		{"average", "avg", ""},
		{"list: Groceries", "list", "Groceries"},
		{"mathematics", "", ""},
		{"shopping list", "", ""},
	} {
		kw, title := keywordOf(c.first)
		if kw != c.kw || title != c.title {
			t.Errorf("%q: %q %q, want %q %q", c.first, kw, title, c.kw, c.title)
		}
	}
}

func TestLists(t *testing.T) {
	in := []string{"list", "milk", "  eggs", "", "// not an item", "# Hardware", "[x] nails", "bread /x", "[x] glue /x", "- [] tape", "timer 5", "past", "pasta"}
	want := []string{"list", "[ ] milk", "  [ ] eggs", "", "// not an item", "# Hardware", "[x] nails", "[x] bread", "[ ] glue", "- [ ] tape", "timer 5", "past", "[ ] pasta"}
	if got := tidyLines(in, "list", "stay"); !reflect.DeepEqual(got, want) {
		t.Errorf("stay:\n%q\nwant\n%q", got, want)
	}
	in = []string{"list", "milk /x", "eggs", "bread", ""}
	if got, want := tidyLines(in, "list", "bottom"), []string{"list", "[ ] eggs", "[ ] bread", "[x] milk", ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("bottom: %q, want %q", got, want)
	}
	if got, want := tidyLines(in, "list", "delete"), []string{"list", "[ ] eggs", "[ ] bread", ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("delete: %q, want %q", got, want)
	}
	// in any note: boxes written the one way, and ticked by the trigger;
	// other lines left be
	in = []string{"notes", "[]call Ann", "[ ] post /x", "a path/x"}
	if got, want := tidyLines(in, "", "stay"), []string{"notes", "[ ] call Ann", "[x] post", "a path/x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("any note: %q, want %q", got, want)
	}
}

func TestSummaries(t *testing.T) {
	lines := []string{"sum", "coffee 3.50", "lunch $12", "// 100", "taxi 1,200.25"}
	if got := summary("sum", lines); got != "sum 1,215.75" {
		t.Error(got)
	}
	if got := summary("avg", lines); got != "avg 405.25 of 3" {
		t.Error(got)
	}
	if got := summary("count", []string{"count", "one two", "", "three"}); got != "2 items, 3 words, 12 chars" {
		t.Error(got)
	}
	if got := summary("list", []string{"list", "[x] a", "[ ] b", "[ ] c"}); got != "1/3 done" {
		t.Error(got)
	}
}

func TestCommands(t *testing.T) {
	before := commands("timer 5\nnotes\npaste")
	if !reflect.DeepEqual(before, map[string]int{"timer 5": 1}) {
		t.Errorf("unfinished paste counted: %v", before)
	}
	got := newCommands(before, "timer 5\nnotes\npaste\ntimer 5\n")
	if want := []string{"paste", "timer 5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
	if got := newCommands(commands("timer 5\n"), "timer 5\n"); got != nil {
		t.Errorf("done again: %q", got)
	}
	if d := pasteCommand("paste(, )"); d != ", " {
		t.Errorf("delimiter %q", d)
	}
}

func TestPlan(t *testing.T) {
	// the sum asked for: the answer after the cursor, and the cursor after it
	text := "math\n2 + 2 ="
	edits, out, at := plan(text, len([]rune(text)), "stay")
	if out != "math\n2 + 2 = 4" || at != len([]rune(out)) || len(edits) != 1 || edits[0] != (edit{12, 12, " 4"}) {
		t.Errorf("asked: %v %q %d", edits, out, at)
	}
	// typing after the = is the writer's: left alone
	text = "math\n2 + 2 = 4 and"
	if edits, _, _ := plan(text, len([]rune(text)), "stay"); len(edits) != 0 {
		t.Errorf("typing after the answer: %v", edits)
	}
	// editing the sum: the answer follows, the cursor stays in the sum
	text = "math\n2 + 3 = 4"
	edits, out, at = plan(text, 10, "stay")
	if out != "math\n2 + 3 = 5" || at != 10 || edits[0] != (edit{13, 14, "5"}) {
		t.Errorf("edited: %v %q %d", edits, out, at)
	}
	// a list item gets its box, the cursor staying with the text
	text = "list\nmilk"
	edits, out, at = plan(text, 9, "stay")
	if out != "list\n[ ] milk" || at != 13 || edits[0] != (edit{5, 5, "[ ] "}) {
		t.Errorf("boxed: %v %q %d", edits, out, at)
	}
	// ticked from the end of its line: the cursor at the end still
	text = "list\n[ ] milk /x"
	_, out, at = plan(text, len([]rune(text)), "stay")
	if out != "list\n[x] milk" || at != len([]rune(out)) {
		t.Errorf("ticked: %q %d", out, at)
	}
	// edits are made last first
	text = "math\n1 + 1 =\nx\n2 + 2 ="
	edits, _, _ = plan(text, 0, "stay")
	if len(edits) != 2 || edits[0].q0 < edits[1].q0 {
		t.Errorf("order: %v", edits)
	}
	// a note with nothing to do
	if edits, out, at := plan("just words", 3, "stay"); edits != nil || out != "just words" || at != -1 {
		t.Errorf("plain: %v %q %d", edits, out, at)
	}
}

func TestTimer(t *testing.T) {
	now := time.Date(2026, 10, 8, 14, 0, 0, 0, time.UTC)
	tm, err := timerCommand(nil, "timer 3:30", now)
	if err != nil || tm.kind != countdown || tm.length != 210*time.Second {
		t.Fatalf("3:30: %+v %v", tm, err)
	}
	if s := tm.show(now.Add(10 * time.Second)); s != "timer 3:20" {
		t.Error(s)
	}
	if _, done := tm.tick(now.Add(4 * time.Minute)); !done {
		t.Error("not done")
	}
	tm, _ = timerCommand(nil, "timer 5: Do laundry: whites", now)
	if tm.title != "Do laundry: whites" || tm.length != 5*time.Minute {
		t.Errorf("titled: %+v", tm)
	}
	tm, _ = timerCommand(nil, "timer 21:15", now)
	if tm.length != 7*time.Hour+15*time.Minute {
		t.Errorf("21:15: %v", tm.length)
	}
	tm, _ = timerCommand(nil, "timer 9am", now)
	if tm.length != 19*time.Hour {
		t.Errorf("9am tomorrow: %v", tm.length)
	}
	tm, _ = timerCommand(nil, "timer 25 5", now)
	if over, _ := tm.tick(now.Add(25 * time.Minute)); over != "work is over: rest" || tm.show(now.Add(26*time.Minute)) != "rest 4:00" {
		t.Errorf("pomodoro: %q %s", over, tm.show(now.Add(26*time.Minute)))
	}
	tm, _ = timerCommand(nil, "timer", now)
	tm, _ = timerCommand(tm, "timer p", now.Add(time.Minute))
	tm, _ = timerCommand(tm, "timer p", now.Add(time.Hour))
	if s := tm.show(now.Add(time.Hour + 30*time.Second)); s != "timer 1:30" {
		t.Errorf("paused stopwatch: %s", s)
	}
	if tm, _ = timerCommand(tm, "timer s", now); tm != nil {
		t.Error("not stopped")
	}
	if _, err := timerCommand(nil, "timer soon", now); err == nil {
		t.Error("timer soon read")
	}
}

func TestStore(t *testing.T) {
	s := store{dir: t.TempDir()}
	a, _ := s.fresh()
	os.WriteFile(a, []byte("a"), 0o600)
	b, _ := s.fresh()
	os.WriteFile(b, []byte("b"), 0o600)
	now := time.Now()
	if err := s.throw(a, now); err != nil {
		t.Fatal(err)
	}
	c, _ := s.fresh()
	if filepath.Base(c) != "000003.txt" {
		t.Errorf("a number used again: %s", c)
	}
	// an empty note thrown is gone, not kept
	if err := s.throw(c, now); err != nil {
		t.Fatal(err)
	}
	if ps, _ := list(s.void()); len(ps) != 1 {
		t.Errorf("void: %v", ps)
	}
	// the empty note's number is free again: nothing kept it
	r, err := s.restore()
	if err != nil || filepath.Base(r) != "000003.txt" {
		t.Errorf("restored %s %v", r, err)
	}
	if got, _ := os.ReadFile(r); string(got) != "a" {
		t.Errorf("restored %q", got)
	}
	old := now.Add(-48 * time.Hour)
	os.Chtimes(b, old, old)
	os.Chtimes(r, old, old)
	if n, _ := s.expire(24*time.Hour, r, now); n != 1 {
		t.Errorf("expired %d", n)
	}
	if ps, _ := s.notes(); len(ps) != 1 || ps[0] != r {
		t.Errorf("left %v", ps)
	}
}

func TestExport(t *testing.T) {
	home := t.TempDir()
	os.Mkdir(filepath.Join(home, "out"), 0o755)
	p, err := exportNote("list: Shop/ping\n[ ] milk\n", "/x/000001.txt", "~/out", home)
	if err != nil || p != filepath.Join(home, "out", "Shop-ping.txt") {
		t.Fatalf("%s %v", p, err)
	}
	if b, _ := os.ReadFile(p); string(b) != "[ ] milk\n" {
		t.Errorf("%q", b)
	}
	p, _ = exportNote("list: Shop/ping\n", "/x/000001.txt", "out/", home)
	if filepath.Base(p) != "Shop-ping 2.txt" {
		t.Errorf("second: %s", p)
	}
	if got := noteTitle(strings.Split("math\n\n  rent  \n", "\n")); got != "rent" {
		t.Errorf("title %q", got)
	}
	if got, err := parseAge("1.5d"); err != nil || got != 36*time.Hour {
		t.Errorf("1.5d: %v %v", got, err)
	}
}
