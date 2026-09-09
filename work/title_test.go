package main

import "testing"

func TestSanitizeAgentTitleMatchesWorkmuxRules(t *testing.T) {
	tests := map[string]string{
		"⠴ Implement dashboard":       "Implement dashboard",
		"OC | OC | Investigating bug": "Investigating bug",
		"Claude Code":                 "",
		"fish":                        "",
		"feature":                     "",
		"Useful title":                "Useful title",
	}
	for input, want := range tests {
		if got := sanitizeAgentTitle(input, "feature", "project"); got != want {
			t.Errorf("sanitizeAgentTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAgentTitlesFromApexWindowsUsesMostSpecificWorktree(t *testing.T) {
	repo := repository{
		MainRoot: "/repo",
		Worktrees: []worktree{
			{Path: "/repo", Main: true},
			{Path: "/repo/.worktrees/feature", Label: "feature"},
		},
	}
	titles := agentTitlesFromWindows(repo, []apexWindow{
		{ID: "1", Name: "/repo/.worktrees/feature/-⠋ Fix the tests", Live: true},
		{ID: "2", Name: "/repo/.worktrees/feature/main.go"},
	})
	if got := titles["/repo/.worktrees/feature"]; got != "Fix the tests" {
		t.Fatalf("title = %q", got)
	}
}

func TestParseApexWindows(t *testing.T) {
	windows := parseApexWindows("12\t>/repo/-Task title\n13\t*/repo/file.go\n")
	if len(windows) != 2 || windows[0].ID != "12" || windows[0].Name != "/repo/-Task title" || !windows[0].Live || windows[1].Live {
		t.Fatalf("windows = %#v", windows)
	}
}
