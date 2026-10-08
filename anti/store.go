package main

// The notes: plain files in a directory, numbered in the order they came,
// the newest the front. A note thrown away goes to the void, a directory
// beside them, and can come back; nothing here deletes a note that has
// anything in it.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type store struct{ dir string }

func (s store) void() string { return filepath.Join(s.dir, "void") }

// seqOf reads a note's number from its name: 000012.txt is 12.
func seqOf(path string) (int, bool) {
	name := filepath.Base(path)
	if !strings.HasSuffix(name, ".txt") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(name, ".txt"))
	return n, err == nil && n > 0
}

func nameOf(n int) string { return fmt.Sprintf("%06d.txt", n) }

// list is the notes in a directory, oldest first.
func list(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if _, ok := seqOf(e.Name()); ok && e.Type().IsRegular() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := seqOf(out[i])
		b, _ := seqOf(out[j])
		return a < b
	})
	return out, nil
}

// notes are the notes, oldest first.
func (s store) notes() ([]string, error) { return list(s.dir) }

// front is the number after every note's, in the notes and the void, so a
// note thrown away and one still here never share a number.
func (s store) front() (int, error) {
	n := 0
	for _, dir := range []string{s.dir, s.void()} {
		ps, err := list(dir)
		if err != nil {
			return 0, err
		}
		for _, p := range ps {
			if k, _ := seqOf(p); k > n {
				n = k
			}
		}
	}
	return n + 1, nil
}

// fresh makes a new, empty note at the front.
func (s store) fresh() (string, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	n, err := s.front()
	if err != nil {
		return "", err
	}
	p := filepath.Join(s.dir, nameOf(n))
	return p, os.WriteFile(p, nil, 0o600)
}

// promote moves a note to the front, as if it were the newest.
func (s store) promote(path string) (string, error) {
	n, err := s.front()
	if err != nil {
		return "", err
	}
	p := filepath.Join(s.dir, nameOf(n))
	return p, os.Rename(path, p)
}

// throw puts a note in the void, dated now so that the last thrown is the
// first back. An empty note is not worth keeping there.
func (s store) throw(path string, now time.Time) error {
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == "" {
		return os.Remove(path)
	}
	if err := os.MkdirAll(s.void(), 0o700); err != nil {
		return err
	}
	p := filepath.Join(s.void(), filepath.Base(path))
	if err := os.Rename(path, p); err != nil {
		return err
	}
	return os.Chtimes(p, now, now)
}

// restore brings back the note last thrown, at the front.
func (s store) restore() (string, error) {
	ps, err := list(s.void())
	if err != nil {
		return "", err
	}
	if len(ps) == 0 {
		return "", errors.New("the void is empty")
	}
	last, lastAt := "", time.Time{}
	for _, p := range ps {
		if fi, err := os.Stat(p); err == nil && !fi.ModTime().Before(lastAt) {
			last, lastAt = p, fi.ModTime()
		}
	}
	return s.promote(last)
}

// expire throws away the notes untouched for longer than age, but for
// keep: what is being shown is not old. It says how many went.
func (s store) expire(age time.Duration, keep string, now time.Time) (int, error) {
	ps, err := s.notes()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range ps {
		fi, err := os.Stat(p)
		if err != nil || p == keep || now.Sub(fi.ModTime()) < age {
			continue
		}
		if err := s.throw(p, now); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
