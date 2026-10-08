package main

// AutoPaste: after a "paste" line, whatever is copied is added to the note,
// each new copy a line of its own, or after the delimiter "paste(, )" names.
// Another "paste" line stops it. The clipboard is the one of the machine
// anti runs on.

import (
	"errors"
	"os/exec"
	"strings"
)

type paster struct {
	delim string
	last  string // the clipboard as last seen: only a change is pasted
	n     int    // how many have been pasted
}

// pasteCommand reads "paste" or "paste(DELIM)": the delimiter, a newline
// when none is given.
func pasteCommand(line string) string {
	rest := strings.TrimSpace(line)[len("paste"):]
	if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
		return rest[1 : len(rest)-1]
	}
	return "\n"
}

// clipboard is the text on the clipboard.
func clipboard() (string, error) {
	for _, c := range [][]string{{"pbpaste"}, {"wl-paste", "-n"}, {"xclip", "-selection", "clipboard", "-o"}, {"xsel", "-b", "-o"}} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		out, err := exec.Command(c[0], c[1:]...).Output()
		return string(out), err
	}
	return "", errors.New("no clipboard here: none of pbpaste, wl-paste, xclip or xsel")
}

// addition is what a copy adds to a note that ends in tail: a line of its
// own, or after the delimiter once something is pasted.
func (p *paster) addition(tail, clip string) string {
	clip = strings.TrimRight(clip, "\n")
	var b strings.Builder
	if tail != "" && !strings.HasSuffix(tail, "\n") && (p.delim == "\n" || p.n == 0) {
		b.WriteString("\n")
	}
	if p.delim == "\n" {
		b.WriteString(clip)
		b.WriteString("\n")
	} else {
		if p.n > 0 {
			b.WriteString(p.delim)
		}
		b.WriteString(clip)
	}
	return b.String()
}
