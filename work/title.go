package main

import (
	"os"
	"path/filepath"
	"strings"
)

type apexWindow struct {
	ID   string
	Name string
	Live bool
}

func (a apexClient) windows() ([]apexWindow, error) {
	output, err := a.output("", "win", "list")
	if err != nil {
		return nil, err
	}
	return parseApexWindows(output), nil
}

func parseApexWindows(output string) []apexWindow {
	var windows []apexWindow
	for _, line := range strings.Split(output, "\n") {
		id, markedName, ok := strings.Cut(line, "\t")
		if !ok || id == "" || markedName == "" {
			continue
		}
		_, size := firstRune(markedName)
		if size == 0 {
			continue
		}
		windows = append(windows, apexWindow{ID: id, Name: markedName[size:], Live: markedName[:size] == ">"})
	}
	return windows
}

func firstRune(value string) (rune, int) {
	for _, character := range value {
		return character, len(string(character))
	}
	return 0, 0
}

func liveAgentTitles(repo repository, getenv func(string) string) map[string]string {
	titles := make(map[string]string)
	if getenv == nil || (getenv("APEX_SOCKET") == "" && getenv("apexsession") == "" && getenv("APEX_SESSION") == "") {
		return titles
	}
	apex, err := newApexClient()
	if err != nil {
		return titles
	}
	windows, err := apex.windows()
	if err != nil {
		return titles
	}
	return agentTitlesFromWindows(repo, windows)
}

func agentTitlesFromWindows(repo repository, windows []apexWindow) map[string]string {
	titles := make(map[string]string)
	project := filepath.Base(repo.MainRoot)
	for _, window := range windows {
		if !window.Live {
			continue
		}
		base := filepath.Base(window.Name)
		if !strings.HasPrefix(base, "-") {
			continue
		}
		directory := filepath.Clean(filepath.Dir(window.Name))
		var matched *worktree
		for index := range repo.Worktrees {
			worktree := &repo.Worktrees[index]
			path := filepath.Clean(worktree.Path)
			if directory != path && !strings.HasPrefix(directory, path+string(filepath.Separator)) {
				continue
			}
			if matched == nil || len(path) > len(filepath.Clean(matched.Path)) {
				matched = worktree
			}
		}
		if matched == nil {
			continue
		}
		path := filepath.Clean(matched.Path)
		title := sanitizeAgentTitle(strings.TrimPrefix(base, "-"), matched.handle(), project)
		if title != "" {
			titles[path] = title
		}
	}
	return titles
}

func windowInWorktree(windowName, worktreePath string) bool {
	directory := filepath.Clean(filepath.Dir(windowName))
	path := filepath.Clean(worktreePath)
	return directory == path || strings.HasPrefix(directory, path+string(filepath.Separator))
}

func sanitizeAgentTitle(raw, worktree, project string) string {
	title := strings.TrimSpace(raw)
	for title != "" {
		character, size := firstRune(title)
		if !(character >= '\u2800' && character <= '\u28ff') && !strings.ContainsRune("✳⠀●○◌✓✗", character) {
			break
		}
		title = strings.TrimSpace(title[size:])
	}
	for {
		prefix, rest, ok := strings.Cut(title, "|")
		if !ok || strings.TrimSpace(prefix) != "OC" {
			break
		}
		title = strings.TrimSpace(rest)
	}
	if title == "" || strings.HasPrefix(title, "Claude Code") {
		return ""
	}
	if matchesGenericTitle(title) || title == worktree || title == project {
		return ""
	}
	return dashboardField(title)
}

func matchesGenericTitle(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "zsh", "bash", "sh", "fish", "dash", "ksh", "csh", "tcsh", "nu", "xonsh", "elvish", "pwsh", "powershell", "cmd", "tmux", "default":
		return true
	}
	hostname, err := os.Hostname()
	if err == nil && strings.EqualFold(strings.SplitN(hostname, ".", 2)[0], title) {
		return true
	}
	return false
}
