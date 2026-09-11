package main

import (
	"reflect"
	"testing"
)

func TestWikiLinks(t *testing.T) {
	body := "- land [[Chrysalis]] now\n" +
		"- see [[Project X|the project]]\n" +
		"- ![[photo.png]] is a file\n" +
		"- ![[Some Note]] is a note\n" +
		"- [[2026-09-10]] and [[2026-02-31]]\n" +
		"- [[  ]] is nothing\n"
	var keys []string
	for _, link := range wikiLinks(body) {
		keys = append(keys, link.key)
	}
	want := []string{"chrysalis", "project x", "some note", "2026-09-10", "2026-02-31"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("got %q, want %q", keys, want)
	}

	links := wikiLinks(body)
	if links[3].date != "2026-09-10" {
		t.Errorf("a real date is a daily reference: got %q", links[3].date)
	}
	if links[4].date != "" {
		t.Errorf("an impossible date is not: got %q", links[4].date)
	}
	if body[links[0].at:links[0].at+len("[[Chrysalis]]")] != "[[Chrysalis]]" {
		t.Errorf("a link's offset should be its own: got %d", links[0].at)
	}
}

func TestResolveKeysFollowsReflectsTiers(t *testing.T) {
	keys := resolveKeys([]noteClaim{
		{rel: "daily/2026-09-10.md"},
		{rel: "notes/the-tenth.md", title: "2026-09-10"},
		{rel: "notes/mum.md", title: "Charlotte MacCaw // Mum", aliases: []string{"Ma"}},
		{rel: "notes/other-mum.md", title: "Someone Else", aliases: []string{"Mum"}},
		{rel: "notes/atlas.md", title: "Project Atlas"},
	})
	for _, test := range []struct {
		name   string
		target string
		want   string
	}{
		{name: "a daily's date beats a note titled with it", target: "2026-09-10", want: "daily/2026-09-10.md"},
		{name: "a title resolves", target: "Project Atlas", want: "notes/atlas.md"},
		{name: "case does not matter", target: "project atlas", want: "notes/atlas.md"},
		{name: "a v1 subject segment is an alias", target: "Charlotte MacCaw", want: "notes/mum.md"},
		{name: "and so is its second segment", target: "Mum", want: "notes/mum.md"},
		{name: "an unclaimed spelling resolves to nothing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := keys.lookup(wikiLink{key: foldText(test.target), date: calendarDate(test.target)})
			if test.want == "" {
				if ok {
					t.Errorf("got %q, want nothing", got)
				}
				return
			}
			if !ok || got != test.want {
				t.Errorf("got (%q, %v), want %q", got, ok, test.want)
			}
		})
	}
}

func TestResolveKeysBreaksTiesByPath(t *testing.T) {
	keys := resolveKeys([]noteClaim{
		{rel: "notes/zebra.md", title: "Shared"},
		{rel: "notes/alpha.md", title: "Shared"},
	})
	if got, _ := keys.lookup(wikiLink{key: "shared"}); got != "notes/alpha.md" {
		t.Errorf("the first path alphabetically wins: got %q", got)
	}
}

func TestSpellingsOf(t *testing.T) {
	keys := resolveKeys([]noteClaim{
		{rel: "notes/mum.md", title: "Charlotte MacCaw // Mum", aliases: []string{"Ma"}},
		{rel: "daily/2026-09-10.md"},
	})
	got := keys.spellingsOf("notes/mum.md")
	for _, spelling := range []string{"charlotte maccaw // mum", "charlotte maccaw", "mum", "ma"} {
		if !got[spelling] {
			t.Errorf("%q should address the note; got %v", spelling, got)
		}
	}
	if daily := keys.spellingsOf("daily/2026-09-10.md"); !daily["2026-09-10"] {
		t.Errorf("a daily answers to its date: got %v", daily)
	}
}

func TestLinkMatcherGroupsBySubjectNotByWords(t *testing.T) {
	match := linkMatcher(map[string]bool{"chrysalis": true, "chry": true})
	if !match("- land [[Chry]] soon") {
		t.Error("a branch linking through an alias belongs to the subject")
	}
	if match("- chrysalis without a link") {
		t.Error("the bare word is not a link to it")
	}
	if match("- see [[Something Else]]") {
		t.Error("a link elsewhere does not belong")
	}
	if linkMatcher(nil) != nil {
		t.Error("no spellings, nothing to match")
	}
}
