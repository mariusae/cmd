package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// statusConcurrency bounds the git invocations that run at once while
// summarizing a list of tries.
const statusConcurrency = 8

// gitStatus summarizes the version-control state of a single try: how its
// branch stands relative to its upstream, and whether its working tree has
// been touched.
func gitStatus(path string) (string, error) {
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return "not a repository", nil
	}

	cmd := exec.Command("git", "status", "--porcelain=v2", "--branch")
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git status in %s: %w", path, err)
	}
	return summarizeGitStatus(string(output)), nil
}

// summarizeGitStatus renders the output of git status --porcelain=v2 --branch
// as a short phrase: the branch's position relative to its upstream, then the
// state of the working tree.
func summarizeGitStatus(output string) string {
	var (
		head                            string
		upstream                        bool
		ahead, behind                   int
		modified, untracked, conflicted bool
	)
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			head = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			upstream = true
			for _, field := range strings.Fields(strings.TrimPrefix(line, "# branch.ab ")) {
				if len(field) < 2 {
					continue
				}
				count, err := strconv.Atoi(field[1:])
				if err != nil {
					continue
				}
				switch field[0] {
				case '+':
					ahead = count
				case '-':
					behind = count
				}
			}
		case strings.HasPrefix(line, "1 "), strings.HasPrefix(line, "2 "):
			modified = true
		case strings.HasPrefix(line, "u "):
			conflicted = true
		case strings.HasPrefix(line, "? "):
			untracked = true
		}
	}

	position := "up to date"
	switch {
	case head == "(detached)":
		position = "detached"
	case !upstream:
		position = "no upstream"
	case ahead > 0 && behind > 0:
		position = fmt.Sprintf("%d ahead, %d behind", ahead, behind)
	case ahead > 0:
		position = fmt.Sprintf("%d ahead", ahead)
	case behind > 0:
		position = fmt.Sprintf("%d behind", behind)
	}

	tree := make([]string, 0, 3)
	if conflicted {
		tree = append(tree, "conflicted")
	}
	if modified {
		tree = append(tree, "modified")
	}
	if untracked {
		tree = append(tree, "untracked")
	}
	if len(tree) == 0 {
		tree = append(tree, "clean")
	}
	return position + ", " + strings.Join(tree, ", ")
}

// summarize returns the status of each path, in the same order, along with the
// paths it could not summarize. Statuses are gathered concurrently because each
// one waits on a separate git process.
func summarize(paths []string, status func(string) (string, error)) ([]string, []error) {
	statuses := make([]string, len(paths))
	errors := make([]error, len(paths))

	var wait sync.WaitGroup
	limit := make(chan struct{}, statusConcurrency)
	for i, path := range paths {
		wait.Add(1)
		go func() {
			defer wait.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			statuses[i], errors[i] = status(path)
		}()
	}
	wait.Wait()

	failures := make([]error, 0, len(paths))
	for _, err := range errors {
		if err != nil {
			failures = append(failures, err)
		}
	}
	return statuses, failures
}
