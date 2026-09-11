# Commands

A collection of small command-line tools. Each tool lives in its own directory.

Run `./update.bash` from the repository root to build every Go command and
update its Linux binary in `~/bin` with `updatebin`. The script also publishes
the shell commands in `9misc`.

## `9misc`

`9misc` contains the Plan 9-style command-history helpers `"` and `""`, the
self-contained `mc` columnator, the column-oriented `lc`, and the
path-preserving `lf` file lister. See [`9misc/README.md`](9misc/README.md) for
behavior, dependencies, and installation.

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
./try -n newproject
./try -n git@github.com:mariusae/cmd.git
```

Names containing whitespace are normalized with hyphens, and repeated names on
the same day receive a numeric suffix rather than reusing an existing directory.
Git progress goes to stderr; stdout remains reserved for resulting paths.

Run `try -help` for complete usage and configuration instructions.

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
