// p prints files a page at a time, in the manner of the Plan 9 command of the
// same name: no screen addressing, no status line, and no way to go backwards.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// defaultPageLen is a third of an nroff page: three pages of p fill one
// printed page.
const defaultPageLen = 22

const helpText = `p prints files a page at a time.

Usage:
  p [-LINES] [FILE ...]
  p -help

p writes LINES lines of each FILE and then waits for a command on /dev/tty,
so that input may still be piped in on standard input. With no FILE, p reads
standard input. LINES defaults to 22, a third of an nroff page; -LINES may
appear between file names, in which case it applies to the files after it.

The last line of a page is written without its newline, so the newline that
ends a command completes the page rather than scrolling it away.

Commands:
  newline    Print the next page.
  q          Quit. End of file on /dev/tty does the same.
  !COMMAND   Run COMMAND with $SHELL, with /dev/tty as its input, and then
             wait for another command.

Examples:
  p main.go
  p -40 main.go
  git log | p
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	for _, arg := range args {
		if arg == "-help" || arg == "--help" || arg == "-h" {
			fmt.Fprint(stdout, helpText)
			return 0
		}
	}

	// Commands are read from the terminal, not from standard input, which
	// may well be the text being printed.
	tty, err := os.Open("/dev/tty")
	if err != nil {
		fmt.Fprintf(stderr, "p: can't open /dev/tty: %v\n", err)
		return 1
	}
	defer tty.Close()

	out := bufio.NewWriter(stdout)
	defer out.Flush()
	p := &pager{
		pageLen: defaultPageLen,
		out:     out,
		cons:    bufio.NewReader(tty),
		shell: func(command string) error {
			return shell(command, tty, stdout, stderr)
		},
	}

	status := 0
	printed := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			n, err := strconv.Atoi(arg[1:])
			if err != nil || n <= 0 {
				n = defaultPageLen
			}
			p.pageLen = n
			continue
		}
		printed = true
		f, err := os.Open(arg)
		if err != nil {
			fmt.Fprintf(stderr, "p: can't open %s: %v\n", arg, err)
			status = 1
			continue
		}
		quit, err := p.printFile(f)
		f.Close()
		if err != nil {
			fmt.Fprintf(stderr, "p: %s: %v\n", arg, err)
			status = 1
		}
		if quit {
			return status
		}
	}
	if !printed {
		if _, err := p.printFile(stdin); err != nil {
			fmt.Fprintf(stderr, "p: %v\n", err)
			status = 1
		}
	}
	return status
}

// A pager prints text in pages of pageLen lines to out, reading the command
// that ends each page from cons and passing shell commands to shell.
type pager struct {
	pageLen int
	out     *bufio.Writer
	cons    *bufio.Reader
	shell   func(command string) error
}

// printFile prints r a page at a time, returning true if it was asked to quit.
// It returns when the text, or the reader of it, runs out.
func (p *pager) printFile(r io.Reader) (quit bool, err error) {
	in := bufio.NewReader(r)
	for {
		for i := 1; i <= p.pageLen; i++ {
			line, readErr := in.ReadString('\n')
			if line != "" {
				if !strings.HasSuffix(line, "\n") {
					// A final line without a newline: print it as it is.
					p.out.WriteString(line)
				} else {
					p.out.WriteString(line[:len(line)-1])
					// The newline of the last line on a page is left to
					// the command that asks for the next one.
					if i < p.pageLen {
						p.out.WriteByte('\n')
					}
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					readErr = nil
				}
				return false, readErr
			}
		}
		if err := p.out.Flush(); err != nil {
			return false, err
		}
		if quit, err := p.command(); quit || err != nil {
			return true, err
		}
	}
}

// command reads commands until one of them calls for the next page, and
// reports whether p should instead stop.
func (p *pager) command() (quit bool, err error) {
	for {
		line, err := p.cons.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		if err != nil || strings.HasPrefix(line, "q") {
			return true, nil
		}
		if !strings.HasPrefix(line, "!") {
			return false, nil
		}
		if err := p.shell(line[1:]); err != nil {
			return false, err
		}
	}
}

// shell runs command with the user's shell, reading its input from the
// terminal so that the command may prompt in turn.
func shell(command string, tty io.Reader, stdout, stderr io.Writer) error {
	name := os.Getenv("SHELL")
	if name == "" {
		name = "/bin/sh"
	}
	cmd := exec.Command(name, "-c", command)
	cmd.Stdin = tty
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "p: %s: %v\n", command, err)
	}
	return nil
}
