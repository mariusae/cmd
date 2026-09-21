package main

// The drafts directory: a flat directory of Markdown files, one per draft,
// each with an optional companion holding the notes for it.
//
// There is nothing else to it — no index, no database, no frontmatter a draft
// is required to carry. A draft is a file, its title is the first thing in it
// that reads like one, and its age is the file's own. That is the whole format,
// which is what makes the directory readable by everything else on the machine.

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

// A draft is one Markdown file in the directory.
type draft struct {
	path    string // absolute
	name    string // the file's own name
	title   string
	modTime time.Time
}

// A dir reads drafts out of a directory, remembering what it has already
// parsed: a listing runs over every draft, and re-reading each one on every
// refresh would make the window's cost the size of the directory.
type dir struct {
	root  string
	cache map[string]cachedDraft
	// The repository the directory sits in, looked for once: asking costs a
	// subprocess, and the answer does not change while the program runs.
	repo   vcs
	probed bool
	// The last timeline read, what it was read from, and how far it went.
	changes     []change
	changeKey   string
	changeLimit int
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

// make creates the drafts directory, for whatever is about to be written into
// it.
func (d *dir) make() error { return os.MkdirAll(d.root, 0o755) }

// draftFile is a Markdown file found on disk, before it is read.
type draftFile struct {
	path    string
	name    string
	modTime time.Time
}

// walk lists the directory's Markdown files, newest first. The listing is flat:
// a drafts directory is a directory of drafts, and a tree of them is a filing
// system, which is a different tool.
func (d *dir) walk() ([]draftFile, error) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []draftFile
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
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
			path:    filepath.Join(d.root, name),
			name:    name,
			modTime: info.ModTime(),
		})
	}
	byRecency(files)
	return files, nil
}

// byRecency orders files most recently modified first. A drafts directory is
// written by hand, so the file's own time is when the draft changed, and
// nothing has to be asked about it.
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
	present := make(map[string]bool, len(files))
	for _, file := range files {
		present[file.name] = true
	}
	drafts := make([]draft, 0, len(files))
	for _, file := range files {
		if isNotesName(file.name) && present[filepath.Base(draftOf(file.path))] {
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

// title names a draft, reusing the last read of a file whose modification time
// has not moved.
func (d *dir) title(file draftFile) (string, error) {
	if cached, ok := d.cache[file.path]; ok && cached.modTime.Equal(file.modTime) {
		return cached.title, nil
	}
	content, err := os.ReadFile(file.path)
	if err != nil {
		return "", err
	}
	title := deriveTitle(file.name, string(content))
	d.cache[file.path] = cachedDraft{modTime: file.modTime, title: title}
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
	d.cache[file.path] = cachedDraft{modTime: file.modTime, title: title}
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
	return strings.TrimSuffix(name, ".md")
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

// contains reports whether a path names a Markdown file of this directory.
func (d *dir) contains(path string) bool {
	rel, err := filepath.Rel(d.root, path)
	if err != nil {
		return false
	}
	return rel == filepath.Base(rel) && !strings.HasPrefix(rel, ".") &&
		strings.HasSuffix(rel, ".md")
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
