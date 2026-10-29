package main

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The HTML is a static site in DEST/html: a page for each page of the wiki,
// and index.html for the first. Mermaid comes from a CDN and draws the diagrams
// in the browser; without it they show as pictures drawn with the wiki (see
// diagram.go), or failing those, as their source.

//go:embed page.html
var pageHTML string

var pageTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"inc":           func(i int) int { return i + 1 },
	"fonts":         func() template.CSS { return fontFaces },
	"mermaidConfig": func() template.JS { return template.JS(mermaidConfig) },
}).Parse(pageHTML))

// The type is mutable.org's: Lato, with code in Source Code Pro. Their Latin
// letters are embedded in every page as data: URLs: pages shown where a
// Content-Security-Policy allows no stylesheets or fonts from elsewhere, as
// Meta's artifacts are, still have them. The fonts are Fontsource's
// (@fontsource/lato 5.3.0 and @fontsource/source-code-pro 5.3.1), under the
// SIL Open Font License in fonts/.
//
//go:embed fonts/*.woff2
var fontFiles embed.FS

type font struct {
	family, style, weight, file string
}

var fonts = []font{
	{"Lato", "normal", "400", "lato-latin-400-normal.woff2"},
	{"Lato", "normal", "700", "lato-latin-700-normal.woff2"},
	{"Lato", "italic", "400", "lato-latin-400-italic.woff2"},
	{"Lato", "italic", "700", "lato-latin-700-italic.woff2"},
	{"Lato", "normal", "900", "lato-latin-900-normal.woff2"},
	{"Source Code Pro", "normal", "400", "source-code-pro-latin-400-normal.woff2"},
	{"Source Code Pro", "normal", "700", "source-code-pro-latin-700-normal.woff2"},
}

var fontFaces = template.CSS(fontFaceRules(fonts))

// fontFaceRules returns @font-face rules for fonts.
func fontFaceRules(fonts []font) string {
	var b strings.Builder
	for _, f := range fonts {
		woff2, err := fontFiles.ReadFile("fonts/" + f.file)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(&b, "  @font-face { font-family: %q; font-style: %s; font-weight: %s; font-display: swap; src: url(data:font/woff2;base64,%s) format(\"woff2\"); }\n",
			f.family, f.style, f.weight, base64.StdEncoding.EncodeToString(woff2))
	}
	return b.String()
}

// renderDir renders the wiki in dest from its state, as -render does.
func renderDir(dest string, log io.Writer) (string, error) {
	s, err := loadState(dest)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", fmt.Errorf("%s has no %s; is it a wiki?", dest, stateFile)
	}
	return render(dest, s, log)
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
// index.html. It notes on log when the diagrams cannot be drawn as pictures.
func render(dest string, s *state, log io.Writer) (string, error) {
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
	diagrams := &diagramRenderer{}
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
			parser.WithASTTransformers(util.Prioritized(l, 100)),
		),
		goldmark.WithRendererOptions(renderer.WithNodeRenderers(
			util.Prioritized(diagrams, 100),
			util.Prioritized(headingRenderer{}, 100),
		)),
	)

	// Every page is parsed before any is rendered, so that the pictures of
	// all their diagrams are drawn at once.
	sources := make([][]byte, len(s.Pages))
	docs := make([]ast.Node, len(s.Pages))
	var mermaid []string
	for i, p := range s.Pages {
		source, err := os.ReadFile(filepath.Join(dest, p.Slug+".md"))
		if err != nil {
			source = []byte("# " + p.Title + "\n\n*This page has not been written yet.*\n")
		}
		sources[i] = source
		docs[i] = md.Parser().Parse(text.NewReader(source))
		mermaid = append(mermaid, mermaidSources(docs[i], source)...)
	}
	var err error
	diagrams.pics, err = pictures(dest, mermaid)
	if err != nil {
		fmt.Fprintf(log, "wiki: diagrams have no pictures for where Mermaid cannot load: %v\n", err)
	}

	keep := map[string]bool{"index.html": true}
	for i, p := range s.Pages {
		source, doc := sources[i], docs[i]
		view := pageView{
			Wiki:     s.Name,
			Title:    p.Title,
			Revision: shortRevision(s.Revision),
			Nav:      make([]navEntry, len(nav)),
		}
		if !s.Generated.IsZero() {
			view.Generated = s.Generated.Local().Format("Jan 2, 2006")
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

// A headingRenderer writes headings with a link to themselves after their
// text, shown when the heading is pointed at, so that a section can be
// linked to.
type headingRenderer struct{}

func (headingRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindHeading, renderHeading)
}

func renderHeading(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	h := n.(*ast.Heading)
	if entering {
		fmt.Fprintf(w, "<h%d", h.Level)
		if h.Attributes() != nil {
			gmhtml.RenderAttributes(w, h, gmhtml.HeadingAttributeFilter)
		}
		w.WriteByte('>')
		return ast.WalkContinue, nil
	}
	if id, ok := h.AttributeString("id"); ok && h.Level > 1 {
		if id, ok := id.([]byte); ok {
			fmt.Fprintf(w, `<a class="anchor" href="#%s" aria-label="Link to this section">#</a>`, util.EscapeHTML(id))
		}
	}
	fmt.Fprintf(w, "</h%d>\n", h.Level)
	return ast.WalkContinue, nil
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
// HTML, or a source file — and marks its citations, the links to source
// files, and its lines of them.
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
			dest, cite := l.resolve(string(n.Destination))
			n.Destination = []byte(dest)
			if cite {
				n.SetAttributeString("class", []byte("cite"))
			}
		case *ast.Paragraph:
			if strings.HasPrefix(plainText(n, source), "Sources:") {
				n.SetAttributeString("class", []byte("sources"))
			}
		}
		return ast.WalkContinue, nil
	})
}

// resolve returns where a link goes in HTML, and whether it cites a source
// file.
func (l *linker) resolve(dest string) (string, bool) {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.Contains(dest, ":") {
		return dest, false
	}
	path, frag, _ := strings.Cut(dest, "#")
	if frag != "" {
		frag = "#" + frag
	}
	if slug, ok := strings.CutSuffix(strings.TrimPrefix(path, "./"), ".md"); ok && l.pages[slug] {
		return slug + ".html" + frag, false
	}
	if p := cleanPath(path); p != "" && (hasPath(l.files, p) || onDisk(l.root, p)) {
		return l.base + p + frag, true
	}
	return dest, false
}
