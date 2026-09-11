package main

// The timeline: recent changes to notes, read out of the graph's git history
// and its working tree, shown as the blocks that changed.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// timelineBlockLimit bounds one change's blocks, so a wholesale rewrite (or a
// note's first commit) does not bury the changes around it.
const timelineBlockLimit = 20

// commitScanLimit bounds how far back the log is read for one timeline: a
// graph that syncs on a timer commits the same daily note many times, so the
// commits examined can far outnumber the changes asked for.
const commitScanLimit = 200

// A change is one note as it stood after a change, with the blocks that moved.
type change struct {
	note   note
	when   time.Time
	blocks []blockContext
}

var hunkHeader = regexp.MustCompile(`^@@ -[0-9,]+ \+(\d+)(?:,(\d+))? @@`)

// timeline lists the most recent changes, newest first: the working tree
// first, since what is written but not yet committed is the newest of all,
// then the commits behind it.
//
// The answer is kept while the history and the working tree stand still.
// Reading it walks the log and reads a file per change, which is more work
// than a window has any business doing every couple of seconds.
func (g *graph) timeline(limit int) ([]change, error) {
	if _, err := g.git("rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%s is not a git repository, so it has no history", g.root)
	}
	key := g.readKey()
	if g.changes != nil && g.changeKey == key && g.changeLimit >= limit {
		return truncate(g.changes, limit), nil
	}
	changes := g.workingTreeChanges()
	if len(changes) < limit {
		committed, err := g.committedChanges(limit - len(changes))
		if err != nil {
			return nil, err
		}
		changes = append(changes, committed...)
	}
	g.changes, g.changeKey, g.changeLimit = truncate(changes, limit), key, limit
	return g.changes, nil
}

// readKey is what the timeline was read from: the commit it stands at, and
// every note written since, by the time and size of its file. Two reads with
// the same key have the same answer.
func (g *graph) readKey() string {
	head, _ := g.git("rev-parse", "HEAD")
	parts := []string{strings.TrimSpace(head)}
	for _, rel := range g.dirtyList() {
		stamp := rel
		if info, err := os.Stat(filepath.Join(g.root, filepath.FromSlash(rel))); err == nil {
			stamp = fmt.Sprintf("%s@%d:%d", rel, info.ModTime().UnixNano(), info.Size())
		}
		parts = append(parts, stamp)
	}
	return strings.Join(parts, "\x00")
}

// workingTreeChanges reports notes edited since the last commit, and notes not
// yet committed at all.
func (g *graph) workingTreeChanges() []change {
	var changes []change
	tracked, untracked := g.written()
	whole := map[string]bool{}
	var paths []string
	for _, rel := range untracked {
		whole[rel] = true
		paths = append(paths, rel)
	}
	paths = append(paths, tracked...)

	for _, rel := range paths {
		if !isNotePath(rel) {
			continue
		}
		path := filepath.Join(g.root, filepath.FromSlash(rel))
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		var diff string
		if !whole[rel] {
			diff, _ = g.git("diff", "--relative", "-U0", "HEAD", "--", rel)
		}
		next, ok := g.changeAt(rel, string(content), diff, whole[rel], info.ModTime())
		if ok {
			changes = append(changes, next)
		}
	}
	return changes
}

// committedChanges walks the log newest first, one change per note per commit.
func (g *graph) committedChanges(limit int) ([]change, error) {
	log, err := g.git("log", "--relative", "--no-renames", "--diff-filter=AM",
		"-n", strconv.Itoa(commitScanLimit),
		"--format=%x1e%H %ct", "--name-only", "--", "*.md")
	if err != nil {
		return nil, err
	}

	var changes []change
	for _, record := range strings.Split(log, "\x1e") {
		lines := strings.Split(strings.Trim(record, "\n"), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		commit, seconds, ok := strings.Cut(lines[0], " ")
		if !ok {
			continue
		}
		stamp, err := strconv.ParseInt(seconds, 10, 64)
		if err != nil {
			continue
		}
		when := time.Unix(stamp, 0)
		for _, rel := range lines[1:] {
			if rel == "" || !isNotePath(rel) {
				continue
			}
			// `:./` resolves the path against the graph rather than the
			// repository root, which is not always the same directory.
			content, err := g.git("show", commit+":./"+rel)
			if err != nil {
				continue
			}
			diff, err := g.git("show", "--relative", "-U0", "--format=", commit, "--", rel)
			if err != nil {
				continue
			}
			next, ok := g.changeAt(rel, content, diff, false, when)
			if !ok {
				continue
			}
			changes = append(changes, next)
			if len(changes) >= limit {
				return changes, nil
			}
		}
	}
	return changes, nil
}

// changeAt builds a change from a note's content and the diff that produced
// it: every block holding a changed line, deduplicated.
func (g *graph) changeAt(rel, content, diff string, whole bool, when time.Time) (change, bool) {
	split := splitFrontmatter(content)
	fields := parseFrontmatter(split.raw)
	if fields.private {
		return change{}, false
	}
	positions := changedPositions(content, diff, whole)
	if len(positions) == 0 {
		return change{}, false
	}
	blocks := prepareBlocks(content).blockContextsAt(positions, nil)
	if len(blocks) == 0 {
		return change{}, false
	}
	if len(blocks) > timelineBlockLimit {
		blocks = blocks[:timelineBlockLimit]
	}
	return change{
		note: note{
			path:    filepath.Join(g.root, filepath.FromSlash(rel)),
			rel:     rel,
			title:   deriveTitle(rel, fields.title, split.body),
			modTime: when,
		},
		when:   when,
		blocks: blocks,
	}, true
}

// changedPositions turns a unified diff's added lines into offsets in content.
// A hunk that only deletes still anchors at the line it left behind, so a
// removal shows the block it was removed from.
func changedPositions(content, diff string, whole bool) []int {
	starts := lineStarts(content)
	if whole {
		return starts
	}
	var positions []int
	seen := map[int]bool{}
	for _, line := range strings.Split(diff, "\n") {
		groups := hunkHeader.FindStringSubmatch(line)
		if groups == nil {
			continue
		}
		first, err := strconv.Atoi(groups[1])
		if err != nil {
			continue
		}
		count := 1
		if groups[2] != "" {
			if count, err = strconv.Atoi(groups[2]); err != nil {
				continue
			}
		}
		if count == 0 {
			count = 1 // a pure deletion: show where it was
		}
		for number := first; number < first+count; number++ {
			index := number - 1
			if index < 0 || index >= len(starts) || seen[index] {
				continue
			}
			seen[index] = true
			positions = append(positions, starts[index])
		}
	}
	return positions
}

func lineStarts(content string) []int {
	starts := []int{0}
	for index := 0; index < len(content); index++ {
		if content[index] == '\n' && index+1 < len(content) {
			starts = append(starts, index+1)
		}
	}
	return starts
}

// isNotePath reports whether a graph-relative path names an eligible note.
func isNotePath(rel string) bool {
	if !strings.HasSuffix(rel, ".md") {
		return false
	}
	parts := strings.Split(rel, "/")
	for index, part := range parts {
		if part == "" || strings.HasPrefix(part, ".") {
			return false
		}
		if index == 0 && len(parts) > 1 && reservedTrees[strings.ToLower(part)] {
			return false
		}
	}
	return true
}

func (g *graph) git(args ...string) (string, error) {
	// quotePath off: a path outside ASCII arrives as its own bytes, not as
	// escapes no caller here unescapes.
	command := exec.Command("git",
		append([]string{"-C", g.root, "-c", "core.quotePath=false"}, args...)...)
	var out, errs bytes.Buffer
	command.Stdout = &out
	command.Stderr = &errs
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errs.String()))
	}
	return out.String(), nil
}
