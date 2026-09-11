package main

import (
	"reflect"
	"strings"
	"testing"
)

// contextAt is the block around the first occurrence of needle, the way a
// match anchors one.
func contextAt(t *testing.T, content, needle string, terms ...string) string {
	t.Helper()
	at := strings.Index(content, needle)
	if at < 0 {
		t.Fatalf("%q is not in the fixture", needle)
	}
	return blockAt(t, content, at, terms...)
}

func blockAt(t *testing.T, content string, at int, terms ...string) string {
	t.Helper()
	blocks := prepareBlocks(content).blockContextsAt([]int{at}, termMatcher(terms))
	if len(blocks) == 0 {
		return ""
	}
	return blocks[0].text
}

// The cases below are Reflect's own block-context suite
// (packages/core/src/indexing/block-context.test.ts), with wiki-link targets
// read as query terms: this tool groups sibling branches by what matched the
// query where Reflect groups them by what they link to.
func TestBlockContext(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		needle  string
		terms   []string
		want    string
	}{{
		name:    "the whole paragraph, not just the physical line",
		content: "intro line\n\nfirst wrapped line with [[Target]]\nsecond wrapped line\n\nafter\n",
		needle:  "[[Target]]",
		want:    "first wrapped line with [[Target]]\nsecond wrapped line",
	}, {
		name:    "whole-file offsets map across frontmatter",
		content: "---\ntitle: Note\n---\n\na paragraph with [[Target]] inside\n",
		needle:  "[[Target]]",
		want:    "a paragraph with [[Target]] inside",
	}, {
		name: "a heading plus its section, stopping at the next heading of any level",
		content: strings.Join([]string{
			"# Title", "", "intro", "", "## Meeting [[Target]]", "",
			"notes for the meeting", "", "- a bullet", "", "### Sub", "", "unrelated", "",
		}, "\n"),
		needle: "[[Target]]",
		want:   "## Meeting [[Target]]\n\nnotes for the meeting\n\n- a bullet",
	}, {
		name:    "a trailing heading section runs to the end of the document",
		content: "## Heading [[Target]]\n\nlast paragraph\n",
		needle:  "[[Target]]",
		want:    "## Heading [[Target]]\n\nlast paragraph",
	}, {
		name:    "only the heading line for a match in the note's title H1",
		content: "# Meeting with [[Target]]\n\nagenda item one\n\nagenda item two\n",
		needle:  "[[Target]]",
		want:    "# Meeting with [[Target]]",
	}, {
		name:    "the section rule survives for a non-title H1",
		content: "# Title\n\nintro\n\n# Second [[Target]]\n\nsection body\n",
		needle:  "[[Target]]",
		want:    "# Second [[Target]]\n\nsection body",
	}, {
		name:    "the section rule survives for an H1 when frontmatter authors the title",
		content: "---\ntitle: Custom\n---\n\n# Heading [[Target]]\n\nsection body\n",
		needle:  "[[Target]]",
		want:    "# Heading [[Target]]\n\nsection body",
	}, {
		name:    "a setext H1 title reads like an ATX title",
		content: "With [[Target]]\n====\n\nbody paragraph\n",
		needle:  "[[Target]]",
		want:    "With [[Target]]\n====",
	}, {
		name:    "a top-level list item keeps every child, matching or not",
		content: "- kickoff with [[Target]]\n  - prep the agenda\n  - book the room\n- unrelated sibling\n",
		needle:  "[[Target]]",
		want:    "- kickoff with [[Target]]\n  - prep the agenda\n  - book the room",
	}, {
		name:    "task children stay inside a top-level item",
		content: "- [[Target]] kickoff\n  + [ ] prep agenda\n  + [x] send invite\n",
		needle:  "[[Target]]",
		want:    "- [[Target]] kickoff\n  + [ ] prep agenda\n  + [x] send invite",
	}, {
		name:    "a nested match sits under its parent line, without unmatched siblings",
		content: "- parent line\n  - mention of [[Target]]\n    - grandchild detail\n  - unrelated sibling\n",
		needle:  "[[Target]]",
		terms:   []string{"target"},
		want:    "- parent line\n  - mention of [[Target]]\n    - grandchild detail",
	}, {
		name:    "sibling branches that matched too are kept",
		content: "- parent line\n  - first [[Target]] mention\n  - also [[Target]] here\n  - nothing relevant\n",
		needle:  "[[Target]]",
		terms:   []string{"target"},
		want:    "- parent line\n  - first [[Target]] mention\n  - also [[Target]] here",
	}, {
		name:    "sibling branches match case-insensitively",
		content: "- parent line\n  - one [[Target]]\n  - two [[target]]\n",
		needle:  "[[Target]]",
		terms:   []string{"target"},
		want:    "- parent line\n  - one [[Target]]\n  - two [[target]]",
	}, {
		// An ordered item nested under an ordered item is deliberately absent:
		// CommonMark lets only `1.` interrupt a paragraph, so `2.` under
		// `1. parent line` is a continuation of that line, not a child.
		name:    "ordered lists keep their source numbering",
		content: "1. parent line\n   - mention of [[Target]]\n   - unrelated\n",
		needle:  "[[Target]]",
		terms:   []string{"target"},
		want:    "1. parent line\n   - mention of [[Target]]",
	}, {
		name:    "both paragraphs of a loose list item are kept",
		content: "- first paragraph\n\n  second [[Target]] paragraph\n",
		needle:  "[[Target]]",
		want:    "- first paragraph\n\n  second [[Target]] paragraph",
	}, {
		name:    "tab-indented nesting dedents by the parent indent only",
		content: "- top\n\t- middle\n\t\t- deep [[Target]]\n",
		needle:  "[[Target]]",
		want:    "- middle\n\t- deep [[Target]]",
	}, {
		name:    "exactly one ancestor level is climbed for a deeply nested match",
		content: "- top item\n  - middle item\n    - deep [[Target]] mention\n  - other branch\n",
		needle:  "[[Target]]",
		terms:   []string{"target"},
		want:    "- middle item\n  - deep [[Target]] mention",
	}, {
		name:    "blockquote chrome is stripped from a quoted paragraph",
		content: "> quoted [[Target]] mention\n> second quoted line\n",
		needle:  "[[Target]]",
		want:    "quoted [[Target]] mention\nsecond quoted line",
	}, {
		name:    "a match in a cell returns the whole table",
		content: "| a | b |\n| --- | --- |\n| [[Target]] | y |\n",
		needle:  "[[Target]]",
		want:    "| a | b |\n| --- | --- |\n| [[Target]] | y |",
	}} {
		t.Run(test.name, func(t *testing.T) {
			if got := contextAt(t, test.content, test.needle, test.terms...); got != test.want {
				t.Errorf("block context:\n got %q\nwant %q", got, test.want)
			}
		})
	}
}

func TestBlockContextFallsBackToTheBareLine(t *testing.T) {
	content := "first paragraph\n\nsecond paragraph\n"
	if got := blockAt(t, content, strings.Index(content, "\n\n")+1); got != "" {
		t.Errorf("an offset between blocks: got %q, want the empty context", got)
	}
}

func TestBlockContextCoversFencedCode(t *testing.T) {
	content := "intro\n\n```go\nfmt.Println(\"target\")\n```\n\nafter\n"
	want := "```go\nfmt.Println(\"target\")\n```"
	if got := contextAt(t, content, "target"); got != want {
		t.Errorf("fenced code block:\n got %q\nwant %q", got, want)
	}
}

func TestBlockContextsDeduplicate(t *testing.T) {
	content := "alpha target and target again\n"
	positions := []int{
		strings.Index(content, "target"),
		strings.LastIndex(content, "target"),
	}
	blocks := prepareBlocks(content).blockContextsAt(positions, termMatcher([]string{"target"}))
	if len(blocks) != 1 {
		t.Fatalf("two matches in one block: got %d contexts, want 1", len(blocks))
	}
	if blocks[0].line != 1 {
		t.Errorf("context line: got %d, want 1", blocks[0].line)
	}
}

func TestBlockContextsReportSourceLines(t *testing.T) {
	content := "---\ntitle: Note\n---\n\nfirst paragraph\n\n- second target\n"
	blocks := prepareBlocks(content).blockContextsAt(
		[]int{strings.Index(content, "target")}, termMatcher([]string{"target"}))
	if len(blocks) != 1 {
		t.Fatalf("got %d contexts, want 1", len(blocks))
	}
	if blocks[0].text != "- second target" {
		t.Errorf("text: got %q", blocks[0].text)
	}
	if blocks[0].line != 7 {
		t.Errorf("line: got %d, want 7 (the whole file's numbering)", blocks[0].line)
	}
}

func TestPreparedSourceServesSeveralContexts(t *testing.T) {
	content := "---\ntitle: Note\n---\n\nfirst [[Target]] mention\n\n- second [[Target]]\n"
	source := prepareBlocks(content)
	first := source.blockContextsAt([]int{strings.Index(content, "[[Target]]")}, nil)
	second := source.blockContextsAt([]int{strings.LastIndex(content, "[[Target]]")}, nil)
	if len(first) != 1 || first[0].text != "first [[Target]] mention" {
		t.Errorf("first context: got %+v", first)
	}
	if len(second) != 1 || second[0].text != "- second [[Target]]" {
		t.Errorf("second context: got %+v", second)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	for _, test := range []struct {
		name       string
		content    string
		raw        string
		body       string
		bodyOffset int
	}{{
		name:       "a fenced block is carved off",
		content:    "---\ntitle: Note\n---\nbody\n",
		raw:        "title: Note",
		body:       "body\n",
		bodyOffset: 20,
	}, {
		name:    "no fence leaves the source alone",
		content: "# Heading\n",
		body:    "# Heading\n",
	}, {
		name:    "an unterminated fence is tolerated as body",
		content: "---\ntitle: Note\n",
		body:    "---\ntitle: Note\n",
	}, {
		name:       "an empty block is still a block",
		content:    "---\n---\nbody\n",
		body:       "body\n",
		bodyOffset: 8,
	}} {
		t.Run(test.name, func(t *testing.T) {
			got := splitFrontmatter(test.content)
			if got.raw != test.raw || got.body != test.body || got.bodyOffset != test.bodyOffset {
				t.Errorf("got %+v, want raw %q body %q offset %d",
					got, test.raw, test.body, test.bodyOffset)
			}
		})
	}
}

func TestParseFrontmatter(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want frontmatterFields
	}{
		{name: "a title is read", raw: "title: A Note", want: frontmatterFields{title: "A Note"}},
		{name: "privacy is read", raw: "private: true", want: frontmatterFields{private: true}},
		{name: "privacy spelled as a string", raw: `private: "true"`, want: frontmatterFields{private: true}},
		{
			name: "aliases are read",
			raw:  "title: Mum\naliases:\n  - Charlotte\n  - \"  \"\n",
			want: frontmatterFields{title: "Mum", aliases: []string{"Charlotte"}},
		},
		{name: "broken YAML degrades quietly", raw: "title: [unclosed"},
		{name: "a non-mapping degrades quietly", raw: "- a list"},
		{name: "nothing is nothing", raw: "  \n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := parseFrontmatter(test.raw)
			if got.title != test.want.title || got.private != test.want.private ||
				!reflect.DeepEqual(got.aliases, test.want.aliases) {
				t.Errorf("got %+v, want %+v", got, test.want)
			}
		})
	}
}
