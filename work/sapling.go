package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

type saplingBackend struct {
	executable string
}

func findSaplingBackend() (backend, error) {
	for _, name := range []string{"sl", "hg"} {
		path, err := exec.LookPath(name)
		if err == nil {
			return &saplingBackend{executable: path}, nil
		}
	}
	return nil, fmt.Errorf("neither sl nor hg is available on PATH")
}

func (b *saplingBackend) name() string {
	return "sapling"
}

type saplingWorktree struct {
	Path    string `json:"path"`
	Role    string `json:"role"`
	Label   string `json:"label"`
	Current bool   `json:"current"`
}

func parseSaplingWorktrees(output []byte) ([]worktree, error) {
	var entries []saplingWorktree
	if err := json.Unmarshal(output, &entries); err != nil {
		return nil, fmt.Errorf("parsing Sapling worktree list: %w", err)
	}

	result := make([]worktree, 0, len(entries))
	for index, entry := range entries {
		if entry.Path == "" {
			return nil, fmt.Errorf("parsing Sapling worktree list: entry %d has no path", index+1)
		}
		result = append(result, worktree{
			Path:    filepath.Clean(entry.Path),
			Label:   entry.Label,
			Main:    entry.Role == "main",
			Current: entry.Current,
		})
	}
	return result, nil
}

func (b *saplingBackend) open(path string) (repository, error) {
	rootOutput, rootError := b.output(path, "root")
	if rootError != nil {
		return repository{}, fmt.Errorf("%w: %v", errNotWorktreeRepository, rootError)
	}
	currentRoot := filepath.Clean(strings.TrimSpace(string(rootOutput)))
	if currentRoot == "." || currentRoot == "" {
		return repository{}, fmt.Errorf("Sapling returned an empty repository root")
	}

	listOutput, listError := b.output(path, "worktree", "list", "-Tjson")
	if listError != nil {
		if strings.Contains(listError.Error(), "require an EdenFS-backed repository") ||
			strings.Contains(listError.Error(), "not supported") {
			return repository{}, fmt.Errorf("%w: %v", errNotWorktreeRepository, listError)
		}
		return repository{}, fmt.Errorf("listing Sapling worktrees: %w", listError)
	}

	worktrees, err := parseSaplingWorktrees(listOutput)
	if err != nil {
		return repository{}, err
	}
	if len(worktrees) == 0 {
		worktrees = []worktree{{Path: currentRoot, Main: true, Current: true}}
	}

	mainRoot := ""
	hasCurrent := false
	for i := range worktrees {
		if worktrees[i].Main && mainRoot == "" {
			mainRoot = worktrees[i].Path
		}
		if worktrees[i].Current {
			hasCurrent = true
		}
	}
	if mainRoot == "" {
		// Sapling documents the main worktree first. This fallback keeps the
		// tool usable with older JSON templates that omitted the role field.
		mainRoot = worktrees[0].Path
		worktrees[0].Main = true
	}
	if !hasCurrent {
		for i := range worktrees {
			if pathsEqual(worktrees[i].Path, currentRoot) {
				worktrees[i].Current = true
				break
			}
		}
	}

	return repository{
		MainRoot:    filepath.Clean(mainRoot),
		CurrentRoot: currentRoot,
		Worktrees:   worktrees,
	}, nil
}

func (b *saplingBackend) latestChangeTitle(path string) (string, error) {
	output, err := b.output(path, "log", "-r", ".", "-T", "{desc|firstline}\\n")
	if err != nil {
		return "", fmt.Errorf("reading current Sapling change: %w", err)
	}
	return strings.Join(strings.Fields(string(output)), " "), nil
}

func (b *saplingBackend) add(repo repository, path, label string, diagnostics io.Writer) error {
	cmd := exec.Command(b.executable, "worktree", "add", path, "--label", label, "--rev", ".")
	cmd.Dir = repo.CurrentRoot
	cmd.Stdout = diagnostics
	cmd.Stderr = diagnostics
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sl worktree add failed: %w", err)
	}
	return nil
}

func (b *saplingBackend) remove(repo repository, path string, diagnostics io.Writer) error {
	cmd := exec.Command(b.executable, "worktree", "remove", path, "-y")
	cmd.Dir = repo.MainRoot
	cmd.Stdout = diagnostics
	cmd.Stderr = diagnostics
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sl worktree remove failed: %w", err)
	}
	return nil
}

func (b *saplingBackend) output(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command(b.executable, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err == nil {
		return output, nil
	}

	detail := strings.TrimSpace(stderr.String())
	if detail == "" {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			detail = exitError.Error()
		} else {
			detail = err.Error()
		}
	}
	return nil, fmt.Errorf("%s %s: %s", filepath.Base(b.executable), strings.Join(args, " "), detail)
}
