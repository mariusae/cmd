package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureNoteFileUsesExistingWorkDirectoryScheme(t *testing.T) {
	home := t.TempDir()
	target := worktree{
		Path:  "/data/users/me/fbsource-2026-09-09-feature",
		Label: "2026-09-09-feature",
	}
	now := time.Date(2026, 9, 9, 8, 30, 0, 0, time.FixedZone("PDT", -7*60*60))
	path, err := ensureNoteFile(home, target, now)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(home, "work", target.Label, "working.md")
	if path != wantPath {
		t.Fatalf("note path = %q, want %q", path, wantPath)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`worktree: "2026-09-09-feature"`,
		`session: "work-2026-09-09-feature"`,
		`directory: "/data/users/me/fbsource-2026-09-09-feature"`,
		`role: "linked"`,
		`created_at: "2026-09-09T08:30:00-07:00"`,
		"# 2026-09-09-feature",
	} {
		if !strings.Contains(string(contents), want) {
			t.Fatalf("note missing %q:\n%s", want, contents)
		}
	}

	custom := []byte("keep my notes\n")
	if err := os.WriteFile(path, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureNoteFile(home, target, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(custom) {
		t.Fatalf("existing note was overwritten: %q", got)
	}
}

func TestMainWorktreeNoteUsesMainDirectory(t *testing.T) {
	path, err := notePath("/home/me", worktree{Path: "/repo/project", Main: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/home/me/work/main/working.md"; path != want {
		t.Fatalf("note path = %q, want %q", path, want)
	}
}

func TestNoteRejectsUnsafeHandle(t *testing.T) {
	if _, err := notePath(t.TempDir(), worktree{Path: "/repo/task", Label: "../task"}); err == nil {
		t.Fatal("notePath accepted an unsafe handle")
	}
}
