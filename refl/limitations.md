# What refl cannot ask Apex for

Two workflows refl wants are not expressible against Apex as it stands, and
both fail for the same reason. This is a note of what they are, where exactly
they stop, and what a session would have to offer for them to work — written
from refl's side, so it says what a tool wants rather than how Apex should give
it.

Read against the Apex working tree at `b7d8cf2`
(`/Users/marius/src/apex`). refl pins the Go side at
`github.com/mariusae/apex/go v0.0.0-20260912002926-0538f7cd8bf0`. The
conclusions below are read from that source; none of them were reproduced by
running a session.

## The two workflows

**`New` should make a note.** refl offers `Note TITLE`, which writes
`notes/<slug>.md` and opens it. The word a reader reaches for is `New`, and a
tool cannot have it.

**A note should be able to start before it is named.** The wanted flow: `New`
with no title opens an empty window called `<graph>/+New`; the reader writes,
heading first; on the first `Put`, refl reads the heading, works out
`notes/<slug>.md`, writes the note there and renames the window to it. The note
is named by being written, which is how a note actually gets named. refl cannot
do this because it never learns that a `Put` happened.

## How a command is dispatched

A B2 goes through `Node::exec` (`crates/apex-core/src/node.rs:1363`), which
asks `Node::resolve` (`node.rs:1343`) who handles the text. `resolve` matches
the first word against a fixed list — `Cut`, `Paste`, `Snarf`, `Undo`, `Redo`,
`Look`, `Edit`, `Newcol`, `Delcol`, `Del`, `Delete`, `Zerox`, `Font`, `Sort`,
`Exit`, `Tab`, `Indent`, `ID`, `Send`, `Web`, and a bare `New` — and gives
those to `Handler::Leader`, the session itself. Everything else goes to
`Handler::Server`.

The server lands in `Server::perform`
(`crates/apex-server/src/lib.rs:803`), which matches the first word again:

| word | line |
| --- | --- |
| `Put` | `lib.rs:833` |
| `Putall` | `lib.rs:837` |
| `Get` | `lib.rs:848` |
| `New` | `lib.rs:856` |
| `Newterm` | `lib.rs:865` |
| `Win` | `lib.rs:871` |
| `Kill` | `lib.rs:881` |
| `Send` | `lib.rs:887` |
| `Newweb` | `lib.rs:898` |
| anything else | `lib.rs:906` |

A tool's rules are consulted in one place: `verb_request`
(`lib.rs:1401`), called from the `_` arm at `lib.rs:908`. So the rules are
asked only about words no arm above them spells. **The set of verbs a tool can
offer is the complement of a hardcoded list, and a tool cannot tell which words
are in it.** `Offer` succeeds for `New` and for `Put` — the `rule` command
(`crates/apex-tool-bridge/src/lib.rs:232`) checks the word against nothing —
and the handler is simply never called, with nothing said anywhere.

This is also why the `_` arm's fallback matters more than it looks: a word that
reaches it and matches no rule becomes a shell command (`lib.rs:914`). A tool's
verb and `sh -c` share a namespace, and both sit below the builtins.

## 1. `New` is spoken for, twice over

A bare `New` is `Handler::Leader` (`node.rs:1351`) and makes an empty window.
`New name` is `Handler::Server` and reaches `lib.rs:856`, which opens the file
if it exists and otherwise proposes an empty window under that name. Neither
path passes a rule. There is no priority that helps: `Rule.Priority` orders
rules against each other, and this is not a contest between rules.

refl's answer is to spell the verb `Note`, which is a fine word and not the
word. The cost is small and entirely one of vocabulary; it is listed here
because it is the same wall as the next one, at a lower height.

## 2. A `Put` is invisible to the tool that owns the window

`Put` is matched at `lib.rs:833` and goes straight to `Server::put`
(`lib.rs:266`), which resolves the name (`lib.rs:273`), writes the file
(`lib.rs:286`), and proposes `Rename` and `Clean` (`lib.rs:291`, `lib.rs:293`).
A tool is not consulted at any point, including for a window the tool created
itself and holds live.

There is no other signal. The tool protocol
(`crates/apex-tool-bridge/src/lib.rs:149`–`277`) offers `windows`, `new`,
`page`, `open`, `read`, `selection`, `write`, `select`, `show`, `line`,
`rename`, `working`, `live`, `delete`, `exec`, `errors`, `switch`, `rule`,
`unrule`, `ack`, `watch`, `unwatch`, `set`, `setting`. `watch`
(`bridge:265`) reports edits to a body, which is typing, not saving.
`windows` (`bridge:150`) returns `id`, `name`, `kind` and `live` — no dirty
flag, so a tool cannot even see a buffer go clean and infer the save after the
fact. A tool can watch a window's *text* change and can watch the *file*
change from outside, but it cannot see the moment that connects them.

So refl can offer the reader a scratch window, and can watch them write in it,
and then has no way to be there when they save it.

## 3. A tool could not finish a `Put` even if it saw one

Suppose the rules did take `Put`. The handler would write the file itself, and
then need to tell the session the buffer is saved. `Server::put` does that with
`Proposal::Clean` (`lib.rs:293`), which carries the buffer's version and a
content hash. The tool protocol has no equivalent: nothing in the list above
marks a buffer clean.

A tool could get most of the way by not writing the file at all — rename the
window to the real path, withdraw its own rule, and `exec` a plain `Put` so
Apex's builtin does the writing and the cleaning. That works, but only because
the tool arranged to stop intercepting first, and it means every tool that
takes a `Put` has to know the trick. A verb that a tool can intercept but not
complete is a sharp edge.

Worth noting that `Node::winclean` (`node.rs:1029`) exempts live windows
(`node.rs:1033`), so a tool window that never goes clean does not nag on `Del`.
The gap is real but it is not loud.

## 4. `+name` is not a property a window can have

`+New` was chosen for the scratch window because `+Errors` reads as a window
that is not a file, and the intent was to borrow that. Apex does not have that
category. It has two different tests for it:

- **whole-name prefix**: `name.starts_with('+')`, at `lib.rs:275`,
  `lib.rs:278`, `lib.rs:614`, `lib.rs:842`, `node.rs:1167`
- **suffix on specific names**: `name.ends_with("+Errors")` (and `"/guide"`),
  at `node.rs:664`, `node.rs:1032`, `node.rs:1081`

A window named `<graph>/+New` is caught by neither. It starts with `/`, so
every prefix test passes it through; it is not `+Errors`, so it is
`WinKind::File` (`node.rs:664`) and not scratch to `winclean`
(`node.rs:1032`). Reading `Server::put`: `buf_name` is absolute, so the
relative-resolve arm at `lib.rs:275` is skipped and `name` stays
`<graph>/+New`; the guard at `lib.rs:278` tests that whole string for a leading
`+` and does not find one; `lib.rs:286` writes it. **`Put` on `<graph>/+New`
appears to create a file literally called `+New` in the graph root** — which,
for refl, means a `Put` before the reader has typed a heading drops a stray
file into the notes graph it is showing.

A bare `+New`, with no directory, would be refused with `Put: no file name`.
But a window with no directory has no directory context either, which a
tool's scratch window wants.

## `Get` is the one exception, and shows the shape of a fix

`Get` is rule-aware. `lib.rs:850` calls `get_rule_request` (`lib.rs:1430`)
before the builtin, and `Node::exec` separately asks `get_uses_rule`
(`node.rs:1356`) so that a rule-handled `Get` skips the dirty-window warning
(`node.rs:1365`). refl relies on this: `Get` in its window re-runs whatever the
first line says.

It works, and it took two call sites in two crates to arrange, one in core and
one in the server, each hardcoding the word. That is the cost of doing it once
per verb.

## What would open this up

Not a proposal, just what refl would need, in order of how much it buys:

1. **A tool can take `Put` on a window it owns**, the way it can take `Get`.
   This alone makes the `+New` flow work, given (2).
2. **A tool can mark a buffer clean** — `Proposal::Clean`'s effect, reachable
   from the tool protocol. Without it (1) is only usable through the
   withdraw-and-re-exec dance.
3. **A window can be declared scratch** by the tool that made it, rather than
   by its name matching `+Errors`. Then `Put` on it is a tool's business, or an
   error, but never a file called `+New`.
4. **A tool can learn which verbs are available**, or offer a verb with an
   answer when the word is already taken. Today `Offer("New", …)` succeeds and
   is silently dead, which is the worst of the three possible outcomes.

A more general version of (1) — builtins asking the rules first, so a tool can
shadow any verb on its own windows — would cover `New` as well, and would
replace `get_rule_request` rather than adding to it. It also changes what every
existing builtin means in the presence of a tool, which is a much larger
question than this note.

## What refl does today

`Note TITLE` writes `notes/<slug>.md` and opens it with the cursor in the body.
`Note` with nothing after it and nothing swept writes `notes/untitled.md`
immediately, with an empty `# ` heading and the cursor in it, so the first
thing typed is the title. The note exists on disk before it is named, and its
filename does not follow the heading afterwards, because nothing in refl
renames a note.

That is the compromise the rest of this file explains: the note has to be
created up front, because the moment the reader would have named it — the
`Put` — is not a moment refl can be present for.
