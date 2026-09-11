# `refl`

`refl` searches and follows a [Reflect](https://github.com/team-reflect/reflect-open)
notes graph: a directory of Markdown files, with daily notes in
`daily/YYYY-MM-DD.md` and everything else in `notes/`.

It reads the files directly. Nothing here talks to Reflect, and no index is
needed — the format is the interface.

```sh
cd refl
go build

./refl chrysalis
./refl -c chrysalis
./refl -t
./refl -a
./refl -a chrysalis
```

## Finding the graph

First match wins:

1. `-g DIR`
2. `$REFLECT_GRAPH`
3. The nearest ancestor of the working directory holding `.reflect/`,
   `daily/`, or `notes/`
4. `~/reflect`

Notes marked `private: true` in their frontmatter are never listed, matched, or
printed, and there is no flag that overrides that. `assets/`, `audio-memos/`,
`templates/` and hidden directories hold no notes.

## When a note changed

The last commit that touched a note, not the time its file was written: a
clone, a checkout, or a pull rewrites every file time at once, and a graph that
syncs would otherwise look like it changed all at once. A note git cannot date
— untracked, or written since its last commit — keeps its file time, which is
the only record of that change there is.

Reading the whole log costs about what reading the graph does, so the answer is
cached per graph under `$XDG_CACHE_HOME/refl` (the user cache directory) and
carried forward commit by commit. The graph's own directory is left alone: a
notes graph holds notes, and a tool's bookkeeping is not one.

## Searching

A query is a list of terms, folded to lowercase; a line matches when it
contains all of them, and double quotes group words into one term. Notes are
searched most recently modified first, and paths under the working directory
print relative to it.

```sh
refl chrysalis
refl "air traffic" control
```

Each match prints as one line:

```text
daily/2026-09-10.md:6 - land chrysalis
```

`-c` prints the Markdown **block** around each match instead of its line:

```text
daily/2026-09-10.md:5
	- priorities:
	  - land chrysalis
```

A block is the unit of meaning the match sits in, by the rules Reflect uses to
show a backlink (`packages/core/src/indexing/block-context.ts`, and its CLI
port `apps/cli/src/block_context.rs`):

- a **paragraph** is taken whole, however it wraps;
- a **heading** carries its section, up to the next heading of any level;
- the note's **title heading** — its first H1, unless frontmatter authors a
  `title:` — is only its own line, or a mention of the note's subject would
  inline the entire note;
- a **top-level list item** keeps everything nested under it;
- a **nested list item** shows its parent's own line plus the branches under
  that parent which also matched, dropping the rest. Exactly one ancestor
  level is climbed;
- a **table** is taken whole, and a blockquote loses its `>` chrome.

Blocks are sliced from the source as full lines, dedented to their own
indentation, and never truncated. Identical blocks are printed once.

Markdown is parsed with [goldmark](https://github.com/yuin/goldmark), so block
structure follows CommonMark where Reflect's own grammar is more permissive.
The one divergence worth naming: CommonMark lets only `1.` interrupt a
paragraph, so `2.` indented under `1. parent line` continues that line instead
of nesting beneath it.

## Timeline

`-t` prints recent changes, newest first, as the blocks that changed: whatever
is written but not yet committed, and then the graph's git history.

```text
daily/2026-09-10.md:10 8:21PM
	- Ant
notes/chartbook-week-37-2026.md:16 7:53PM
	![](assets/pasted-1789084403055.png)
```

## Apex

`-a` opens a window on the graph in the current Apex session: the notes that
changed most recently, newest first, one name per row, ten at a time. The
window ends on a `more` row while there is more to show; Look (B3) there shows
the next ten. Search, Timeline and backlinks windows page the same way. Look on a note
row opens that note, and on a block, the note at that line. A query on the
command line opens the window already searching.

    Search QUERY      Narrow the window to matching notes and their blocks.
                      Search with no query restores the full list.
    Timeline          Show recent changes, as `-t` prints them.
    Get               Re-read the graph, showing whatever the window's own
                      first line says.
    Backlinks [NOTE]  Open a window on what links to a note, with the block
                      around each link: the note named, the note the window
                      holds, or the note under the pointer.
    Note TITLE        Create `notes/<slug>.md` for TITLE and open it. With no
                      title, the selection is one.
    Today             Open today's daily note.
    Sync              Commit, merge and push the graph now.

`Note`, `Backlinks` and `Sync` are offered on the graph's own notes too, so a
note open in the session can ask what points at it, beget one, or be sent on.
Daily notes answer to `Yesterday`, `Today` and `Tomorrow`, which open the
neighbouring day — dailies are created lazily, so a day that does not exist yet
opens empty and is written on Put.

The verb is `Note`, not `New`: `New` is one of Apex's own commands, and those
take every B2 before a tool's verbs are tried, so no tool can answer to that
word.

### The window's state is its own text

`Get` does not remember what the window was showing; it reads the window's
first line and does what it says. `Search chrysalis` there runs that search,
`Timeline` shows recent changes, and anything else brings the whole graph back.
So the way to change what the window shows is to edit it and ask:

```text
Search chrysalis          ← type this at the top, then B2 Get
Timeline                  ← or this
```

The three views are the whole vocabulary, and a line naming none of them is the
note list. The one place this misreads is a note whose own name begins with
`Search` or `Timeline` and happens to be the top row; `Get` there lists the
graph, which is where you already were.

Because the text is the state, a body that has been edited is the reader's: the
refresh that runs every couple of seconds leaves it alone until `Get` or
`Search` asks for it. Everything else is written back as the smallest edit that
does the job, so selection and scroll position survive.

A backlinks window is a throwaway. It is named for the note it is about
(`/path/to/note.md+Backlinks`), pages like the main window, and goes when you
Del it. Asking twice for the same note refreshes the window already open.

### Syncing

While the window is open the graph is synced every five minutes, and `Sync`
does it now: commit what was written here, fetch, merge what was written
elsewhere, push the result. A sync that moved something says so in `+Errors`; a
sync that changed nothing says nothing unless you asked.

A merge that conflicts is undone rather than left behind — a graph half in
conflict renders as notes full of markers, and a background sync has no
business leaving one there to be discovered. The conflict is reported and the
graph is left where it was, to be merged by hand.

A graph with no remote is committed and left alone.

## Backlinks

A `[[link]]` carries a note's name, never its path, so it resolves against the
graph's key map: a daily note answers to its date, every note to its title, and
a note may claim further spellings through its `aliases:` frontmatter or a v1
`Subject // Alias` title. Where two notes claim one spelling, Reflect's tiers
decide — date over title over alias, and within a tier the first path
alphabetically.

Backlinks are grouped by where a link lands, not by how it is spelled: under a
list item, the branches kept beside a link are the ones pointing at the same
note, whether through its title, an alias, or its date.

## Creating a note

`Note TITLE` writes `notes/<slug>.md`, where the slug follows Reflect's rules:
lowercase, letters and numbers kept, separator runs collapsed to one dash,
capped at 60 code points, never empty and never a Windows device name. Other
scripts pass through untransliterated. A name already taken takes a numeric
suffix; nothing is ever overwritten. The note carries a freshly minted
lowercase ULID `id`, the durable identity Reflect gives a note it creates, so
the note survives every later rename of its file.

Reflect normalizes a title to NFC before slugging and this does not, which
would need a dependency for the one case it changes: a decomposed accent typed
on a Mac, where the filesystem composes the name anyway.

The window follows the graph on its own: a note saved elsewhere moves to the
top within a couple of seconds, and only what changed is rewritten, so the
reader's selection and scroll position survive the refresh.

Run `refl -help` for complete usage.
