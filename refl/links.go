package main

// Wiki links and where they land.
//
// A `[[link]]` carries a note's name, never its path, so it resolves at read
// time against the graph's key map: a daily note answers to its date, every
// note to its title, and a note may claim further spellings through its
// `aliases:` frontmatter or a v1 `Subject // Alias` title. Reflect's tiers
// decide who wins a spelling two notes claim — date over title over alias, and
// within a tier the first path alphabetically.

import (
	"regexp"
	"sort"
	"strings"
)

// A wikiLink is one `[[target]]` in a note's body.
type wikiLink struct {
	key  string // the target, folded for matching
	date string // set when the target is a real YYYY-MM-DD
	at   int    // the link's offset in the body
}

var wikiLinkRe = regexp.MustCompile(`(!?)\[\[([^\[\]\n]*)\]\]`)

// wikiLinks finds a body's links. An embed of an attachment (`![[a.png]]`)
// names a file, not a note, and is never a link to one.
func wikiLinks(body string) []wikiLink {
	var links []wikiLink
	for _, found := range wikiLinkRe.FindAllStringSubmatchIndex(body, -1) {
		inner := body[found[4]:found[5]]
		if pipe := strings.IndexByte(inner, '|'); pipe >= 0 {
			inner = inner[:pipe]
		}
		target := strings.TrimSpace(inner)
		embed := body[found[2]:found[3]] == "!"
		if target == "" || embed && isAttachmentTarget(target) {
			continue
		}
		links = append(links, wikiLink{
			key:  foldText(target),
			date: calendarDate(target),
			at:   found[0],
		})
	}
	return links
}

// isAttachmentTarget reports whether a target names a stored file. A dotted
// target with an unknown extension is a note name that happens to hold a dot.
func isAttachmentTarget(target string) bool {
	dot := strings.LastIndexByte(target, '.')
	return dot >= 0 && attachmentExtensions[strings.ToLower(target[dot+1:])]
}

// attachmentExtensions are the formats Reflect stores under assets/. Kept
// short deliberately: a target that is not one of these is a note name.
var attachmentExtensions = map[string]bool{
	"avif": true, "bmp": true, "gif": true, "heic": true, "jpeg": true,
	"jpg": true, "m4a": true, "mov": true, "mp3": true, "mp4": true,
	"pdf": true, "png": true, "svg": true, "tif": true, "tiff": true,
	"wav": true, "webm": true, "webp": true,
}

// calendarDate is the ISO date a target names, or "". The check is
// calendar-valid, not shape-valid: no note is ever dated 2026-02-31.
func calendarDate(target string) string {
	if !dailyDateRe.MatchString(target) {
		return ""
	}
	return dailyDate(dailyDir + "/" + target + ".md")
}

var dailyDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// A keyMap is the graph's resolved address map: one winning note per spelling.
type keyMap struct {
	byKey  map[string]string // folded spelling -> graph-relative path
	byDate map[string]string // YYYY-MM-DD -> graph-relative path
}

// noteClaim is one note's claim on the graph's spellings.
type noteClaim struct {
	rel     string
	title   string
	aliases []string
}

// resolveKeys settles every spelling on the note that wins it, by Reflect's
// tiers: a daily's date first, then titles, then aliases, and within a tier
// the first path alphabetically.
func resolveKeys(claims []noteClaim) keyMap {
	sorted := append([]noteClaim(nil), claims...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].rel < sorted[j].rel })
	resolved := keyMap{byKey: map[string]string{}, byDate: map[string]string{}}
	for _, claim := range sorted {
		if date := dailyDate(claim.rel); date != "" {
			if _, taken := resolved.byDate[date]; !taken {
				resolved.byDate[date] = claim.rel
			}
		}
	}
	for _, spellingsOf := range []func(noteClaim) []string{titleSpelling, aliasSpellings} {
		for _, claim := range sorted {
			for _, spelling := range spellingsOf(claim) {
				key := foldText(strings.TrimSpace(spelling))
				if _, taken := resolved.byKey[key]; key == "" || taken {
					continue
				}
				resolved.byKey[key] = claim.rel
			}
		}
	}
	return resolved
}

func titleSpelling(claim noteClaim) []string { return []string{claim.title} }

// aliasSpellings are the further names a note answers to: its `aliases:`
// frontmatter, and the segments of a v1 `Subject // Alias` title.
func aliasSpellings(claim noteClaim) []string {
	return append(append([]string(nil), claim.aliases...), subjectSegments(claim.title)...)
}

// lookup is the note a link lands on, preferring an explicit daily date.
func (k keyMap) lookup(link wikiLink) (string, bool) {
	if link.date != "" {
		if rel, ok := k.byDate[link.date]; ok {
			return rel, true
		}
	}
	rel, ok := k.byKey[link.key]
	return rel, ok
}

// note is the note a reference names: a graph-relative path when it is one,
// else whatever spelling of it a link would resolve to.
func (k keyMap) note(ref string, notes map[string]string) (string, bool) {
	if _, ok := notes[ref]; ok {
		return ref, true
	}
	return k.lookup(wikiLink{key: foldText(strings.TrimSpace(ref)), date: calendarDate(strings.TrimSpace(ref))})
}

// spellingsOf is every way a link can address this note — what a backlink
// search looks for, and what groups the branches of one list item together.
func (k keyMap) spellingsOf(rel string) map[string]bool {
	spellings := map[string]bool{}
	for key, owner := range k.byKey {
		if owner == rel {
			spellings[key] = true
		}
	}
	for date, owner := range k.byDate {
		if owner == rel {
			spellings[foldText(date)] = true
		}
	}
	return spellings
}

// linkMatcher groups the branches of a list item the way Reflect does: by the
// note they point at, under any of its spellings, not by the words they use.
func linkMatcher(spellings map[string]bool) matcher {
	if len(spellings) == 0 {
		return nil
	}
	return func(text string) bool {
		for _, link := range wikiLinks(text) {
			if spellings[link.key] {
				return true
			}
		}
		return false
	}
}
