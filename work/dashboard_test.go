package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apexapi "github.com/mariusae/apex/go/apex"
)

type recordingDashboardEditor struct {
	replacements []dashboardEdit
	selections   [][2]int
	shownOffsets []int
}

func (e *recordingDashboardEditor) Replace(q0, q1 int, text string) error {
	e.replacements = append(e.replacements, dashboardEdit{q0: q0, q1: q1, text: text})
	return nil
}

func (e *recordingDashboardEditor) Select(q0, q1 int) error {
	e.selections = append(e.selections, [2]int{q0, q1})
	return nil
}

func (e *recordingDashboardEditor) Show(at int) error {
	e.shownOffsets = append(e.shownOffsets, at)
	return nil
}

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
	home := t.TempDir()
	store, err := newStateStore(home, func(string) string { return "" })
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
	note := filepath.Join(home, "work", filepath.Base(linked), "working.md")
	if !strings.Contains(text, "\t"+note+"\n") {
		t.Fatalf("dashboard has no notes path %q:\n%s", note, text)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatalf("dashboard did not create notes file: %v", err)
	}
}

func TestRenderDashboardSeparatesSessionHistoryFromRecentGlobalEvents(t *testing.T) {
	home := t.TempDir()
	store, err := newStateStore(home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0).UTC()
	root := "/repo"
	firstPath := "/repo-first"
	secondPath := "/repo-second"
	first := agentState{WorktreePath: firstPath, RepositoryRoot: root, Agent: "codex"}
	second := agentState{WorktreePath: secondPath, RepositoryRoot: root, Agent: "claude"}
	transcript := filepath.Join(t.TempDir(), "current.jsonl")
	if err := os.WriteFile(transcript, []byte(fmt.Sprintf(
		`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Finished current work."}]}}`+"\n",
		now.Add(-5*time.Minute).Format(time.RFC3339),
	)), 0o600); err != nil {
		t.Fatal(err)
	}
	first.TranscriptPath = transcript
	for _, state := range []agentState{
		withEvent(first, "working", "old", now.Add(-3*time.Hour)),
		withEvent(first, "done", "old", now.Add(-150*time.Minute)),
		withEvent(first, "working", "current", now.Add(-20*time.Minute)),
		withEvent(first, "waiting", "current", now.Add(-15*time.Minute)),
		withEvent(second, "working", "other", now.Add(-10*time.Minute)),
		withEvent(first, "done", "current", now.Add(-5*time.Minute)),
	} {
		if err := store.update(state); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.toggleExpanded(firstPath); err != nil {
		t.Fatal(err)
	}

	text, err := renderDashboard(repository{
		MainRoot: root,
		Worktrees: []worktree{
			{Path: root, Main: true},
			{Path: firstPath},
			{Path: secondPath},
		},
	}, store, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstStart := strings.Index(text, directoryPath(firstPath)+"\t")
	secondStart := strings.Index(text, directoryPath(secondPath)+"\t")
	if firstStart < 0 || secondStart <= firstStart {
		t.Fatalf("could not isolate expanded session:\n%s", text)
	}
	expanded := text[firstStart:secondStart]
	wantSession := []string{
		"\t2:26AM working\n",
		"\t2:31AM waiting\n",
		"\t2:41AM complete (15m)\n",
	}
	last := -1
	for _, want := range wantSession {
		position := strings.Index(expanded, want)
		if position <= last {
			t.Fatalf("expanded session is missing or unordered at %q:\n%s", want, expanded)
		}
		last = position
	}
	if strings.Contains(expanded, "11:46PM") || strings.Contains(expanded, "12:16AM") {
		t.Fatalf("expanded session includes the previous session:\n%s", expanded)
	}
	if !strings.Contains(expanded, "\t\tFinished current work.\n") {
		t.Fatalf("expanded session has no assistant output:\n%s", expanded)
	}

	wantGlobal := "\n" +
		directoryPath(firstPath) + "\tcomplete (15m)\t2:41AM\tcodex\n" +
		"\tFinished current work.\n" +
		directoryPath(secondPath) + "\tworking\t2:36AM\tclaude\n" +
		directoryPath(firstPath) + "\twaiting\t2:31AM\tcodex\n" +
		directoryPath(firstPath) + "\tworking\t2:26AM\tcodex\n"
	if !strings.HasSuffix(text, wantGlobal) {
		t.Fatalf("dashboard has no reverse-chronological global events:\n%s", text)
	}
}

func TestRenderDashboardCapsRecentEventsAtTwenty(t *testing.T) {
	store, err := newStateStore(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0).UTC()
	root := "/repo"
	for index := 0; index < 21; index++ {
		state := agentState{
			WorktreePath:   fmt.Sprintf("/repo-task-%02d", index),
			RepositoryRoot: root,
			Status:         "working",
			Agent:          "codex",
			SessionID:      fmt.Sprintf("session-%02d", index),
			UpdatedAt:      now.Add(-time.Duration(index) * time.Minute).Unix(),
		}
		if err := store.update(state); err != nil {
			t.Fatal(err)
		}
	}

	text, err := renderDashboard(repository{
		MainRoot: root, Worktrees: []worktree{{Path: root, Main: true}},
	}, store, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != dashboardRecentEventLimit {
		t.Fatalf("dashboard lines = %d, want %d events:\n%s", len(lines), dashboardRecentEventLimit, text)
	}
	if !strings.Contains(lines[0], "/repo-task-00/") ||
		!strings.Contains(lines[len(lines)-1], "/repo-task-19/") ||
		strings.Contains(text, "/repo-task-20/") {
		t.Fatalf("dashboard did not retain the newest 20 events:\n%s", text)
	}
}

func TestRenderDashboardTracksNewestEventLine(t *testing.T) {
	store, err := newStateStore(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10_000, 0).UTC()
	root := "/répo"
	linked := "/répo-tâche"
	state := agentState{
		WorktreePath: linked, RepositoryRoot: root, Status: "working",
		Agent: "codex", SessionID: "current", UpdatedAt: now.Add(-time.Minute).Unix(),
	}
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(t.TempDir(), "current.jsonl")
	if err := os.WriteFile(transcript, []byte(fmt.Sprintf(
		`{"timestamp":%q,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Newest output."}]}}`+"\n",
		now.Format(time.RFC3339),
	)), 0o600); err != nil {
		t.Fatal(err)
	}
	state.Status = "done"
	state.TranscriptPath = transcript
	state.UpdatedAt = now.Unix()
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}

	rendered, err := renderDashboardView(repository{
		MainRoot: root,
		Worktrees: []worktree{
			{Path: root, Main: true},
			{Path: linked},
		},
	}, store, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.recentEvents == "" || !strings.HasSuffix(rendered.text, rendered.recentEvents) {
		t.Fatalf("recent events are not the dashboard suffix: %#v", rendered)
	}
	prefix := strings.TrimSuffix(rendered.text, rendered.recentEvents)
	if want := len([]rune(prefix)); rendered.newestEventStart != want {
		t.Fatalf("newest event start = %d, want %d", rendered.newestEventStart, want)
	}
	wantBlock := directoryPath(linked) + "\tcomplete (1m)\t2:46AM\tcodex\n\tNewest output.\n"
	if got := string([]rune(rendered.text)[rendered.newestEventStart:rendered.newestEventEnd]); got != wantBlock {
		t.Fatalf("newest event selection = %q, want %q", got, wantBlock)
	}
}

func TestUpdateDashboardWindowScrollsWhenRecentEventsChange(t *testing.T) {
	editor := &recordingDashboardEditor{}
	previous := dashboardRender{text: "old", recentEvents: "old event\n"}
	next := dashboardRender{
		text: "new", recentEvents: "new event\n",
		newestEventStart: 42, newestEventEnd: 67,
	}
	if err := updateDashboardWindow(editor, previous, next); err != nil {
		t.Fatal(err)
	}
	if len(editor.replacements) != 1 {
		t.Fatalf("replacements = %#v, want one", editor.replacements)
	}
	if len(editor.shownOffsets) != 1 || editor.shownOffsets[0] != 42 {
		t.Fatalf("shown offsets = %#v, want newest event", editor.shownOffsets)
	}
	if len(editor.selections) != 1 || editor.selections[0] != [2]int{42, 67} {
		t.Fatalf("selections = %#v, want complete newest event", editor.selections)
	}

	editor.shownOffsets = nil
	editor.selections = nil
	editor.replacements = nil
	unchangedEvents := next
	unchangedEvents.text = "new worktree title\n" + next.recentEvents
	if err := updateDashboardWindow(editor, next, unchangedEvents); err != nil {
		t.Fatal(err)
	}
	if len(editor.shownOffsets) != 0 {
		t.Fatalf("unchanged events were shown: %#v", editor.shownOffsets)
	}
	if len(editor.selections) != 0 {
		t.Fatalf("unchanged events were selected: %#v", editor.selections)
	}
	if len(editor.replacements) != 1 {
		t.Fatalf("changed dashboard was not updated: %#v", editor.replacements)
	}
}

func TestMinimalDashboardEditUsesCharacterOffsets(t *testing.T) {
	edit, changed := minimalDashboardEdit("æ status old tail", "æ status new tail")
	if !changed {
		t.Fatal("changed dashboard produced no edit")
	}
	want := dashboardEdit{q0: 9, q1: 12, text: "new"}
	if edit != want {
		t.Fatalf("minimalDashboardEdit = %#v, want %#v", edit, want)
	}
	if _, changed := minimalDashboardEdit("same", "same"); changed {
		t.Fatal("identical dashboards produced an edit")
	}
}

func TestDashboardWorktreeAtPointIncludesExpandedRows(t *testing.T) {
	first := "/repo-first"
	second := "/repo-second"
	text := directoryPath(first) + "\tworking\t5:17AM\tcodex\n" +
		"\t5:17AM working\n" + directoryPath(second) + "\tidle\n\n" +
		directoryPath(first) + "\twaiting\t5:18AM\tcodex\n"
	worktrees := []worktree{{Path: "/repo", Main: true}, {Path: first}, {Path: second}}

	point := utf8Point(text, "\t2026")
	if path, ok := dashboardWorktreeAtPoint(text, point, worktrees); !ok || path != first {
		t.Fatalf("detail point resolved to (%q, %v), want (%q, true)", path, ok, first)
	}
	point = utf8Point(text, directoryPath(second))
	if path, ok := dashboardWorktreeAtPoint(text, point, worktrees); !ok || path != second {
		t.Fatalf("second row resolved to (%q, %v), want (%q, true)", path, ok, second)
	}
	point = strings.LastIndex(text, directoryPath(first))
	if path, ok := dashboardWorktreeAtPoint(text, point, worktrees); !ok || path != first {
		t.Fatalf("global event resolved to (%q, %v), want (%q, true)", path, ok, first)
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

func TestDashboardAgentNavigationSupportsB3AndAgentVerb(t *testing.T) {
	path := "/repo-task"
	text := directoryPath(path) + "\tworking\t5:17AM\tcodex\tA title\n\t5:18AM activity\n"
	worktrees := []worktree{{Path: "/repo", Main: true}, {Path: path}}
	events := []apexapi.Plumb{
		{Verb: "plumb", Text: "codex", At: &apexapi.Span{Q0: utf8Point(text, "codex")}},
		{Verb: "Agent", At: &apexapi.Span{Q0: len([]rune(text))}},
	}
	for _, event := range events {
		gotPath, agent, ok := dashboardAgentForPlumb(text, event, worktrees)
		if !ok || gotPath != path || (event.Verb == "plumb" && agent != "codex") {
			t.Fatalf("dashboardAgentForPlumb(%q) = (%q, %q, %v)", event.Verb, gotPath, agent, ok)
		}
	}
	at := &apexapi.Span{Q0: utf8Point(text, "codex")}
	if _, _, ok := dashboardAgentForPlumb(text, apexapi.Plumb{Verb: "plumb", Text: "claude", At: at}, worktrees); ok {
		t.Fatal("B3 navigation accepted a different agent")
	}
}

func TestMatchingAgentLocationIncludesOriginatingSession(t *testing.T) {
	const sessionID = "f5f61b53-a693-4803-87d6-b2b1ae1d9b5b"
	state := agentState{
		Agent: "codex", ApexSocket: "/tmp/apex/main.sock",
		ApexSession: sessionID, ApexWindow: "12",
	}
	if got, ok := matchingAgentLocation(state, "codex", "/tmp/apex/main.sock"); !ok || got.session != sessionID || got.window != 12 {
		t.Fatalf("matchingAgentLocation = (%#v, %v)", got, ok)
	}
	if _, ok := matchingAgentLocation(state, "codex", "/tmp/other-apex/main.sock"); ok {
		t.Fatal("matched an agent from another Apex daemon")
	}
	state.ApexWindow = "not-a-window"
	if _, ok := matchingAgentLocation(state, "codex", "/tmp/apex/main.sock"); ok {
		t.Fatal("matched an invalid Apex window")
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
