package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStateStoreRoundTripAndDelete(t *testing.T) {
	home := t.TempDir()
	store, err := newStateStore(home, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	want := agentState{
		WorktreePath:   "/repo/it's-a-task",
		RepositoryRoot: "/repo",
		Status:         "done",
		Agent:          "codex",
		SessionID:      "session-1",
		Title:          "Implement feature",
		TranscriptPath: "/sessions/session-1.jsonl",
		ApexSocket:     "/tmp/apex-user/main.sock",
		ApexSession:    "code",
		ApexWindow:     "42",
		UpdatedAt:      time.Now().Unix(),
	}
	if err := store.update(want); err != nil {
		t.Fatal(err)
	}
	states, err := store.list("/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0] != want {
		t.Fatalf("states = %#v, want %#v", states, want)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v, want 0600", info.Mode().Perm())
	}
	if filepath.Base(store.path) != "work.db" {
		t.Fatalf("database path = %q", store.path)
	}
	if err := store.delete(want.WorktreePath); err != nil {
		t.Fatal(err)
	}
	states, err = store.list("/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("states after delete = %#v", states)
	}
}

func TestStateStoreUpsertReplacesPriorState(t *testing.T) {
	store, err := newStateStore(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	state := agentState{
		WorktreePath:   "/repo/task",
		RepositoryRoot: "/repo",
		Status:         "working",
		Agent:          "claude",
		UpdatedAt:      1,
	}
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	state.Status = "done"
	state.Title = "Finished"
	state.TranscriptPath = "/sessions/one.jsonl"
	state.UpdatedAt = 2
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	states, err := store.list("/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Status != "done" || states[0].Title != "Finished" || states[0].TranscriptPath != "/sessions/one.jsonl" {
		t.Fatalf("states = %#v", states)
	}
	events, err := store.listEvents(state.WorktreePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Status != "working" || events[1].Status != "done" {
		t.Fatalf("events = %#v", events)
	}
}

func TestStateStorePreservesOriginatingApexLocation(t *testing.T) {
	store, err := newStateStore(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	state := agentState{
		WorktreePath:   "/repo/task",
		RepositoryRoot: "/repo",
		Status:         "working",
		Agent:          "codex",
		SessionID:      "agent-session",
		ApexSocket:     "/tmp/apex/main.sock",
		ApexSession:    "f5f61b53-a693-4803-87d6-b2b1ae1d9b5b",
		ApexWindow:     "12",
		UpdatedAt:      1,
	}
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	state.Status = "done"
	state.SessionID = ""
	state.ApexSocket = ""
	state.ApexSession = ""
	state.ApexWindow = ""
	state.UpdatedAt = 2
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.get(state.WorktreePath)
	if err != nil || !found {
		t.Fatalf("get = (%#v, %v, %v)", got, found, err)
	}
	if got.ApexSocket != "/tmp/apex/main.sock" || got.ApexSession != "f5f61b53-a693-4803-87d6-b2b1ae1d9b5b" || got.ApexWindow != "12" {
		t.Fatalf("Apex location = (%q, %q, %q)", got.ApexSocket, got.ApexSession, got.ApexWindow)
	}
}

func TestStateStoreDeduplicatesRepeatedEventsAndTogglesExpansion(t *testing.T) {
	store, err := newStateStore(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	state := agentState{
		WorktreePath: "/repo/task", RepositoryRoot: "/repo", Status: "working",
		Agent: "codex", SessionID: "session", Title: "Original title",
		TranscriptPath: "/sessions/session.jsonl", UpdatedAt: 1,
	}
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	state.UpdatedAt = 2
	state.Title = ""
	state.TranscriptPath = ""
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	states, err := store.list("/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].UpdatedAt != 1 || states[0].Title != "Original title" || states[0].TranscriptPath != "/sessions/session.jsonl" {
		t.Fatalf("repeated status changed its timestamp: %#v", states)
	}
	events, err := store.listEvents(state.WorktreePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one deduplicated event", events)
	}
	if err := store.toggleExpanded(state.WorktreePath); err != nil {
		t.Fatal(err)
	}
	expanded, err := store.expandedWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if !expanded[state.WorktreePath] {
		t.Fatalf("expanded = %#v", expanded)
	}
	if err := store.toggleExpanded(state.WorktreePath); err != nil {
		t.Fatal(err)
	}
	expanded, err = store.expandedWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if expanded[state.WorktreePath] {
		t.Fatalf("expanded after second toggle = %#v", expanded)
	}
}

func TestStateStoreBackfillsTranscriptPointerOnExistingEvent(t *testing.T) {
	store, err := newStateStore(t.TempDir(), func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	state := agentState{
		WorktreePath: "/repo/task", RepositoryRoot: "/repo", Status: "done",
		Agent: "codex", SessionID: "session", UpdatedAt: 1,
	}
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	state.TranscriptPath = "/sessions/session.jsonl"
	state.UpdatedAt = 2
	if err := store.update(state); err != nil {
		t.Fatal(err)
	}
	events, err := store.listEvents(state.WorktreePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TranscriptPath != state.TranscriptPath {
		t.Fatalf("events = %#v", events)
	}
}
