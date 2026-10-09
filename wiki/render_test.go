package main

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	l := &linker{
		pages: map[string]bool{"overview": true},
		files: map[string]string{"a.go": "1", "pkg/b.go": "2"},
		base:  "https://github.com/o/r/blob/abc/",
	}
	for dest, want := range map[string]string{
		"overview.md":        "overview.html",
		"./overview.md#flow": "overview.html#flow",
		"missing.md":         "missing.md",
		"a.go#L1-L9":         "https://github.com/o/r/blob/abc/a.go#L1-L9",
		"./pkg/b.go":         "https://github.com/o/r/blob/abc/pkg/b.go",
		"pkg":                "https://github.com/o/r/blob/abc/pkg",
		"other.go":           "other.go",
		"#section":           "#section",
		"https://x.org/a.go": "https://x.org/a.go",
		"mailto:a@b":         "mailto:a@b",
	} {
		if got := l.resolve(dest); got != want {
			t.Errorf("resolve(%q) = %q, want %q", dest, got, want)
		}
	}
}

func TestNavigation(t *testing.T) {
	nav := navigation([]page{
		{Slug: "a"}, {Slug: "b"}, {Slug: "b1", Parent: "b"}, {Slug: "b2", Parent: "b"}, {Slug: "c"},
	})
	var numbers []string
	for _, n := range nav {
		numbers = append(numbers, n.Number)
	}
	if want := []string{"1", "2", "2.1", "2.2", "3"}; !reflect.DeepEqual(numbers, want) {
		t.Errorf("numbers = %v, want %v", numbers, want)
	}
}

func TestRender(t *testing.T) {
	dest := t.TempDir()
	s := &state{
		Source: filepath.Join(dest, "..", "proj"),
		Name:   "proj",
		Files:  map[string]string{"a.go": "1"},
		Pages: []page{
			{Slug: "overview", Title: "Overview", Files: []string{"a.go"}},
			{Slug: "unwritten", Title: "Unwritten", Parent: "overview"},
		},
	}
	md := "# The *Real* Title\n\nSee [next](unwritten.md) and [code](a.go#L3).\n\n" +
		"## Flow & Data\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\nSources: [a.go:3](a.go#L3)\n\n### Detail\n\n<script>alert(1)</script>\n"
	if err := os.WriteFile(filepath.Join(dest, "overview.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dest, "html"), 0o755)
	os.WriteFile(filepath.Join(dest, "html", "old.html"), nil, 0o644)

	index, err := render(dest, s, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	for _, want := range []string{
		"<title>The Real Title · proj</title>",
		"<h1>The Real Title</h1>",
		`href="unwritten.html"`,
		`href="../../proj/a.go#L3"`,
		`<li><a href="../../proj/a.go">a.go</a></li>`,
		`<p class="sources">`,
		`class="language-mermaid"`,
		`<li class="l2"><a href="#flow--data">Flow &amp; Data</a>`,
		`<li class="l3"><a href="#detail">Detail</a>`,
		"Relevant source files",
		`<li class="top current"><a href="overview.html"><span class="num">1</span>`,
		`<span class="num">1.1</span>Unwritten</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// The fonts are in the page, for viewers that allow no stylesheets from
	// elsewhere.
	if strings.Count(page, "src: url(data:font/woff2;base64,d09GMg") != 2 || strings.Contains(page, "fonts.googleapis.com") {
		t.Error("the fonts are not embedded")
	}
	if strings.Count(page, "<h1") != 1 {
		t.Error("the title heading was not taken out of the content")
	}
	if strings.Contains(page, "<script>alert") {
		t.Error("raw HTML passed through")
	}
	if data, _ := os.ReadFile(filepath.Join(dest, "html", "unwritten.html")); !strings.Contains(string(data), "not been written yet") {
		t.Error("unwritten page not rendered as such")
	}
	if _, err := os.Stat(filepath.Join(dest, "html", "old.html")); !os.IsNotExist(err) {
		t.Error("stale HTML kept")
	}
}
