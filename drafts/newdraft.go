package main

// Making a draft.
//
// A draft's filename is a projection of its title: lowercase, letters and
// numbers kept, separator runs collapsed to one dash. Nothing renames a draft
// afterwards — the file is the draft, and a title that moves is not worth a
// path that moves under everything pointing at it.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// maxSlugRunes caps a slug. Filesystems cap a basename at 255 bytes, and a
// letter can cost four of them, so 60 runes always fits with room for `.md`,
// the notes suffix, and a collision suffix.
const maxSlugRunes = 60

// windowsReserved device names cannot be filenames on Windows, and a drafts
// directory may sync there.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

var (
	slugStrip     = regexp.MustCompile(`[^\p{L}\p{N}\s_-]+`)
	slugSeparator = regexp.MustCompile(`[\s_-]+`)
	slugEdges     = regexp.MustCompile(`^-+|-+$`)
)

// slugForTitle derives a draft's filename from its title. Other scripts pass
// through untransliterated, so a CJK title keeps its characters.
func slugForTitle(title string) string {
	folded := strings.ToLower(title)
	dashed := slugEdges.ReplaceAllString(
		slugSeparator.ReplaceAllString(slugStrip.ReplaceAllString(folded, ""), "-"), "")
	runes := []rune(dashed)
	if len(runes) > maxSlugRunes {
		dashed = slugEdges.ReplaceAllString(string(runes[:maxSlugRunes]), "")
	}
	switch {
	case dashed == "":
		return "untitled"
	case windowsReserved[dashed]:
		return dashed + "-draft"
	}
	return dashed
}

// opening is what a new draft's window starts with: the title as its heading,
// and the offset below it where writing begins. With nothing to call it yet,
// the window opens empty and the first thing typed is the title.
func opening(title string) (string, int) {
	if title = strings.TrimSpace(title); title == "" {
		return "", 0
	}
	text := fmt.Sprintf("# %s\n\n", headingSafe(title))
	return text, len([]rune(text))
}

// saveDraft writes a draft begun before it was named: what is written in it
// names the file, by the same reading the listing names any draft by. The name
// is taken as it is chosen, so two drafts saved at once cannot land on one
// file, and a draft with nothing in it yet is `untitled.md`.
func (d *dir) saveDraft(text string) (string, error) {
	// deriveTitle falls back on the file's own name, and there is no file yet
	// — which is the whole point — so it is given none.
	return d.take(slugForTitle(deriveTitle("", text)), text)
}

// createNotes writes the notes file for a draft, if it is not there yet, and
// returns its path. Notes open saying what they are about, so a file opened
// for the first time is not a blank one.
func (d *dir) createNotes(path, title string) (string, error) {
	notes := notesFor(path)
	if _, err := os.Stat(notes); err == nil {
		return notes, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := d.make(); err != nil {
		return "", err
	}
	body := fmt.Sprintf("# Notes on %s\n\n", headingSafe(title))
	file, err := os.OpenFile(notes, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return notes, nil // someone else got there first, which is the same answer
	}
	if err != nil {
		return "", err
	}
	_, err = file.WriteString(body)
	if closeErr := file.Close(); err != nil || closeErr != nil {
		return "", firstError(err, closeErr)
	}
	return notes, nil
}

// renameDraft files a draft under the title it now carries, and takes its
// notes with it. It returns where the draft ended up, which is where it
// already was when the name still fits.
//
// The title is read from the file, not from whatever window may be showing it:
// the name follows what was saved, so a draft renamed is a draft that has been
// written down. Put first, which is wanted anyway.
//
// Nothing is ever overwritten and the two files never come apart: the new name
// is held before anything moves, a notes file whose new name is taken sends the
// draft looking for the next one, and a notes file that will not move puts the
// draft back where it was.
func (d *dir) renameDraft(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	slug := slugForTitle(deriveTitle("", string(content)))
	notes := notesFor(path)
	// notesFor a notes file is itself, which is not a companion to carry.
	carried := notes != path && exists(notes)
	if filepath.Base(path) == slug+".md" {
		return path, nil
	}
	for attempt := 1; ; attempt++ {
		name := slug
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", slug, attempt)
		}
		target := filepath.Join(d.root, name+".md")
		// The same rule a new draft is filed under: a name where another
		// draft's notes live is already spoken for. The draft being renamed
		// does not count, since it is leaving.
		if isNotesName(name+".md") && exists(draftOf(target)) && draftOf(target) != path {
			continue
		}
		// O_EXCL holds the name the moment it is chosen, so nothing else can
		// take it between looking and moving.
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(target)
			return "", err
		}
		if carried && exists(notesFor(target)) {
			_ = os.Remove(target) // give the name back and look further on
			continue
		}
		if err := os.Rename(path, target); err != nil {
			_ = os.Remove(target)
			return "", err
		}
		if carried {
			if err := os.Rename(notes, notesFor(target)); err != nil {
				// Put the draft back: a draft and its notes under two
				// different names is worse than neither having moved.
				_ = os.Rename(target, path)
				return "", err
			}
		}
		return target, nil
	}
}

// take makes <slug>.md and writes body to it, returning the path. A name
// already taken takes a numeric suffix rather than overwriting anything, and
// the name is held the moment it is chosen, so two drafts begun at once are
// two drafts.
func (d *dir) take(slug, body string) (string, error) {
	if err := d.make(); err != nil {
		return "", err
	}
	for attempt := 1; ; attempt++ {
		name := slug
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", slug, attempt)
		}
		path := filepath.Join(d.root, name+".md")
		// A draft called "Meeting Notes" files as `meeting-notes.md`, which is
		// where the notes for a draft called "Meeting" live. Where that draft
		// exists the name is already spoken for, however free the file is, and
		// the next one is taken instead.
		if isNotesName(name+".md") && exists(draftOf(path)) {
			continue
		}
		// O_EXCL: two hands creating the same title at once each get a draft,
		// and neither overwrites a draft already there.
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = file.WriteString(body)
		if closeErr := file.Close(); err != nil || closeErr != nil {
			return "", firstError(err, closeErr)
		}
		return path, nil
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// headingSafe keeps a title on one line: a heading is a line, and a newline in
// the title would end it and bury the rest in the body.
func headingSafe(title string) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || unicode.IsControl(character) {
			return ' '
		}
		return character
	}, title)
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
