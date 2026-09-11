package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// clonedGraph is a graph and a clone of it, the two sides of a sync.
func clonedGraph(t *testing.T) (origin, clone string) {
	t.Helper()
	origin = gitGraph(t, map[string]string{"notes/shared.md": "# Shared\n\nfirst\n"})
	// A bare remote, so both sides can push to it.
	remote := filepath.Join(t.TempDir(), "remote.git")
	git(t, origin, 1, "init", "--bare", "-q", remote)
	git(t, origin, 1, "remote", "add", "origin", remote)
	git(t, origin, 1, "push", "-q", "-u", "origin", "HEAD")

	clone = filepath.Join(t.TempDir(), "clone")
	command := exec.Command("git", "clone", "-q", remote, clone)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v: %s", err, out)
	}
	git(t, clone, 1, "config", "user.email", "refl@example.com")
	git(t, clone, 1, "config", "user.name", "refl")
	return origin, clone
}

func TestSyncCommitsPushesAndPulls(t *testing.T) {
	origin, clone := clonedGraph(t)

	// Written here, and not yet anywhere else.
	fresh := filepath.Join(origin, notesDir, "fresh.md")
	if err := os.WriteFile(fresh, []byte("# Fresh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := openGraph(origin).sync()
	if err != nil {
		t.Fatal(err)
	}
	if report.committed != 1 || report.pushed != 1 || report.pulled != 0 {
		t.Fatalf("got %+v", report)
	}
	if got := report.String(); got != "1 file committed, 1 commit pushed" {
		t.Errorf("report: got %q", got)
	}

	// The other side takes it.
	report, err = openGraph(clone).sync()
	if err != nil {
		t.Fatal(err)
	}
	if report.pulled != 1 || report.committed != 0 || report.pushed != 0 {
		t.Fatalf("got %+v", report)
	}
	if _, err := os.Stat(filepath.Join(clone, notesDir, "fresh.md")); err != nil {
		t.Errorf("the note should have arrived: %v", err)
	}
}

func TestSyncIsQuietWhenNothingMoved(t *testing.T) {
	origin, _ := clonedGraph(t)
	report, err := openGraph(origin).sync()
	if err != nil {
		t.Fatal(err)
	}
	if !report.quiet() || report.String() != "" {
		t.Errorf("got %+v (%q)", report, report.String())
	}
}

func TestSyncUndoesAMergeItCannotMake(t *testing.T) {
	origin, clone := clonedGraph(t)
	shared := "notes/shared.md"

	// Both sides write the same line of the same note, differently.
	if err := os.WriteFile(filepath.Join(clone, filepath.FromSlash(shared)),
		[]byte("# Shared\n\ntheirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openGraph(clone).sync(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, filepath.FromSlash(shared)),
		[]byte("# Shared\n\nours\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := openGraph(origin).sync()
	if err == nil {
		t.Fatal("a conflicting merge should be reported, not made")
	}
	if !strings.Contains(err.Error(), "resolve it by hand") {
		t.Errorf("error: got %q", err)
	}
	// The graph is where it was, not half-merged.
	content, readErr := os.ReadFile(filepath.Join(origin, filepath.FromSlash(shared)))
	if readErr != nil || strings.Contains(string(content), "<<<<<<<") {
		t.Errorf("the note should be untouched: %q, %v", content, readErr)
	}
	status, statusErr := openGraph(origin).git("status", "--porcelain")
	if statusErr != nil || strings.TrimSpace(status) != "" {
		t.Errorf("the working tree should be clean: %q, %v", status, statusErr)
	}
}

func TestSyncCommitsWithoutARemote(t *testing.T) {
	root := gitGraph(t, map[string]string{"notes/a.md": "# A\n"})
	if err := os.WriteFile(filepath.Join(root, notesDir, "b.md"), []byte("# B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := openGraph(root).sync()
	if err != nil {
		t.Fatal(err)
	}
	if report.committed != 1 || report.pushed != 0 {
		t.Errorf("got %+v", report)
	}
}

func TestSyncNeedsARepository(t *testing.T) {
	root := testGraph(t, map[string]string{"notes/a.md": "# A\n"})
	if _, err := openGraph(root).sync(); err == nil {
		t.Error("a graph outside git has nothing to sync")
	}
}

func TestCommitMessage(t *testing.T) {
	for _, test := range []struct {
		name    string
		changed []string
		notes   int
		want    string
	}{
		{name: "one daily", changed: []string{"daily/2026-09-10.md"}, notes: 1, want: "Update daily note for 2026-09-10"},
		{name: "one note", changed: []string{"notes/a.md"}, notes: 1, want: "Update notes/a.md"},
		{name: "several notes", changed: []string{"notes/a.md", "notes/b.md"}, notes: 2, want: "Update 2 notes"},
		{name: "notes and more", changed: []string{"notes/a.md", "assets/x.png"}, notes: 1, want: "Update 2 files"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := commitMessage(test.changed, test.notes); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}
