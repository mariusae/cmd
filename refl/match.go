package main

// Query matching, following Reflect's index-free scan: a query folds to
// lowercase terms, and text matches when it contains every one of them.

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// foldText lowercases text without moving any offset: a rune whose lowercase
// form is a different width keeps its own case, so a folded offset is always
// an offset into the original. The alternative — folding and indexing the
// result — silently misplaces every match after the first such rune.
func foldText(text string) string {
	if isASCIILower(text) {
		return text
	}
	var folded strings.Builder
	folded.Grow(len(text))
	for _, character := range text {
		lower := unicode.ToLower(character)
		if utf8.RuneLen(lower) != utf8.RuneLen(character) {
			lower = character
		}
		folded.WriteRune(lower)
	}
	return folded.String()
}

func isASCIILower(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] >= 'A' && text[index] <= 'Z' || text[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// queryTerms splits a query into the folded terms every match must contain.
// Double quotes group words into one term, so a phrase can be searched whole.
func queryTerms(query string) []string {
	var terms []string
	var current strings.Builder
	quoted := false
	flush := func() {
		if current.Len() > 0 {
			terms = append(terms, foldText(current.String()))
			current.Reset()
		}
	}
	for _, character := range query {
		switch {
		case character == '"':
			quoted = !quoted
			if !quoted {
				flush()
			}
		case !quoted && unicode.IsSpace(character):
			flush()
		default:
			current.WriteRune(character)
		}
	}
	flush()
	return terms
}

// textMatches reports whether text contains every term.
func textMatches(text string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	folded := foldText(text)
	for _, term := range terms {
		if !strings.Contains(folded, term) {
			return false
		}
	}
	return true
}

// termRanges returns the byte ranges of every term occurrence in text, merged
// where they overlap, in order.
func termRanges(text string, terms []string) []span {
	folded := foldText(text)
	var ranges []span
	for _, term := range terms {
		for offset := 0; offset < len(folded); {
			found := strings.Index(folded[offset:], term)
			if found < 0 {
				break
			}
			start := offset + found
			ranges = append(ranges, span{from: start, to: start + len(term)})
			offset = start + len(term)
		}
	}
	sortSpans(ranges)
	var merged []span
	for _, candidate := range ranges {
		if last := len(merged) - 1; last >= 0 && candidate.from <= merged[last].to {
			if candidate.to > merged[last].to {
				merged[last].to = candidate.to
			}
			continue
		}
		merged = append(merged, candidate)
	}
	return merged
}

func sortSpans(spans []span) {
	for index := 1; index < len(spans); index++ {
		for at := index; at > 0 && spans[at].from < spans[at-1].from; at-- {
			spans[at], spans[at-1] = spans[at-1], spans[at]
		}
	}
}

// A lineMatch is one physical line of a note that contains every term.
type lineMatch struct {
	// number is the 1-based line, offset the line's first character, and
	// at the first term occurrence within it — where the block context is
	// anchored.
	number int
	offset int
	at     int
	text   string
}

// matchingLines finds the lines of content that contain every term.
func matchingLines(content string, terms []string) []lineMatch {
	if len(terms) == 0 {
		return nil
	}
	var matches []lineMatch
	offset, number := 0, 1
	for offset <= len(content) {
		end := lineEndAt(content, offset)
		line := content[offset:end]
		if textMatches(line, terms) {
			at := offset
			if ranges := termRanges(line, terms); len(ranges) > 0 {
				at = offset + ranges[0].from
			}
			matches = append(matches, lineMatch{
				number: number,
				offset: offset,
				at:     at,
				text:   line,
			})
		}
		if end >= len(content) {
			break
		}
		offset = end + 1
		number++
	}
	return matches
}
