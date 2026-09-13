package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testDate = time.Date(2026, time.September, 9, 12, 0, 0, 0, time.Local)

func TestRunFindsMatchingTries(t *testing.T) {
	base := t.TempDir()
	makeDir(t, filepath.Join(base, "2026-08-21-mariusae-md"))
	makeDir(t, filepath.Join(base, "2026-08-21-mariusae-mdiff"))
	makeDir(t, filepath.Join(base, "2026-09-08-new-mdiff"))
	makeDir(t, filepath.Join(base, "2026-09-05-newproject"))
	makeDir(t, filepath.Join(base, ".hidden-md"))
	makeFile(t, filepath.Join(base, "notes-md"))

	c, stdout, stderr := testCommand(base)
	if code := c.run([]string{"md"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := strings.Join([]string{
		directoryPath(filepath.Join(base, "2026-08-21-mariusae-md")),
		directoryPath(filepath.Join(base, "2026-09-08-new-mdiff")),
		directoryPath(filepath.Join(base, "2026-08-21-mariusae-mdiff")),
		"",
	}, "\n")
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunQueryWithMissingTriesDirectoryReportsNoMatches(t *testing.T) {
	base := filepath.Join(t.TempDir(), "missing")
	c, stdout, stderr := testCommand(base)

	if code := c.run([]string{"anything"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `no tries match "anything"`) {
		t.Fatalf("stderr = %q, want no-match message", stderr.String())
	}
}

func TestRunWithoutQueryListsTwentyMostRecentTries(t *testing.T) {
	base := t.TempDir()
	for day := 1; day <= 21; day++ {
		makeDir(t, filepath.Join(base, testDate.AddDate(0, 0, -day).Format("2006-01-02")+"-project"))
	}
	c, stdout, stderr := testCommand(base)

	if code := c.run(nil); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != recentTryLimit {
		t.Fatalf("printed %d paths, want %d", len(lines), recentTryLimit)
	}
	newest := directoryPath(filepath.Join(base, "2026-09-08-project"))
	if lines[0] != newest {
		t.Fatalf("first path = %q, want %q", lines[0], newest)
	}
}

func TestRunShortStatusAnnotatesEachMatch(t *testing.T) {
	base := t.TempDir()
	makeDir(t, filepath.Join(base, "2026-09-08-mariusae-over"))
	makeDir(t, filepath.Join(base, "2026-09-07-thunk"))
	c, stdout, stderr := testCommand(base)
	c.status = func(path string) (string, error) {
		if filepath.Base(path) == "2026-09-08-mariusae-over" {
			return "up to date, untracked", nil
		}
		return "2 ahead, clean", nil
	}

	if code := c.run([]string{"-s"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := strings.Join([]string{
		directoryPath(filepath.Join(base, "2026-09-08-mariusae-over")) + " up to date, untracked",
		directoryPath(filepath.Join(base, "2026-09-07-thunk")) + " 2 ahead, clean",
		"",
	}, "\n")
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunShortStatusHonorsQuery(t *testing.T) {
	base := t.TempDir()
	makeDir(t, filepath.Join(base, "2026-09-08-mdiff"))
	makeDir(t, filepath.Join(base, "2026-09-07-other"))
	c, stdout, stderr := testCommand(base)
	c.status = func(string) (string, error) { return "up to date, clean", nil }

	if code := c.run([]string{"-s", "mdiff"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	want := directoryPath(filepath.Join(base, "2026-09-08-mdiff")) + " up to date, clean\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunShortStatusReportsFailuresAndKeepsGoing(t *testing.T) {
	base := t.TempDir()
	makeDir(t, filepath.Join(base, "2026-09-08-broken"))
	makeDir(t, filepath.Join(base, "2026-09-07-fine"))
	c, stdout, stderr := testCommand(base)
	c.status = func(path string) (string, error) {
		if filepath.Base(path) == "2026-09-08-broken" {
			return "", errors.New("git status failed")
		}
		return "up to date, clean", nil
	}

	if code := c.run([]string{"-s"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	want := directoryPath(filepath.Join(base, "2026-09-07-fine")) + " up to date, clean\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "git status failed") {
		t.Fatalf("stderr = %q, want status error", stderr.String())
	}
}

func TestRunShortStatusWithoutQueryLimitsToRecentTries(t *testing.T) {
	base := t.TempDir()
	for day := 1; day <= recentTryLimit+1; day++ {
		makeDir(t, filepath.Join(base, testDate.AddDate(0, 0, -day).Format("2006-01-02")+"-project"))
	}
	c, stdout, stderr := testCommand(base)
	c.status = func(string) (string, error) { return "up to date, clean", nil }

	if code := c.run([]string{"-s"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != recentTryLimit {
		t.Fatalf("printed %d lines, want %d", len(lines), recentTryLimit)
	}
}

func TestRunCreatesNamedTryAndNormalizesWhitespace(t *testing.T) {
	base := filepath.Join(t.TempDir(), "tries")
	c, stdout, stderr := testCommand(base)

	if code := c.run([]string{"-n", "  new   project\tidea "}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := filepath.Join(base, "2026-09-09-new-project-idea")
	if got := stdout.String(); got != directoryPath(want)+"\n" {
		t.Fatalf("stdout = %q, want %q", got, directoryPath(want)+"\n")
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("created path is not a directory: info = %v, err = %v", info, err)
	}
}

func TestRunClonesGitURLIntoNamedTry(t *testing.T) {
	base := filepath.Join(t.TempDir(), "tries")
	c, stdout, stderr := testCommand(base)
	var gotRemote, gotDestination string
	c.clone = func(remote, destination string, output io.Writer) error {
		gotRemote = remote
		gotDestination = destination
		fmt.Fprintln(output, "Cloning repository...")
		return nil
	}

	remote := "git@github.com:mariusae/cmd.git"
	if code := c.run([]string{"-n", remote}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := filepath.Join(base, "2026-09-09-mariusae-cmd")
	if gotRemote != remote || gotDestination != want {
		t.Fatalf("clone(%q, %q), want clone(%q, %q)", gotRemote, gotDestination, remote, want)
	}
	if got := stdout.String(); got != directoryPath(want)+"\n" {
		t.Fatalf("stdout = %q, want %q", got, directoryPath(want)+"\n")
	}
	if got := stderr.String(); got != "Cloning repository...\n" {
		t.Fatalf("stderr = %q, want streamed clone output", got)
	}
}

func TestRunAddsSuffixWhenTryAlreadyExists(t *testing.T) {
	base := t.TempDir()
	makeDir(t, filepath.Join(base, "2026-09-09-existing"))
	makeDir(t, filepath.Join(base, "2026-09-09-existing-2"))
	c, stdout, stderr := testCommand(base)

	if code := c.run([]string{"-n", "existing"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	want := filepath.Join(base, "2026-09-09-existing-3")
	if got := stdout.String(); got != directoryPath(want)+"\n" {
		t.Fatalf("stdout = %q, want %q", got, directoryPath(want)+"\n")
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("stat created try: %v", err)
	}
}

func TestRunReportsCloneFailure(t *testing.T) {
	base := filepath.Join(t.TempDir(), "tries")
	c, stdout, stderr := testCommand(base)
	var destination string
	c.clone = func(_ string, path string, output io.Writer) error {
		destination = path
		fmt.Fprintln(output, "clone progress")
		return errors.New("network unavailable")
	}

	if code := c.run([]string{"-n", "https://github.com/mariusae/cmd.git"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "network unavailable") {
		t.Fatalf("stderr = %q, want clone error", stderr.String())
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("incomplete clone still exists: %v", err)
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"-help", "--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			c, stdout, stderr := testCommand("")
			c.getenv = func(string) string {
				t.Fatal("help should not inspect the environment")
				return ""
			}

			if code := c.run([]string{arg}); code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			for _, want := range []string{"try QUERY", "try -n NAME", "TRY_PATH", "git@github.com"} {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("help output does not contain %q", want)
				}
			}
		})
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"-n"}, {"-n", "one", "two"}, {"-unknown"}} {
		c, stdout, stderr := testCommand(t.TempDir())
		if code := c.run(args); code != 2 {
			t.Errorf("run(%q) exit code = %d, want 2", args, code)
		}
		if stdout.Len() != 0 {
			t.Errorf("run(%q) stdout = %q, want empty", args, stdout.String())
		}
		if !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("run(%q) stderr = %q, want usage", args, stderr.String())
		}
	}
}

func TestTriesPath(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name    string
		environ string
		want    string
	}{
		{name: "default", want: filepath.Join(home, "src", "tries")},
		{name: "environment override", environ: filepath.Join(home, "scratch"), want: filepath.Join(home, "scratch")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := triesPath(
				func(string) string { return test.environ },
				func() (string, error) { return home, nil },
			)
			if err != nil {
				t.Fatalf("triesPath: %v", err)
			}
			if got != test.want {
				t.Fatalf("triesPath = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGitSourceFor(t *testing.T) {
	tests := map[string]gitSource{
		"git@github.com:mariusae/cmd.git":        {"git@github.com:mariusae/cmd.git", "mariusae-cmd"},
		"https://github.com/mariusae/cmd.git":    {"git@github.com:mariusae/cmd.git", "mariusae-cmd"},
		"ssh://git@github.com/mariusae/cmd.git/": {"ssh://git@github.com/mariusae/cmd.git/", "mariusae-cmd"},
		"file:///tmp/owner/repository":           {"file:///tmp/owner/repository", "owner-repository"},

		"mariusae/over":            {"git@github.com:mariusae/over.git", "mariusae-over"},
		"mariusae/over/":           {"git@github.com:mariusae/over.git", "mariusae-over"},
		"mariusae/over.git":        {"git@github.com:mariusae/over.git", "mariusae-over"},
		"github.com/mariusae/over": {"git@github.com:mariusae/over.git", "mariusae-over"},
		"gitlab.com/group/sub/repository": {
			"git@gitlab.com:group/sub/repository.git", "sub-repository",
		},
		"github.com/mariusae/over/tree/main/internal": {
			"git@github.com:mariusae/over.git", "mariusae-over",
		},
		"https://github.com/mariusae/over/pull/7": {
			"git@github.com:mariusae/over.git", "mariusae-over",
		},
		"https://github.com/mariusae/over/blob/main/main.go#L10": {
			"git@github.com:mariusae/over.git", "mariusae-over",
		},
		"https://gitlab.com/owner/repository/-/tree/main": {
			"https://gitlab.com/owner/repository", "owner-repository",
		},
	}

	for value, want := range tests {
		got, err := gitSourceFor(value)
		if err != nil {
			t.Errorf("gitSourceFor(%q): %v", value, err)
			continue
		}
		if got != want {
			t.Errorf("gitSourceFor(%q) = %#v, want %#v", value, got, want)
		}
	}
}

func TestIsGitSource(t *testing.T) {
	tests := map[string]bool{
		"git@github.com:mariusae/cmd.git": true,
		"https://github.com/mariusae/cmd": true,
		"mariusae/over":                   true,
		"github.com/mariusae/over":        true,
		"newproject":                      false,
		"new project idea":                false,
		"a/b c":                           false,
		"/absolute/path":                  false,
		"./relative":                      false,
	}

	for value, want := range tests {
		if got := isGitSource(value); got != want {
			t.Errorf("isGitSource(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestRunClonesGitHubShorthandIntoNamedTry(t *testing.T) {
	base := filepath.Join(t.TempDir(), "tries")
	c, stdout, stderr := testCommand(base)
	var gotRemote, gotDestination string
	c.clone = func(remote, destination string, _ io.Writer) error {
		gotRemote, gotDestination = remote, destination
		return nil
	}

	if code := c.run([]string{"-n", "mariusae/over"}); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}

	want := filepath.Join(base, "2026-09-09-mariusae-over")
	if gotRemote != "git@github.com:mariusae/over.git" || gotDestination != want {
		t.Fatalf("clone(%q, %q), want clone(%q, %q)",
			gotRemote, gotDestination, "git@github.com:mariusae/over.git", want)
	}
	if got := stdout.String(); got != directoryPath(want)+"\n" {
		t.Fatalf("stdout = %q, want %q", got, directoryPath(want)+"\n")
	}
}

func TestFindTriesReturnsSortedDirectoryPaths(t *testing.T) {
	base := t.TempDir()
	makeDir(t, filepath.Join(base, "z-match"))
	makeDir(t, filepath.Join(base, "a-match"))

	got, err := findTries(base, "match")
	if err != nil {
		t.Fatalf("findTries: %v", err)
	}
	want := []string{filepath.Join(base, "a-match"), filepath.Join(base, "z-match")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findTries = %#v, want %#v", got, want)
	}
}

func testCommand(base string) (command, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return command{
		getenv: func(key string) string {
			if key != "TRY_PATH" {
				panic("unexpected environment variable: " + key)
			}
			return base
		},
		userHomeDir: func() (string, error) { return "", errors.New("unexpected home lookup") },
		now:         func() time.Time { return testDate },
		clone:       func(string, string, io.Writer) error { return errors.New("unexpected clone") },
		status:      func(string) (string, error) { return "", errors.New("unexpected status") },
		stdout:      stdout,
		stderr:      stderr,
	}, stdout, stderr
}

func makeDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
}

func makeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
