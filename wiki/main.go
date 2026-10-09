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

const helpText = `wiki writes a wiki for a code base, after DeepWiki: an overview, then a page
for each of its parts, with diagrams and citations of the source.

Usage:
  wiki [flags] SOURCE [DEST]
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

The Markdown is the wiki: edit a page by hand, and -render shows the change.
Links between pages are written [Title](slug.md), and citations of the source
[path:12-40](path#L12-L40); in HTML the first go to the page, and the second
to the file on GitHub when SOURCE is a GitHub clone, or on disk otherwise.

Flags:
  -harness NAME  the agent to run: claude (the default) or codex
  -model MODEL   the model the agent uses, instead of its default
  -j N           how many pages to write at once (default 4)
  -full          plan and write every page again
  -render        render DEST's pages as HTML, and nothing else

Examples:
  wiki ~/src/apex
  wiki ~/src/apex ~/mycustomwikipath
  wiki -harness codex -j 8 .
  wiki -render ~/wiki/apex
`

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
	help := flags.Bool("help", false, "")
	flags.BoolVar(help, "h", false, "")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(stderr, "wiki: %v\n", err)
		fmt.Fprintln(stderr, "usage: wiki [-harness claude|codex] [-model MODEL] [-j N] [-full] SOURCE [DEST]")
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

	if len(rest) < 1 || len(rest) > 2 || *jobs < 1 {
		fmt.Fprintln(stderr, "usage: wiki [-harness claude|codex] [-model MODEL] [-j N] [-full] SOURCE [DEST]")
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
	var dest string
	if len(rest) == 2 {
		dest = rest[1]
	} else {
		dest, err = defaultDest(source, getenv)
		if err != nil {
			fmt.Fprintf(stderr, "wiki: %v\n", err)
			return 1
		}
	}
	if dest, err = filepath.Abs(dest); err != nil {
		fmt.Fprintf(stderr, "wiki: %v\n", err)
		return 1
	}

	h, err := newHarness(*harnessName, *model)
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
