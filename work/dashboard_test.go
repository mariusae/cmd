package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderDashboardShowsPlainWorktreeRows(t *testing.T) {
	home := t.TempDir()
	store, err := newStateStore(home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "fbsource")
	working := root + "-working"
	done := root + "-done"
	idle := root + "-idle"
	repo := repository{
		MainRoot: root,
		Worktrees: []worktree{
			{Path: root, Main: true},
			{Path: working},
			{Path: done},
			{Path: idle},
		},
	}
	for _, state := range []agentState{
		{
			WorktreePath: working, RepositoryRoot: root, Status: "working",
			Agent: "codex", UpdatedAt: now.Add(-12 * time.Second).Unix(),
		},
		{
			WorktreePath: done, RepositoryRoot: root, Status: "done",
			Agent: "claude", Title: "Implement dashboard", UpdatedAt: now.Add(-2 * time.Minute).Unix(),
		},
	} {
		if err := store.update(state); err != nil {
			t.Fatal(err)
		}
	}

	text, err := renderDashboard(repo, store, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		directoryPath(working) + "\tworking\t11:59AM\tcodex",
		directoryPath(done) + "\tdone\t11:58AM\tclaude\tImplement dashboard",
		directoryPath(idle) + "\tidle",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, directoryPath(root)+"\t") {
		t.Fatalf("dashboard includes main worktree:\n%s", text)
	}
	if strings.Contains(text, "WORKTREE") || strings.Contains(text, "work dashboard") {
		t.Fatalf("dashboard contains decoration:\n%s", text)
	}
	later, err := renderDashboard(repo, store, now.Add(30*time.Second), nil)
	if err != nil {
		t.Fatal(err)
	}
	if later != text {
		t.Fatalf("dashboard changed only because time passed:\nbefore: %q\nafter:  %q", text, later)
	}
}

func TestRenderDashboardExpansionShowsTransitionHistory(t *testing.T) {
	store, err := newStateStore(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	root := "/repo"
	linked := "/repo-task"
	state := agentState{
		WorktreePath: linked, RepositoryRoot: root, Status: "working",
		Agent: "codex", SessionID: "one", UpdatedAt: 80,
	}
	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(
		`{"timestamp":"1970-01-01T00:01:30Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"All done.\n\n- Tests pass."}]}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	state.TranscriptPath = transcript
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	state.UpdatedAt = 85
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	state.Status = "done"
	state.UpdatedAt = 90
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	if err := store.toggleExpanded(linked); err != nil {
		t.Fatal(err)
	}

	text, err := renderDashboard(repository{
		MainRoot:  root,
		Worktrees: []worktree{{Path: root, Main: true}, {Path: linked}},
	}, store, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(text, " working\n"); got != 1 {
		t.Fatalf("working event count = %d, want 1:\n%s", got, text)
	}
	if !strings.Contains(text, "\t12:01AM complete (10s)\n\t\tAll done.\n\t\t\n\t\t- Tests pass.\n") {
		t.Fatalf("dashboard has no done history event:\n%s", text)
	}
}

func TestDashboardWorktreeAtPointIncludesExpandedRows(t *testing.T) {
	first := "/repo-first"
	second := "/repo-second"
	text := directoryPath(first) + "\tworking\t5:17AM\tcodex\n" +
		"\t5:17AM working\n" + directoryPath(second) + "\tidle\n"
	worktrees := []worktree{{Path: "/repo", Main: true}, {Path: first}, {Path: second}}

	point := utf8Point(text, "\t2026")
	if path, ok := dashboardWorktreeAtPoint(text, point, worktrees); !ok || path != first {
		t.Fatalf("detail point resolved to (%q, %v), want (%q, true)", path, ok, first)
	}
	point = utf8Point(text, directoryPath(second))
	if path, ok := dashboardWorktreeAtPoint(text, point, worktrees); !ok || path != second {
		t.Fatalf("second row resolved to (%q, %v), want (%q, true)", path, ok, second)
	}
}

func TestDashboardAgentAtPointFindsOnlyAgentField(t *testing.T) {
	first := "/repo-first"
	second := "/repo-second"
	text := directoryPath(first) + "\tworking\t5:17AM\tcodex\tA title\n" +
		directoryPath(second) + "\tdone\t5:18AM\tcodex\tAnother title\n"
	worktrees := []worktree{{Path: "/repo", Main: true}, {Path: first}, {Path: second}}

	point := utf8Point(text, "codex\tAnother")
	path, agent, ok := dashboardAgentAtPoint(text, point, worktrees)
	if !ok || path != second || agent != "codex" {
		t.Fatalf("agent point resolved to (%q, %q, %v)", path, agent, ok)
	}
	point += len([]rune("codex"))
	if path, agent, ok := dashboardAgentAtPoint(text, point, worktrees); !ok || path != second || agent != "codex" {
		t.Fatalf("agent boundary resolved to (%q, %q, %v)", path, agent, ok)
	}
	if _, _, ok := dashboardAgentAtPoint(text, utf8Point(text, "Another"), worktrees); ok {
		t.Fatal("title was treated as an agent target")
	}
}

func TestMatchingAgentWindowRequiresCurrentLiveApexLocation(t *testing.T) {
	state := agentState{
		Agent: "codex", ApexSocket: "/tmp/apex/main.sock",
		ApexSession: "code", ApexWindow: "12",
	}
	windows := []apexWindow{{ID: "12", Name: "/repo-task/-codex", Live: true}}
	if got, ok := matchingAgentWindow(state, "codex", "/repo-task", "/tmp/apex/main.sock", "code", windows); !ok || got != windows[0].Name {
		t.Fatalf("matchingAgentWindow = (%q, %v)", got, ok)
	}
	if _, ok := matchingAgentWindow(state, "codex", "/repo-task", "/tmp/apex/main.sock", "other", windows); ok {
		t.Fatal("matched an agent from another Apex session")
	}
	windows[0].Live = false
	if _, ok := matchingAgentWindow(state, "codex", "/repo-task", "/tmp/apex/main.sock", "code", windows); ok {
		t.Fatal("matched a window that is no longer live")
	}
}

func TestApexReplaceProgramEscapesText(t *testing.T) {
	if got, want := apexReplaceProgram("hello\nworld|back\\slash"), `,c|hello\nworld\|back\\slash|`; got != want {
		t.Fatalf("apex program = %q, want %q", got, want)
	}
}

func TestWorktreeStatusLine(t *testing.T) {
	now := time.Unix(1_000, 0).UTC()
	state := agentState{Status: "done", Agent: "codex", UpdatedAt: now.Add(-90 * time.Second).Unix()}
	if got, want := worktreeStatusLine("/repo-task", state, true, "Do the thing", now), "/repo-task/\tdone\t12:15AM\tcodex\tDo the thing"; got != want {
		t.Fatalf("status line = %q, want %q", got, want)
	}
	if got, want := worktreeStatusLine("/repo-idle", agentState{}, false, "", now), "/repo-idle/\tidle"; got != want {
		t.Fatalf("idle line = %q, want %q", got, want)
	}
}

func utf8Point(text, needle string) int {
	byteOffset := strings.Index(text, needle)
	if byteOffset < 0 {
		return -1
	}
	return len([]rune(text[:byteOffset]))
}
