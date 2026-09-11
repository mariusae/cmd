package main

import "testing"

func searchGraph(t *testing.T) *graph {
	t.Helper()
	return openGraph(testGraph(t, map[string]string{
		"daily/2026-09-10.md": "- priorities:\n  - land chrysalis\n  - something else\n",
		"notes/chrysalis.md":  "# Chrysalis\n\nthe design of chrysalis, wrapped\nover two lines\n",
		"notes/other.md":      "# Other\n\nnothing to see\n",
		"notes/secret.md":     "---\nprivate: true\n---\n# Secret\n\nchrysalis is mentioned here\n",
	}))
}

func TestSearchFindsLinesNewestFirst(t *testing.T) {
	hits, err := searchGraph(t).search(queryTerms("chrysalis"), false, bounds{})
	if err != nil {
		t.Fatal(err)
	}
	// testGraph stamps in name order, so notes/other.md is newest and
	// daily/2026-09-10.md oldest; only the two public matches should appear.
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].note.rel != "notes/chrysalis.md" || hits[1].note.rel != "daily/2026-09-10.md" {
		t.Fatalf("order: got %q and %q", hits[0].note.rel, hits[1].note.rel)
	}
	if len(hits[0].lines) != 2 {
		t.Errorf("got %d matching lines in chrysalis.md, want 2", len(hits[0].lines))
	}
	if hits[0].lines[0].number != 1 || hits[0].lines[0].text != "# Chrysalis" {
		t.Errorf("first line: got %+v", hits[0].lines[0])
	}
}

func TestSearchExtractsBlocks(t *testing.T) {
	hits, err := searchGraph(t).search(queryTerms("chrysalis"), true, bounds{})
	if err != nil {
		t.Fatal(err)
	}
	blocks := hits[1].blocks // daily/2026-09-10.md
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	want := "- priorities:\n  - land chrysalis"
	if blocks[0].text != want {
		t.Errorf("block:\n got %q\nwant %q", blocks[0].text, want)
	}
	if blocks[0].line != 1 {
		t.Errorf("block line: got %d, want 1", blocks[0].line)
	}
	// The title heading yields only its own line; the paragraph below it wraps
	// into one block, so the note contributes two.
	if len(hits[0].blocks) != 2 {
		t.Fatalf("got %d blocks in chrysalis.md, want 2", len(hits[0].blocks))
	}
	if hits[0].blocks[1].text != "the design of chrysalis, wrapped\nover two lines" {
		t.Errorf("wrapped paragraph: got %q", hits[0].blocks[1].text)
	}
}

func TestSearchHonoursTheLimitAcrossNotes(t *testing.T) {
	hits, err := searchGraph(t).search(queryTerms("chrysalis"), false, bounds{results: 1})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, matched := range hits {
		total += len(matched.lines)
	}
	if total != 1 {
		t.Errorf("got %d lines, want 1", total)
	}
}

func TestSearchMatchesATitleWithNoMatchingLine(t *testing.T) {
	g := openGraph(testGraph(t, map[string]string{
		"notes/a.md": "---\ntitle: Air Traffic Control\n---\n\nnothing else\n",
	}))
	hits, err := g.search(queryTerms("traffic"), true, bounds{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || len(hits[0].blocks) != 1 {
		t.Fatalf("got %+v", hits)
	}
	if hits[0].blocks[0].text != "Air Traffic Control" {
		t.Errorf("a title-only match stands for itself: got %q", hits[0].blocks[0].text)
	}
}

func TestSearchWithNoTermsFindsNothing(t *testing.T) {
	hits, err := searchGraph(t).search(nil, false, bounds{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits, want none", len(hits))
	}
}

func backlinkGraph(t *testing.T) *graph {
	t.Helper()
	return openGraph(testGraph(t, map[string]string{
		"notes/chrysalis.md": "---\naliases:\n  - Chry\n---\n\n# Chrysalis\n\nAbout [[Chrysalis]] itself.\n",
		"daily/2026-09-10.md": "- priorities:\n  - land [[Chrysalis]]\n  - unrelated thing\n" +
			"- talked to [[Chry]] people\n",
		"notes/planning.md":  "# Planning\n\nA paragraph mentioning [[Chrysalis]] in passing.\n",
		"notes/elsewhere.md": "# Elsewhere\n\nNothing about [[Something Else]].\n",
		"notes/secret.md":    "---\nprivate: true\n---\n# Secret\n\nAbout [[Chrysalis]].\n",
	}))
}

func TestBacklinksFindsLinksUnderEverySpelling(t *testing.T) {
	hits, found, err := backlinkGraph(t).backlinks("Chrysalis", bounds{})
	if err != nil {
		t.Fatal(err)
	}
	if found.title != "Chrysalis" || found.rel != "notes/chrysalis.md" {
		t.Errorf("subject: got %+v", found)
	}
	var got []string
	for _, matched := range hits {
		got = append(got, matched.note.rel)
	}
	// notes/planning.md is newest, then the daily; elsewhere links elsewhere,
	// secret is private, and a note's link to itself is not a backlink.
	want := []string{"notes/planning.md", "daily/2026-09-10.md"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBacklinksShowsTheBlockAroundEachLink(t *testing.T) {
	hits, _, err := backlinkGraph(t).backlinks("Chrysalis", bounds{})
	if err != nil {
		t.Fatal(err)
	}
	daily := hits[1]
	if len(daily.blocks) != 2 {
		t.Fatalf("got %d blocks, want 2: %+v", len(daily.blocks), daily.blocks)
	}
	// The nested link keeps its parent's line and drops the sibling that does
	// not point here; the alias link is a block of its own.
	if want := "- priorities:\n  - land [[Chrysalis]]"; daily.blocks[0].text != want {
		t.Errorf("first block:\n got %q\nwant %q", daily.blocks[0].text, want)
	}
	if want := "- talked to [[Chry]] people"; daily.blocks[1].text != want {
		t.Errorf("second block: got %q, want %q", daily.blocks[1].text, want)
	}
}

func TestBacklinksTakesAPathOrAName(t *testing.T) {
	g := backlinkGraph(t)
	for _, ref := range []string{"notes/chrysalis.md", "Chrysalis", "chrysalis", "Chry"} {
		hits, _, err := g.backlinks(ref, bounds{})
		if err != nil || len(hits) != 2 {
			t.Errorf("backlinks of %q: got %d hits, %v", ref, len(hits), err)
		}
	}
}

func TestBacklinksRefusesAnUnknownNote(t *testing.T) {
	if _, _, err := backlinkGraph(t).backlinks("No Such Note", bounds{}); err == nil {
		t.Error("a name no note answers to should be refused")
	}
}

func TestBacklinksHonoursTheNoteBound(t *testing.T) {
	hits, _, err := backlinkGraph(t).backlinks("Chrysalis", bounds{notes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("got %d hits, want 1", len(hits))
	}
}
