# `drafts`

`drafts` keeps a directory of documents being written: a flat directory of
Markdown files, one per draft.

There is no index and no database. A draft is a file, its title is the first
thing in it that reads like one, and its age is the file's own. That is the
whole format, which is what makes the directory readable by everything else on
the machine.

```sh
cd drafts
go build

./drafts foobar
./drafts
./drafts -t
./drafts -a
./drafts -a foobar
```

## Finding the directory

First match wins:

1. `-d DIR`
2. `$DRAFTS_DIR`
3. `~/drafts`

A directory that is not there yet is not an error: nothing is created to answer
a question, so a search of a directory that does not exist finds nothing. It is
made the first time something is written into it.

The listing is flat. A drafts directory is a directory of drafts; a tree of
them is a filing system, which is a different tool.

## What a draft is called

A frontmatter `title:`, else the first H1, else the first line with anything on
it, else the file's own name.

The first line stands in because a draft is often begun before it is titled,
and what someone wrote first is what the draft is about. Frontmatter is skipped
throughout: it is bookkeeping, and a draft named `id: 01j…` would be named
after nothing.

A new draft's filename is a projection of its title — lowercase, letters and
numbers kept, separator runs collapsed to one dash — and nothing renames it
afterwards. The file is the draft, and a title that moves is not worth a path
that moves under everything pointing at it.

## Notes

Beside a draft may lie its notes, `<name>-notes.md`: the same directory, the
same Markdown, the thinking about the draft rather than the draft.

A companion is left out of the listing, where it would stand beside its draft
as though it were another one — but only where the draft it belongs to is
there. A `-notes.md` with nothing to be the notes of is a draft called
Notes-something, or the notes of a draft since deleted; either way it is the
only copy of what is written in it, and it is not hidden.

The same rule runs the other way when a draft is made: a draft called "Meeting
Notes" files as `meeting-notes.md` when that is a name of its own, and takes
the next one where a draft called "Meeting" already lives there.

Notes are searched along with the drafts. Notes are written to be found again,
and a search that skipped them would be a search of half of what was written.

## Searching

A query is a list of terms, folded to lowercase; a line matches when it
contains every one of them, and double quotes group words into one term. Drafts
are searched most recently modified first.

```sh
drafts foobar
drafts "air traffic" control
```

Each match prints as the Markdown **block** it was made in, under the file and
line it came from:

```text
/Users/you/drafts/foobar.md:10
	lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do
	eiusmod tempor incididunt foobar ut labore et dolore magna aliqua
```

A draft is prose, and a line of prose is a wrap: the sentence it belongs to is
the smallest piece of it that still says something. The block rules are
Reflect's, ported here as [`refl`](../refl) ported them:

- a **paragraph** is taken whole, however it wraps;
- a **heading** carries its section, up to the next heading of any level;
- the draft's **title heading** — its first H1, unless frontmatter authors a
  `title:` — is only its own line, or a match on it would quote the entire
  draft back;
- a **top-level list item** keeps everything nested under it;
- a **nested list item** shows its parent's own line plus the branches under
  that parent which also matched, dropping the rest;
- a **table** is taken whole, and a blockquote loses its `>` chrome.

Blocks are sliced from the source as full lines, dedented to their own
indentation, and never truncated. Identical blocks are printed once. Markdown
is parsed with [goldmark](https://github.com/yuin/goldmark), so block structure
follows CommonMark.

With no query at all, the drafts are listed newest first, one path per line —
the form an editor or a shell can use directly.

## Timeline

`-t` prints recent modifications, newest first, as the blocks that changed.

```text
airport.md:13 8:21PM
	## A new section

	with a new paragraph about widgets.
second.md:5 7:53PM
	nothing much here
```

Under version control it reads the history: every `Put` is committed, so the
history is the record of the writing rather than a chore done to it afterwards.
Whatever is written but not yet committed comes first, being the newest of all,
and a draft never recorded is shown whole — there is no diff to say which part
of it is new, because all of it is.

Outside version control it reads the files' own times, and each draft stands
for what it opens with. Unlike a synced notes graph, a drafts directory's
modification times are real: nothing here rewrites them all at once.

Reading the timeline walks the history and reads a file per change, which is
more work than a window has any business doing every couple of seconds, so the
answer is kept while the directory stands still.

## Version control

A drafts directory under git or Sapling is committed on every `Put`, and the
commit is pushed in the background.

Writing is the only thing asked of the writer; keeping what was written is not
a separate chore, and a draft kept nowhere but this disk is not kept. `Put`
waits for the disk, which is fast. It never waits for the network: the commit
is its own thread of work and the push another, so a remote having a bad day
costs nothing at the keyboard. A push that fails says so in `+Errors`.

Everything asked of version control is held to the drafts directory, so a
drafts directory nested in a larger repository commits its own files and
nothing else. A repository with nowhere to send its history is committed and
left alone; a directory under neither git nor Sapling is left entirely as it
is, and says nothing about it.

`Put` is Apex's own word, taken here for the files of this directory. A rule
may take one so long as it says which windows it is about, and a handler that
refuses — `Put` with a filename after it, which is the writer saying where this
goes — hands the word back, so Apex does with it what it always does. It is
unlisted, which keeps it out of the tools menu: Apex already offers `Put` in
the tag of a window with a file behind it, and two of them would say there were
two.

## Apex

`-a` opens a window on the directory in the current Apex session: the drafts,
most recently modified first, ten at a time. The window ends on a `more` row
while there is more to show; Look (B3) there shows the next ten. Look on a
draft's row opens it in a window of its own, and on a block, the draft at that
line. A query on the command line opens the window already searching.

    Search [QUERY]    Narrow the window to matching drafts and their blocks,
                      for the query or for what is swept in the window it was
                      run in. Search with no query restores the full list.
    Timeline          Show recent modifications, as `-t` prints them.
    Get               Re-read the directory, showing whatever the window's own
                      first line says.
    New [TITLE]       Write `<slug>.md` for TITLE and open it, the cursor in
                      the body. With no title, the selection is one; with
                      neither, the draft opens with its heading empty and the
                      cursor in it.
    Preview           Show the draft under the pointer as a page.
    Notes             Open `<name>-notes.md`, making it if it is not there yet.

`New`, `Search` and `Notes` are offered on the directory's own drafts too, so a
draft open in the session can beget one, look for a phrase in it, or be written
about. Those verbs are about real files, not about a window some tool made and
happened to name like one.

### The window's state is its own text

`Get` does not remember what the window was showing; it reads the window's
first line and does what it says. `Search foobar` there runs that search,
`Timeline` shows recent modifications, and anything else brings every draft
back. So the way to change what the window shows is to edit it and ask:

```text
Search foobar             ← type this at the top, then B2 Get
Timeline                  ← or this
```

The three views are the whole vocabulary, and a line naming none of them is the
listing. The one place this misreads is a draft whose title begins with
`Search` or `Timeline` and happens to be the top row; `Get` there lists the
directory, which is where you already were.

Because the text is the state, a body that has been edited is the reader's: the
refresh that runs every couple of seconds leaves it alone until `Get` or
`Search` asks for it. Everything else is written back as the smallest edit that
does the job, so selection and scroll position survive.

### Preview

Apex previews a Markdown file itself — live, against the buffer, unsaved edits
and all — and offers `Preview` in the tag of every Markdown window, so a draft
open in the session already has it and `drafts` adds nothing there. What Apex
cannot do is preview a draft that is only a row in a listing, so `Preview`
there opens the draft and asks Apex for the preview it would have given anyway.

Run `drafts -help` for complete usage.
# drafts
