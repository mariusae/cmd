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

type fakeBackend struct {
	repositories map[string]repository
	openCalls    []string
	addedPath    string
	addedLabel   string
	removedPath  string
	addError     error
	removeError  error
}

func (b *fakeBackend) name() string { return "sapling" }

func (b *fakeBackend) open(path string) (repository, error) {
	path = filepath.Clean(path)
	b.openCalls = append(b.openCalls, path)
	if repo, ok := b.repositories[path]; ok {
		return repo, nil
	}
	return repository{}, errNotWorktreeRepository
}

func (b *fakeBackend) add(_ repository, path, label string, _ io.Writer) error {
	b.addedPath = path
	b.addedLabel = label
	return b.addError
}

func (b *fakeBackend) remove(_ repository, path string, _ io.Writer) error {
	b.removedPath = path
	return b.removeError
}

func testRepository(root, linked string) repository {
	return repository{
		MainRoot:    root,
		CurrentRoot: root,
		Worktrees: []worktree{
			{Path: root, Main: true, Current: true},
			{Path: linked, Label: filepath.Base(linked)},
		},
	}
}

func newTestCommand(t *testing.T, cwd, home string, b backend) (*command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	c := &command{
		backend: b,
		getwd: func() (string, error) {
			return cwd, nil
		},
		homeDir: func() (string, error) {
			return home, nil
		},
		chdir:  func(string) error { return nil },
		getenv: func(string) string { return "" },
		now:    func() time.Time { return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC) },
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: &stderr,
	}
	return c, &stdout, &stderr
}

func TestListUsesCurrentRepositoryAndOmitsMain(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "fbsource")
	linkedA := root + "-2026-08-28-vdc"
	linkedB := root + "-2026-09-02-bricolage-wirth"
	repo := testRepository(root, linkedA)
	repo.Worktrees = append(repo.Worktrees, worktree{Path: linkedB, Label: "2026-09-02-bricolage-wirth"})
	b := &fakeBackend{repositories: map[string]repository{root: repo}}
	c, stdout, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"ls"}); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	want := directoryPath(linkedA) + "\n" + directoryPath(linkedB) + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestListFallsBackToConfiguredDefault(t *testing.T) {
	home := t.TempDir()
	defaultRoot := filepath.Join(home, "fbsource")
	linked := defaultRoot + "-task"
	writeConfig(t, home, "default: ~/fbsource\n")
	b := &fakeBackend{repositories: map[string]repository{
		defaultRoot: testRepository(defaultRoot, linked),
	}}
	c, stdout, stderr := newTestCommand(t, home, home, b)

	if code := c.run(nil); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	if stdout.String() != directoryPath(linked)+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if want := []string{home, defaultRoot}; !reflect.DeepEqual(b.openCalls, want) {
		t.Fatalf("open calls = %#v, want %#v", b.openCalls, want)
	}
}

func TestNewCreatesDatedSiblingAndPrintsIt(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "fbsource")
	repo := testRepository(root, root+"-old")
	b := &fakeBackend{repositories: map[string]repository{root: repo}}
	c, stdout, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"new", "test feature"}); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	wantPath := root + "-2026-09-09-test-feature"
	if b.addedPath != wantPath || b.addedLabel != "2026-09-09-test-feature" {
		t.Fatalf("add = (%q, %q), want (%q, %q)", b.addedPath, b.addedLabel, wantPath, "2026-09-09-test-feature")
	}
	if stdout.String() != directoryPath(wantPath)+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestNewUsesNumericSuffixForCollision(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "fbsource")
	first := root + "-2026-09-09-task"
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := testRepository(root, root+"-old")
	b := &fakeBackend{repositories: map[string]repository{root: repo}}
	c, _, stderr := newTestCommand(t, root, t.TempDir(), b)

	if code := c.run([]string{"new", "task"}); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	if want := first + "-2"; b.addedPath != want {
		t.Fatalf("added path = %q, want %q", b.addedPath, want)
	}
	if b.addedLabel != "2026-09-09-task-2" {
		t.Fatalf("added label = %q", b.addedLabel)
	}
}

func TestRemoveCurrentWorktree(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "fbsource")
	linked := root + "-2026-09-09-task"
	repo := testRepository(root, linked)
	repo.CurrentRoot = linked
	repo.Worktrees[0].Current = false
	repo.Worktrees[1].Current = true
	b := &fakeBackend{repositories: map[string]repository{linked: repo}}
	c, stdout, stderr := newTestCommand(t, linked, home, b)
	changedTo := ""
	c.chdir = func(path string) error {
		changedTo = path
		return nil
	}

	if code := c.run([]string{"rm"}); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	if b.removedPath != linked {
		t.Fatalf("removed path = %q, want %q", b.removedPath, linked)
	}
	if changedTo != root {
		t.Fatalf("changed directory to %q, want %q", changedTo, root)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRemoveByPathRunsPreRemoveHook(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "fbsource")
	linked := root + "-2026-09-09-task"
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(parent, "hook.txt")
	hook := fmt.Sprintf("printf '%%s\\n%%s\\n%%s\\n' \"$WORK_HANDLE\" \"$WORK_WORKTREE_PATH\" \"$WORK_PROJECT_ROOT\" > %q", marker)
	writeConfig(t, home, "pre_remove:\n  - "+hook+"\n")
	repo := testRepository(root, linked)
	b := &fakeBackend{repositories: map[string]repository{root: repo}}
	c, _, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"rm", linked}); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	contents, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{filepath.Base(linked), linked, root, ""}, "\n")
	if string(contents) != want {
		t.Fatalf("hook environment = %q, want %q", contents, want)
	}
	if b.removedPath != linked {
		t.Fatalf("removed path = %q", b.removedPath)
	}
}

func TestFailingHookStopsRemoval(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "fbsource")
	linked := root + "-task"
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, home, "pre_remove: false\n")
	b := &fakeBackend{repositories: map[string]repository{root: testRepository(root, linked)}}
	c, _, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"rm", linked}); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if b.removedPath != "" {
		t.Fatalf("removed %q despite hook failure", b.removedPath)
	}
	if !strings.Contains(stderr.String(), "pre_remove hook failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRemoveWithoutTargetDoesNotUseFallbackRepository(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "fbsource")
	linked := root + "-task"
	writeConfig(t, home, "default: ~/fbsource\n")
	b := &fakeBackend{repositories: map[string]repository{root: testRepository(root, linked)}}
	c, _, stderr := newTestCommand(t, home, home, b)

	if code := c.run([]string{"rm"}); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if b.removedPath != "" {
		t.Fatalf("removed path = %q", b.removedPath)
	}
	if !strings.Contains(stderr.String(), "must be run from a linked worktree") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRemoveRefusesMainWorktree(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "fbsource")
	repo := testRepository(root, root+"-task")
	b := &fakeBackend{repositories: map[string]repository{root: repo}}
	c, _, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"rm"}); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "refusing to remove the main worktree") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestBackendErrorsAreReported(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "fbsource")
	b := &fakeBackend{
		repositories: map[string]repository{root: testRepository(root, root+"-task")},
		addError:     errors.New("boom"),
	}
	c, stdout, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"new", "task"}); code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestHelpDoesNotRequireBackendOrConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	c := command{
		findBackend: func() (backend, error) {
			t.Fatal("help tried to find a backend")
			return nil, nil
		},
		homeDir: func() (string, error) {
			t.Fatal("help tried to find the home directory")
			return "", nil
		},
		stdout: &stdout,
		stderr: &stderr,
	}
	if code := c.run([]string{"-help"}); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	if !strings.Contains(stdout.String(), "work manages linked Sapling worktrees") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestParseDashboardCommands(t *testing.T) {
	if parsed, ok := parseArgs([]string{"dash"}); !ok || parsed.action != "dash" {
		t.Fatalf("parseArgs(dash) = (%#v, %v)", parsed, ok)
	}
	if parsed, ok := parseArgs([]string{"dash-live"}); !ok || parsed.action != "dash-live" {
		t.Fatalf("parseArgs(dash-live) = (%#v, %v)", parsed, ok)
	}
	if parsed, ok := parseArgs([]string{"dash-expand", "42"}); !ok || parsed.action != "dash-expand" || parsed.argument != "42" {
		t.Fatalf("parseArgs(dash-expand) = (%#v, %v)", parsed, ok)
	}
	if parsed, ok := parseArgs([]string{"agent-status", "done", "opencode", "session-1"}); !ok || parsed.session != "session-1" {
		t.Fatalf("parseArgs(agent-status session) = (%#v, %v)", parsed, ok)
	}
}

func TestAgentStatusAndStatusOutput(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "fbsource")
	linked := root + "-task"
	repo := testRepository(root, linked)
	repo.CurrentRoot = linked
	repo.Worktrees[0].Current = false
	repo.Worktrees[1].Current = true
	b := &fakeBackend{repositories: map[string]repository{linked: repo}}
	c, stdout, stderr := newTestCommand(t, linked, home, b)
	c.stdin = strings.NewReader(`{
  "cwd": "` + linked + `",
  "session_id": "session-1",
	  "title": "Implement the feature",
	  "transcript_path": "/tmp/session-1.jsonl"
}`)

	if code := c.run([]string{"agent-status", "done", "codex"}); code != 0 {
		t.Fatalf("agent-status returned %d: %s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	c.stdin = strings.NewReader("")
	if code := c.run([]string{"status"}); code != 0 {
		t.Fatalf("status returned %d: %s", code, stderr.String())
	}
	want := directoryPath(linked) + "\tdone\t12:00PM\tcodex\tImplement the feature\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestStatusIncludesIdleWorktrees(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "fbsource")
	linked := root + "-idle"
	repo := testRepository(root, linked)
	b := &fakeBackend{repositories: map[string]repository{root: repo}}
	c, stdout, stderr := newTestCommand(t, root, home, b)

	if code := c.run([]string{"status"}); code != 0 {
		t.Fatalf("status returned %d: %s", code, stderr.String())
	}
	if got, want := stdout.String(), directoryPath(linked)+"\tidle\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestEventTimeFormatting(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		age  time.Duration
		want string
	}{
		{0, "12:00PM"},
		{45 * time.Second, "11:59AM"},
		{24 * time.Hour, "12:00PM"},
		{48 * time.Hour, "Mon12:00PM"},
		{8 * 24 * time.Hour, "1Sep26"},
	}
	for _, test := range tests {
		if got := formatEventTime(now.Add(-test.age).Unix(), now); got != test.want {
			t.Errorf("formatEventTime(%s) = %q, want %q", test.age, got, test.want)
		}
	}
}

func TestOperationDurationFormatting(t *testing.T) {
	tests := []struct {
		seconds int64
		want    string
	}{
		{2, "2s"},
		{4*60 + 3, "4m3s"},
		{60*60 + 33*60, "1h33m"},
		{8*24*60*60 + 2*60*60, "8d2h"},
	}
	for _, test := range tests {
		if got := formatOperationDuration(100, 100+test.seconds); got != test.want {
			t.Errorf("formatOperationDuration(%d) = %q, want %q", test.seconds, got, test.want)
		}
	}
}

func TestHookPayloadReadsTitleAndTranscriptPointer(t *testing.T) {
	payload := parseHookPayload([]byte(`{
  "cwd": "/repo",
  "sessionId": "abc",
	  "title": "Implement dashboard",
	  "transcript_path": "/tmp/abc.jsonl"
}`))
	if payload.CWD != "/repo" || payload.SessionID != "abc" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Title != "Implement dashboard" || payload.TranscriptPath != "/tmp/abc.jsonl" {
		t.Fatalf("payload = %#v", payload)
	}
}

func writeConfig(t *testing.T, home, contents string) {
	t.Helper()
	dir := filepath.Dir(configFile(home))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile(home), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
