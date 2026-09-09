package main

import (
	"strings"
	"testing"
)

func TestParseSaplingWorktrees(t *testing.T) {
	worktrees, err := parseSaplingWorktrees([]byte(`[
  {"path":"/data/users/me/fbsource","role":"main","current":true},
  {"path":"/data/users/me/fbsource-2026-09-09-task","role":"linked","label":"2026-09-09-task","current":false}
]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("len = %d", len(worktrees))
	}
	if !worktrees[0].Main || !worktrees[0].Current {
		t.Fatalf("main worktree = %#v", worktrees[0])
	}
	if got := worktrees[1].handle(); got != "2026-09-09-task" {
		t.Fatalf("handle = %q", got)
	}
}

func TestParseSaplingWorktreesRejectsMissingPath(t *testing.T) {
	_, err := parseSaplingWorktrees([]byte(`[{"role":"main"}]`))
	if err == nil || !strings.Contains(err.Error(), "has no path") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseSaplingWorktreesRejectsInvalidJSON(t *testing.T) {
	_, err := parseSaplingWorktrees([]byte(`not json`))
	if err == nil || !strings.Contains(err.Error(), "parsing Sapling worktree list") {
		t.Fatalf("error = %v", err)
	}
}
