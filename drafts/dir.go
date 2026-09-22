package main

// The drafts directory: a flat directory of Markdown files, one per draft,
// each with an optional companion holding the notes for it.
//
// There is nothing else to it — no index, no database, no frontmatter a draft
// is required to carry. A draft is a file, its title is the first thing in it
// that reads like one, and its age is its last recorded change where there is
// history, or the file's own time where there is not.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// defaultDir is where drafts live when nothing says otherwise.
const defaultDir = "drafts"

// notesSuffix marks a draft's companion notes file. A companion is not itself
// a draft: it is listed with nothing of its own, and belongs to the draft it
// is named after.
const notesSuffix = "-notes.md"

// readmeName is documentation for the directory, not one of its drafts.
const readmeName = "README.md"

// archiveSubdir holds the drafts that are done with. It is the one directory
// under a drafts directory, and it is not a filing system growing back: it says
// only that a draft is no longer being written, which is the one thing a flat
// directory cannot say about a file it keeps showing. What is there is left out
// of everything until it is asked for.
const archiveSubdir = "archive"

// A draft is one Markdown file in the directory.
type draft struct {
	path    string // absolute
	name    string // relative to the drafts directory
	title   string
	modTime time.Time
}

// archived reports whether a draft is one of the archived ones, by where it is
// filed. A draft is where it is: nothing else records this.
func (s draft) archived() bool { return filepath.Dir(s.name) == archiveSubdir }

// A dir reads drafts out of a directory, remembering what it has already
// parsed: a listing runs over every draft, and re-reading each one on every
// refresh would make the window's cost the size of the directory.
type dir struct {
	root  string
	cache map[string]cachedDraft
	// includeArchive takes the archive into everything this reads: the
	// listing, the search and the timeline alike. It is off, because an
	// archived draft is one put away.
	includeArchive bool
	// The repository the directory sits in, looked for once: asking costs a
	// subprocess, and the answer does not change while the program runs.
	repo   vcs
	probed bool
	// The last timeline read, what it was read from, and how far it went.
	changes     []change
	changeKey   string
	changeLimit int
	// Per-file commit times are expensive enough to read once per revision and
	// set of visible files, rather than on every refresh of an Apex window.
	timeKey      string
	recordedTime map[string]time.Time
}

type cachedDraft struct {
	modTime time.Time
	title   string
}

func openDir(root string) *dir {
	return &dir{root: root, cache: map[string]cachedDraft{}}
}

// findDir resolves the drafts directory: an explicit one, then $DRAFTS_DIR,
// then ~/drafts.
//
// A directory that is not there yet is not an error. Nothing is created to
// answer a question — a search of a directory that does not exist finds
// nothing, which is the truth — and the directory is made when something is
// first written into it.
func findDir(explicit string, getenv func(string) string, home func() (string, error)) (string, error) {
	path := explicit
	if path == "" {
		path = getenv("DRAFTS_DIR")
	}
	if path == "" {
		where, err := home()
		if err != nil {
			return "", err
		}
		path = filepath.Join(where, defaultDir)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", path)
	}
	return path, nil
}

// repository is the version control the directory sits in, or nil. It is
// looked for once: a directory does not join a repository while a command runs.
func (d *dir) repository() vcs {
	if !d.probed {
		d.repo, d.probed = openVCS(d.root), true
	}
	return d.repo
}

// archiveRoot is where archived drafts live.
func (d *dir) archiveRoot() string { return filepath.Join(d.root, archiveSubdir) }

// shows reports whether a file of the directory, named relative to it, is one
// this reading is about: a Markdown file of the directory itself, and of the
// archive when the archive has been asked for.
func (d *dir) shows(rel string) bool {
	if filepath.Base(rel) == readmeName || !strings.HasSuffix(rel, ".md") || strings.HasPrefix(rel, ".") {
		return false
	}
	switch filepath.Dir(rel) {
	case ".":
		return true
	case archiveSubdir:
		return d.includeArchive
	}
	return false
}

// make creates the drafts directory, for whatever is about to be written into
// it.
func (d *dir) make() error { return os.MkdirAll(d.root, 0o755) }

// draftFile is a Markdown file found on disk, before it is read. Its name is
// relative to the drafts directory, so an archived draft says where it is.
type draftFile struct {
	path     string
	name     string
	modTime  time.Time // the last edit, from history where one was recorded
	fileTime time.Time // the filesystem time, for detecting content changes
}

// walk lists the directory's Markdown files, newest first, and the archive's
// too where the archive has been asked for. The listing is flat: a drafts
// directory is a directory of drafts, and a tree of them is a filing system,
// which is a different tool. The archive is the one exception, and it is a flat
// directory of drafts itself.
func (d *dir) walk() ([]draftFile, error) {
	files, err := readDrafts(d.root, "")
	if err != nil {
		return nil, err
	}
	if d.includeArchive {
		archived, err := readDrafts(d.archiveRoot(), archiveSubdir)
		if err != nil {
			return nil, err
		}
		files = append(files, archived...)
	}
	d.applyRecordedTimes(files)
	byRecency(files)
	return files, nil
}

// applyRecordedTimes replaces the incidental filesystem times of clean,
// tracked files with the times of their last changes in history. A checkout
// writes many files at once and therefore makes their mtimes say when they were
// checked out, not when the drafts were last worked on. Files changed or added
// in the working tree keep their own times: that writing is newer than history.
func (d *dir) applyRecordedTimes(files []draftFile) {
	repo := d.repository()
	if repo == nil || len(files) == 0 {
		return
	}
	names := make([]string, len(files))
	for index, file := range files {
		names[index] = file.name
	}

	// The newest relevant revision and the visible names make a stable cache
	// key. A newly committed file changes the revision; showing the archive or
	// adding an untracked file changes the names.
	latest := ""
	if revisions, err := repo.history(1); err == nil && len(revisions) > 0 {
		latest = revisions[0].id
	}
	key := latest + "\x00" + strings.Join(names, "\x00")
	if d.recordedTime == nil || d.timeKey != key {
		times, err := repo.modTimes(names)
		if err != nil {
			return
		}
		d.timeKey, d.recordedTime = key, times
	}

	changed, added := repo.written()
	dirty := make(map[string]bool, len(changed)+len(added))
	for _, name := range changed {
		dirty[name] = true
	}
	for _, name := range added {
		dirty[name] = true
	}
	for index := range files {
		if when, ok := d.recordedTime[files[index].name]; ok && !dirty[files[index].name] {
			files[index].modTime = when
		}
	}
}

// readDrafts is one directory's Markdown files, named under prefix. A directory
// that is not there holds no drafts, which is not an error: nothing is created
// to answer a question.
func readDrafts(root, prefix string) ([]draftFile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []draftFile
	for _, entry := range entries {
		name := entry.Name()
		if name == readmeName || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
			continue
		}
		// Only regular files: a symlink points outside the directory as far as
		// anything here can prove.
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, draftFile{
			path:     filepath.Join(root, name),
			name:     filepath.Join(prefix, name),
			modTime:  info.ModTime(),
			fileTime: info.ModTime(),
		})
	}
	return files, nil
}

// byRecency orders files most recently modified first.
func byRecency(files []draftFile) {
	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.After(files[j].modTime)
		}
		return files[i].name < files[j].name
	})
}

// list returns the drafts, most recently modified first. A companion is left
// out — it belongs to its draft, and listing it beside one would say there
// were two — but only where the draft it belongs to is there. A `-notes.md`
// with nothing to be the notes of is a draft called Notes-something, or the
// notes of a draft since deleted; either way it is the only copy of what is
// written in it, and it is not going to be hidden.
func (d *dir) list() ([]draft, error) {
	files, err := d.walk()
	if err != nil {
		return nil, err
	}
	// By path, not by name: the archive may hold a draft called what one here
	// is called, and a companion belongs to the draft beside it.
	present := make(map[string]bool, len(files))
	for _, file := range files {
		present[file.path] = true
	}
	drafts := make([]draft, 0, len(files))
	for _, file := range files {
		if isNotesName(file.name) && present[draftOf(file.path)] {
			continue
		}
		title, err := d.title(file)
		if err != nil {
			continue
		}
		drafts = append(drafts, draft{
			path: file.path, name: file.name, title: title, modTime: file.modTime,
		})
	}
	return drafts, nil
}

// title names a draft, reusing the last read of a file whose filesystem time
// has not moved. The displayed modification time may come from history instead.
func (d *dir) title(file draftFile) (string, error) {
	if cached, ok := d.cache[file.path]; ok && cached.modTime.Equal(file.fileTime) {
		return cached.title, nil
	}
	content, err := os.ReadFile(file.path)
	if err != nil {
		return "", err
	}
	title := deriveTitle(file.name, string(content))
	d.cache[file.path] = cachedDraft{modTime: file.fileTime, title: title}
	return title, nil
}

// read returns a draft's text and the title derived from it, recording the
// title for the listing to reuse.
func (d *dir) read(file draftFile) (string, string, error) {
	content, err := os.ReadFile(file.path)
	if err != nil {
		return "", "", err
	}
	title := deriveTitle(file.name, string(content))
	d.cache[file.path] = cachedDraft{modTime: file.fileTime, title: title}
	return string(content), title, nil
}

var atxTitle = regexp.MustCompile(`^ {0,3}#(?:[ \t]+(.*?))?[ \t]*$`)

// deriveTitle is what a draft is called: a frontmatter `title:`, else the
// first H1, else the first line with anything on it, else the file's own name.
//
// The first line stands in because a draft is often begun before it is titled,
// and what someone wrote first is what the draft is about. Frontmatter is
// skipped throughout: it is bookkeeping, and a draft named `id: 01j...` would
// be named after nothing.
func deriveTitle(name, content string) string {
	split := splitFrontmatter(content)
	if title := frontmatterTitle(split.raw); title != "" {
		return oneLine(title)
	}
	if heading := firstHeading(split.body); heading != "" {
		return oneLine(heading)
	}
	for _, line := range strings.Split(split.body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return oneLine(trimmed)
		}
	}
	if name == "" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(name), ".md")
}

// firstHeading is the text of the first non-empty H1 outside any code fence.
func firstHeading(body string) string {
	fenced := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case fenced != "":
			if strings.HasPrefix(trimmed, fenced) {
				fenced = ""
			}
			continue
		case strings.HasPrefix(trimmed, "```"):
			fenced = "```"
			continue
		case strings.HasPrefix(trimmed, "~~~"):
			fenced = "~~~"
			continue
		}
		groups := atxTitle.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if groups == nil {
			continue
		}
		text := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(groups[1]), "#"))
		if text != "" {
			return text
		}
	}
	return ""
}

var (
	embeddedWikiLink = regexp.MustCompile(`\[\[([^\[\]\n]*)\]\]`)
	markdownLink     = regexp.MustCompile(`\[([^\[\]\n]*)\]\([^)\n]*\)`)
)

// oneLine is a title as a reader sees it in a list: links flattened to their
// text, and whatever markup a heading carried left as it was written.
func oneLine(title string) string {
	title = embeddedWikiLink.ReplaceAllStringFunc(strings.TrimSpace(title), func(match string) string {
		inner := match[2 : len(match)-2]
		if pipe := strings.IndexByte(inner, '|'); pipe >= 0 {
			if alias := strings.TrimSpace(inner[pipe+1:]); alias != "" {
				return alias
			}
			inner = inner[:pipe]
		}
		return strings.TrimSpace(inner)
	})
	return markdownLink.ReplaceAllString(title, "$1")
}

// isNotesName reports whether a file is a draft's companion rather than a
// draft. `-notes.md` alone is a draft called `-notes`, not the companion of a
// draft with no name.
func isNotesName(name string) bool {
	return len(name) > len(notesSuffix) && strings.HasSuffix(name, notesSuffix)
}

// notesFor is where a draft's notes live: the sibling `<name>-notes.md`. A
// notes file's own notes are itself, so Notes on one is where it already is
// rather than a companion's companion.
func notesFor(path string) string {
	name := filepath.Base(path)
	if isNotesName(name) {
		return path
	}
	return filepath.Join(filepath.Dir(path),
		strings.TrimSuffix(name, ".md")+notesSuffix)
}

// draftOf is the draft a notes file belongs to, for naming a window after what
// it is about.
func draftOf(path string) string {
	name := filepath.Base(path)
	if !isNotesName(name) {
		return path
	}
	return filepath.Join(filepath.Dir(path),
		strings.TrimSuffix(name, notesSuffix)+".md")
}

// contains reports whether a path names a Markdown file of this directory, the
// archive included. An archived draft is still a draft of this directory: it is
// opened, written and kept like any other, whether or not the listing is
// showing the archive at the time.
func (d *dir) contains(path string) bool {
	rel, err := filepath.Rel(d.root, path)
	if err != nil {
		return false
	}
	if filepath.Base(rel) == readmeName || !strings.HasSuffix(rel, ".md") || strings.HasPrefix(rel, ".") {
		return false
	}
	switch filepath.Dir(rel) {
	case ".", archiveSubdir:
		return true
	}
	return false
}

// formatTime shows a timestamp at the resolution that distinguishes it: the
// time of day today, the weekday within the week, and the date beyond it.
func formatTime(at, now time.Time) string {
	layout := time.Kitchen
	switch duration := now.Sub(at); {
	case duration > 7*24*time.Hour:
		layout = "2Jan06"
	case duration > 24*time.Hour:
		layout = "Mon3:04PM"
	}
	return at.Format(layout)
}

// displayPath prints a draft under the working directory relative to it, and
// anything else absolute.
func displayPath(path, wd string) string {
	if wd == "" {
		return path
	}
	rel, err := filepath.Rel(wd, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}
