package main

// Querying the graph: the notes a query matches, the lines that matched, and
// the Markdown blocks around them.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// bodyMatches finds the matching lines of a note's text, numbered within the
// whole file. Frontmatter is metadata, not text: a query never matches it, and
// the title it may carry is matched as a title instead.
func bodyMatches(content string, terms []string) []lineMatch {
	split := splitFrontmatter(content)
	if split.bodyOffset == 0 {
		return matchingLines(content, terms)
	}
	shift := strings.Count(content[:split.bodyOffset], "\n")
	matches := matchingLines(split.body, terms)
	for index := range matches {
		matches[index].number += shift
		matches[index].offset += split.bodyOffset
		matches[index].at += split.bodyOffset
	}
	return matches
}

// A hit is one note that matches a query, with the lines and blocks that did.
type hit struct {
	note   note
	lines  []lineMatch
	blocks []blockContext
}

// bounds cap a result set. results counts what is printed — blocks with
// context, lines without — so one note's many matches cannot crowd out every
// other note. notes caps the notes themselves, which is what a paged window
// asks for. Zero is no bound.
type bounds struct {
	notes   int
	results int
}

func (b bounds) reached(notes, results int) bool {
	return b.notes > 0 && notes >= b.notes || b.results > 0 && results >= b.results
}

// keep trims a note's share of the results to what is left of the bound.
func (b bounds) keep(found, count int) int {
	if b.results > 0 && found+count > b.results {
		return b.results - found
	}
	return count
}

// search finds the notes a query matches, most recently changed first. A note
// matches when its text or its title contains every term. Blocks are extracted
// only when asked for: parsing a note costs more than scanning it, and the
// line form has no use for them.
func (g *graph) search(terms []string, withBlocks bool, limit bounds) ([]hit, error) {
	files, err := g.recent()
	if err != nil {
		return nil, err
	}
	match := termMatcher(terms)

	var hits []hit
	found := 0
	for _, file := range files {
		if limit.reached(len(hits), found) {
			break
		}
		content, meta, err := g.load(file)
		if err != nil || meta.private {
			continue
		}
		lines := bodyMatches(content, terms)
		if len(lines) == 0 && !textMatches(meta.title, terms) {
			continue
		}
		next := hit{note: noteOf(file, meta)}
		if withBlocks {
			next.blocks = blocksFor(content, meta.title, lines, match)
		} else {
			next.lines = linesFor(meta.title, lines)
		}
		next.blocks = truncate(next.blocks, limit.keep(found, len(next.blocks)))
		next.lines = truncate(next.lines, limit.keep(found, len(next.lines)))
		found += len(next.blocks) + len(next.lines)
		hits = append(hits, next)
	}
	return hits, nil
}

// blocksFor extracts the block around each matching line. A note matched only
// by its title has no line to anchor to, and stands for itself.
func blocksFor(content, title string, lines []lineMatch, match matcher) []blockContext {
	if len(lines) == 0 {
		return []blockContext{{text: displayTitle(title), line: 1}}
	}
	positions := make([]int, 0, len(lines))
	for _, line := range lines {
		positions = append(positions, line.at)
	}
	return prepareBlocks(content).blockContextsAt(positions, match)
}

// linesFor is the matching lines, or the title for a note matched only by it.
func linesFor(title string, lines []lineMatch) []lineMatch {
	if len(lines) == 0 {
		return []lineMatch{{number: 1, text: displayTitle(title)}}
	}
	return lines
}

func truncate[T any](values []T, limit int) []T {
	if limit < 0 {
		limit = 0
	}
	if len(values) > limit {
		return values[:limit]
	}
	return values
}

// backlinks finds the notes linking to a note, with the block around each
// link, most recently changed first. Links are matched by where they land, not
// by how they are spelled: a note reached through an alias or its date shows
// the same backlink as one reached through its title.
func (g *graph) backlinks(ref string, limit bounds) ([]hit, noteRef, error) {
	files, err := g.recent()
	if err != nil {
		return nil, noteRef{}, err
	}

	// Every note's claims, so a spelling two notes share resolves the way it
	// would when the link is followed.
	contents := make(map[string]string, len(files))
	claims := make([]noteClaim, 0, len(files))
	public := files[:0]
	for _, file := range files {
		content, meta, err := g.load(file)
		if err != nil || meta.private {
			continue
		}
		contents[file.rel] = content
		claims = append(claims, noteClaim{rel: file.rel, title: meta.title, aliases: meta.aliases})
		public = append(public, file)
	}
	keys := resolveKeys(claims)
	subject, ok := keys.note(ref, contents)
	if !ok {
		return nil, noteRef{}, fmt.Errorf("no note answers to %q", ref)
	}
	match := linkMatcher(keys.spellingsOf(subject))

	var hits []hit
	found := 0
	for _, file := range public {
		if limit.reached(len(hits), found) {
			break
		}
		if file.rel == subject {
			continue // a note linking to itself is not a backlink
		}
		body := splitFrontmatter(contents[file.rel])
		var positions []int
		for _, link := range wikiLinks(body.body) {
			if rel, ok := keys.lookup(link); ok && rel == subject {
				positions = append(positions, link.at+body.bodyOffset)
			}
		}
		if len(positions) == 0 {
			continue
		}
		meta, err := g.meta(file)
		if err != nil {
			continue
		}
		blocks := prepareBlocks(contents[file.rel]).blockContextsAt(positions, match)
		if len(blocks) == 0 {
			continue
		}
		blocks = truncate(blocks, limit.keep(found, len(blocks)))
		found += len(blocks)
		hits = append(hits, hit{note: noteOf(file, meta), blocks: blocks})
	}
	return hits, noteRef{rel: subject, title: g.titleOf(subject)}, nil
}

// A noteRef is a note settled on: where it lives, and what it is called.
type noteRef struct {
	rel   string
	title string
}

// titleOf names a note the graph has already read, else its file's own name.
func (g *graph) titleOf(rel string) string {
	if cached, ok := g.cache[filepath.Join(g.root, filepath.FromSlash(rel))]; ok {
		return cached.title
	}
	return strings.TrimSuffix(filepath.Base(rel), ".md")
}
