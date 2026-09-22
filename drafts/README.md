# `drafts`

`drafts` keeps a directory of documents being written: a flat directory of
Markdown files, one per draft.

There is no index and no database. A draft is a file, its title is the first
thing in it that reads like one, and its age is its last change in version
control, or the file's own time when it is unrecorded. That is the whole format,
which is what makes the directory readable by everything else on the machine.

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
them is a filing system, which is a different tool. The one directory under it
is `archive/`, where a draft goes when it is done with — and that is a flat
directory of drafts itself. An exact `README.md` documents either directory and
is ignored by the listing, search and timeline.

## What a draft is called

A frontmatter `title:`, else the first H1, else the first line with anything on
it, else the file's own name.

The first line stands in because a draft is often begun before it is titled,
and what someone wrote first is what the draft is about. Frontmatter is skipped
throughout: it is bookkeeping, and a draft named `id: 01j…` would be named
after nothing.

A new draft's filename is a projection of its title — lowercase, letters and
numbers kept, separator runs collapsed to one dash — and nothing renames it on
its own. The file is the draft, and a title that moves is not worth a path that
moves under everything pointing at it. `Rename` is how a filename catches up
with a title when that is what is wanted.

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
the form an editor or a shell can use directly. In a git or Sapling repository,
"newest" is the last commit that changed each file, not the filesystem time a
checkout happened to give it. Writing not yet committed keeps its file time.

## Timeline

`-t` prints recent modifications, newest first, as the full Markdown blocks
that changed. Blocks separated by no more than one blank line are joined into
excerpts of up to twenty lines; a semantic block longer than that is still kept
whole. Saves to the same nearby blocks of a draft within five minutes are one
entry showing the final text of that writing burst, rather than a stack of its
intermediate versions.

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
for what it opens with.

Reading the timeline walks the history and reads a file per change, which is
more work than a window has any business doing every couple of seconds, so the
answer is kept while the directory stands still.

## Version control

A drafts directory under git or Sapling is committed on every `Put`, and the
commit is pushed in the background.

Writing is the only thing asked of the writer; keeping what was written is not
a separate chore, and a draft kept nowhere but this disk is not kept. `Put`
waits for the disk, which is fast. It never waits for the network: the commit
is its own thread of work, the push another and the sync a third, so a remote
having a bad day costs nothing at the keyboard. A push that fails says so in
`+Errors`.

### Syncing

While the window is open the directory is synced every five minutes, and `Sync`
does it now: commit what was written here, fetch, take what was written
elsewhere, push the result. A sync that moved something says so in `+Errors`; a
sync that changed nothing says nothing unless you asked.

A merge that conflicts is undone rather than left behind — a directory half in
conflict renders as drafts full of markers, and a sync running behind the
writer's back has no business leaving one there to be discovered. The conflict
is reported and the directory is left where it was, to be merged by hand.

Only the commit is held to the drafts directory: a drafts directory nested in a
larger repository commits its own files and nothing else. Fetching, merging and
pushing are about the repository, because that is what a repository is, so a
nested directory syncs the whole of it.

A repository with nowhere to send its history is committed and left alone; a
directory under neither git nor Sapling is left entirely as it is, and says
nothing about it unless `Sync` asks.

Anything that writes to the repository takes its turn, so a commit never runs
into a merge. The turn is taken in the background, never at the keyboard.

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
    New [TITLE]       Begin a draft in a window of its own; `Put` there names
                      it. TITLE opens it as the heading, with no title the
                      selection is one, and with neither the window opens empty.
    Preview           Show the draft under the pointer as a page.
    Notes             Open `<name>-notes.md`, making it if it is not there yet.
    Rename            File the draft under the title it now carries, taking its
                      notes with it.
    Archive           Put the draft away, in the `archive` directory, taking
                      its notes with it.
    IncludeArchive    Show what is archived along with the drafts, or stop
                      showing it.
    Sync              Bring the directory and its remote into step now.

`New`, `Search`, `Notes`, `Rename`, `Archive` and `Sync` are offered on the
directory's own drafts too, so a draft open in the session can beget one, look
for a phrase in it, be written about, be filed afresh, be put away, or be sent
on. Those verbs are about real files, not about a window some tool made and
happened to name like one. `IncludeArchive` is not among them: it is about what
a window shows, and a draft's window shows a draft.

### Renaming

Nothing renames a draft on its own. A filename is a projection of a title, and
a title rewritten mid-draft would otherwise drag the path out from under
everything pointing at it — a link, a shell's history, another window. So the
name stays where it was put, and `Rename` is how it catches up when that is
what is wanted.

`Rename` reads the title the way the listing reads any draft, slugs it, and
moves the file there. The title is read from the **file**, not from whatever
window is showing it: the name follows what was saved, so a draft renamed is a
draft that has been written down. `Put` first, which is wanted anyway.

Notes go with their draft. A companion is named after the draft it belongs to,
and a draft that moved out from under one would leave it orphaned — so `Rename`
in a notes window renames the draft the notes belong to, which is what names
them both. Orphaned notes, having no draft, are renamed as the draft they are
listed as.

Nothing is overwritten. The new name is held before anything moves, a name
already taken takes the next one, and a draft retitled to where another draft's
notes live steps aside exactly as a new draft would. The two files never come
apart: a notes name that is taken sends the draft looking further on, and a
notes file that will not move puts the draft back where it was. The windows
showing the files follow them, so what is open stays open and its tag says
where it now lives.

### Archiving

A draft that is done with is not a draft being written, and a flat directory has
no way to say so about a file it keeps showing. `Archive` moves it into
`archive/` and out of the listing, the search and the timeline alike.

The filename goes along unchanged. A name is a projection of the title a draft
carried when it was filed, and being done with is not a retitling: what points
at the draft under that name points at it still, one directory down. Notes go
with their draft, exactly as they go with a `Rename`, and nothing is
overwritten — a name already taken in the archive takes the next one.

`IncludeArchive` shows what is there, and shows it no longer. It is a toggle
because there is nothing to say: what is archived is either in front of you or
it is not. It is not a view of its own — the archive appears in whichever view
is up, listing, search or timeline — and an archived draft's row says `archive`
after its time, so a window showing both says which is which.

The archive is still the drafts directory. An archived draft is opened, written,
renamed and committed like any other, and `Rename` there leaves it archived,
since renaming is about the name. On the command line it is a drafts directory
of its own:

```sh
drafts -d ~/drafts/archive foobar
```

### A draft begun before it is named

A draft's filename comes from its title, and the title is not known until
something has been written. So `New` writes nothing: the draft is begun in an
empty window called `/path/to/drafts+New`, whose `Put` `drafts` answers to. A
draft thrown away — `Del`, and no `Put` — leaves nothing behind.

A title given to `New` opens the window as its heading rather than naming a
file, so it is ordinary text that can be rewritten before it is ever a
filename. What names the file is whatever is there when `Put` comes, read the
way the listing reads any draft: a frontmatter `title:`, else the first H1,
else the first line with anything on it, else `untitled`.

The window is then that file's window: said to be clean, since what it holds is
now what is on disk, and renamed to the file, which is what puts the path in
its tag. The name comes last on purpose — until the window has it nothing is
watching the file, so what `drafts` wrote there is never taken for someone
else's change and read back over the writer's hands. Afterwards it is an
ordinary draft window, committed on `Put` like any other.

A begun draft is deliberately not claimed as one of `drafts`' own windows,
though `drafts` made it: it is a draft on its way to being a file, so it wants
what any file window has — `Undo` and `Redo` while it is written, `Put` in the
tag as soon as there is something to save, and `Del` asking before it throws
away what was typed.

### The window's state is its own text

`Get` does not remember what the window was showing; it reads the window's
first line and does what it says. `Search foobar` there runs that search,
`Timeline` shows recent modifications, and anything else brings every draft
back. So the way to change what the window shows is to edit it and ask:

```text
Search foobar             ← type this at the top, then B2 Get
Timeline                  ← or this
IncludeArchive Timeline   ← or this
```

`IncludeArchive` comes first when it comes, because it is said of whatever view
follows it rather than instead of one.

The three views are the whole vocabulary, and a line naming none of them is the
listing. The one place this misreads is a draft whose title begins with
`Search`, `Timeline` or `IncludeArchive` and happens to be the top row; `Get`
there lists the directory, which is where you already were.

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
