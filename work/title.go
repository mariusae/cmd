package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apexapi "github.com/mariusae/apex/go/apex"
)

type apexWindow = apexapi.WindowInfo

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
	tool, err := attachApexTool(fmt.Sprintf("work-titles-%d", os.Getpid()), getenv)
	if err != nil {
		return titles
	}
	defer tool.Close()
	return liveAgentTitlesFromTool(repo, tool)
}

func liveAgentTitlesFromTool(repo repository, tool *apexapi.Tool) map[string]string {
	titles := make(map[string]string)
	windows, err := tool.Windows()
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
