# Commands

A collection of small command-line tools. Each tool lives in its own directory.

Run `./install.bash` from the repository root to build every Go command for the
local machine and install it, along with the shell commands in `9misc`, into
`~/bin` — or into `$INSTALL_DIR`, if that is set.

`./update.bash` does the same thing for Meta devservers, where the Linux
binaries are published with `updatebin` and invoked through dotslash launchers.

## `9misc`

`9misc` contains the Plan 9-style command-history helpers `"` and `""`, the
self-contained `mc` columnator, the column-oriented `lc`, and the
path-preserving `lf` file lister. See [`9misc/README.md`](9misc/README.md) for
behavior, dependencies, and installation.

## `anti`

`anti` is a scratchpad in Apex, after [Antinote](https://antinote.io/): a
window for the notes you are going to throw away. Run it in a session and it
shows the newest note; what is typed is kept as it is typed, each note a plain
file in `~/.anti` (or `-dir`, or `$ANTI`). `Prev` and `Next` walk the notes,
and past the newest is a new one; a note left empty goes, and one left alone
for two weeks (`apex set anti.expire`) goes to the void, from which `Restore`
brings back the last. `Export` writes a note out for keeping and opens it.

The first line can make a note something more. `math` works out each line that
ends in `=` and writes the answer after it, with `name : value` for names, `ans`
for the last answer, and unit conversions (`10 ft to m`); the words around the
numbers stay, so a line says what its number was for. `list` makes each line a
checkbox, ticked by ending it in `/x` or by B3 on the box; `sum`, `avg` and
`count` put what the note adds up to in the window's label. In any note a
`timer` line starts a stopwatch, countdown or pomodoro, shown in the label, and
a `paste` line adds whatever is copied afterwards. `anti -help` has the rest.

Not here from Antinote: currency rates, text from screenshots, shortened
links, and a global hotkey; a second `anti` brings the first one's window
forward instead.

## `drafts`

`drafts` keeps a directory of documents being written: a flat directory of
Markdown files, one per draft, with no index and nothing running. A draft is a
file, its title is the first thing in it that reads like one — a frontmatter
`title:`, else the first H1, else the first line with anything on it — and its
age is its last change in version control, or the file's own time when it is
unrecorded.

A query prints the Markdown block each match was made in, by the same block
rules `refl` uses, because a line of prose is a wrap and the sentence it
belongs to is the smallest piece that still says something. `-t` prints recent
modifications, and `-a` opens an Apex window on the directory: the drafts, most
recently modified first, a page at a time, with `Search`, `Timeline`, `Get`,
`New`, `Preview`, `Notes`, `Rename` and `Sync`. Beside a draft may lie its
notes, `<name>-notes.md`, which `Notes` opens and makes, and which follow their
draft when `Rename` files it under the title it now carries. `New` writes
nothing — a draft's filename comes from its title, and the title is not known
until something has been written — so it begins the draft in a window of its
own, and `Put` there names the file after what is in it.

A drafts directory under git or Sapling is committed on every `Put`, the commit
is pushed in the background, and the directory is synced with its remote every
five minutes: writing is the only thing asked of the writer, and a draft kept
nowhere but one disk is not kept. The commit, the push and the sync are each
their own thread of work, so a remote having a bad day costs nothing at the
keyboard, and a merge that conflicts is undone rather than left behind.

```sh
cd drafts
go build

./drafts foobar
./drafts -t
./drafts -a
```

`drafts` uses `-d`, else `$DRAFTS_DIR`, else `~/drafts`. See
[`drafts/README.md`](drafts/README.md) or run `drafts -help` for the block
rules, the timeline, and the Apex window.

## `p`

`p` prints files a page at a time, after the Plan 9 command of the same name:
22 lines — a third of an nroff page — then a wait for a command. There is no
screen addressing, no status line, and no way to go backwards, so the text
stays in the scrollback where it can be searched and copied.

Commands are read from `/dev/tty`, which leaves standard input free for the
text being printed. A newline prints the next page, `q` quits, and `!COMMAND`
runs a command with `$SHELL` before asking again. The last line of a page is
printed without its newline, so the newline that ends a command completes the
page instead of scrolling it away.

```sh
cd p
go build

./p main.go
./p -40 main.go
git log | ./p
```

Run `p -help` for complete usage.

## `refl`

`refl` searches and follows a Reflect notes graph: a directory of Markdown
files, with daily notes in `daily/YYYY-MM-DD.md` and everything else in
`notes/`. It reads the files directly, with no index and nothing running.

A query prints one matching line per result; `-c` prints the Markdown block
around each match instead, using the same block rules Reflect uses to show a
backlink. `-t` prints recent changes out of the graph's git history, and `-a`
opens an Apex window on the graph: the notes that changed most recently, a page
at a time, with `Search`, `Timeline`, `Get`, `Backlinks`, `Note`, `Today`,
`Sync`, and
`Yesterday`/`Tomorrow` on the daily notes themselves. The window's state is its
own text — `Get` reads the first line and shows what it says — and the graph is
synced while the window is open.

A note changed when the last commit that touched it did — a clone or a pull
rewrites every file time at once — and git's answer is cached per graph.

```sh
cd refl
go build

./refl chrysalis
./refl -c "air traffic" control
./refl -t
./refl -a
```

`refl` uses `$REFLECT_GRAPH`, else the nearest enclosing graph of the working
directory, else `~/reflect`. Notes marked `private: true` are never listed,
matched, or printed. See [`refl/README.md`](refl/README.md) or run `refl -help`
for the block rules, the timeline, and the Apex window.

## `where`

`where` finds files and directories whose names contain a query. It searches the
top level of each root in `WHEREPATH`; unlike `find`, it does not recursively scan
the entire directory tree.

Queries can contain slash-separated components. Each component selects matching
directories for the next component to search, which makes queries such as
`mon/BUCK` useful for quickly locating a file when only parts of its path are
known.

`WHEREPATH` uses the platform path-list separator and accepts glob patterns.
Results are printed one per line, with a trailing slash for directories.
Run `where -help` for complete configuration and usage instructions.

```sh
cd where
go build

export WHEREPATH="$HOME/src/*:$HOME/work/*"
./where mon/BUCK
```

## `try`

`try` finds and creates dated experiment directories under `~/src/tries`, or
the directory configured by `TRY_PATH`. Its output is deliberately just a list
of directory paths, making it suitable for use from Apex, Acme, or a shell.
Run it without a query to list the 20 most recent tries. Query results favor
exact name components and then newer tries.

```sh
cd try
go build

./try md
./try -s
./try -n newproject
./try -n git@github.com:mariusae/cmd.git
```

`-s` follows each matching path with a short version-control summary — the
position of the branch relative to its upstream, then the state of the working
tree — so that a bare `try -s` shows which recent tries still have work in them.

Names containing whitespace are normalized with hyphens, and repeated names on
the same day receive a numeric suffix rather than reusing an existing directory.
Git progress goes to stderr; stdout remains reserved for resulting paths.

Run `try -help` for complete usage and configuration instructions.

## `wiki`

`wiki` writes a wiki for a code base, after [DeepWiki](https://deepwiki.com):
an overview, then a page for each of its parts, with diagrams, tables, and
citations of the source down to the line. A coding agent does the reading and
the writing — Claude Code through `claude -p`, or Codex through `codex exec`
with `-harness codex` — so authentication and models are the agent's own
business; it is given only the tools to read the source. It is run once to plan
the wiki and then once for each page, several at a time.

The wiki is Markdown, a file per page, in `~/wiki/NAME` (or `$WIKI_DIR/NAME`,
or the destination given), and `wiki` renders it as plain HTML in `html/`
there: Times New Roman and Inconsolata, the pages numbered down the side,
Mermaid diagrams drawn in the browser, and citations linked to GitHub when the
source is a GitHub clone, or to the files themselves when it is not.

Run again, `wiki` updates rather than starts over. `wiki.json` keeps the outline
and a hash of every source file; when nothing has changed the pages are only
rendered, and when something has, the agent revises the outline and rewrites
just the pages that rest on what changed, each from its last version. `-full`
starts over, and `-render` renders pages edited by hand.

```sh
cd wiki
go build

./wiki ~/src/apex
./wiki ~/src/apex ~/mycustomwikipath
./wiki -harness codex -j 8 .
./wiki -render ~/wiki/apex
```

Run `wiki -help` for complete usage.

## `work`

`work` lists, creates, and removes linked Sapling worktrees. It uses the
worktree group for the current repository, falling back to a default repository
configured in `~/.config/work/config.yaml`. Its path-oriented output works
naturally with Apex, Acme, and shell pipelines.

```sh
cd work
go build

./work ls
./work new test-feature
./work rm /path/to/worktree
./work note
./work install-hooks
./work status
./work -a
```

See [`work/README.md`](work/README.md) or run `work -help` for configuration,
agent hooks, and the live Apex dashboard.
