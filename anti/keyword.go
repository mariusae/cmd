package main

// Keywords: the first line of a note says what kind of note it is. "math"
// works out its lines, "list" makes each line a checkbox, "sum", "avg" and
// "count" say what the note adds up to, "code" leaves it be. "list: Groceries"
// is a list titled Groceries. Lines beginning "timer" and "paste" are
// commands, done when the line is finished, in any note.

import (
	"fmt"
	"regexp"
	"strings"
)

var keywords = map[string]bool{"math": true, "list": true, "sum": true, "avg": true, "average": true, "count": true, "code": true}

// keywordOf reads the first line: the keyword, and the title after its
// colon if there is one.
func keywordOf(first string) (kw, title string) {
	t := strings.TrimSpace(first)
	word, rest, _ := strings.Cut(t, ":")
	word = strings.ToLower(strings.TrimSpace(word))
	if !keywords[word] {
		return "", ""
	}
	if word == "average" {
		word = "avg"
	}
	return word, strings.TrimSpace(rest)
}

// ---- checkboxes ---------------------------------------------------------------

// A checkbox line: its indent, an optional "- ", the box, and the item.
var boxRE = regexp.MustCompile(`^(\s*)(- )?\[([ xX]?)\]( ?)(.*)$`)

// checkTrigger is what a line ends in to check it off, or to uncheck it.
const checkTrigger = "/x"

// isChecked says a line is a ticked checkbox.
func isChecked(line string) bool {
	m := boxRE.FindStringSubmatch(line)
	return m != nil && strings.EqualFold(m[3], "x")
}

// hasBox says a line is a checkbox.
func hasBox(line string) bool { return boxRE.MatchString(line) }

// toggle ticks a checkbox line, or unticks it; a line without one is
// returned as it was.
func toggle(line string) string {
	m := boxRE.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	mark := "x"
	if strings.EqualFold(m[3], "x") {
		mark = " "
	}
	return m[1] + m[2] + "[" + mark + "] " + m[5]
}

// normalBox writes a checkbox the one way: "[] item" and "[ ]item" are
// "[ ] item".
func normalBox(line string) string {
	m := boxRE.FindStringSubmatch(line)
	if m == nil {
		return line
	}
	mark := " "
	if strings.EqualFold(m[3], "x") {
		mark = "x"
	}
	if m[5] == "" && m[4] == "" && m[3] == "" {
		// "[]" alone, being typed: left for the next keystroke
		return line
	}
	return m[1] + m[2] + "[" + mark + "] " + m[5]
}

// tidyLines does what a note asks of its lines as they are written: in a
// list every item gets a checkbox; in any note a checkbox is written the
// one way, and one ending in the trigger is ticked (or unticked). An item
// ticked so then stays, goes to the bottom, or goes, as `checked` says.
func tidyLines(lines []string, kw string, checked string) []string {
	out := make([]string, 0, len(lines))
	var done []string
	for i, line := range lines {
		if i == 0 || isComment(line) || kw == "math" || kw == "code" {
			out = append(out, line)
			continue
		}
		ticked := false
		if strings.HasSuffix(line, checkTrigger) && (kw == "list" || hasBox(line)) {
			line = strings.TrimRight(strings.TrimSuffix(line, checkTrigger), " ")
			if !hasBox(line) {
				line = boxed(line)
			}
			line = toggle(line)
			ticked = isChecked(line)
		}
		if kw == "list" && !hasBox(line) && strings.TrimSpace(line) != "" && !isComment(line) && !commandish(line) {
			line = boxed(line)
		}
		line = normalBox(line)
		if ticked {
			switch checked {
			case "delete":
				continue
			case "bottom":
				done = append(done, line)
				continue
			}
		}
		out = append(out, line)
	}
	if len(done) > 0 {
		// the bottom of the list: before any blank lines the note ends in
		end := len(out)
		for end > 1 && strings.TrimSpace(out[end-1]) == "" {
			end--
		}
		tail := append([]string{}, out[end:]...)
		out = append(append(out[:end], done...), tail...)
	}
	return out
}

// boxed puts an unticked checkbox on a line, after its indent.
func boxed(line string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rest := strings.TrimPrefix(line[len(indent):], "- ")
	return indent + "[ ] " + rest
}

// ---- sum, avg and count -------------------------------------------------------

var numberRE = regexp.MustCompile(`-?[0-9][0-9.,]*[0-9]|-?[0-9]`)

// numbers are every number in the note but its keyword line and comments.
func numbers(lines []string) []float64 {
	var out []float64
	for i, line := range lines {
		if i == 0 || isComment(line) {
			continue
		}
		for _, m := range numberRE.FindAllString(line, -1) {
			if v, ok := parseNumber(m); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

// summary is what a keyword says of its note, for the window's label.
func summary(kw string, lines []string) string {
	switch kw {
	case "sum":
		total := 0.0
		for _, v := range numbers(lines) {
			total += v
		}
		return "sum " + formatNumber(total)
	case "avg":
		ns := numbers(lines)
		if len(ns) == 0 {
			return "avg -"
		}
		total := 0.0
		for _, v := range ns {
			total += v
		}
		return fmt.Sprintf("avg %s of %d", formatNumber(total/float64(len(ns))), len(ns))
	case "count":
		items, words, chars := 0, 0, 0
		for i, line := range lines {
			if i == 0 || isComment(line) {
				continue
			}
			if strings.TrimSpace(line) != "" {
				items++
			}
			words += len(strings.Fields(line))
			chars += len([]rune(line))
		}
		return fmt.Sprintf("%d items, %d words, %d chars", items, words, chars)
	case "list":
		n, done := 0, 0
		for i, line := range lines {
			if i > 0 && hasBox(line) {
				n++
				if isChecked(line) {
					done++
				}
			}
		}
		return fmt.Sprintf("%d/%d done", done, n)
	}
	return kw
}

// ---- commands -----------------------------------------------------------------

// A command line: "timer ..." or "paste", finished by a newline.
var commandRE = regexp.MustCompile(`(?i)^\s*(timer\b.*|paste(\(.*\))?)\s*$`)

// commands counts the finished command lines of a text: a line is finished
// once a newline follows it. A command is done when its count goes up.
func commands(text string) map[string]int {
	out := map[string]int{}
	lines := strings.Split(text, "\n")
	for _, line := range lines[:len(lines)-1] {
		if commandRE.MatchString(line) {
			out[strings.TrimSpace(line)]++
		}
	}
	return out
}

// newCommands are the commands finished since `before`, in note order.
func newCommands(before map[string]int, text string) []string {
	now := commands(text)
	var out []string
	lines := strings.Split(text, "\n")
	seen := map[string]int{}
	for _, line := range lines[:len(lines)-1] {
		c := strings.TrimSpace(line)
		if now[c] == 0 {
			continue
		}
		seen[c]++
		if seen[c] > before[c] {
			out = append(out, c)
		}
	}
	return out
}

// commandish says a line is a command, or is on its way to being one as
// it is typed: in a list, such a line is not an item.
func commandish(line string) bool {
	t := strings.ToLower(strings.TrimSpace(line))
	return commandRE.MatchString(line) || strings.HasPrefix("timer", t) || strings.HasPrefix("paste", t)
}

// ---- titles ---------------------------------------------------------------------

// noteTitle is what a note is called when it leaves: the keyword's title,
// else its first line with something on it, the keyword aside.
func noteTitle(lines []string) string {
	kw, title := "", ""
	if len(lines) > 0 {
		kw, title = keywordOf(lines[0])
	}
	if title != "" {
		return title
	}
	for i, line := range lines {
		if i == 0 && kw != "" {
			continue
		}
		t := strings.TrimSpace(line)
		t = strings.TrimSpace(boxRE.ReplaceAllString(t, "$5"))
		if t != "" {
			return t
		}
	}
	return ""
}
