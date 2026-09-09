package main

import (
	"errors"
	"io"
	"path/filepath"
)

var errNotWorktreeRepository = errors.New("not a worktree-capable repository")

type worktree struct {
	Path    string
	Label   string
	Main    bool
	Current bool
}

func (w worktree) handle() string {
	if w.Label != "" {
		return w.Label
	}
	return filepath.Base(w.Path)
}

type repository struct {
	MainRoot    string
	CurrentRoot string
	Worktrees   []worktree
}

type backend interface {
	name() string
	open(path string) (repository, error)
	add(repo repository, path, label string, diagnostics io.Writer) error
	remove(repo repository, path string, diagnostics io.Writer) error
}
