package main

import (
	"reflect"
	"testing"
)

func TestFoldTextKeepsOffsets(t *testing.T) {
	for _, text := range []string{"Alpha BETA", "Ünïcödé Straße", "İstanbul", "plain"} {
		if got := foldText(text); len(got) != len(text) {
			t.Errorf("fold %q: got %q, which is %d bytes, not %d", text, got, len(got), len(text))
		}
	}
	if got := foldText("Alpha BETA"); got != "alpha beta" {
		t.Errorf("fold: got %q", got)
	}
}

func TestQueryTerms(t *testing.T) {
	for _, test := range []struct {
		query string
		want  []string
	}{
		{query: "chrysalis", want: []string{"chrysalis"}},
		{query: "  Air   Traffic ", want: []string{"air", "traffic"}},
		{query: `"air traffic" control`, want: []string{"air traffic", "control"}},
		{query: `"unclosed phrase`, want: []string{"unclosed phrase"}},
		{query: "   ", want: nil},
	} {
		if got := queryTerms(test.query); !reflect.DeepEqual(got, test.want) {
			t.Errorf("terms of %q: got %q, want %q", test.query, got, test.want)
		}
	}
}

func TestTextMatchesEveryTerm(t *testing.T) {
	terms := []string{"air", "control"}
	if !textMatches("Air traffic control tower", terms) {
		t.Error("a line with both terms should match")
	}
	if textMatches("air traffic", terms) {
		t.Error("a line missing a term should not match")
	}
	if textMatches("anything", nil) {
		t.Error("an empty query should match nothing")
	}
}

func TestMatchingLines(t *testing.T) {
	content := "first line\nsecond TARGET line\n\nfourth target\n"
	matches := matchingLines(content, []string{"target"})
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2", len(matches))
	}
	if matches[0].number != 2 || matches[0].text != "second TARGET line" {
		t.Errorf("first match: got %+v", matches[0])
	}
	if content[matches[0].at:matches[0].at+6] != "TARGET" {
		t.Errorf("first match anchors at %q", content[matches[0].at:matches[0].at+6])
	}
	if matches[1].number != 4 || matches[1].offset != len("first line\nsecond TARGET line\n\n") {
		t.Errorf("second match: got %+v", matches[1])
	}
}

func TestTermRangesMergeOverlaps(t *testing.T) {
	got := termRanges("targeting a target", []string{"target", "targeting"})
	want := []span{{from: 0, to: 9}, {from: 12, to: 18}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
