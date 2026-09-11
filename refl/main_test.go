package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func testCommand(t *testing.T, root string) (command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	return command{
		getenv: func(key string) string {
			if key == "REFLECT_GRAPH" {
				return root
			}
			return ""
		},
		getwd:  func() (string, error) { return root, nil },
		home:   func() (string, error) { return root, nil },
		now:    func() time.Time { return time.Date(2026, 9, 1, 15, 0, 0, 0, time.Local) },
		stdout: stdout,
		stderr: stderr,
	}, stdout, stderr
}

func commandGraph(t *testing.T) string {
	t.Helper()
	return testGraph(t, map[string]string{
		"daily/2026-09-10.md": "- priorities:\n  - land chrysalis\n  - something else\n",
		"notes/chrysalis.md":  "# Chrysalis\n\nthe design of chrysalis\n",
	})
}

func TestRunPrintsMatchingLines(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run([]string{"chrysalis"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := strings.Join([]string{
		"notes/chrysalis.md:1 # Chrysalis",
		"notes/chrysalis.md:3 the design of chrysalis",
		"daily/2026-09-10.md:2 - land chrysalis",
		"",
	}, "\n")
	if stdout.String() != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestRunPrintsBlocksWithContext(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run([]string{"-c", "chrysalis"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := strings.Join([]string{
		"notes/chrysalis.md:1",
		"\t# Chrysalis",
		"notes/chrysalis.md:3",
		"\tthe design of chrysalis",
		"daily/2026-09-10.md:1",
		"\t- priorities:",
		"\t  - land chrysalis",
		"",
	}, "\n")
	if stdout.String() != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestRunKeepsAtMostTheLimit(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run([]string{"-n", "2", "chrysalis"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := strings.Count(stdout.String(), "\n"); got != 2 {
		t.Errorf("got %d lines, want 2:\n%s", got, stdout)
	}
}

func TestRunWithNoQueryListsRecentNotes(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run(nil); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := "notes/chrysalis.md\ndaily/2026-09-10.md\n"
	if stdout.String() != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestRunQuotesPhrases(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run([]string{`"design of"`}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := stdout.String(); got != "notes/chrysalis.md:3 the design of chrysalis\n" {
		t.Errorf("got %q", got)
	}
}

func TestRunPrintsHelpOnceToWhoeverAskedForIt(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run([]string{"-help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(stdout.String(), "refl searches") {
		t.Errorf("stdout: got %q", stdout)
	}
	if stderr.Len() != 0 {
		t.Errorf("asked-for help should not reach stderr: %q", stderr)
	}
}

func TestRunRefusesAFlagItDoesNotHave(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run([]string{"-nope"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout carried %q", stdout)
	}
	if !strings.Contains(stderr.String(), "refl searches") {
		t.Errorf("stderr should explain: got %q", stderr)
	}
}

func TestRunReportsAGraphItCannotFind(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandGraph(t))
	if code := c.run([]string{"-g", t.TempDir(), "chrysalis"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout carried %q", stdout)
	}
	if !strings.HasPrefix(stderr.String(), "refl: ") {
		t.Errorf("stderr: got %q", stderr)
	}
}

func TestRunPrintsTheTimeline(t *testing.T) {
	root := gitGraph(t, map[string]string{"notes/a.md": "# A\n\nfirst paragraph\n"})
	c, stdout, stderr := testCommand(t, root)
	if code := c.run([]string{"-t"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %q", stdout)
	}
	if !strings.HasPrefix(lines[0], "notes/a.md:1 ") || lines[1] != "\t# A" {
		t.Errorf("first change: got %q and %q", lines[0], lines[1])
	}
	if lines[2] != "notes/a.md:3 "+strings.TrimPrefix(lines[0], "notes/a.md:1 ") ||
		lines[3] != "\tfirst paragraph" {
		t.Errorf("second block: got %q and %q", lines[2], lines[3])
	}
}
