package main

// The notes graph: a directory of Markdown files, as Reflect keeps it.
// Daily notes live in daily/YYYY-MM-DD.md and everything else in notes/, with
// assets/, audio-memos/ and templates/ reserved.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	dailyDir      = "daily"
	notesDir      = "notes"
	defaultGraph  = "reflect"
	dailyDateForm = "2006-01-02"
)

// reservedTrees hold attachments, recordings and templates: Markdown under
// them is content or scaffolding, never a note.
var reservedTrees = map[string]bool{"assets": true, "audio-memos": true, "templates": true}

// graphMarkers are the directories that identify a graph root.
var graphMarkers = []string{".reflect", dailyDir, notesDir}

var dailyNameRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}\.md$`)

// A note is one Markdown file in the graph.
type note struct {
	path    string // absolute
	rel     string // graph-relative, slash-separated
	title   string
	modTime time.Time
}

// findGraph resolves the graph root: an explicit directory, then
// $REFLECT_GRAPH, then the nearest ancestor of the working directory that
// looks like a graph, then ~/reflect.
func findGraph(explicit string, getenv func(string) string, getwd func() (string, error), home func() (string, error)) (string, error) {
	if explicit != "" {
		return checkGraph(explicit)
	}
	if fromEnv := getenv("REFLECT_GRAPH"); fromEnv != "" {
		return checkGraph(fromEnv)
	}
	if wd, err := getwd(); err == nil {
		for dir := filepath.Clean(wd); ; {
			if isGraph(dir) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	dir, err := home()
	if err != nil {
		return "", err
	}
	return checkGraph(filepath.Join(dir, defaultGraph))
}

func checkGraph(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if !isGraph(dir) {
		return "", fmt.Errorf("%s is not a notes graph (no %s)", dir, strings.Join(graphMarkers, ", "))
	}
	return dir, nil
}

func isGraph(dir string) bool {
	for _, marker := range graphMarkers {
		if info, err := os.Stat(filepath.Join(dir, marker)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// A graph reads notes out of a root directory, remembering what it has already
// parsed: a listing runs over every note in the graph, and re-reading each one
// on every refresh would make the window's cost the size of the graph.
type graph struct {
	root  string
	cache map[string]cachedNote
	// The history's dating of every note, and the commit it was read at.
	stamps map[string]time.Time
	head   string
	// The last timeline read, what it was read from, and how far it went.
	changes     []change
	changeKey   string
	changeLimit int
}

type cachedNote struct {
	modTime time.Time
	title   string
	aliases []string
	private bool
}

func openGraph(root string) *graph {
	return &graph{root: root, cache: map[string]cachedNote{}}
}

// A noteFile is a Markdown file found on disk, before it is read. modTime is
// the file's own time; at is when the note last changed, which git decides.
type noteFile struct {
	path    string
	rel     string
	modTime time.Time
	at      time.Time
}

// walk lists the graph's note files in path order, skipping hidden entries,
// the reserved trees, and anything that is not a plain Markdown file.
func (g *graph) walk() ([]noteFile, error) {
	var files []noteFile
	err := filepath.WalkDir(g.root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(g.root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if !strings.Contains(rel, "/") && reservedTrees[strings.ToLower(name)] {
				return fs.SkipDir
			}
			return nil
		}
		// Only regular files: a symlink points outside the graph's lexical
		// boundary as far as anything here can prove.
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".md") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		files = append(files, noteFile{path: path, rel: rel, modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, nil
}

// byRecency orders note files most recently changed first.
func byRecency(files []noteFile) {
	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].at.Equal(files[j].at) {
			return files[i].at.After(files[j].at)
		}
		return files[i].rel < files[j].rel
	})
}

// load reads a note and records what the listing needs of it.
func (g *graph) load(file noteFile) (string, cachedNote, error) {
	content, err := os.ReadFile(file.path)
	if err != nil {
		return "", cachedNote{}, err
	}
	split := splitFrontmatter(string(content))
	fields := parseFrontmatter(split.raw)
	entry := cachedNote{
		modTime: file.modTime,
		title:   deriveTitle(file.rel, fields.title, split.body),
		aliases: fields.aliases,
		private: fields.private,
	}
	g.cache[file.path] = entry
	return string(content), entry, nil
}

// recent walks the graph and orders it, most recently changed first.
func (g *graph) recent() ([]noteFile, error) {
	files, err := g.walk()
	if err != nil {
		return nil, err
	}
	files = g.stamp(files)
	byRecency(files)
	return files, nil
}

func noteOf(file noteFile, entry cachedNote) note {
	return note{path: file.path, rel: file.rel, title: entry.title, modTime: file.at}
}

// meta reuses the last read of a note whose modification time has not moved.
// A listing runs over every note in the graph, and re-reading each one on
// every refresh would make the window's cost the size of the graph.
func (g *graph) meta(file noteFile) (cachedNote, error) {
	if cached, ok := g.cache[file.path]; ok && cached.modTime.Equal(file.modTime) {
		return cached, nil
	}
	_, entry, err := g.load(file)
	return entry, err
}

// list returns the graph's public notes, most recently modified first. Notes
// marked `private: true` are left out: Reflect keeps them off every scriptable
// surface, and there is deliberately no flag that overrides it.
func (g *graph) list() ([]note, error) {
	files, err := g.recent()
	if err != nil {
		return nil, err
	}
	notes := make([]note, 0, len(files))
	for _, file := range files {
		entry, err := g.meta(file)
		if err != nil || entry.private {
			continue
		}
		notes = append(notes, noteOf(file, entry))
	}
	return notes, nil
}

var atxTitle = regexp.MustCompile(`^ {0,3}#(?:[ \t]+(.*?))?[ \t]*$`)

// deriveTitle follows Reflect: a frontmatter `title:` owns the title, else the
// first non-empty H1, else the daily date, else the file's own name.
func deriveTitle(rel, frontmatterTitle, body string) string {
	if frontmatterTitle != "" {
		return frontmatterTitle
	}
	if heading := firstHeading(body); heading != "" {
		return heading
	}
	if date := dailyDate(rel); date != "" {
		return date
	}
	return strings.TrimSuffix(filepath.Base(rel), ".md")
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

// dailyDate is the ISO date a daily note's path names, or "".
func dailyDate(rel string) string {
	dir, name := splitRel(rel)
	if dir != dailyDir || !dailyNameRe.MatchString(name) {
		return ""
	}
	date := strings.TrimSuffix(name, ".md")
	if _, err := time.Parse(dailyDateForm, date); err != nil {
		return ""
	}
	return date
}

// splitRel splits a graph-relative path into its directory and file name.
func splitRel(rel string) (dir, name string) {
	if at := strings.LastIndexByte(rel, '/'); at >= 0 {
		return rel[:at], rel[at+1:]
	}
	return "", rel
}

// displayTitle is a title as a reader sees it: a v1 `Subject // Alias` title
// reduced to its first segment, then links flattened to their text.
func displayTitle(title string) string {
	display := strings.TrimSpace(title)
	if segments := subjectSegments(title); len(segments) > 0 {
		display = segments[0]
	}
	return flattenLinks(display)
}

// subjectSegments splits a v1 subject-alias title on `//`. The separator is
// exactly two slashes, neither preceded nor followed by one and not preceded
// by a colon, so a URL in a title never splits it.
func subjectSegments(title string) []string {
	var segments []string
	start := 0
	for at := 0; at+1 < len(title); at++ {
		if title[at] != '/' || title[at+1] != '/' {
			continue
		}
		if at > 0 && (title[at-1] == ':' || title[at-1] == '/') {
			continue
		}
		if at+2 < len(title) && title[at+2] == '/' {
			continue
		}
		segments = append(segments, title[start:at])
		start, at = at+2, at+1
	}
	if segments == nil {
		return nil
	}
	var trimmed []string
	for _, segment := range append(segments, title[start:]) {
		if text := strings.TrimSpace(segment); text != "" {
			trimmed = append(trimmed, text)
		}
	}
	return trimmed
}

var (
	embeddedWikiLink = regexp.MustCompile(`\[\[([^\[\]\n]*)\]\]`)
	markdownLink     = regexp.MustCompile(`\[([^\[\]\n]*)\]\([^)\n]*\)`)
)

func flattenLinks(text string) string {
	text = embeddedWikiLink.ReplaceAllStringFunc(text, func(match string) string {
		inner := match[2 : len(match)-2]
		if pipe := strings.IndexByte(inner, '|'); pipe >= 0 {
			if alias := strings.TrimSpace(inner[pipe+1:]); alias != "" {
				return alias
			}
			inner = inner[:pipe]
		}
		return strings.TrimSpace(inner)
	})
	return markdownLink.ReplaceAllString(text, "$1")
}

// relative turns an absolute path into the graph-relative one, when the path
// names a note of this graph.
func (g *graph) relative(path string) (string, bool) {
	rel, err := filepath.Rel(g.root, path)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	return rel, isNotePath(rel)
}

// dailyPath is the absolute path of a day's note, which need not exist:
// dailies are created lazily, so this is how one is opened for the first time.
func (g *graph) dailyPath(day time.Time) string {
	return filepath.Join(g.root, dailyDir, day.Format(dailyDateForm)+".md")
}

// dailyDay reads the day a note path names, for stepping between dailies.
func (g *graph) dailyDay(path string) (time.Time, error) {
	rel, err := filepath.Rel(g.root, path)
	if err != nil {
		return time.Time{}, err
	}
	date := dailyDate(filepath.ToSlash(rel))
	if date == "" {
		return time.Time{}, errors.New("not a daily note")
	}
	return time.ParseInLocation(dailyDateForm, date, time.Local)
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

// displayPath prints a note under the working directory relative to it, and
// anything else absolute — the same rule Reflect's CLI uses.
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
