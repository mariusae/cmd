package main

import (
	"strings"
	"testing"
)

func blockTexts(hits []hit) []string {
	var texts []string
	for _, matched := range hits {
		for _, block := range matched.blocks {
			texts = append(texts, matched.draft.name+":"+block.text)
		}
	}
	return texts
}

func wantBlocks(t *testing.T, hits []hit, want ...string) {
	t.Helper()
	got := blockTexts(hits)
	if len(got) != len(want) {
		t.Fatalf("got %d blocks %q, want %d %q", len(got), got, len(want), want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestSearchShowsTheBlockAMatchWasMadeIn(t *testing.T) {
	root := testDir(t, map[string]string{
		"foobar.md": "# Foobar\n\nlorem ipsum\nwrapped foobar over two lines\n\n" +
			"- priorities:\n  - land the foobar\n  - something else\n",
	}, "foobar.md")
	hits, err := openDir(root).search(queryTerms("foobar"), bounds{})
	if err != nil {
		t.Fatal(err)
	}
	wantBlocks(t, hits,
		// The title heading is its own line: taking its section would quote
		// the whole draft back.
		"foobar.md:# Foobar",
		"foobar.md:lorem ipsum\nwrapped foobar over two lines",
		// A nested item keeps its parent's line and the branches that matched.
		"foobar.md:- priorities:\n  - land the foobar",
	)
}

func TestSearchOrdersByRecencyAndSearchesNotes(t *testing.T) {
	root := testDir(t, map[string]string{
		"one.md":       "# One\n\nabout widgets\n",
		"one-notes.md": "# Notes on One\n\nmore about widgets\n",
	}, "one.md", "one-notes.md")
	hits, err := openDir(root).search(queryTerms("widgets"), bounds{})
	if err != nil {
		t.Fatal(err)
	}
	wantBlocks(t, hits,
		"one-notes.md:more about widgets",
		"one.md:about widgets",
	)
}

func TestSearchMatchesEveryTermAndQuotesAPhrase(t *testing.T) {
	root := testDir(t, map[string]string{
		"a.md": "# A\n\nair traffic control tower\nair and traffic apart\n",
	}, "a.md")
	d := openDir(root)
	hits, err := d.search(queryTerms(`"air traffic" control`), bounds{})
	if err != nil {
		t.Fatal(err)
	}
	wantBlocks(t, hits, "a.md:air traffic control tower\nair and traffic apart")

	if hits, err = d.search(queryTerms("traffic missing"), bounds{}); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("a term that matches nothing still hit: %q", blockTexts(hits))
	}
}

// A draft matched only by what it is called has no line to anchor to, and
// stands for itself.
func TestSearchMatchesTheTitle(t *testing.T) {
	root := testDir(t, map[string]string{
		"a.md": "---\ntitle: Chrysalis\n---\n\nnothing about it here\n",
	}, "a.md")
	hits, err := openDir(root).search(queryTerms("chrysalis"), bounds{})
	if err != nil {
		t.Fatal(err)
	}
	wantBlocks(t, hits, "a.md:Chrysalis")
}

// Frontmatter is bookkeeping, not text.
func TestSearchDoesNotMatchFrontmatter(t *testing.T) {
	root := testDir(t, map[string]string{
		"a.md": "---\nstatus: parked\n---\n\nthe body\n",
	}, "a.md")
	hits, err := openDir(root).search(queryTerms("parked"), bounds{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("frontmatter matched: %q", blockTexts(hits))
	}
}

// Line numbers are counted in the whole file, frontmatter and all, because
// that is where an editor will look.
func TestSearchLineNumbersCountThroughFrontmatter(t *testing.T) {
	root := testDir(t, map[string]string{
		"a.md": "---\ntitle: A\n---\n\nfirst\n\nthe widgets line\n",
	}, "a.md")
	hits, err := openDir(root).search(queryTerms("widgets"), bounds{})
	if err != nil {
		t.Fatal(err)
	}
	if got := hits[0].blocks[0].line; got != 7 {
		t.Errorf("line: got %d, want 7", got)
	}
}

func TestSearchBoundsCapResultsAndDrafts(t *testing.T) {
	root := testDir(t, map[string]string{
		"a.md": "# A\n\none widgets\n\ntwo widgets\n\nthree widgets\n",
		"b.md": "# B\n\nfour widgets\n",
	}, "b.md", "a.md")
	d := openDir(root)

	hits, err := d.search(queryTerms("widgets"), bounds{results: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(blockTexts(hits)); got != 2 {
		t.Errorf("results bound: got %d blocks, want 2", got)
	}
	if hits, err = d.search(queryTerms("widgets"), bounds{drafts: 1}); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].draft.name != "a.md" {
		t.Errorf("drafts bound: got %d hits, first %q", len(hits), hits[0].draft.name)
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	root := testDir(t, map[string]string{"a.md": "# A\n\nThe Widgets Are Here\n"}, "a.md")
	hits, err := openDir(root).search(queryTerms("WIDGETS are"), bounds{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].blocks[0].text, "Widgets") {
		t.Errorf("got %q", blockTexts(hits))
	}
}
