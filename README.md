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
