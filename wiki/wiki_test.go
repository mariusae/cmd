package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// fakeHarness answers plans with its outline and pages with a page naming
// their slug, and records what it was asked.
type fakeHarness struct {
	mu      sync.Mutex
	outline []page
	fail    map[string]bool // slugs whose pages fail
	plans   []string
	pages   map[string]string // slug → prompt
}

func (f *fakeHarness) ask(ctx context.Context, dir, prompt string, schema json.RawMessage, out any) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if string(schema) == string(planSchema) {
		f.plans = append(f.plans, prompt)
		data, _ := json.Marshal(map[string]any{"pages": f.outline})
		return 0.5, json.Unmarshal(data, out)
	}
	slug, title := promptField.FindStringSubmatch(prompt), promptTitle.FindStringSubmatch(prompt)
	if slug == nil || title == nil {
		return 0, errors.New("unexpected prompt")
	}
	if f.pages == nil {
		f.pages = map[string]string{}
	}
	f.pages[slug[1]] = prompt
	if f.fail[slug[1]] {
		return 0, errors.New("no")
	}
	md := "# " + title[1] + "\n\nAbout [the code](a.go#L1-L2).\n\n## Part\n\nText.\n\nSources: [b.go:3](b.go#L3)\n"
	data, _ := json.Marshal(map[string]any{"markdown": md, "sources": []string{"a.go", "missing.go"}})
	return 0.25, json.Unmarshal(data, out)
}

var (
	promptField = regexp.MustCompile(`(?m)^  slug: (.+)$`)
	promptTitle = regexp.MustCompile(`(?m)^  title: (.+)$`)
)

func (f *fakeHarness) reset() {
	f.plans = nil
	f.pages = nil
}

func (f *fakeHarness) written() []string {
	var slugs []string
	for s := range f.pages {
		slugs = append(slugs, s)
	}
	return union(nil, slugs)
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildAndUpdate(t *testing.T) {
	source := filepath.Join(t.TempDir(), "proj")
	dest := filepath.Join(t.TempDir(), "wiki")
	writeTree(t, source, map[string]string{
		"a.go":      "package a\n",
		"b.go":      "package a\n",
		"sub/c.go":  "package sub\n",
		".hidden/x": "skipped\n",
		"README.md": "proj\n",
	})
	f := &fakeHarness{outline: []page{
		{Slug: "overview", Title: "Overview", Description: "All of it.", Files: []string{"README.md"}},
		{Slug: "core-bits", Title: "Core", Parent: "overview", Description: "a and b.", Files: []string{"a.go", "nope.go"}},
		{Slug: "sub", Title: "Sub", Description: "The sub package.", Files: []string{"sub/"}},
	}}
	b := &builder{source: source, dest: dest, harness: f, jobs: 2, log: io.Discard}

	index, err := b.build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if index != filepath.Join(dest, "html", "index.html") {
		t.Errorf("index = %s", index)
	}
	if len(f.plans) != 1 || !reflect.DeepEqual(f.written(), []string{"core-bits", "overview", "sub"}) {
		t.Fatalf("plans %d, pages %v", len(f.plans), f.written())
	}
	if strings.Contains(f.plans[0], ".hidden") {
		t.Error("hidden file listed")
	}
	if b.cost != 1.25 {
		t.Errorf("cost = %v", b.cost)
	}
	s, err := loadState(dest)
	if err != nil || s == nil {
		t.Fatal(err)
	}
	core := s.page("core-bits")
	if core == nil || core.Parent != "overview" {
		t.Fatalf("core = %+v", core)
	}
	// Planned, reported and cited files that exist, and nothing else.
	if want := []string{"a.go", "b.go"}; !reflect.DeepEqual(core.Files, want) {
		t.Errorf("core files = %v, want %v", core.Files, want)
	}
	for _, name := range []string{"overview.md", "core-bits.md", "sub.md", "html/sub.html", "html/core-bits.html"} {
		if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
			t.Error(err)
		}
	}
	generated := s.Generated

	// Nothing changed: nothing is asked.
	f.reset()
	if _, err := b.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.plans) != 0 || len(f.pages) != 0 {
		t.Errorf("unchanged tree: plans %d, pages %v", len(f.plans), f.written())
	}
	if s, _ := loadState(dest); !s.Generated.Equal(generated) {
		t.Error("unchanged tree moved the update time")
	}

	// A file under sub/ changes: the outline is revised, and only sub is
	// rewritten, from its last version.
	f.reset()
	writeTree(t, source, map[string]string{"sub/c.go": "package sub // changed\n"})
	if _, err := b.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.plans) != 1 || !strings.Contains(f.plans[0], "sub/c.go") || !strings.Contains(f.plans[0], "The existing outline") {
		t.Errorf("revision plan: %v", f.plans)
	}
	if !reflect.DeepEqual(f.written(), []string{"sub"}) {
		t.Errorf("rewrote %v, want [sub]", f.written())
	}
	if p := f.pages["sub"]; !strings.Contains(p, "<previous-page>") || !strings.Contains(p, "sub/c.go") {
		t.Errorf("sub prompt lacks its previous version or the change:\n%s", p)
	}

	// The outline drops a page and a page fails: the dropped page goes, and
	// the failed one is written on the next run.
	f.reset()
	f.outline = f.outline[:2]
	f.outline[1].Description = "a and b, anew."
	f.fail = map[string]bool{"core-bits": true}
	writeTree(t, source, map[string]string{"a.go": "package a // changed\n"})
	if _, err := b.build(context.Background()); err == nil {
		t.Error("a failed page went unreported")
	}
	for _, name := range []string{"sub.md", "html/sub.html"} {
		if _, err := os.Stat(filepath.Join(dest, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived its page", name)
		}
	}
	if s, _ := loadState(dest); !s.page("core-bits").Stale {
		t.Error("failed page not marked stale")
	}
	f.reset()
	f.fail = nil
	if _, err := b.build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.plans) != 0 || !reflect.DeepEqual(f.written(), []string{"core-bits"}) {
		t.Errorf("retry: plans %d, pages %v", len(f.plans), f.written())
	}
}

func TestTidyOutline(t *testing.T) {
	tr := &tree{files: map[string]string{"a.go": "1", "d/e.go": "2"}}
	got := tidyOutline([]page{
		{Slug: "child", Title: "Child", Parent: "top", Files: []string{"./a.go", "../x", "d", "zz"}},
		{Slug: "Top!", Title: "Top"},
		{Slug: "top", Title: "Dup"},
		{Slug: "", Title: "From Title", Parent: "child"},
		{Slug: "index", Title: "Index"},
		{Slug: "orphan", Title: "Orphan", Parent: "gone"},
		{Slug: "self", Title: "Self", Parent: "self"},
		{Slug: "untitled", Title: " "},
	}, tr)
	var slugs []string
	for _, p := range got {
		slugs = append(slugs, p.Slug+"<"+p.Parent)
	}
	want := []string{"top<", "child<top", "from-title<", "orphan<", "self<"}
	if !reflect.DeepEqual(slugs, want) {
		t.Errorf("outline = %v, want %v", slugs, want)
	}
	if !reflect.DeepEqual(got[1].Files, []string{"a.go", "d"}) {
		t.Errorf("files = %v", got[1].Files)
	}
}

func TestChangedFiles(t *testing.T) {
	got := changedFiles(
		map[string]string{"same": "1", "edit": "1", "gone": "1"},
		map[string]string{"same": "1", "edit": "2", "new": "1"},
	)
	if want := []string{"edit", "gone", "new"}; !reflect.DeepEqual(got, want) {
		t.Errorf("changed = %v, want %v", got, want)
	}
}

func TestGithubBase(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:mariusae/cmd.git":       "https://github.com/mariusae/cmd",
		"https://github.com/mariusae/cmd":       "https://github.com/mariusae/cmd",
		"https://github.com/mariusae/cmd.git\n": "https://github.com/mariusae/cmd",
		"ssh://git@github.com/a/b.git":          "https://github.com/a/b",
		"https://gitlab.com/a/b.git":            "",
		"":                                      "",
	} {
		if got := githubBase(remote); got != want {
			t.Errorf("githubBase(%q) = %q, want %q", remote, got, want)
		}
	}
}

func TestDefaultDest(t *testing.T) {
	env := map[string]string{"HOME": "/home/u"}
	getenv := func(k string) string { return env[k] }
	if got, _ := defaultDest("/src/apex", getenv); got != "/home/u/wiki/apex" {
		t.Errorf("dest = %s", got)
	}
	env["WIKI_DIR"] = "/w"
	if got, _ := defaultDest("/src/apex", getenv); got != "/w/apex" {
		t.Errorf("dest = %s", got)
	}
}

func TestDecodeClaude(t *testing.T) {
	var out struct{ Msg string }
	cost, err := decodeClaude([]byte(`{"is_error":false,"total_cost_usd":0.5,"structured_output":{"msg":"hi"}}`), "", nil, &out)
	if err != nil || cost != 0.5 || out.Msg != "hi" {
		t.Errorf("got %v %v %+v", cost, err, out)
	}
	if _, err := decodeClaude([]byte(`{"is_error":true,"result":"Not logged in\nmore"}`), "", nil, &out); err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("error = %v", err)
	}
	if _, err := decodeClaude(nil, "boom\n", errors.New("exit 1"), &out); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v", err)
	}
}
