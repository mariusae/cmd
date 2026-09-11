package main

// Keeping the graph in step with its remote.
//
// A notes graph is a git repository, and syncing it is what makes the same
// notes readable from another machine: commit what was written here, take what
// was written elsewhere, and hand this back. Reflect's own sync does exactly
// this, and refl does it itself rather than through Reflect — the repository is
// the interface.
//
// A merge that conflicts is undone rather than left behind: a graph half in
// conflict renders as notes full of markers, and a background sync has no
// business leaving one there for the reader to discover.

import (
	"fmt"
	"strconv"
	"strings"
)

// syncReport says what a sync did, for the reader who asked. It is empty when
// nothing moved, which is the usual answer and not worth saying.
type syncReport struct {
	committed int
	pulled    int
	pushed    int
}

func (r syncReport) String() string {
	var parts []string
	if r.committed > 0 {
		parts = append(parts, count(r.committed, "file")+" committed")
	}
	if r.pulled > 0 {
		parts = append(parts, count(r.pulled, "commit")+" pulled")
	}
	if r.pushed > 0 {
		parts = append(parts, count(r.pushed, "commit")+" pushed")
	}
	return strings.Join(parts, ", ")
}

func (r syncReport) quiet() bool { return r.committed+r.pulled+r.pushed == 0 }

func count(many int, thing string) string {
	if many == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", many, thing)
}

// sync commits what is written here, merges what is on the remote, and pushes
// the result. A graph with no remote is committed and left alone; a graph
// outside git is an error.
func (g *graph) sync() (syncReport, error) {
	var report syncReport
	if _, err := g.git("rev-parse", "--git-dir"); err != nil {
		return report, fmt.Errorf("%s is not a git repository", g.root)
	}

	written, err := g.commitWritten()
	if err != nil {
		return report, err
	}
	report.committed = written

	upstream, err := g.git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return report, nil // no remote to sync with; the commit is the whole job
	}
	if _, err := g.git("fetch", "--quiet"); err != nil {
		return report, fmt.Errorf("fetching %s: %w", strings.TrimSpace(upstream), err)
	}

	behind, ahead, err := g.divergence()
	if err != nil {
		return report, err
	}
	if behind > 0 {
		if _, err := g.git("merge", "--no-edit", "@{u}"); err != nil {
			// Undo it: a conflicted graph is worse than an unsynced one, and
			// the reader never asked to resolve one right now.
			_, _ = g.git("merge", "--abort")
			return report, fmt.Errorf("merging %s conflicts; resolve it by hand", strings.TrimSpace(upstream))
		}
		report.pulled = behind
		if _, ahead, err = g.divergence(); err != nil {
			return report, err
		}
	}
	if ahead > 0 {
		if _, err := g.git("push", "--quiet"); err != nil {
			return report, fmt.Errorf("pushing to %s: %w", strings.TrimSpace(upstream), err)
		}
		report.pushed = ahead
	}
	return report, nil
}

// commitWritten records everything written in the graph since the last commit,
// and reports how many notes that was.
func (g *graph) commitWritten() (int, error) {
	// `-- .` holds the add to the graph: a graph nested in a larger
	// repository must not sweep up the rest of it.
	if _, err := g.git("add", "-A", "--", "."); err != nil {
		return 0, err
	}
	staged, err := g.git("diff", "--cached", "--relative", "-z", "--name-only")
	if err != nil {
		return 0, err
	}
	changed := splitNul(staged)
	if len(changed) == 0 {
		return 0, nil
	}
	notes := 0
	for _, rel := range changed {
		if isNotePath(rel) {
			notes++
		}
	}
	if _, err := g.git("commit", "--quiet", "-m", commitMessage(changed, notes)); err != nil {
		return 0, err
	}
	return len(changed), nil
}

// commitMessage names what the commit holds, the way Reflect's own does.
func commitMessage(changed []string, notes int) string {
	if len(changed) == 1 {
		if date := dailyDate(changed[0]); date != "" {
			return "Update daily note for " + date
		}
		return "Update " + changed[0]
	}
	if notes == len(changed) {
		return fmt.Sprintf("Update %d notes", notes)
	}
	return fmt.Sprintf("Update %d files", len(changed))
}

// divergence is how far the branch is behind its upstream and ahead of it.
func (g *graph) divergence() (behind, ahead int, err error) {
	counts, err := g.git("rev-list", "--left-right", "--count", "@{u}...HEAD")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(counts)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("git rev-list said %q", strings.TrimSpace(counts))
	}
	if behind, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, err
	}
	ahead, err = strconv.Atoi(fields[1])
	return behind, ahead, err
}
