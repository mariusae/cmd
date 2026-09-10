package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const noteFileName = "working.md"

func notePath(home string, target worktree) (string, error) {
	handle := target.handle()
	if target.Main {
		handle = "main"
	}
	if handle == "" || handle == "." || handle == ".." || filepath.Base(handle) != handle || strings.ContainsAny(handle, `/\`) {
		return "", fmt.Errorf("invalid worktree handle %q", handle)
	}
	return filepath.Join(home, "work", handle, noteFileName), nil
}

func ensureNoteFile(home string, target worktree, now time.Time) (string, error) {
	path, err := notePath(home, target)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("checking note file %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating note directory %s: %w", filepath.Dir(path), err)
	}

	role := "linked"
	if target.Main {
		role = "main"
	}
	handle := filepath.Base(filepath.Dir(path))
	contents := fmt.Sprintf("---\nworktree: %s\nsession: %s\ndirectory: %s\nrole: %s\ndescription: \"\"\ncreated_at: %s\n---\n\n# %s\n\n",
		strconv.Quote(handle), strconv.Quote("work-"+handle), strconv.Quote(filepath.Clean(target.Path)),
		strconv.Quote(role), strconv.Quote(now.Format(time.RFC3339)), handle)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return path, nil
	}
	if err != nil {
		return "", fmt.Errorf("creating note file %s: %w", path, err)
	}
	if _, err := file.WriteString(contents); err != nil {
		file.Close()
		return "", fmt.Errorf("writing note file %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing note file %s: %w", path, err)
	}
	return path, nil
}

func plumbNote(path string) error {
	tool, err := attachApexTool(fmt.Sprintf("work-note-%d", os.Getpid()), os.Getenv)
	if err != nil {
		return err
	}
	defer tool.Close()
	if _, err := tool.Open(path, 0); err != nil {
		return fmt.Errorf("opening note in Apex: %w", err)
	}
	return nil
}
