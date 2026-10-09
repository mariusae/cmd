package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const helpText = `wiki writes a wiki for a code base, or for a change to one, after DeepWiki:
an overview, then a page for each of its parts, with diagrams and citations
of the source.

Usage:
  wiki [flags] SOURCE [DEST]
  wiki [flags] -c COMMIT [SOURCE [DEST]]
  wiki [flags] -r REVSET [SOURCE [DEST]]
  wiki -render DEST
  wiki -help

A coding agent — Claude Code, or Codex — reads SOURCE and writes the wiki, one
Markdown file per page, into DEST; wiki then renders the pages as HTML in
DEST/html, and prints the path of the front page. DEST is ~/wiki/NAME, where
NAME is SOURCE's base name; $WIKI_DIR, if set, stands in for ~/wiki.

The agent runs with the tools to read SOURCE and nothing else. It is run once
to plan the wiki's pages, then once for each page, several at a time.

Run again on the same DEST, wiki updates the wiki rather than writing it anew.
DEST/wiki.json holds the outline and a hash of every file in SOURCE. When no
file has changed, the pages are only rendered again. When some have, the agent
revises the outline in their light, and only the pages that rest on a changed
file, or that are new, are written again — each from its last version. -full
plans and writes the whole wiki from the start.

With -c or -r, the wiki is of a change rather than of the code base: one
commit, or a linear stack of them, in the git or Sapling repository holding
SOURCE (by default the current directory), named as the VCS names them —
D123 or .^::. for Sapling, HEAD^! or main..topic for git. Given to -r in
git, a lone revision R stands for the commits since it, R..HEAD. The wiki
begins with an overview of the change and a walkthrough for its reviewers —
foundations first, then behavior, integration, edge cases and tests, with
the decisive code inline — and goes on to the background a reviewer needs.
The agent may also run the VCS's read-only commands (git show, sl cat and
the like), to read the change when it is not checked out.

DEST is then ~/wiki/NAME@SPEC, where NAME is the repository's base name and
SPEC the revision as given. Run again with the same revision after the
change is revised, wiki updates its wiki: the overview and walkthrough are
written again, and of the other pages those resting on a file whose part of
the patch is different.

The Markdown is the wiki: edit a page by hand, and -render shows the change.
Links between pages are written [Title](slug.md), and citations of the source
[path:12-40](path#L12-L40); in HTML the first go to the page, and the second
to the file on GitHub when SOURCE is a GitHub clone, or on disk otherwise.

Flags:
  -harness NAME  the agent to run: claude (the default) or codex
  -model MODEL   the model the agent uses, instead of its default
  -c COMMIT      write of the change made by COMMIT
  -r REVSET      write of the change made by the commits of REVSET
  -j N           how many pages to write at once (default 4)
  -full          plan and write every page again
  -render        render DEST's pages as HTML, and nothing else

Examples:
  wiki ~/src/apex
  wiki ~/src/apex ~/mycustomwikipath
  wiki -harness codex -j 8 .
  wiki -c D12345678
  wiki -r HEAD~2
  wiki -render ~/wiki/apex
`

const usage = `usage: wiki [-harness claude|codex] [-model MODEL] [-j N] [-full] SOURCE [DEST]
       wiki [flags] -c COMMIT | -r REVSET [SOURCE [DEST]]`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("wiki", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	harnessName := flags.String("harness", "claude", "")
	model := flags.String("model", "", "")
	jobs := flags.Int("j", 4, "")
	full := flags.Bool("full", false, "")
	renderOnly := flags.Bool("render", false, "")
	commit := flags.String("c", "", "")
	revset := flags.String("r", "", "")
	help := flags.Bool("help", false, "")
	flags.BoolVar(help, "h", false, "")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(stderr, "wiki: %v\n", err)
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if *help {
		fmt.Fprint(stdout, helpText)
		return 0
	}
	rest := flags.Args()

	if *renderOnly {
		if len(rest) != 1 {
			fmt.Fprintln(stderr, "usage: wiki -render DEST")
			return 2
		}
		index, err := renderDir(rest[0])
		if err != nil {
			fmt.Fprintf(stderr, "wiki: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, index)
		return 0
	}

	spec, single := *revset, false
	if *commit != "" {
		spec, single = *commit, true
	}
	if spec != "" && len(rest) == 0 {
		rest = []string{"."}
	}
	if len(rest) < 1 || len(rest) > 2 || *jobs < 1 || (*commit != "" && *revset != "") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	source, err := filepath.Abs(rest[0])
	if err != nil {
		fmt.Fprintf(stderr, "wiki: %v\n", err)
		return 1
	}
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "wiki: %s is not a directory\n", rest[0])
		return 1
	}
	var c *change
	if spec != "" {
		if c, source, err = resolveChange(source, spec, single); err != nil {
			fmt.Fprintf(stderr, "wiki: %v\n", err)
			return 1
		}
		fmt.Fprintf(stderr, "wiki: %s is %s, changing %s\n", spec, pluralize(len(c.Commits), "commit"), pluralize(len(c.parts), "file"))
		if !c.atHead {
			fmt.Fprintf(stderr, "wiki: note: the working copy is not at %s; the agent reads the change through %s\n", shortRevision(c.Head), c.VCS)
		}
	}
	var dest string
	if len(rest) == 2 {
		dest = rest[1]
	} else {
		dest, err = defaultDest(source, getenv)
		if err != nil {
			fmt.Fprintf(stderr, "wiki: %v\n", err)
			return 1
		}
		if c != nil {
			dest += "@" + specName(spec)
		}
	}
	if dest, err = filepath.Abs(dest); err != nil {
		fmt.Fprintf(stderr, "wiki: %v\n", err)
		return 1
	}

	var commands []string
	if c != nil {
		commands = c.commands()
	}
	h, err := newHarness(*harnessName, *model, commands)
	if err != nil {
		fmt.Fprintf(stderr, "wiki: %v\n", err)
		return 2
	}
	b := &builder{
		source:  source,
		dest:    dest,
		harness: h,
		jobs:    *jobs,
		full:    *full,
		change:  c,
		log:     stderr,
	}
	index, err := b.build(ctx)
	if index != "" {
		fmt.Fprintln(stdout, index)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "wiki: interrupted")
		} else {
			fmt.Fprintf(stderr, "wiki: %v\n", err)
		}
		return 1
	}
	return 0
}

// defaultDest is where the wiki for source goes when no destination is given:
// a directory named after it in $WIKI_DIR, else in ~/wiki.
func defaultDest(source string, getenv func(string) string) (string, error) {
	root := getenv("WIKI_DIR")
	if root == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("HOME is not set")
		}
		root = filepath.Join(home, "wiki")
	}
	return filepath.Join(root, filepath.Base(source)), nil
}
