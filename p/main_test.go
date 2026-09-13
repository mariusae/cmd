package main

import (
	"bufio"
	"strings"
	"testing"
)

func newTestPager(pageLen int, commands string) (*pager, *strings.Builder, *[]string) {
	var out strings.Builder
	var ran []string
	p := &pager{
		pageLen: pageLen,
		out:     bufio.NewWriter(&out),
		cons:    bufio.NewReader(strings.NewReader(commands)),
		shell: func(command string) error {
			ran = append(ran, command)
			return nil
		},
	}
	return p, &out, &ran
}

func TestPrintFilePages(t *testing.T) {
	p, out, _ := newTestPager(2, "\n\n\n")
	quit, err := p.printFile(strings.NewReader("a\nb\nc\nd\ne\n"))
	if err != nil {
		t.Fatalf("printFile: %v", err)
	}
	if quit {
		t.Error("printFile quit without being asked to")
	}
	p.out.Flush()
	// The last line of each page is printed without its newline; the
	// command that ends the page supplies it.
	if got, want := out.String(), "a\nbc\nde\n"; got != want {
		t.Errorf("printFile wrote %q, want %q", got, want)
	}
}

func TestPrintFileQuit(t *testing.T) {
	p, out, _ := newTestPager(1, "q\n")
	quit, err := p.printFile(strings.NewReader("a\nb\nc\n"))
	if err != nil {
		t.Fatalf("printFile: %v", err)
	}
	if !quit {
		t.Error("printFile did not quit on q")
	}
	p.out.Flush()
	if got, want := out.String(), "a"; got != want {
		t.Errorf("printFile wrote %q, want %q", got, want)
	}
}

func TestPrintFileEndOfCommands(t *testing.T) {
	p, _, _ := newTestPager(1, "")
	quit, err := p.printFile(strings.NewReader("a\nb\n"))
	if err != nil {
		t.Fatalf("printFile: %v", err)
	}
	if !quit {
		t.Error("printFile did not quit at end of file on the terminal")
	}
}

func TestPrintFileShellCommand(t *testing.T) {
	p, out, ran := newTestPager(1, "!date\n\nq\n")
	if _, err := p.printFile(strings.NewReader("a\nb\nc\n")); err != nil {
		t.Fatalf("printFile: %v", err)
	}
	if len(*ran) != 1 || (*ran)[0] != "date" {
		t.Errorf("ran %q, want [date]", *ran)
	}
	p.out.Flush()
	// A shell command does not print a page, so b follows a.
	if got, want := out.String(), "ab"; got != want {
		t.Errorf("printFile wrote %q, want %q", got, want)
	}
}

func TestPrintFileUnterminatedLine(t *testing.T) {
	p, out, _ := newTestPager(4, "")
	if _, err := p.printFile(strings.NewReader("a\nb")); err != nil {
		t.Fatalf("printFile: %v", err)
	}
	p.out.Flush()
	if got, want := out.String(), "a\nb"; got != want {
		t.Errorf("printFile wrote %q, want %q", got, want)
	}
}

func TestPrintFileShorterThanPage(t *testing.T) {
	p, out, _ := newTestPager(10, "")
	quit, err := p.printFile(strings.NewReader("a\nb\n"))
	if err != nil {
		t.Fatalf("printFile: %v", err)
	}
	if quit {
		t.Error("printFile quit at the end of a short file")
	}
	p.out.Flush()
	if got, want := out.String(), "a\nb\n"; got != want {
		t.Errorf("printFile wrote %q, want %q", got, want)
	}
}
