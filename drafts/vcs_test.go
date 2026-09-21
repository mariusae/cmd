package main

import (
	"os"
	"path/filepath"
	"testing"
)

// gitRepo makes root a git repository with an identity of its own, so the test
// does not depend on whoever is running it.
func gitRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "drafts@example"},
		{"config", "user.name", "Drafts Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := run(root, "git", args...); err != nil {
			t.Skipf("git is not usable here: %v", err)
		}
	}
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenVCSFindsGitAndNothingElse(t *testing.T) {
	plain := t.TempDir()
	if repo := openVCS(plain); repo != nil {
		t.Errorf("a plain directory reported %s", repo.name())
	}
	root := t.TempDir()
	gitRepo(t, root)
	repo := openVCS(root)
	if repo == nil || repo.name() != "git" {
		t.Fatalf("got %v, want git", repo)
	}
}

func TestGitCommitRecordsWhatWasWritten(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	repo := openVCS(root)

	write(t, root, "one.md", "# One\n")
	committed, err := repo.commit()
	if err != nil {
		t.Fatal(err)
	}
	if committed != 1 {
		t.Errorf("got %d files committed, want 1", committed)
	}

	// A commit with nothing written is no commit, and no complaint: a Put that
	// changed nothing has nothing to record.
	if committed, err = repo.commit(); err != nil || committed != 0 {
		t.Errorf("got %d, %v; want 0, nil", committed, err)
	}

	write(t, root, "one.md", "# One\n\nmore\n")
	write(t, root, "two.md", "# Two\n")
	if committed, err = repo.commit(); err != nil || committed != 2 {
		t.Errorf("got %d, %v; want 2, nil", committed, err)
	}

	history, err := repo.history(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("got %d revisions, want 2", len(history))
	}
	if len(history[0].files) != 2 {
		t.Errorf("newest revision touched %v", history[0].files)
	}
}

// A drafts directory may sit inside a larger repository. Everything asked of
// version control is about the drafts, and must not sweep up the rest of it.
func TestGitCommitIsHeldToTheDraftsDirectory(t *testing.T) {
	outer := t.TempDir()
	gitRepo(t, outer)
	write(t, outer, "unrelated.txt", "not a draft\n")
	root := filepath.Join(outer, "drafts")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, "one.md", "# One\n")

	repo := openVCS(root)
	if repo == nil {
		t.Fatal("the nested directory found no repository")
	}
	committed, err := repo.commit()
	if err != nil {
		t.Fatal(err)
	}
	if committed != 1 {
		t.Errorf("got %d files committed, want 1 — the rest of the repository came along", committed)
	}
	// Named relative to the drafts directory, not to the repository's root.
	history, err := repo.history(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || len(history[0].files) != 1 || history[0].files[0] != "one.md" {
		t.Errorf("history names %v, want [one.md]", history[0].files)
	}
	// And the file outside is still untracked, exactly as it was found.
	if out, err := run(outer, "git", "status", "--porcelain", "--", "unrelated.txt"); err != nil || out == "" {
		t.Errorf("unrelated.txt was committed: %q, %v", out, err)
	}
}

func TestGitWrittenAndDiff(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	repo := openVCS(root)
	write(t, root, "one.md", "# One\n\nfirst\n")
	if _, err := repo.commit(); err != nil {
		t.Fatal(err)
	}

	write(t, root, "one.md", "# One\n\nfirst\nsecond\n")
	write(t, root, "two.md", "# Two\n")
	changed, added := repo.written()
	if len(changed) != 1 || changed[0] != "one.md" {
		t.Errorf("changed: %v", changed)
	}
	if len(added) != 1 || added[0] != "two.md" {
		t.Errorf("added: %v", added)
	}

	diff, err := repo.diff("one.md")
	if err != nil {
		t.Fatal(err)
	}
	if !hunkHeader.MatchString(firstHunk(diff)) {
		t.Errorf("diff has no hunk header:\n%s", diff)
	}
}

func firstHunk(diff string) string {
	for _, line := range splitLines(diff) {
		if hunkHeader.MatchString(line) {
			return line
		}
	}
	return ""
}

func splitLines(text string) []string {
	var lines []string
	start := 0
	for index := 0; index <= len(text); index++ {
		if index == len(text) || text[index] == '\n' {
			lines = append(lines, text[start:index])
			start = index + 1
		}
	}
	return lines
}

func TestGitContentAndRevDiff(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	repo := openVCS(root)
	write(t, root, "one.md", "# One\n\nfirst\n")
	if _, err := repo.commit(); err != nil {
		t.Fatal(err)
	}
	write(t, root, "one.md", "# One\n\nfirst\nsecond\n")
	if _, err := repo.commit(); err != nil {
		t.Fatal(err)
	}
	history, err := repo.history(0)
	if err != nil {
		t.Fatal(err)
	}
	content, err := repo.content(history[1].id, "one.md")
	if err != nil {
		t.Fatal(err)
	}
	if content != "# One\n\nfirst\n" {
		t.Errorf("content at the first revision: %q", content)
	}
	diff, err := repo.revDiff(history[0].id, "one.md")
	if err != nil {
		t.Fatal(err)
	}
	if firstHunk(diff) == "" {
		t.Errorf("revDiff has no hunk header:\n%s", diff)
	}
}

// A repository with nowhere to send its history is not failing at anything.
func TestPushWithNoRemoteIsQuiet(t *testing.T) {
	root := t.TempDir()
	gitRepo(t, root)
	repo := openVCS(root)
	write(t, root, "one.md", "# One\n")
	if _, err := repo.commit(); err != nil {
		t.Fatal(err)
	}
	if err := repo.push(); err != nil {
		t.Errorf("push with no remote: %v", err)
	}
}

func TestPushSendsToTheRemote(t *testing.T) {
	remote := t.TempDir()
	if _, err := run(remote, "git", "init", "--quiet", "--bare", "-b", "main"); err != nil {
		t.Skipf("git is not usable here: %v", err)
	}
	root := t.TempDir()
	gitRepo(t, root)
	repo := openVCS(root)
	write(t, root, "one.md", "# One\n")
	if _, err := repo.commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := run(root, "git", "remote", "add", "origin", remote); err != nil {
		t.Fatal(err)
	}
	if _, err := run(root, "git", "push", "--quiet", "--set-upstream", "origin", "main"); err != nil {
		t.Fatal(err)
	}

	write(t, root, "two.md", "# Two\n")
	if _, err := repo.commit(); err != nil {
		t.Fatal(err)
	}
	if err := repo.push(); err != nil {
		t.Fatal(err)
	}
	out, err := run(remote, "git", "log", "--format=%s", "-n", "1")
	if err != nil {
		t.Fatal(err)
	}
	if got := trimLine(out); got != "Update two.md" {
		t.Errorf("the remote has %q", got)
	}
}

func trimLine(text string) string {
	for index := 0; index < len(text); index++ {
		if text[index] == '\n' {
			return text[:index]
		}
	}
	return text
}

func TestCommitMessage(t *testing.T) {
	for _, test := range []struct {
		changed []string
		want    string
	}{
		{nil, "Update drafts"},
		{[]string{"one.md"}, "Update one.md"},
		{[]string{"one.md", "two.md"}, "Update 2 drafts"},
	} {
		if got := commitMessage(test.changed); got != test.want {
			t.Errorf("commitMessage(%v): got %q, want %q", test.changed, got, test.want)
		}
	}
}

// Sapling names files from the repository's root; the drafts directory needs
// them named from itself, and wants nothing from outside it.
func TestRelPath(t *testing.T) {
	for _, test := range []struct {
		prefix, file, want string
		ok                 bool
	}{
		{"", "one.md", "one.md", true},
		{"drafts/", "drafts/one.md", "one.md", true},
		{"drafts/", "elsewhere/one.md", "", false},
		{"drafts/", "drafts/sub/one.md", "", false},
		{"drafts/", "drafts/", "", false},
	} {
		got, ok := relPath(test.prefix, test.file)
		if got != test.want || ok != test.ok {
			t.Errorf("relPath(%q, %q): got %q, %v", test.prefix, test.file, got, ok)
		}
	}
}
