package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const helpText = `refl searches and follows a Reflect notes graph.

Usage:
  refl QUERY
  refl -c QUERY
  refl -t
  refl -a [QUERY]
  refl -help

A graph is a directory of Markdown files: daily notes in daily/YYYY-MM-DD.md,
everything else in notes/. refl uses $REFLECT_GRAPH, else the nearest enclosing
graph of the working directory, else ~/reflect. Notes marked "private: true"
are never listed, matched or printed.

A note changed when the last commit touching it did, not when its file was
written: a clone or a pull rewrites every file time at once. A note git cannot
date — untracked, or written since its last commit — keeps its file time.

Searching:
  QUERY is a list of terms, matched case-insensitively; a line matches when it
  contains all of them, and quoting groups words into one term. Notes are
  searched most recently changed first, and paths under the working directory
  print relative to it.

    refl chrysalis
    refl "air traffic" control

  -c prints the Markdown block around each match rather than its line: the
  whole paragraph, the heading's section, or the list item with the branches
  under it that matched too. These are the blocks Reflect shows for a backlink.

    refl -c chrysalis

Timeline:
  -t prints recent changes, newest first, as the blocks that changed. It reads
  the graph's git history, preceded by whatever is written but not yet
  committed.

    refl -t

Apex:
  -a opens a window on the graph in the current Apex session: the notes that
  changed most recently, newest first, a page at a time. Look (B3) on a row
  opens that note, on a block the note at that line, and on the "more" row the
  window ends with, the next page. A query opens the window already searching.

    Search QUERY      Narrow the window to matching notes and their blocks.
                      Search with no query restores the full list.
    Timeline          Show recent changes, as -t prints them.
    Get               Re-read the graph, showing whatever the window's own
                      first line says: "Search QUERY" runs that search,
                      "Timeline" the timeline, and anything else the whole
                      graph.
    Backlinks [NOTE]  Open a window on what links to a note: the one named, the
                      one the window holds, or the one under the pointer.
    Note TITLE        Create notes/<slug>.md for TITLE and open it. With no
                      title, the selection is one.
    Today             Open today's daily note.
    Sync              Commit, merge and push the graph now.

  The window's state is its own text. Edit the first line to "Search whatever"
  or "Timeline" and Get runs it; clear it and Get brings the whole graph back.
  A body that has been edited is left alone until then, so nothing is typed
  over. Every view ends on a "more" row while there is more to show.

  A backlinks window is a throwaway, named for the note it is about; Del is how
  it goes. Note, Backlinks and Sync are offered on the graph's own notes too,
  and daily notes answer to Yesterday, Today and Tomorrow, which open the
  neighbouring day.

  The graph is synced every five minutes while the window is open, and a sync
  that moves something says so in +Errors. A merge that conflicts is undone
  rather than left behind, and reported.

  The verb is Note, not New: New is one of Apex's own commands, and those take
  every B2 before a tool's verbs are tried.

Options:
  -c        Print the block around each match.
  -t        Print recent changes instead of searching.
  -a        Open an Apex window on the graph.
  -n N      Keep at most N results (default 100 lines, 20 blocks, 10 changes).
  -g DIR    Use the graph in DIR.
`

// Result counts each mode settles for when -n is not given.
const (
	defaultLines    = 100
	defaultBlocks   = 20
	defaultChanges  = 10
	defaultListings = 20
)

type command struct {
	getenv func(string) string
	getwd  func() (string, error)
	home   func() (string, error)
	now    func() time.Time
	stdout io.Writer
	stderr io.Writer
}

func main() {
	c := command{
		getenv: os.Getenv,
		getwd:  os.Getwd,
		home:   os.UserHomeDir,
		now:    time.Now,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
	os.Exit(c.run(os.Args[1:]))
}

func (c command) run(args []string) int {
	flags := flag.NewFlagSet("refl", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	// The help belongs to whoever asked for it: on stdout when it was asked
	// for, on stderr behind the complaint when it was not. Parse prints its own
	// complaint, so its usage says nothing.
	flags.Usage = func() {}
	var (
		withBlocks = flags.Bool("c", false, "print the block around each match")
		asTimeline = flags.Bool("t", false, "print recent changes")
		asWindow   = flags.Bool("a", false, "open an Apex window on the graph")
		limit      = flags.Int("n", 0, "keep at most N results")
		root       = flags.String("g", "", "use the graph in DIR")
	)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(c.stdout, helpText)
			return 0
		}
		fmt.Fprint(c.stderr, helpText)
		return 2
	}

	dir, err := findGraph(*root, c.getenv, c.getwd, c.home)
	if err != nil {
		return c.fail(err)
	}
	g := openGraph(dir)

	switch query := strings.Join(flags.Args(), " "); {
	case *asWindow:
		return c.fail(openWindow(g, query, c.getenv, c.now))
	case *asTimeline:
		return c.fail(c.timeline(g, at(*limit, defaultChanges)))
	case strings.TrimSpace(query) == "":
		return c.fail(c.recent(g, at(*limit, defaultListings)))
	case *withBlocks:
		return c.fail(c.blocks(g, query, at(*limit, defaultBlocks)))
	default:
		return c.fail(c.lines(g, query, at(*limit, defaultLines)))
	}
}

func at(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	return fallback
}

func (c command) fail(err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(c.stderr, "refl: %v\n", err)
	return 1
}

// recent lists the most recently modified notes, one path per line, the form
// an editor or a shell can use directly.
func (c command) recent(g *graph, limit int) error {
	notes, err := g.list()
	if err != nil {
		return err
	}
	wd, _ := c.getwd()
	for index, subject := range notes {
		if index >= limit {
			break
		}
		fmt.Fprintln(c.stdout, displayPath(subject.path, wd))
	}
	return nil
}

// lines prints one matching line per result, the scriptable form.
func (c command) lines(g *graph, query string, limit int) error {
	hits, err := g.search(queryTerms(query), false, bounds{results: limit})
	if err != nil {
		return err
	}
	wd, _ := c.getwd()
	for _, matched := range hits {
		path := displayPath(matched.note.path, wd)
		for _, line := range matched.lines {
			fmt.Fprintf(c.stdout, "%s:%d %s\n", path, line.number, strings.TrimSpace(line.text))
		}
	}
	return nil
}

// blocks prints the Markdown block around each match, indented under the
// place it came from.
func (c command) blocks(g *graph, query string, limit int) error {
	hits, err := g.search(queryTerms(query), true, bounds{results: limit})
	if err != nil {
		return err
	}
	wd, _ := c.getwd()
	for _, matched := range hits {
		path := displayPath(matched.note.path, wd)
		for _, block := range matched.blocks {
			fmt.Fprintf(c.stdout, "%s:%d\n", path, block.line)
			c.writeBlock(block.text)
		}
	}
	return nil
}

// timeline prints recent changes as the blocks that changed, newest first.
func (c command) timeline(g *graph, limit int) error {
	changes, err := g.timeline(limit)
	if err != nil {
		return err
	}
	now := c.now()
	wd, _ := c.getwd()
	for _, changed := range changes {
		path := displayPath(changed.note.path, wd)
		for _, block := range changed.blocks {
			fmt.Fprintf(c.stdout, "%s:%d %s\n", path, block.line, formatTime(changed.when, now))
			c.writeBlock(block.text)
		}
	}
	return nil
}

func (c command) writeBlock(text string) {
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(c.stdout, "\t%s\n", line)
	}
}
