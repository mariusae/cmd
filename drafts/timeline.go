package main

// The timeline: recent modifications, newest first, shown as the blocks that
// moved.
//
// Where the directory is a repository the answer comes out of its history,
// which — since every Put is committed — is the record of the writing itself.
// Where it is not, the answer comes out of the files: their own times, and
// what each draft opens with. Both are reverse-chronological lists of what was
// worked on, which is what a timeline is for.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// changeBlockLimit bounds one change's blocks, so a wholesale rewrite (or a
// draft's first commit) does not bury the changes around it.
const changeBlockLimit = 20

// A change is one draft as it stood after a change, with the blocks that moved.
type change struct {
	draft  draft
	when   time.Time
	blocks []blockContext
}

var hunkHeader = regexp.MustCompile(`^@@ -[0-9,]+ \+(\d+)(?:,(\d+))? @@`)

// timeline lists the most recent modifications, newest first.
//
// The answer is kept while the directory stands still. Reading it walks the
// history and reads a file per change, which is more work than a window has
// any business doing every couple of seconds.
func (d *dir) timeline(limit int) ([]change, error) {
	key, err := d.timelineKey()
	if err != nil {
		return nil, err
	}
	if d.changes != nil && d.changeKey == key && d.changeLimit >= limit {
		return truncate(d.changes, limit), nil
	}
	changes, err := d.readTimeline(limit)
	if err != nil {
		return nil, err
	}
	d.changes, d.changeKey, d.changeLimit = truncate(changes, limit), key, limit
	return d.changes, nil
}

func (d *dir) readTimeline(limit int) ([]change, error) {
	repo := d.repository()
	if repo == nil {
		return d.recentChanges(limit)
	}
	changes := d.writtenChanges(repo)
	if len(changes) < limit {
		recorded, err := d.recordedChanges(repo, limit-len(changes))
		if err != nil {
			return nil, err
		}
		changes = append(changes, recorded...)
	}
	return changes, nil
}

// timelineKey is what the timeline was read from: the revision it stands at,
// and every file written since, by the time and size of it. Two reads with the
// same key have the same answer.
func (d *dir) timelineKey() (string, error) {
	files, err := d.walk()
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(files)+1)
	if repo := d.repository(); repo != nil {
		if recent, err := repo.history(1); err == nil && len(recent) > 0 {
			parts = append(parts, recent[0].id)
		}
	}
	for _, file := range files {
		parts = append(parts, fmt.Sprintf("%s@%d", file.name, file.modTime.UnixNano()))
	}
	return strings.Join(parts, "\x00"), nil
}

// recentChanges is the timeline of a directory under no version control: the
// drafts by their own times, each shown by what it opens with. There is no
// history to say what changed in one, so it stands for itself.
func (d *dir) recentChanges(limit int) ([]change, error) {
	files, err := d.walk()
	if err != nil {
		return nil, err
	}
	var changes []change
	for _, file := range files {
		if len(changes) >= limit {
			break
		}
		content, title, err := d.read(file)
		if err != nil {
			continue
		}
		blocks := prepareBlocks(content).blockContextsAt([]int{openingPosition(content)}, nil)
		changes = append(changes, change{
			draft: draft{
				path: file.path, name: file.name, title: title, modTime: file.modTime,
			},
			when:   file.modTime,
			blocks: truncate(blocks, changeBlockLimit),
		})
	}
	return changes, nil
}

// openingPosition is where a draft begins: the first character of it that was
// actually written. Offset zero is not that — a draft may open with
// frontmatter, or with the blank line under it — and a block asked for at a
// position between blocks is no block at all.
func openingPosition(content string) int {
	split := splitFrontmatter(content)
	body := split.body
	for index := 0; index < len(body); index++ {
		if !strings.ContainsRune(" \t\r\n", rune(body[index])) {
			return split.bodyOffset + index
		}
	}
	return split.bodyOffset
}

// writtenChanges reports files edited since the last commit, and files not
// recorded at all — what is written but not yet kept is the newest of all.
func (d *dir) writtenChanges(repo vcs) []change {
	changed, added := repo.written()
	whole := map[string]bool{}
	var names []string
	for _, rel := range added {
		whole[rel] = true
		names = append(names, rel)
	}
	names = append(names, changed...)

	var changes []change
	for _, rel := range names {
		if !strings.HasSuffix(rel, ".md") || strings.HasPrefix(rel, ".") {
			continue
		}
		path := filepath.Join(d.root, rel)
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
			diff, _ = repo.diff(rel)
		}
		if next, ok := changeAt(d.root, rel, string(content), diff, whole[rel], info.ModTime()); ok {
			changes = append(changes, next)
		}
	}
	return changes
}

// recordedChanges walks the history newest first, one change per file per
// revision.
func (d *dir) recordedChanges(repo vcs, limit int) ([]change, error) {
	revisions, err := repo.history(0)
	if err != nil {
		return nil, err
	}
	var changes []change
	for _, rev := range revisions {
		for _, rel := range rev.files {
			if !strings.HasSuffix(rel, ".md") || strings.HasPrefix(rel, ".") {
				continue
			}
			content, err := repo.content(rev.id, rel)
			if err != nil {
				continue
			}
			diff, err := repo.revDiff(rev.id, rel)
			if err != nil {
				continue
			}
			next, ok := changeAt(d.root, rel, content, diff, false, rev.when)
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

// changeAt builds a change from a file's content and the diff that produced
// it: every block holding a changed line, deduplicated.
func changeAt(root, rel, content, diff string, whole bool, when time.Time) (change, bool) {
	positions := changedPositions(content, diff, whole)
	if len(positions) == 0 {
		return change{}, false
	}
	blocks := prepareBlocks(content).blockContextsAt(positions, nil)
	if len(blocks) == 0 {
		return change{}, false
	}
	return change{
		draft: draft{
			path:    filepath.Join(root, rel),
			name:    rel,
			title:   deriveTitle(rel, content),
			modTime: when,
		},
		when:   when,
		blocks: truncate(blocks, changeBlockLimit),
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
