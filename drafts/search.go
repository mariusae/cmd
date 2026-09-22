package main

// Searching the drafts directory: the drafts a query matches, and the Markdown
// block around each match.
//
// A match is always shown as its block rather than its line. A draft is prose,
// and a line of prose is a wrap: the sentence it belongs to is the smallest
// piece of it that still says something.

import "strings"

// A hit is one file that matches a query, with the blocks that did.
type hit struct {
	draft  draft
	blocks []blockContext
}

// bounds cap a result set. results counts the blocks printed, so one draft's
// many matches cannot crowd out every other draft; drafts caps the files
// themselves, which is what a paged window asks for. Zero is no bound.
type bounds struct {
	drafts  int
	results int
}

func (b bounds) reached(drafts, results int) bool {
	return b.drafts > 0 && drafts >= b.drafts || b.results > 0 && results >= b.results
}

// keep trims a draft's share of the results to what is left of the bound.
func (b bounds) keep(found, count int) int {
	if b.results > 0 && found+count > b.results {
		return b.results - found
	}
	return count
}

// search finds the files a query matches, most recently modified first. A file
// matches when its text or its title contains every term.
//
// Notes files are searched along with the drafts they belong to: notes are
// written to be found again, and a search that skipped them would be a search
// of half of what was written here. They are only kept out of the listing,
// where a companion would stand beside its draft as though it were another one.
func (d *dir) search(terms []string, limit bounds) ([]hit, error) {
	files, err := d.walk()
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
		content, title, err := d.read(file)
		if err != nil {
			continue
		}
		lines := bodyMatches(content, terms)
		if len(lines) == 0 && !textMatches(title, terms) {
			continue
		}
		blocks := blocksFor(content, title, lines, match)
		blocks = truncate(blocks, limit.keep(found, len(blocks)))
		if len(blocks) == 0 {
			continue
		}
		found += len(blocks)
		hits = append(hits, hit{
			draft: draft{
				path: file.path, name: file.name, title: title, modTime: file.modTime,
			},
			blocks: blocks,
		})
	}
	return hits, nil
}

// bodyMatches finds the matching lines of a file's text, numbered within the
// whole file. Frontmatter is bookkeeping, not text: a query never matches it,
// and the title it may carry is matched as a title instead.
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

// blocksFor extracts the block around each matching line. A draft matched only
// by its title has no line to anchor to, and stands for itself.
func blocksFor(content, title string, lines []lineMatch, match matcher) []blockContext {
	if len(lines) == 0 {
		return []blockContext{{text: title, line: 1, endLine: 1}}
	}
	positions := make([]int, 0, len(lines))
	for _, line := range lines {
		positions = append(positions, line.at)
	}
	return prepareBlocks(content).blockContextsAt(positions, match)
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
