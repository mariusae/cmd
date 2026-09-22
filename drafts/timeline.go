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
	"sort"
	"strconv"
	"strings"
	"time"
)

// changeBlockLimit bounds one change's blocks, so a wholesale rewrite (or a
// draft's first commit) does not bury the changes around it.
const changeBlockLimit = 20

// Changes belong to one writing burst when they touch nearby blocks of the
// same draft within this long. One blank source line still makes blocks
// neighbours: it is Markdown's ordinary separator between paragraphs.
const (
	changeTimeGap   = 5 * time.Minute
	changeBlockGap  = 1
	changeBlockSpan = 20
)

// A change is one draft as it stood after a change, with the blocks that moved.
type change struct {
	draft  draft
	when   time.Time
	since  time.Time
	blocks []blockContext
	source string
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
	return d.recordedChanges(repo, d.writtenChanges(repo), limit)
}

// timelineKey is what the timeline was read from: the revision it stands at,
// and every file written since, by the time and size of it. Two reads with the
// same key have the same answer.
func (d *dir) timelineKey() (string, error) {
	files, err := d.walk()
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(files)+2)
	parts = append(parts, fmt.Sprintf("archive=%t", d.includeArchive))
	if repo := d.repository(); repo != nil {
		if recent, err := repo.history(1); err == nil && len(recent) > 0 {
			parts = append(parts, recent[0].id)
		}
	}
	for _, file := range files {
		parts = append(parts, fmt.Sprintf("%s@%d", file.name, file.fileTime.UnixNano()))
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
			since:  file.modTime,
			blocks: truncate(blocks, changeBlockLimit),
			source: content,
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
		if !d.shows(rel) {
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

// recordedChanges walks history newest first, folding nearby edits into the
// changes already found in the working tree. Once enough changes have been
// found, it reads through the coalescing window of the last one before
// stopping, so a page is not left short by several commits collapsing into it.
func (d *dir) recordedChanges(repo vcs, changes []change, limit int) ([]change, error) {
	revisions, err := repo.history(0)
	if err != nil {
		return nil, err
	}
	for index := range changes {
		if changes[index].since.IsZero() {
			changes[index].since = changes[index].when
		}
		changes[index].blocks = coalesceTimelineBlocks(changes[index].source, changes[index].blocks)
	}
	cutoff := changeCutoff(changes, limit)
	for _, rev := range revisions {
		if !cutoff.IsZero() && rev.when.Before(cutoff) {
			break
		}
		for _, file := range rev.files {
			if !d.shows(file.name) {
				continue
			}
			content, err := repo.content(rev.id, file.path)
			if err != nil {
				continue
			}
			paths := []string{file.path}
			if file.from != "" {
				paths = append([]string{file.from}, paths...)
			}
			diff, err := repo.revDiff(rev.id, paths...)
			if err != nil {
				continue
			}
			next, ok := changeAt(d.root, file.name, content, diff, false, rev.when)
			if !ok {
				continue
			}
			changes = addTimelineChange(changes, next)
		}
		cutoff = changeCutoff(changes, limit)
	}
	return truncate(changes, limit), nil
}

func changeCutoff(changes []change, limit int) time.Time {
	if limit <= 0 || len(changes) < limit {
		return time.Time{}
	}
	cutoff := changes[0].since.Add(-changeTimeGap)
	for _, changed := range changes[1:limit] {
		if next := changed.since.Add(-changeTimeGap); next.Before(cutoff) {
			cutoff = next
		}
	}
	return cutoff
}

// addTimelineChange merges an older change into a newer writing burst when
// both its time and its source range are near. The newer source wins: a burst
// is shown as it stood after the burst, rather than as a stack of intermediate
// versions of the same paragraph.
func addTimelineChange(changes []change, older change) []change {
	if older.since.IsZero() {
		older.since = older.when
	}
	older.blocks = coalesceTimelineBlocks(older.source, older.blocks)
	for index := len(changes) - 1; index >= 0; index-- {
		newer := &changes[index]
		if newer.draft.name != older.draft.name {
			continue
		}
		if newer.since.Sub(older.when) > changeTimeGap {
			continue
		}
		aligned := alignTimelineBlocks(newer.source, older.blocks)
		if !nearbyBlocks(newer.blocks, aligned) {
			continue
		}
		newer.blocks = coalesceTimelineBlocks(newer.source, append(newer.blocks, aligned...))
		if older.since.Before(newer.since) {
			newer.since = older.since
		}
		return changes
	}
	return append(changes, older)
}

func nearbyBlocks(left, right []blockContext) bool {
	for _, one := range left {
		for _, other := range right {
			if blocksCoalesce(one.line, contextEndLine(one), other.line, contextEndLine(other)) {
				return true
			}
		}
	}
	return false
}

func blocksCoalesce(left, leftEnd, right, rightEnd int) bool {
	if left > right {
		left, right = right, left
		leftEnd, rightEnd = rightEnd, leftEnd
	}
	if right <= leftEnd {
		return true
	}
	return right-leftEnd-1 <= changeBlockGap && max(leftEnd, rightEnd)-left+1 <= changeBlockSpan
}

func contextEndLine(block blockContext) int {
	if block.endLine >= block.line {
		return block.endLine
	}
	return block.line + strings.Count(block.text, "\n")
}

// alignTimelineBlocks relocates an older complete block when its text still
// occurs in the newest version. This accounts for lines inserted above it
// during the same burst; when the text itself changed, its old location remains
// the best anchor and will overlap the newer context that replaced it.
func alignTimelineBlocks(source string, blocks []blockContext) []blockContext {
	aligned := append([]blockContext(nil), blocks...)
	for index := range aligned {
		at := strings.Index(source, aligned[index].text)
		if at < 0 {
			continue
		}
		aligned[index].line = lineNumberAt(source, at)
		aligned[index].endLine = aligned[index].line + strings.Count(aligned[index].text, "\n")
	}
	return aligned
}

// coalesceTimelineBlocks unions overlapping contexts and contexts separated by
// at most one blank line. Every range begins and ends at a complete semantic
// Markdown block; the union therefore never clips the block that was updated.
func coalesceTimelineBlocks(source string, blocks []blockContext) []blockContext {
	if len(blocks) < 2 {
		return blocks
	}
	sorted := append([]blockContext(nil), blocks...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].line != sorted[j].line {
			return sorted[i].line < sorted[j].line
		}
		return contextEndLine(sorted[i]) > contextEndLine(sorted[j])
	})

	type lineRange struct{ first, last int }
	ranges := make([]lineRange, 0, len(sorted))
	for _, block := range sorted {
		next := lineRange{first: block.line, last: contextEndLine(block)}
		if len(ranges) > 0 && blocksCoalesce(
			ranges[len(ranges)-1].first, ranges[len(ranges)-1].last, next.first, next.last,
		) {
			current := &ranges[len(ranges)-1]
			if next.last > current.last {
				current.last = next.last
			}
			continue
		}
		ranges = append(ranges, next)
	}

	lines := strings.Split(source, "\n")
	coalesced := make([]blockContext, 0, len(ranges))
	for _, span := range ranges {
		if span.first < 1 {
			span.first = 1
		}
		if span.last > len(lines) {
			span.last = len(lines)
		}
		if span.last < span.first {
			continue
		}
		text := strings.Join(lines[span.first-1:span.last], "\n")
		text = strings.TrimRight(text, " \t\r\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		coalesced = append(coalesced, blockContext{
			text: text, line: span.first, endLine: span.last,
		})
	}
	return coalesced
}

// changeAt builds a change from a file's content and the diff that produced
// it: every block holding a changed line, deduplicated.
func changeAt(root, rel, content, diff string, whole bool, when time.Time) (change, bool) {
	positions := changedPositions(content, diff, whole)
	if len(positions) == 0 {
		return change{}, false
	}
	blocks := prepareBlocks(content).blockContextsAt(positions, nil)
	blocks = coalesceTimelineBlocks(content, blocks)
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
		since:  when,
		blocks: truncate(blocks, changeBlockLimit),
		source: content,
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
