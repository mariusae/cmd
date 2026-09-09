# work

`work` is a small, path-oriented command for Sapling worktrees. Its output is
designed to be useful as clickable paths in Apex and Acme as well as in a shell.

```sh
work ls
work new test-feature
work rm /data/users/me/fbsource-2026-09-09-test-feature/
work install-hooks
work status
work dash
```

Running `work` without arguments is equivalent to `work ls`. `ls` omits the
main worktree. `new` creates a dated sibling of the main worktree and checks out
the current revision. `rm` without an argument removes the current worktree,
but only when run from a linked worktree. The main worktree is never removable.

## Configuration

Configuration lives at `~/.config/work/config.yaml`:

```yaml
default: ~/fbsource

pre_remove:
  - /home/me/.config/work/hooks/pre-remove-cleanup.sh
```

When the current directory is not in an EdenFS-backed Sapling repository,
`default` selects the repository whose worktrees are managed.

Each `pre_remove` entry is run with Bash from the worktree being removed. A
nonzero exit stops removal. Hooks receive `WORK_HANDLE`, `WORK_WORKTREE_PATH`,
and `WORK_PROJECT_ROOT`.

## Agent status

Run `work install-hooks` once to add lifecycle hooks for detected Claude Code,
Codex, Gemini CLI, and OpenCode installations. Existing JSON settings and hooks
are preserved, and repeated installation is safe. Restart running agent
sessions afterward so they reload their configuration. The `work` and
`sqlite3` executables must be available on `PATH` when the hooks run.

The hooks record `working`, `waiting`, and `done` states in
`~/.local/state/work/work.db` (or `$XDG_STATE_HOME/work/work.db`). Use
`work status` to show the latest state for linked worktrees:

```text
/data/users/me/fbsource-2026-09-09-test-feature/  done  5:17AM  codex  Implement dashboard
```

The final column is the agent's live Apex title when one is available. Generic
shell and agent names are filtered out in the same way as workmux pane titles.

## Apex dashboard

`work dash` opens a live `MAIN_WORKTREE/-work` window in the current Apex
session. It lists every linked worktree, including ones with no agent activity,
and refreshes automatically as worktrees, agent states, and titles change. The
body contains only the worktree rows; there is no header or dashboard chrome.

Choose `Expand` from Apex's tools menu with the point on a worktree row (or one
of its detail rows) to toggle the last five status events in chronological
order. Completed events include the corresponding assistant response read from
the recorded Claude, Codex, Gemini, or OpenCode session transcript when it is
available, plus the elapsed time from that operation's first `working` event.
The history continues to update while the dashboard is open.
When an agent is running in the current Apex session, B3 on its agent name
(such as `codex`) warps to that agent's live window.
`Get` forces an immediate repository and status refresh.
Deleting the Apex window stops the dashboard process.

## Build and install

Run `./update.bash` to build and install the `~/bin/work` DotSlash launcher.
