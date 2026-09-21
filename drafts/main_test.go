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
			if key == "DRAFTS_DIR" {
				return root
			}
			return ""
		},
		getwd:  func() (string, error) { return root, nil },
		home:   func() (string, error) { return root, nil },
		now:    func() time.Time { return time.Date(2026, 9, 20, 15, 0, 0, 0, time.Local) },
		stdout: stdout,
		stderr: stderr,
	}, stdout, stderr
}

func commandDir(t *testing.T) string {
	t.Helper()
	return testDir(t, map[string]string{
		"foobar.md": "# Foobar\n\nlorem ipsum dolor sit amet, consectetur\nadipiscing elit foobar sed do\n",
		"second.md": "---\ntitle: The Second\n---\n\nnothing much here\n",
	}, "second.md", "foobar.md")
}

// The search prints the Markdown block each match was made in, under the file
// and line it came from.
func TestRunPrintsBlocks(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandDir(t))
	if code := c.run([]string{"foobar"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := strings.Join([]string{
		"foobar.md:1",
		"\t# Foobar",
		"foobar.md:3",
		"\tlorem ipsum dolor sit amet, consectetur",
		"\tadipiscing elit foobar sed do",
		"",
	}, "\n")
	if stdout.String() != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestRunWithNoQueryListsPaths(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandDir(t))
	if code := c.run(nil); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := stdout.String(); got != "foobar.md\nsecond.md\n" {
		t.Errorf("got %q", got)
	}
}

func TestRunHonoursTheLimit(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandDir(t))
	if code := c.run([]string{"-n", "1"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := stdout.String(); got != "foobar.md\n" {
		t.Errorf("got %q", got)
	}
}

func TestRunTimeline(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandDir(t))
	if code := c.run([]string{"-t"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	want := strings.Join([]string{
		"foobar.md:1 10:00AM",
		"\t# Foobar",
		"second.md:5 9:00AM",
		"\tnothing much here",
		"",
	}, "\n")
	if stdout.String() != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

// -d names the directory, and outranks the environment.
func TestRunTakesTheDirectoryFromTheFlag(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandDir(t))
	other := testDir(t, map[string]string{"only.md": "# Only\n"}, "only.md")
	if code := c.run([]string{"-d", other}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if got := stdout.String(); !strings.HasSuffix(strings.TrimSpace(got), "only.md") {
		t.Errorf("got %q", got)
	}
}

// The help belongs to whoever asked for it: on stdout when it was asked for,
// on stderr behind the complaint when it was not.
func TestHelp(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandDir(t))
	if code := c.run([]string{"-help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(stdout.String(), "drafts keeps a directory") {
		t.Errorf("got %q", stdout)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr: %q", stderr)
	}
}

func TestUnknownFlag(t *testing.T) {
	c, stdout, stderr := testCommand(t, commandDir(t))
	if code := c.run([]string{"-nope"}); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout: %q", stdout)
	}
	if !strings.Contains(stderr.String(), "drafts keeps a directory") {
		t.Errorf("stderr: %q", stderr)
	}
}

// Several words are one query, and quoting groups them into one term.
func TestRunJoinsItsArguments(t *testing.T) {
	root := testDir(t, map[string]string{
		"a.md": "# A\n\nair traffic control tower\n",
	}, "a.md")
	c, stdout, stderr := testCommand(t, root)
	if code := c.run([]string{"air traffic", "control"}); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout.String(), "air traffic control tower") {
		t.Errorf("got %q", stdout)
	}
}
