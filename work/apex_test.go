package main

import (
	"os"
	"testing"

	apexapi "github.com/mariusae/apex/go/apex"
)

func TestApexHookLocation(t *testing.T) {
	const sessionID = "f5f61b53-a693-4803-87d6-b2b1ae1d9b5b"
	environment := map[string]string{
		"APEX_SOCKET": "/tmp/apex-user/main.sock",
		"apexsession": sessionID,
		"winid":       "42",
	}
	getenv := func(key string) string { return environment[key] }
	socket, session, window := apexHookLocation(getenv)
	if socket != environment["APEX_SOCKET"] || session != sessionID || window != "42" {
		t.Fatalf("apexHookLocation = (%q, %q, %q)", socket, session, window)
	}
	environment["winid"] = ""
	if socket, session, window := apexHookLocation(getenv); socket != environment["APEX_SOCKET"] || session != sessionID || window != "" {
		t.Fatalf("windowless Apex location = (%q, %q, %q)", socket, session, window)
	}
	delete(environment, "apexsession")
	environment["APEX_SESSION"] = "tooling"
	if socket, session, window := apexHookLocation(getenv); socket != "" || session != "" || window != "" {
		t.Fatalf("non-Apex location = (%q, %q, %q)", socket, session, window)
	}
}

func TestApexAgentWindowInWorktreeUsesNewestLiveToolWindow(t *testing.T) {
	windows := []apexapi.WindowInfo{
		{ID: 10, Name: "/repo/task/-terminal", Live: true},
		{ID: 12, Name: "/repo/task/subdir/-agent", Live: true},
		{ID: 14, Name: "/repo/task/-work", Live: true},
		{ID: 16, Name: "/repo/task/-stale"},
		{ID: 18, Name: "/repo/other/-agent", Live: true},
	}
	if got, ok := apexAgentWindowInWorktree(windows, "/repo/task"); !ok || got != 12 {
		t.Fatalf("apexAgentWindowInWorktree = (%d, %v), want (12, true)", got, ok)
	}
}

func TestDiscoverApexAgentWindowIntegration(t *testing.T) {
	session := os.Getenv("WORK_APEX_TEST_SESSION")
	worktree := os.Getenv("WORK_APEX_TEST_WORKTREE")
	if session == "" || worktree == "" {
		t.Skip("set WORK_APEX_TEST_SESSION and WORK_APEX_TEST_WORKTREE")
	}
	window, ok := discoverApexAgentWindow(apexSocketPath(os.Getenv), session, worktree)
	if !ok || window == "" {
		t.Fatalf("discoverApexAgentWindow = (%q, %v)", window, ok)
	}
	if want := os.Getenv("WORK_APEX_TEST_WINDOW"); want != "" && window != want {
		t.Fatalf("discoverApexAgentWindow = %q, want %q", window, want)
	}
}
