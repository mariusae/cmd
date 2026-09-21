package main

// The drafts directory under version control.
//
// A drafts directory does not have to be a repository, and nothing here
// requires it to be one. When it is, every Put is committed and the commit is
// pushed behind the writer's back, so the history is the record of the writing
// rather than a chore done to it afterwards: drafts are kept, and a draft not
// kept anywhere but this disk is not kept.
//
// git and Sapling are both spoken. The commands differ; what is asked of them
// does not, so the rest of the program asks a vcs and never asks which.

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A revision is one recorded change: what it is called, when it was made, and
// the files of the drafts directory it touched, named relative to it.
type revision struct {
	id    string
	when  time.Time
	files []string
}

// A vcs is a repository, as the drafts directory needs one. Paths in and out
// are relative to the drafts directory, whether or not it is the repository's
// root.
type vcs interface {
	// name is what to call it when something goes wrong.
	name() string
	// written lists the files changed since the last commit, and those never
	// committed at all.
	written() (changed, added []string)
	// diff is the working tree's unified diff for one file, without context.
	diff(rel string) (string, error)
	// history is the most recent revisions, newest first.
	history(limit int) ([]revision, error)
	// content is a file as it stood at a revision.
	content(rev, rel string) (string, error)
	// revDiff is the unified diff one revision made to one file.
	revDiff(rev, rel string) (string, error)
	// commit records everything written in the directory, reporting how many
	// files that was. Nothing written is no commit and no error, since a Put
	// that changed nothing has nothing to record.
	commit() (int, error)
	// push hands the history to wherever it belongs. A repository with
	// nowhere to send it is not an error: there is nothing to do.
	push() error
}

// openVCS finds the repository the drafts directory sits in, if it sits in
// one. A directory under no version control is not a failure — it is a
// directory of files, which is what a drafts directory is.
func openVCS(root string) vcs {
	if _, err := run(root, "git", "rev-parse", "--show-toplevel"); err == nil {
		return &gitVCS{root: root}
	}
	for _, executable := range []string{"sl", "hg"} {
		if _, err := run(root, executable, "root"); err == nil {
			return &saplingVCS{root: root, executable: executable}
		}
	}
	return nil
}

// run executes a command in the drafts directory and returns its output. The
// directory is the working directory throughout, which is what makes both
// tools name files relative to it.
func run(dir, executable string, args ...string) (string, error) {
	command := exec.Command(executable, args...)
	command.Dir = dir
	var out, errs bytes.Buffer
	command.Stdout = &out
	command.Stderr = &errs
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s",
			executable, strings.Join(args, " "), err, strings.TrimSpace(errs.String()))
	}
	return out.String(), nil
}

// commitMessage names what a commit holds, in the terms the directory is kept
// in: drafts, by the names they are filed under.
func commitMessage(changed []string) string {
	switch len(changed) {
	case 0:
		return "Update drafts"
	case 1:
		return "Update " + changed[0]
	}
	return fmt.Sprintf("Update %d drafts", len(changed))
}

// ---- git -----------------------------------------------------------------

type gitVCS struct{ root string }

func (g *gitVCS) name() string { return "git" }

// git runs a command in the drafts directory. quotePath is off so a path
// outside ASCII arrives as its own bytes, not as escapes nothing here unescapes.
func (g *gitVCS) git(args ...string) (string, error) {
	return run(g.root, "git", append([]string{"-c", "core.quotePath=false"}, args...)...)
}

func (g *gitVCS) written() (changed, added []string) {
	// --relative names files relative to the working directory, which is the
	// drafts directory: a directory nested in a larger repository still talks
	// about its own files.
	tracked, _ := g.git("diff", "--relative", "-z", "--name-only", "--diff-filter=AM", "HEAD", "--", ".")
	others, _ := g.git("ls-files", "-z", "--others", "--exclude-standard", "--", ".")
	return splitNul(tracked), splitNul(others)
}

func (g *gitVCS) diff(rel string) (string, error) {
	return g.git("diff", "--relative", "-U0", "HEAD", "--", rel)
}

// historyScan bounds how far back the log is read for one timeline: a
// directory committed on every Put records the same draft many times, so the
// revisions examined can far outnumber the changes asked for.
const historyScan = 200

func (g *gitVCS) history(limit int) ([]revision, error) {
	log, err := g.git("log", "--relative", "--no-renames", "--diff-filter=AM",
		"-n", strconv.Itoa(historyScan),
		"--format=%x1e%H %ct", "--name-only", "--", ".")
	if err != nil {
		return nil, err
	}
	var revisions []revision
	for _, record := range strings.Split(log, "\x1e") {
		lines := strings.Split(strings.Trim(record, "\n"), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		id, seconds, ok := strings.Cut(lines[0], " ")
		if !ok {
			continue
		}
		stamp, err := strconv.ParseInt(seconds, 10, 64)
		if err != nil {
			continue
		}
		next := revision{id: id, when: time.Unix(stamp, 0)}
		for _, rel := range lines[1:] {
			if rel != "" {
				next.files = append(next.files, rel)
			}
		}
		if len(next.files) == 0 {
			continue
		}
		revisions = append(revisions, next)
		if limit > 0 && len(revisions) >= limit {
			break
		}
	}
	return revisions, nil
}

func (g *gitVCS) content(rev, rel string) (string, error) {
	// `:./` resolves the path against the working directory rather than the
	// repository root, which is not always the same directory.
	return g.git("show", rev+":./"+rel)
}

func (g *gitVCS) revDiff(rev, rel string) (string, error) {
	return g.git("show", "--relative", "-U0", "--format=", rev, "--", rel)
}

func (g *gitVCS) commit() (int, error) {
	// `-- .` holds the add to the drafts directory: a directory nested in a
	// larger repository must not sweep up the rest of it.
	if _, err := g.git("add", "-A", "--", "."); err != nil {
		return 0, err
	}
	staged, err := g.git("diff", "--cached", "--relative", "-z", "--name-only", "--", ".")
	if err != nil {
		return 0, err
	}
	changed := splitNul(staged)
	if len(changed) == 0 {
		return 0, nil
	}
	if _, err := g.git("commit", "--quiet", "--only", "-m", commitMessage(changed), "--", "."); err != nil {
		return 0, err
	}
	return len(changed), nil
}

func (g *gitVCS) push() error {
	if _, err := g.git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		return nil // nowhere to send it
	}
	_, err := g.git("push", "--quiet")
	return err
}

func splitNul(out string) []string {
	var paths []string
	for _, path := range strings.Split(out, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// ---- Sapling -------------------------------------------------------------

// saplingVCS speaks to Sapling, or to the Mercurial it grew out of: the
// commands used here mean the same thing to both.
type saplingVCS struct {
	root       string
	executable string
}

func (s *saplingVCS) name() string { return s.executable }

func (s *saplingVCS) run(args ...string) (string, error) {
	return run(s.root, s.executable, args...)
}

// written reads the status of the drafts directory. Status names files
// relative to the working directory, and `.` holds the answer to that
// directory.
func (s *saplingVCS) written() (changed, added []string) {
	out, err := s.run("status", "--no-status", "-0", "-m", "-a", ".")
	if err == nil {
		changed = splitNul(out)
	}
	out, err = s.run("status", "--no-status", "-0", "-u", ".")
	if err == nil {
		added = splitNul(out)
	}
	return changed, added
}

func (s *saplingVCS) diff(rel string) (string, error) {
	return s.run("diff", "-U", "0", "--", rel)
}

// historyTemplate prints one record per revision: a separator, the revision
// and its time, then the files it touched. Files are named from the
// repository's root, and are brought back to the drafts directory by relPath.
const historyTemplate = "\x1e{node} {date|hgdate}\n{files % \"{file}\\n\"}"

func (s *saplingVCS) history(limit int) ([]revision, error) {
	log, err := s.run("log", "-l", strconv.Itoa(historyScan), "--template", historyTemplate, ".")
	if err != nil {
		return nil, err
	}
	prefix, err := s.prefix()
	if err != nil {
		return nil, err
	}
	var revisions []revision
	for _, record := range strings.Split(log, "\x1e") {
		lines := strings.Split(strings.Trim(record, "\n"), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		id, stamp, ok := strings.Cut(lines[0], " ")
		if !ok {
			continue
		}
		// hgdate is "seconds offset"; the offset is the writer's timezone,
		// which a local time already accounts for.
		seconds, err := strconv.ParseInt(strings.Fields(stamp)[0], 10, 64)
		if err != nil {
			continue
		}
		next := revision{id: id, when: time.Unix(seconds, 0)}
		for _, file := range lines[1:] {
			if rel, ok := relPath(prefix, file); ok {
				next.files = append(next.files, rel)
			}
		}
		if len(next.files) == 0 {
			continue
		}
		revisions = append(revisions, next)
		if limit > 0 && len(revisions) >= limit {
			break
		}
	}
	return revisions, nil
}

// prefix is where the drafts directory sits in the repository, which is what
// a root-relative filename has to lose to become a local one.
func (s *saplingVCS) prefix() (string, error) {
	out, err := s.run("root")
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(strings.TrimSpace(out), s.root)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", nil
	}
	return filepath.ToSlash(rel) + "/", nil
}

// relPath brings a root-relative filename back to the drafts directory,
// reporting whether it is in it at all.
func relPath(prefix, file string) (string, bool) {
	rel, ok := strings.CutPrefix(file, prefix)
	if !ok || rel == "" || strings.Contains(rel, "/") {
		return "", false
	}
	return rel, true
}

func (s *saplingVCS) content(rev, rel string) (string, error) {
	return s.run("cat", "-r", rev, rel)
}

func (s *saplingVCS) revDiff(rev, rel string) (string, error) {
	return s.run("diff", "-c", rev, "-U", "0", "--", rel)
}

func (s *saplingVCS) commit() (int, error) {
	// addremove takes up what was written and lets go of what was deleted,
	// held to this directory by the `.`.
	if _, err := s.run("addremove", "."); err != nil {
		return 0, err
	}
	changed, added := s.written()
	staged := append(append([]string{}, changed...), added...)
	if len(staged) == 0 {
		return 0, nil
	}
	if _, err := s.run("commit", "-m", commitMessage(staged), "."); err != nil {
		return 0, err
	}
	return len(staged), nil
}

func (s *saplingVCS) push() error {
	if _, err := s.run("paths", "default"); err != nil {
		return nil // nowhere to send it
	}
	_, err := s.run("push", "--quiet")
	return err
}
