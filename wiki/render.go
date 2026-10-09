package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The HTML is a static site in DEST/html: a page for each page of the wiki,
// and index.html for the first. Mermaid and highlight.js come from a CDN and
// are applied in the browser; without them the diagrams show as their source.

//go:embed page.html
var pageHTML string

var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"inc": func(i int) int { return i + 1 },
}).Parse(pageHTML))

// renderDir renders the wiki in dest from its state, as -render does.
func renderDir(dest string) (string, error) {
	s, err := loadState(dest)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", fmt.Errorf("%s has no %s; is it a wiki?", dest, stateFile)
	}
	return render(dest, s)
}

type navEntry struct {
	Slug, Title, Number string
	Child, Current      bool
}

type tocEntry struct {
	ID, Text string
	Level    int
}

type sourceLink struct {
	Path, Href string
}

type pageView struct {
	Wiki       string
	Title      string
	Revision   string
	Generated  string
	Nav        []navEntry
	Sources    []sourceLink
	TOC        []tocEntry
	Content    template.HTML
	Prev, Next *navEntry
}

// render writes DEST/html from the Markdown pages and returns the path of
// index.html.
func render(dest string, s *state) (string, error) {
	out := filepath.Join(dest, "html")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	nav := navigation(s.Pages)
	l := &linker{pages: map[string]bool{}, files: s.Files, base: s.LinkBase}
	if s.Change != nil {
		// The wiki of a change knows only the files it changes, and cites
		// others besides.
		l.root = s.Source
	}
	if l.base == "" {
		// Files not on GitHub are linked where they are, relative to the
		// HTML so that the links need no file: URLs, which are refused.
		rel, err := filepath.Rel(out, s.Source)
		if err != nil {
			rel = s.Source
		}
		l.base = filepath.ToSlash(rel) + "/"
	}
	for _, p := range s.Pages {
		l.pages[p.Slug] = true
	}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(l, 100)),
		),
	)

	keep := map[string]bool{"index.html": true}
	for i, p := range s.Pages {
		source, err := os.ReadFile(filepath.Join(dest, p.Slug+".md"))
		if err != nil {
			source = []byte("# " + p.Title + "\n\n*This page has not been written yet.*\n")
		}
		view := pageView{
			Wiki:     s.Name,
			Title:    p.Title,
			Revision: shortRevision(s.Revision),
			Nav:      make([]navEntry, len(nav)),
		}
		if !s.Generated.IsZero() {
			view.Generated = s.Generated.Local().Format("January 2, 2006")
		}
		copy(view.Nav, nav)
		view.Nav[i].Current = true
		if i > 0 {
			view.Prev = &nav[i-1]
		}
		if i+1 < len(nav) {
			view.Next = &nav[i+1]
		}
		for _, f := range p.Files {
			view.Sources = append(view.Sources, sourceLink{Path: f, Href: l.base + f})
		}

		doc := md.Parser().Parse(text.NewReader(source))
		if title := takeTitle(doc, source); title != "" {
			view.Title = title
		}
		view.TOC = contents(doc, source)
		var content bytes.Buffer
		if err := md.Renderer().Render(&content, source, doc); err != nil {
			return "", fmt.Errorf("%s: %v", p.Slug, err)
		}
		view.Content = template.HTML(content.String())

		var page bytes.Buffer
		if err := pageTemplate.Execute(&page, view); err != nil {
			return "", err
		}
		names := []string{p.Slug + ".html"}
		if i == 0 {
			names = append(names, "index.html")
		}
		for _, name := range names {
			keep[name] = true
			if err := writeFile(filepath.Join(out, name), page.Bytes()); err != nil {
				return "", err
			}
		}
	}

	// Drop the pages of earlier outlines.
	if entries, err := os.ReadDir(out); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".html") && !keep[e.Name()] {
				os.Remove(filepath.Join(out, e.Name()))
			}
		}
	}
	return filepath.Join(out, "index.html"), nil
}

// navigation numbers the pages as DeepWiki does: 1, 2, 2.1, 2.2, 3.
func navigation(pages []page) []navEntry {
	nav := make([]navEntry, len(pages))
	top, sub := 0, 0
	for i, p := range pages {
		if p.Parent == "" || top == 0 {
			top++
			sub = 0
			nav[i] = navEntry{Slug: p.Slug, Title: p.Title, Number: fmt.Sprint(top)}
		} else {
			sub++
			nav[i] = navEntry{Slug: p.Slug, Title: p.Title, Number: fmt.Sprintf("%d.%d", top, sub), Child: true}
		}
	}
	return nav
}

func shortRevision(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// takeTitle removes the page's leading H1, which the layout shows itself, and
// returns its text.
func takeTitle(doc ast.Node, source []byte) string {
	first := doc.FirstChild()
	h, ok := first.(*ast.Heading)
	if !ok || h.Level != 1 {
		return ""
	}
	title := plainText(h, source)
	doc.RemoveChild(doc, h)
	return title
}

// contents lists the page's second- and third-level headings.
func contents(doc ast.Node, source []byte) []tocEntry {
	var toc []tocEntry
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		h, ok := n.(*ast.Heading)
		if !ok || h.Level < 2 || h.Level > 3 {
			continue
		}
		id, _ := h.AttributeString("id")
		idBytes, _ := id.([]byte)
		toc = append(toc, tocEntry{ID: string(idBytes), Text: plainText(h, source), Level: h.Level})
	}
	return toc
}

func plainText(n ast.Node, source []byte) string {
	var b strings.Builder
	ast.Walk(n, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Text:
			b.Write(n.Segment.Value(source))
			if n.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(n.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// A linker points a page's links where they belong in HTML — another page's
// HTML, or a source file — and marks its lines of citations.
type linker struct {
	pages map[string]bool
	files map[string]string
	root  string // where to look for files not listed, if anywhere
	base  string
}

func (l *linker) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	source := reader.Source()
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Link:
			n.Destination = []byte(l.resolve(string(n.Destination)))
		case *ast.Paragraph:
			if strings.HasPrefix(plainText(n, source), "Sources:") {
				n.SetAttributeString("class", []byte("sources"))
			}
		}
		return ast.WalkContinue, nil
	})
}

func (l *linker) resolve(dest string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.Contains(dest, ":") {
		return dest
	}
	path, frag, _ := strings.Cut(dest, "#")
	if frag != "" {
		frag = "#" + frag
	}
	if slug, ok := strings.CutSuffix(strings.TrimPrefix(path, "./"), ".md"); ok && l.pages[slug] {
		return slug + ".html" + frag
	}
	if p := cleanPath(path); p != "" && (hasPath(l.files, p) || onDisk(l.root, p)) {
		return l.base + p + frag
	}
	return dest
}
