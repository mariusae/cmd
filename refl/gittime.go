package main

// When a note last changed, taken from git.
//
// File times are the wrong answer in a synced graph: a clone, a checkout, or
// a pull rewrites them all at once, and every note looks equally new. The last
// commit that touched a note is what actually dates it. A note git has not
// seen — untracked, or edited since its last commit — keeps its file time,
// which is the only record of that change there is.
//
// Reading the whole log costs about as much as reading the graph, so the
// answer is cached per graph and carried forward commit by commit.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// stampCache is what one graph's history came to, as of one commit.
type stampCache struct {
	Head  string           `json:"head"`
	Times map[string]int64 `json:"times"`
}

// stamp dates each note: the last commit that touched it, or its file time
// when git cannot date it.
func (g *graph) stamp(files []noteFile) []noteFile {
	commits := g.commitTimes()
	dirty := g.dirtyNotes()
	for index := range files {
		files[index].at = files[index].modTime
		if dirty[files[index].rel] {
			continue
		}
		if at, ok := commits[files[index].rel]; ok {
			files[index].at = at
		}
	}
	return files
}

// commitTimes is the last commit time of every note, kept between calls and
// carried forward as commits land, so a window open all day reads the log once.
func (g *graph) commitTimes() map[string]time.Time {
	head, err := g.git("rev-parse", "HEAD")
	if err != nil {
		return nil // not a repository, or nothing committed yet
	}
	head = strings.TrimSpace(head)
	if g.head == head && g.stamps != nil {
		return g.stamps
	}
	if g.stamps == nil {
		cached := g.readStamps()
		g.stamps, g.head = decodeStamps(cached.Times), cached.Head
	}
	switch {
	case g.head == head:
	case g.head != "" && g.isAncestor(g.head, head):
		g.scanCommits(g.head + ".." + head)
	default:
		g.stamps = map[string]time.Time{}
		g.scanCommits("")
	}
	g.head = head
	g.writeStamps(head)
	return g.stamps
}

// scanCommits walks the log newest first, dating each note by the first commit
// it appears in — which, over a range newer than what is already known, is
// also the newest one there is.
func (g *graph) scanCommits(revisions string) {
	args := []string{"log", "--relative", "--no-renames", "--diff-filter=AM",
		"--format=%x1e%ct", "--name-only"}
	if revisions != "" {
		args = append(args, revisions)
	}
	log, err := g.git(append(args, "--", "*.md")...)
	if err != nil {
		return
	}
	if g.stamps == nil {
		g.stamps = map[string]time.Time{}
	}
	seen := map[string]bool{}
	for _, record := range strings.Split(log, "\x1e") {
		lines := strings.Split(strings.Trim(record, "\n"), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		seconds, err := strconv.ParseInt(lines[0], 10, 64)
		if err != nil {
			continue
		}
		for _, rel := range lines[1:] {
			if rel == "" || seen[rel] || !isNotePath(rel) {
				continue
			}
			seen[rel] = true
			g.stamps[rel] = time.Unix(seconds, 0)
		}
	}
}

// written is the notes changed since the last commit that touched them, and
// the notes never committed at all.
func (g *graph) written() (changed, untracked []string) {
	tracked, _ := g.git("diff", "--relative", "-z", "--name-only", "--diff-filter=AM", "HEAD", "--", "*.md")
	others, _ := g.git("ls-files", "-z", "--others", "--exclude-standard", "--", "*.md")
	return splitNul(tracked), splitNul(others)
}

// dirtyList is every note git's history cannot date, in one list.
func (g *graph) dirtyList() []string {
	changed, untracked := g.written()
	return append(changed, untracked...)
}

// dirtyNotes are those same notes, to look one up by.
func (g *graph) dirtyNotes() map[string]bool {
	dirty := map[string]bool{}
	for _, rel := range g.dirtyList() {
		dirty[rel] = true
	}
	return dirty
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

func (g *graph) isAncestor(older, newer string) bool {
	_, err := g.git("merge-base", "--is-ancestor", older, newer)
	return err == nil
}

// stampPath names this graph's cache. The graph's own directory is left
// alone: a notes graph holds notes, and a tool's bookkeeping is not one.
func (g *graph) stampPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(g.root))
	return filepath.Join(dir, "refl", hex.EncodeToString(sum[:8])+".json")
}

func (g *graph) readStamps() stampCache {
	var cached stampCache
	path := g.stampPath()
	if path == "" {
		return cached
	}
	content, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(content, &cached) != nil {
		return stampCache{}
	}
	return cached
}

// writeStamps replaces the cache in one step, so a reader started midway
// through never sees half a file.
func (g *graph) writeStamps(head string) {
	path := g.stampPath()
	if path == "" {
		return
	}
	content, err := json.Marshal(stampCache{Head: head, Times: encodeStamps(g.stamps)})
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "stamps-*.json")
	if err != nil {
		return
	}
	_, err = temporary.Write(content)
	if closeErr := temporary.Close(); err != nil || closeErr != nil ||
		os.Rename(temporary.Name(), path) != nil {
		_ = os.Remove(temporary.Name())
	}
}

func encodeStamps(times map[string]time.Time) map[string]int64 {
	seconds := make(map[string]int64, len(times))
	for rel, at := range times {
		seconds[rel] = at.Unix()
	}
	return seconds
}

func decodeStamps(seconds map[string]int64) map[string]time.Time {
	if seconds == nil {
		return nil
	}
	times := make(map[string]time.Time, len(seconds))
	for rel, at := range seconds {
		times[rel] = time.Unix(at, 0)
	}
	return times
}
