# 9misc

Small shell commands inspired by Plan 9 from User Space:

- `" [prefix]` prints commands from the current Apex, acme, 9term, or tmux
  window. With no prefix it prints the newest command; with a regular-expression
  prefix it prints the matching commands, keeping only their newest occurrence.
- `"" [prefix]` prints and executes the newest command selected by `"`, using
  `rc` (or the command named by `$RC`).
- `mc [-] [-N] [-t] [files ...]` lays out newline-separated input in Plan 9's
  column-major order. It reads the live terminal width, emits tabs in Apex and
  Acme, supports colon-delimited sections with `-`, and uses spaces with `-t`.
- `lc [ls options] [paths ...]` prints Plan 9's compact, column-oriented
  directory listing through the bundled `mc`.
- `lf [ls options] [paths ...]` lists files like GNU `ls`, adding type indicators
  by default, retaining paths relative to the current directory, and printing
  exactly one entry per line.

Run `./test.bash` to test the commands. Run `./update.bash` to publish Linux
artifacts with `updatebin` and install their dotslash launchers in `~/bin`.
When replacing a legacy non-dotslash command, the updater preserves it with a
`.pre-dotslash` suffix.

The quote commands reproduce plan9port's `quote1` and `quote2`, which its
installer publishes under the literal names `"` and `""`.
The commands do not require a plan9port installation. `MC_COLUMNS` and
`MC_TABSTOP` can override `mc`'s detected output width and tab stop.
