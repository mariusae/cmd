package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInManifoldUsesBlockVariable(t *testing.T) {
	if inManifold(func(string) string { return "" }) {
		t.Fatal("inManifold without MANIFOLD_BLOCK")
	}
	environment := map[string]string{"MANIFOLD_BLOCK": "host/block", "apexsession": "1"}
	if !inManifold(func(key string) string { return environment[key] }) {
		t.Fatal("inManifold with MANIFOLD_BLOCK = false")
	}
}

const testManiLayout = `{
  "shown": "h/space-1",
  "spaces": [
    {"id": "h/space-1", "title": "2026-09-09-task", "columns": [
      {"tiles": [
        {"block": "h/terminal", "kind": "terminal", "title": "Terminal", "cwd": "/repo-task/fbcode"},
        {"block": "h/folder", "kind": "folder", "title": "fbcode", "path": "/repo-task/fbcode",
         "stack": [{"block": "h/stacked-agent", "kind": "agent", "title": "⠋ Fix the tests", "cwd": "/repo-task/fbcode"}]}
      ]},
      {"tiles": [{"block": "h/agent", "kind": "agent", "title": "Agent", "cwd": "/repo-task"}]}
    ]},
    {"id": "h/space-2", "title": "other", "columns": [
      {"tiles": [{"block": "h/other-agent", "kind": "agent", "title": "Other work", "cwd": "/elsewhere"}]}
    ]}
  ]
}`

func parseTestManiLayout(t *testing.T) maniLayout {
	t.Helper()
	var layout maniLayout
	if err := json.Unmarshal([]byte(testManiLayout), &layout); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestAgentTitlesFromManifoldReadsAgentBlocks(t *testing.T) {
	repo := repository{
		MainRoot: "/repo",
		Worktrees: []worktree{
			{Path: "/repo", Main: true},
			{Path: "/repo-task", Label: "2026-09-09-task"},
		},
	}
	titles := agentTitlesFromManifold(repo, parseTestManiLayout(t))
	if got := titles["/repo-task"]; got != "Fix the tests" {
		t.Fatalf("title = %q, want the stacked agent's title over the generic one", got)
	}
	if len(titles) != 1 {
		t.Fatalf("titles = %#v", titles)
	}
}

func TestManifoldAgentBlockFindsAgentInWorktree(t *testing.T) {
	block, ok := manifoldAgentBlock(parseTestManiLayout(t), "/repo-task")
	if !ok || block != "h/stacked-agent" {
		t.Fatalf("manifoldAgentBlock = (%q, %v)", block, ok)
	}
	if block, ok := manifoldAgentBlock(parseTestManiLayout(t), "/repo-other"); ok {
		t.Fatalf("manifoldAgentBlock outside worktrees = %q", block)
	}
}

func TestFindManifoldSpacePrefersRecordedIDOverTitle(t *testing.T) {
	layout := parseTestManiLayout(t)
	space, ok := findManifoldSpace(layout, "h/space-2", "2026-09-09-task")
	if !ok || space.ID != "h/space-2" {
		t.Fatalf("recorded space = (%q, %v)", space.ID, ok)
	}
	space, ok = findManifoldSpace(layout, "h/closed", "2026-09-09-task")
	if !ok || space.ID != "h/space-1" {
		t.Fatalf("space by title = (%q, %v)", space.ID, ok)
	}
	if block, ok := spaceFocusBlock(space); !ok || block != "h/stacked-agent" {
		t.Fatalf("spaceFocusBlock = (%q, %v)", block, ok)
	}
	space, ok = findManifoldSpace(layout, "", "task", "2026-09-09-task")
	if !ok || space.ID != "h/space-1" {
		t.Fatalf("space by older dated title = (%q, %v)", space.ID, ok)
	}
	if _, ok := findManifoldSpace(layout, "", "missing"); ok {
		t.Fatal("found a space for a missing title")
	}
}

func TestManifoldSpaceTitleDropsDate(t *testing.T) {
	tests := map[string]string{
		"2026-09-09-task":   "task",
		"2026-09-09-task-2": "task-2",
		"2026-13-09-task":   "2026-13-09-task",
		"2026-09-09-":       "2026-09-09-",
		"feature":           "feature",
	}
	for handle, want := range tests {
		if got := manifoldSpaceTitle(handle); got != want {
			t.Errorf("manifoldSpaceTitle(%q) = %q, want %q", handle, got, want)
		}
	}
}

func TestStartDirectoryResolvesRepositoryRelativePaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fbsource")
	linked := root + "-task"
	for _, directory := range []string{filepath.Join(root, "fbcode", "brick"), linked} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	repo := testRepository(root, linked)

	tests := map[string]string{
		"":                                    "",
		".":                                   "",
		"fbcode/brick/":                       filepath.Join("fbcode", "brick"),
		filepath.Join(root, "fbcode/brick"):   filepath.Join("fbcode", "brick"),
		filepath.Join(linked, "fbcode/brick"): filepath.Join("fbcode", "brick"),
	}
	for requested, want := range tests {
		got, err := startDirectory(repo, requested)
		if err != nil || got != want {
			t.Errorf("startDirectory(%q) = (%q, %v), want %q", requested, got, err, want)
		}
	}
	if _, err := startDirectory(repo, "fbcode/missing"); !errors.Is(err, errNoStartDirectory) {
		t.Errorf("missing directory error = %v", err)
	}
	for _, requested := range []string{"../elsewhere", "/elsewhere"} {
		if _, err := startDirectory(repo, requested); err == nil {
			t.Errorf("startDirectory(%q) accepted a directory outside the repository", requested)
		}
	}
}

func TestWorktreeStartFallsBackToRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "fbcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := worktree{Path: root}
	if got := worktreeStart(target, "fbcode"); got != filepath.Join(root, "fbcode") {
		t.Fatalf("worktreeStart = %q", got)
	}
	if got := worktreeStart(target, "missing"); got != root {
		t.Fatalf("worktreeStart for a missing directory = %q", got)
	}
	if got := worktreeStart(target, ""); got != root {
		t.Fatalf("worktreeStart without a directory = %q", got)
	}
}

func TestNewStartsSessionInSubdirectory(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "fbsource")
	if err := os.MkdirAll(filepath.Join(root, "fbcode", "brick"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(parent, "session.txt")
	hook := fmt.Sprintf("printf '%%s\\n' \"$WORK_START_PATH\" > %q", marker)
	writeConfig(t, home, "session:\n  switch: "+hook+"\n")
	b := &fakeBackend{
		repositories: map[string]repository{root: testRepository(root, root+"-old")},
		onAdd: func(path string) error {
			return os.MkdirAll(filepath.Join(path, "fbcode", "brick"), 0o755)
		},
	}
	c, _, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"add", "task", "fbcode/brick"}); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	wantPath := root + "-2026-09-09-task"
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wantPath, "fbcode", "brick") + "\n"; string(contents) != want {
		t.Fatalf("WORK_START_PATH = %q, want %q", contents, want)
	}

	store, err := newStateStore(home, c.getenv)
	if err != nil {
		t.Fatal(err)
	}
	recorded, found, err := store.worktreeSpace(wantPath)
	if err != nil || !found || recorded.StartDirectory != filepath.Join("fbcode", "brick") {
		t.Fatalf("worktreeSpace = (%#v, %v, %v)", recorded, found, err)
	}

	// Switching later starts where the worktree was added.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	b.repositories[root] = testRepository(root, wantPath)
	if code := c.run([]string{"switch", "task"}); code != 0 {
		t.Fatalf("switch returned %d: %s", code, stderr.String())
	}
	contents, err = os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wantPath, "fbcode", "brick") + "\n"; string(contents) != want {
		t.Fatalf("WORK_START_PATH after switch = %q, want %q", contents, want)
	}
}

func TestNewRejectsMissingSubdirectoryBeforeCreating(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "fbsource")
	b := &fakeBackend{repositories: map[string]repository{root: testRepository(root, root+"-old")}}
	c, _, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"new", "task", "fbcode/missing"}); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if b.addedPath != "" {
		t.Fatalf("created %s despite the missing start directory", b.addedPath)
	}
	if !strings.Contains(stderr.String(), "fbcode/missing") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRenderManifoldDashboardRowsAndExpansion(t *testing.T) {
	home := t.TempDir()
	store, err := newStateStore(home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "fbsource")
	done := root + "-2026-09-09-done"
	idle := root + "-2026-09-09-idle"
	repo := repository{
		MainRoot: root,
		Worktrees: []worktree{
			{Path: root, Main: true},
			{Path: done, Label: "2026-09-09-done"},
			{Path: idle, Label: "2026-09-09-idle"},
		},
	}
	if err := store.update(agentState{
		WorktreePath: done, RepositoryRoot: root, Status: "done", Agent: "claude",
		Title: "Recorded title", UpdatedAt: now.Add(-2 * time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.toggleExpanded(done); err != nil {
		t.Fatal(err)
	}

	widgets, err := renderManifoldDashboard(repo, store, now, map[string]string{done: "Live title"}, "")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(widgets)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, want := range []string{
		`"text":"2026-09-09-done"`,
		`"text":"done · 11:58AM · claude"`,
		`"text":"Live title"`,
		`"id":"space:2026-09-09-done"`,
		`"id":"expand:2026-09-09-done","label":"Collapse"`,
		`"id":"history:2026-09-09-done"`,
		`"text":"idle"`,
		`"disabled":true,"id":"agent:2026-09-09-idle"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("dashboard missing %s:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Recorded title") {
		t.Errorf("dashboard prefers the recorded title over the live one:\n%s", text)
	}
}
