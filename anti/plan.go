package main

// What the tool writes into a note as it is written, worked out from the
// text and the cursor alone, so that it can be tested without a window.

import "strings"

// An edit to the window's body: characters q0 to q1 become text.
type edit struct {
	q0, q1 int
	text   string
}

// plan says what a note's keyword makes of it: the edits, last first so
// that each one's offsets still hold when it is made; the text they make;
// and where the cursor is to go, or -1 to leave it where the edits leave
// it. cursor is the insertion point, or -1 for a selection.
//
// A math answer is written into the line being typed only once that line
// ends in "=" with the cursor after it, the moment the sum is asked for,
// or when the cursor is before the "=", where the answer is out of the way.
func plan(text string, cursor int, checked string) ([]edit, string, int) {
	lines := strings.Split(text, "\n")
	kw, _ := keywordOf(lines[0])
	cl, cc := lineCol(lines, cursor)

	after := tidyLines(lines, kw, checked)
	if len(after) != len(lines) {
		// lines came or went: the whole of it, and the cursor where its
		// line was
		out := strings.Join(after, "\n")
		at := -1
		if cursor >= 0 {
			l := min(cl, len(after)-1)
			at = offsetOf(after, l) + min(cc, len([]rune(after[l])))
		}
		return []edit{{0, len([]rune(text)), out}}, out, at
	}
	if kw == "math" {
		for i, want := range mathAnswers(after) {
			if i == cl {
				line := []rune(after[i])
				eq := strings.LastIndex(after[i], "=")
				eqAt := len([]rune(after[i][:eq]))
				asked := cc == len(line) && strings.TrimSpace(string(line[eqAt+1:])) == ""
				if cc > eqAt && !asked {
					continue
				}
			}
			after[i] = want
		}
	}

	var edits []edit
	newCol := cc
	for i := range lines {
		if after[i] == lines[i] {
			continue
		}
		old, neu := []rune(lines[i]), []rune(after[i])
		p, s := common(old, neu)
		start := offsetOf(lines, i)
		edits = append(edits, edit{start + p, start + len(old) - s, string(neu[p : len(neu)-s])})
		if i == cl {
			switch {
			case cc == len(old):
				newCol = len(neu)
			case cc <= p:
			case cc >= len(old)-s:
				newCol = len(neu) - (len(old) - cc)
			default:
				newCol = len(neu) - s
			}
		}
	}
	for i, j := 0, len(edits)-1; i < j; i, j = i+1, j-1 {
		edits[i], edits[j] = edits[j], edits[i]
	}
	out := strings.Join(after, "\n")
	at := -1
	if cursor >= 0 && after[cl] != lines[cl] {
		at = offsetOf(after, cl) + newCol
	}
	return edits, out, at
}

// lineCol is the line and column (in characters) of an offset.
func lineCol(lines []string, at int) (int, int) {
	if at < 0 {
		return -1, -1
	}
	for i, l := range lines {
		n := len([]rune(l))
		if at <= n || i == len(lines)-1 {
			return i, min(at, n)
		}
		at -= n + 1
	}
	return len(lines) - 1, 0
}

// offsetOf is where line i begins, in characters.
func offsetOf(lines []string, i int) int {
	at := 0
	for _, l := range lines[:i] {
		at += len([]rune(l)) + 1
	}
	return at
}

// common is how much two lines share at the start, and then at the end.
func common(a, b []rune) (int, int) {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	return p, s
}
