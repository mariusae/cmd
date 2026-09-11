package main

// Making a note.
//
// A regular note lives at notes/<slug>.md, where the slug is derived from the
// title and never edited directly — Reflect's filenames are a projection of
// the title, and its rules (docs/readable-filenames.md) are what keep a graph's
// names agreeing across case-insensitive and case-sensitive filesystems.

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// maxSlugRunes caps a slug. Filesystems cap a basename at 255 bytes, and a
// letter can cost four of them, so 60 runes always fits with room for
// `notes/`, `.md`, and a collision suffix.
const maxSlugRunes = 60

// windowsReserved device names cannot be filenames on Windows, and a graph
// may sync there.
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

// slugForTitle derives a note's filename from its title: lowercase, letters
// and numbers kept, separator runs collapsed to one dash. Other scripts pass
// through untransliterated, so a CJK title keeps its characters.
//
// Reflect normalizes to NFC first; this does not, which would need a
// dependency for the one case it changes — a decomposed accent typed on a Mac,
// where the filesystem composes the name anyway.
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
		return dashed + "-note"
	}
	return dashed
}

// crockford is ULID's alphabet: base32 without the letters that read as
// digits (i, l, o) or as each other (u).
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// newNoteID mints the durable identity Reflect gives a note it creates: a
// lowercase ULID, 48 bits of millisecond timestamp and 80 of randomness, so
// the note survives every later rename of its file.
func newNoteID(now time.Time) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes[6:]); err != nil {
		return "", err
	}
	milliseconds := now.UnixMilli()
	for index := 5; index >= 0; index-- {
		bytes[index] = byte(milliseconds)
		milliseconds >>= 8
	}
	id := make([]byte, 26)
	// 26 base-32 digits hold 130 bits; the first carries only the top two of
	// the timestamp, which is why a ULID never starts above '7'.
	for index := 25; index >= 0; index-- {
		id[index] = crockford[bitsAt(bytes, (25-index)*5)]
		_ = index
	}
	return string(id), nil
}

// bitsAt reads the 5 bits ending offset bits from the right of a 128-bit
// big-endian value.
func bitsAt(bytes []byte, offset int) int {
	value := 0
	for bit := 0; bit < 5; bit++ {
		at := offset + bit
		if at >= 128 {
			continue
		}
		byteIndex := len(bytes) - 1 - at/8
		if bytes[byteIndex]&(1<<(at%8)) != 0 {
			value |= 1 << bit
		}
	}
	return value
}

// createNote writes a new note for this title and returns its path. The title
// is the note's first heading, and the minted id its identity; a name already
// taken takes a numeric suffix rather than overwriting anything.
func (g *graph) createNote(title string, now time.Time) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("a note needs a title")
	}
	id, err := newNoteID(now)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(g.root, notesDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	slug := slugForTitle(title)
	for attempt := 1; ; attempt++ {
		name := slug
		if attempt > 1 {
			name = fmt.Sprintf("%s-%d", slug, attempt)
		}
		path := filepath.Join(dir, name+".md")
		// O_EXCL: two hands creating the same title at once each get a note,
		// and neither overwrites a note already there.
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = fmt.Fprintf(file, "---\nid: %s\n---\n\n# %s\n\n", id, headingSafe(title))
		if closeErr := file.Close(); err != nil || closeErr != nil {
			return "", firstError(err, closeErr)
		}
		return path, nil
	}
}

// headingSafe keeps a title on one line: a heading is a line, and a newline
// in the title would end it and bury the rest in the body.
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
