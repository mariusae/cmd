# Commands

A collection of small command-line tools. Each tool lives in its own directory.

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
./work install-hooks
./work status
./work dash
```

See [`work/README.md`](work/README.md) or run `work -help` for configuration,
agent hooks, and the live Apex dashboard.
