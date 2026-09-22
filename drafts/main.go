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

const helpText = `drafts keeps a directory of documents being written.

Usage:
  drafts QUERY
  drafts -t
  drafts -a [QUERY]
  drafts -help

A drafts directory is a flat directory of Markdown files, one per draft. There
is no index and no database: a draft is a file, and its title is a frontmatter
"title:", else its first H1, else the first line with anything on it, else the
file's own name. README.md documents the directory and is ignored. drafts uses
-d, else $DRAFTS_DIR, else ~/drafts.

The one directory under a drafts directory is archive/, where a draft goes when
it is done with. What is there is left out of the listing, the search and the
timeline until it is asked for, and is a drafts directory of its own.

Beside a draft may lie its notes, <name>-notes.md: the same directory, the same
Markdown, the thinking about the draft rather than the draft. Notes are left
out of the listing, where a companion would stand beside its draft as though it
were another one, and searched along with it, because notes are written to be
found again.

Searching:
  QUERY is a list of terms, matched case-insensitively; a line matches when it
  contains all of them, and quoting groups words into one term. Drafts are
  searched most recently modified first.

    drafts foobar
    drafts "air traffic" control

  Each match prints as the Markdown block it was made in — the whole paragraph,
  the heading's section, the list item with the branches under it that matched
  too — under the file and line it came from.

    /Users/you/drafts/foobar.md:10
        lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do
        eiusmod tempor incididunt foobar ut labore et dolore magna aliqua

  With no query at all, the drafts are listed newest first, one path per line,
  the form an editor or a shell can use directly. Under version control,
  recency is the last commit that changed each file rather than its checkout
  time; writing not yet committed keeps its file time.

Timeline:
  -t prints recent modifications, newest first, as the full Markdown blocks
  that changed. Nearby blocks are joined, and saves to the same place within
  five minutes are one entry showing the final text of that writing burst.
  Under version control it reads the history — every Put is committed, so the
  history is the record of the writing. Elsewhere it reads the files' own times,
  and each draft stands for itself.

    drafts -t

Apex:
  -a opens a window on the directory in the current Apex session: the drafts,
  most recently modified first, a page at a time. Look (B3) on a row opens that
  draft in a window of its own, on a block the draft at that line, and on the
  "more" row the window ends with, the next page. A query opens the window
  already searching.

    Search [QUERY]    Narrow the window to matching drafts and their blocks,
                      for the query or for what is swept in the window it was
                      run in. Search with no query restores the full list.
    Timeline          Show recent modifications, as -t prints them.
    Get               Re-read the directory, showing whatever the window's own
                      first line says: "Search QUERY" runs that search,
                      "Timeline" the timeline, and anything else every draft.
    New [TITLE]       Begin a draft in a window of its own. TITLE opens it as
                      the heading; with no title, the selection is one; with
                      neither, the window opens empty. Nothing is written until
                      Put, which reads what is there, names the file after it,
                      and leaves an ordinary draft window behind.
    Preview           Show the draft under the pointer as a page.
    Notes             Open <name>-notes.md, the notes for the draft, making it
                      if it is not there yet.
    Rename            File the draft under the title it now carries, taking its
                      notes with it. The title is read from the file, so Put
                      first; nothing is overwritten, and a name already taken
                      takes the next one.
    Archive           Put the draft away, in the archive directory, taking its
                      notes with it. The filename goes along unchanged: being
                      done with a draft is not retitling it.
    IncludeArchive    Show what is archived along with the drafts, or stop
                      showing it. Not a view of its own: the archive appears in
                      whichever view is up, and an archived draft's row says so.
    Sync              Bring the directory and its remote into step now.

  The window's state is its own text. Edit the first line to "Search whatever",
  "Timeline" or "IncludeArchive Timeline" and Get runs it; clear it and Get
  brings every draft back. A body that has been edited is left alone until then,
  so nothing is typed over.

  New, Search, Notes, Rename, Archive and Sync are offered on the directory's
  own drafts too, so a draft open in the session can beget one, look for a
  phrase in it, be written about, be filed afresh, be put away, or be sent on.

  Apex previews a Markdown file itself, live against the buffer, and offers
  Preview in the tag of every Markdown window; what it cannot do is preview a
  draft that is only a row in a listing, which is what Preview here is for.

Version control:
  A drafts directory under git or Sapling is committed on every Put, and the
  commit is pushed in the background. Writing is the only thing asked of the
  writer; keeping what was written is not a separate chore.

  While the window is open the directory is synced every five minutes, and Sync
  does it now: commit what was written here, fetch, take what was written
  elsewhere, push the result. A sync that moved something says so in +Errors; a
  sync that changed nothing says nothing unless you asked. A merge that
  conflicts is undone rather than left behind, and reported.

  A directory under neither git nor Sapling is left entirely as it is.

Options:
  -t        Print recent modifications instead of searching.
  -a        Open an Apex window on the drafts directory.
  -n N      Keep at most N results (default 20 blocks, 10 changes, 20 listings).
  -d DIR    Use the drafts directory DIR.
`

// Result counts each mode settles for when -n is not given.
const (
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
	flags := flag.NewFlagSet("drafts", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	// The help belongs to whoever asked for it: on stdout when it was asked
	// for, on stderr behind the complaint when it was not. Parse prints its own
	// complaint, so its usage says nothing.
	flags.Usage = func() {}
	var (
		asTimeline = flags.Bool("t", false, "print recent modifications")
		asWindow   = flags.Bool("a", false, "open an Apex window on the drafts")
		limit      = flags.Int("n", 0, "keep at most N results")
		root       = flags.String("d", "", "use the drafts directory DIR")
	)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(c.stdout, helpText)
			return 0
		}
		fmt.Fprint(c.stderr, helpText)
		return 2
	}

	path, err := findDir(*root, c.getenv, c.home)
	if err != nil {
		return c.fail(err)
	}
	d := openDir(path)

	switch query := strings.Join(flags.Args(), " "); {
	case *asWindow:
		return c.fail(openWindow(d, query, c.getenv, c.now))
	case *asTimeline:
		return c.fail(c.timeline(d, at(*limit, defaultChanges)))
	case strings.TrimSpace(query) == "":
		return c.fail(c.list(d, at(*limit, defaultListings)))
	default:
		return c.fail(c.search(d, query, at(*limit, defaultBlocks)))
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
	fmt.Fprintf(c.stderr, "drafts: %v\n", err)
	return 1
}

// list prints the drafts newest first, one path per line, the form an editor
// or a shell can use directly.
func (c command) list(d *dir, limit int) error {
	drafts, err := d.list()
	if err != nil {
		return err
	}
	wd, _ := c.getwd()
	for index, subject := range drafts {
		if index >= limit {
			break
		}
		fmt.Fprintln(c.stdout, displayPath(subject.path, wd))
	}
	return nil
}

// search prints the Markdown block around each match, indented under the place
// it came from.
func (c command) search(d *dir, query string, limit int) error {
	hits, err := d.search(queryTerms(query), bounds{results: limit})
	if err != nil {
		return err
	}
	wd, _ := c.getwd()
	for _, matched := range hits {
		path := displayPath(matched.draft.path, wd)
		for _, block := range matched.blocks {
			fmt.Fprintf(c.stdout, "%s:%d\n", path, block.line)
			c.writeBlock(block.text)
		}
	}
	return nil
}

// timeline prints recent modifications as the blocks that changed, newest
// first.
func (c command) timeline(d *dir, limit int) error {
	changes, err := d.timeline(limit)
	if err != nil {
		return err
	}
	now := c.now()
	wd, _ := c.getwd()
	for _, changed := range changes {
		path := displayPath(changed.draft.path, wd)
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
