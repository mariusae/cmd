package main

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestSummarizeGitStatus(t *testing.T) {
	tests := map[string]string{
		"# branch.oid abc\n# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n":  "up to date, clean",
		"# branch.head main\n# branch.ab +0 -0\n? notes.md\n":                                       "up to date, untracked",
		"# branch.head main\n# branch.ab +2 -0\n":                                                   "2 ahead, clean",
		"# branch.head main\n# branch.ab +0 -3\n":                                                   "3 behind, clean",
		"# branch.head main\n# branch.ab +2 -3\n":                                                   "2 ahead, 3 behind, clean",
		"# branch.head main\n# branch.ab +1 -0\n1 .M N... 100644 100644 100644 abc abc main.go\n":   "1 ahead, modified",
		"# branch.head main\n# branch.ab +0 -0\n2 R. N... 100644 100644 100644 abc abc R100 b\ta\n": "up to date, modified",
		"# branch.head main\n# branch.ab +0 -0\nu UU N... 100644 100644 100644 100644 a b c d x\n":  "up to date, conflicted",
		"# branch.head main\n# branch.ab +0 -0\n1 M. N... 100644 100644 100644 a b main.go\n? x\n":  "up to date, modified, untracked",
		"# branch.oid abc\n# branch.head main\n":                                                    "no upstream, clean",
		"# branch.oid abc\n# branch.head (detached)\n? x\n":                                         "detached, untracked",
		"": "no upstream, clean",
	}

	for output, want := range tests {
		if got := summarizeGitStatus(output); got != want {
			t.Errorf("summarizeGitStatus(%q) = %q, want %q", output, got, want)
		}
	}
}

func TestGitStatusWithoutRepository(t *testing.T) {
	got, err := gitStatus(t.TempDir())
	if err != nil {
		t.Fatalf("gitStatus: %v", err)
	}
	if want := "not a repository"; got != want {
		t.Fatalf("gitStatus = %q, want %q", got, want)
	}
}

func TestGitStatusOnRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	path := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = path
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "--quiet")
	git("config", "user.email", "try@example.com")
	git("config", "user.name", "try")

	got, err := gitStatus(path)
	if err != nil {
		t.Fatalf("gitStatus: %v", err)
	}
	if want := "no upstream, clean"; got != want {
		t.Fatalf("empty repository status = %q, want %q", got, want)
	}

	makeFile(t, filepath.Join(path, "notes.md"))
	if got, err = gitStatus(path); err != nil {
		t.Fatalf("gitStatus: %v", err)
	} else if want := "no upstream, untracked"; got != want {
		t.Fatalf("untracked repository status = %q, want %q", got, want)
	}

	git("add", "notes.md")
	if got, err = gitStatus(path); err != nil {
		t.Fatalf("gitStatus: %v", err)
	} else if want := "no upstream, modified"; got != want {
		t.Fatalf("staged repository status = %q, want %q", got, want)
	}
}

func TestSummarizePreservesOrderOfPaths(t *testing.T) {
	paths := make([]string, 50)
	for i := range paths {
		paths[i] = "try-" + strconv.Itoa(i)
	}

	var called atomic.Int64
	statuses, failures := summarize(paths, func(path string) (string, error) {
		called.Add(1)
		return "status of " + path, nil
	})
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}

	want := make([]string, len(paths))
	for i, path := range paths {
		want[i] = "status of " + path
	}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %#v, want %#v", statuses, want)
	}
	if got := int(called.Load()); got != len(paths) {
		t.Fatalf("status called %d times, want %d", got, len(paths))
	}
}
