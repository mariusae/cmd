package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// A page is one page of the wiki, as planned. Its text is DEST/SLUG.md.
type page struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Parent      string   `json:"parent,omitempty"`
	Description string   `json:"description"`
	Files       []string `json:"files"`           // the source the page rests on
	Stale       bool     `json:"stale,omitempty"` // its last writing failed
	Draft       bool     `json:"draft,omitempty"` // it was written as a draft
}

// state is DEST/wiki.json: the outline, and the tree it was written from. A
// wiki of a change has the change too, and its files are only those the
// change touches, each with a hash of its part of the patch.
type state struct {
	Name      string            `json:"name"`
	Source    string            `json:"source"`
	Revision  string            `json:"revision,omitempty"`
	LinkBase  string            `json:"link_base,omitempty"`
	Generated time.Time         `json:"generated"`
	Pages     []page            `json:"pages"`
	Files     map[string]string `json:"files"`
	Change    *change           `json:"change,omitempty"`
}

const stateFile = "wiki.json"

func loadState(dest string) (*state, error) {
	data, err := os.ReadFile(filepath.Join(dest, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.Join(dest, stateFile), err)
	}
	return &s, nil
}

func (s *state) save(dest string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(dest, stateFile), append(data, '\n'))
}

func (s *state) page(slug string) *page {
	for i := range s.Pages {
		if s.Pages[i].Slug == slug {
			return &s.Pages[i]
		}
	}
	return nil
}

type builder struct {
	source  string
	dest    string
	harness harness
	jobs    int // how many pages to write at once; 0 for all of them
	full    bool
	draft   bool
	change  *change // the change to write of, rather than the code base
	log     io.Writer

	mu   sync.Mutex
	cost float64
}

func (b *builder) logf(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	fmt.Fprintf(b.log, "wiki: "+format+"\n", args...)
}

func (b *builder) addCost(c float64) {
	b.mu.Lock()
	b.cost += c
	b.mu.Unlock()
}

// build writes or updates the wiki, renders it, and returns the path of its
// front page.
func (b *builder) build(ctx context.Context) (string, error) {
	if err := os.MkdirAll(b.dest, 0o755); err != nil {
		return "", err
	}
	old, err := loadState(b.dest)
	if err != nil {
		return "", err
	}
	if old != nil && old.Source != "" && old.Source != b.source {
		b.logf("note: %s was written from %s", b.dest, old.Source)
	}
	var t *tree
	if b.change != nil {
		t = b.change.tree(b.source)
	} else if t, err = scanTree(b.source); err != nil {
		return "", err
	}
	if len(t.files) == 0 {
		return "", fmt.Errorf("%s has no files", b.source)
	}

	next := &state{
		Name:     filepath.Base(b.source),
		Source:   b.source,
		Revision: t.revision,
		LinkBase: t.linkBase,
		Files:    t.files,
		Change:   b.change,
	}
	if b.change != nil {
		next.Name = filepath.Base(b.dest)
	}
	var (
		changed []string
		retold  bool // the change's commit messages are new
	)
	if old != nil && (b.full || (old.Change == nil) != (b.change == nil)) {
		old = nil
	}
	switch {
	case b.change != nil:
		// The pages of a change's wiki are always the same, and need no plan.
		next.Pages = changePages(t.sortedPaths())
		if old != nil {
			changed = changedFiles(old.Files, t.files)
			retold = !sameMessages(old.Change, b.change)
		}
	case old == nil || len(old.Pages) == 0:
		b.logf("planning %s from %d files", b.source, len(t.files))
		pages, err := b.plan(ctx, t, nil, nil)
		if err != nil {
			return "", err
		}
		next.Pages = pages
		old = nil
	default:
		changed = changedFiles(old.Files, t.files)
		if len(changed) == 0 {
			next.Pages = old.Pages
			break
		}
		b.logf("%d files changed; revising the outline", len(changed))
		pages, err := b.plan(ctx, t, old, changed)
		if err != nil {
			return "", err
		}
		next.Pages = pages
	}

	stale := stalePages(old, next.Pages, changed, b.dest, b.draft)
	if b.change != nil && (len(changed) > 0 || retold) {
		// Every page of a change's wiki rests on the whole of it.
		for _, p := range next.Pages {
			stale[p.Slug] = true
		}
	}
	if len(stale) == 0 {
		b.logf("nothing changed")
	}
	writeErr := b.writePages(ctx, t, old, next, stale, changed)

	if old != nil {
		for _, p := range old.Pages {
			if next.page(p.Slug) == nil {
				os.Remove(filepath.Join(b.dest, p.Slug+".md"))
			}
		}
	}
	next.Generated = time.Now().UTC().Truncate(time.Second)
	if len(stale) == 0 && len(changed) == 0 && !retold {
		next.Generated = old.Generated
	}
	if err := next.save(b.dest); err != nil {
		return "", err
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	index, err := render(b.dest, next, b.log)
	if err != nil {
		return "", err
	}
	if b.cost > 0 {
		b.logf("done, for $%.2f", b.cost)
	}
	return index, writeErr
}

// stalePages returns the slugs of the pages that need writing: new ones,
// ones whose subject has changed, ones resting on a changed file, ones whose
// last writing failed, ones whose file is gone, and unless drafting, drafts.
func stalePages(old *state, pages []page, changed []string, dest string, draft bool) map[string]bool {
	stale := map[string]bool{}
	for _, p := range pages {
		var was *page
		if old != nil {
			was = old.page(p.Slug)
		}
		switch {
		case was == nil, was.Stale, p.Stale, was.Draft && !draft,
			was.Title != p.Title, was.Description != p.Description:
			stale[p.Slug] = true
		default:
			for _, f := range changed {
				if covers(p.Files, f) || covers(was.Files, f) {
					stale[p.Slug] = true
					break
				}
			}
		}
		if _, err := os.Stat(filepath.Join(dest, p.Slug+".md")); err != nil {
			stale[p.Slug] = true
		}
	}
	return stale
}

func (b *builder) writePages(ctx context.Context, t *tree, old, next *state, stale map[string]bool, changed []string) error {
	var (
		wg    sync.WaitGroup
		sem   chan struct{}
		mu    sync.Mutex
		errs  []error
		done  int
		total = len(stale)
		// Read once here, as the pages are written to as they finish.
		outline = mustJSON(outlineOf(next.Pages, false))
	)
	if b.jobs > 0 {
		sem = make(chan struct{}, b.jobs)
	}
	if total > 0 {
		b.logf("writing %s", pluralize(total, "page"))
	}
	for i := range next.Pages {
		p := &next.Pages[i]
		if !stale[p.Slug] {
			if old != nil {
				if was := old.page(p.Slug); was != nil {
					p.Files = union(p.Files, was.Files)
					p.Draft = was.Draft
				}
			}
			continue
		}
		p.Stale = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			if sem != nil {
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()
			}
			start := time.Now()
			files, err := b.writePage(ctx, t, next.Name, outline, p, changed)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if ctx.Err() == nil {
					b.logf("%s: %v", p.Slug, err)
					errs = append(errs, fmt.Errorf("%s: %w", p.Slug, err))
				}
				return
			}
			p.Files = files
			p.Stale = false
			p.Draft = b.draft
			done++
			b.logf("wrote %s (%d/%d, %s)", p.Slug, done, total, time.Since(start).Round(time.Second))
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("%d of %d pages failed; run again to retry them", len(errs), total)
	}
	return ctx.Err()
}

// writePage has the agent write one page, or revise it when it was written
// before, and returns the files it rests on.
func (b *builder) writePage(ctx context.Context, t *tree, name, outline string, p *page, changed []string) ([]string, error) {
	path := filepath.Join(b.dest, p.Slug+".md")
	var previous string
	if data, err := os.ReadFile(path); err == nil {
		previous = string(data)
	}
	var relevant []string
	for _, f := range changed {
		if covers(p.Files, f) {
			relevant = append(relevant, f)
		}
	}
	var answer struct {
		Markdown string   `json:"markdown"`
		Sources  []string `json:"sources"`
	}
	prompt := pagePrompt(name, outline, p, previous, relevant, b.draft)
	if b.change != nil {
		prompt = diffPagePrompt(b.change, outline, p, previous, relevant, b.draft)
	}
	level := minor
	if p.Slug == "overview" || p.Slug == "walkthrough" {
		level = major
	}
	cost, err := b.harness.ask(ctx, b.source, prompt, pageSchema, level, &answer)
	b.addCost(cost)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(answer.Markdown)
	if text == "" {
		return nil, errors.New("the page came back empty")
	}
	if err := writeFile(path, []byte(text+"\n")); err != nil {
		return nil, err
	}
	files := p.Files
	for _, f := range append(answer.Sources, citedFiles(text)...) {
		if f = cleanPath(f); f != "" && t.has(f) {
			files = union(files, []string{f})
		}
	}
	return files, nil
}

// plan has the agent outline the wiki, or revise the outline of old in light
// of the changed files.
func (b *builder) plan(ctx context.Context, t *tree, old *state, changed []string) ([]page, error) {
	var answer struct {
		Pages []page `json:"pages"`
	}
	start := time.Now()
	cost, err := b.harness.ask(ctx, b.source, planPrompt(t, old, changed, b.draft), planSchema, major, &answer)
	b.addCost(cost)
	if err != nil {
		return nil, fmt.Errorf("planning: %w", err)
	}
	pages := tidyOutline(answer.Pages, t)
	if len(pages) == 0 {
		return nil, errors.New("planning: the outline came back empty")
	}
	b.logf("planned %d pages (%s)", len(pages), time.Since(start).Round(time.Second))
	return pages, nil
}

var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// tidyOutline makes an outline from the agent safe to use: slugs that are
// file names and unique, parents that exist and are not themselves children,
// children kept beside their parent, and only files that are in the tree.
func tidyOutline(planned []page, t *tree) []page {
	var pages []page
	seen := map[string]bool{}
	for _, p := range planned {
		p.Slug = slugify(p.Slug)
		if p.Slug == "" {
			p.Slug = slugify(p.Title)
		}
		p.Title = strings.TrimSpace(p.Title)
		if p.Slug == "" || p.Title == "" || p.Slug == "index" || seen[p.Slug] {
			continue
		}
		seen[p.Slug] = true
		var files []string
		for _, f := range p.Files {
			if f = cleanPath(f); f != "" && t.has(f) {
				files = union(files, []string{f})
			}
		}
		p.Files = files
		p.Parent = slugify(p.Parent)
		p.Stale = false
		pages = append(pages, p)
	}
	top := map[string]bool{}
	for i := range pages {
		if pages[i].Parent == pages[i].Slug || !seen[pages[i].Parent] {
			pages[i].Parent = ""
		}
	}
	for _, p := range pages {
		if p.Parent == "" {
			top[p.Slug] = true
		}
	}
	for i := range pages {
		if !top[pages[i].Parent] {
			pages[i].Parent = ""
		}
	}
	var ordered []page
	for _, p := range pages {
		if p.Parent != "" {
			continue
		}
		ordered = append(ordered, p)
		for _, c := range pages {
			if c.Parent == p.Slug {
				ordered = append(ordered, c)
			}
		}
	}
	return ordered
}

var citation = regexp.MustCompile(`\]\(([^)\s#]+)(?:#L\d+(?:-L?\d+)?)?\)`)

// citedFiles returns the targets of the links in a page that are not pages.
func citedFiles(text string) []string {
	var files []string
	for _, m := range citation.FindAllStringSubmatch(text, -1) {
		if !strings.Contains(m[1], "://") && !strings.HasSuffix(m[1], ".md") {
			files = append(files, m[1])
		}
	}
	return files
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "://") {
		return ""
	}
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "/")
	if p == "." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// writeFile replaces a file whole, so that an interrupted write never leaves
// half of one behind.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wiki-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
