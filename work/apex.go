package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	apexapi "github.com/mariusae/apex/go/apex"
)

func apexSessionName(getenv func(string) string) string {
	if name := apexEnvironmentSession(getenv); name != "" {
		return name
	}
	return "default"
}

func apexEnvironmentSession(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if name := getenv("apexsession"); name != "" {
		return name
	}
	if name := getenv("APEX_SESSION"); name != "" {
		return name
	}
	return ""
}

func apexSocketPath(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if path := getenv("APEX_SOCKET"); path != "" {
		return filepath.Clean(path)
	}
	base := getenv("TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	who := getenv("USER")
	if who == "" {
		who = getenv("LOGNAME")
	}
	if who == "" {
		if current, err := user.Current(); err == nil {
			who = current.Username
		}
	}
	if who == "" {
		who = strconv.Itoa(os.Getuid())
	}
	return filepath.Join(base, "apex-"+who, "main.sock")
}

func attachApexTool(name string, getenv func(string) string) (*apexapi.Tool, error) {
	return attachApexToolAt(name, apexSocketPath(getenv), apexSessionName(getenv))
}

func attachApexToolAt(name, socket, session string) (*apexapi.Tool, error) {
	return apexapi.Attach(name, &apexapi.Options{Socket: socket, Session: session})
}

func discoverApexAgentWindow(socket, session, worktreePath string) (string, bool) {
	tool, err := attachApexToolAt(fmt.Sprintf("work-agent-%d", os.Getpid()), socket, session)
	if err != nil {
		return "", false
	}
	defer tool.Close()
	windows, err := tool.Windows()
	if err != nil {
		return "", false
	}
	window, ok := apexAgentWindowInWorktree(windows, worktreePath)
	if !ok {
		return "", false
	}
	return strconv.Itoa(window), true
}

func apexAgentWindowInWorktree(windows []apexapi.WindowInfo, worktreePath string) (int, bool) {
	window := 0
	for _, candidate := range windows {
		if !candidate.Live || filepath.Base(candidate.Name) == "-work" ||
			!strings.HasPrefix(filepath.Base(candidate.Name), "-") ||
			!windowInWorktree(candidate.Name, worktreePath) {
			continue
		}
		if candidate.ID > window {
			window = candidate.ID
		}
	}
	return window, window > 0
}

func windowInWorktree(windowName, worktreePath string) bool {
	directory := filepath.Clean(filepath.Dir(windowName))
	path := filepath.Clean(worktreePath)
	return directory == path || strings.HasPrefix(directory, path+string(filepath.Separator))
}

func apexHookLocation(getenv func(string) string) (socket, session, window string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	// Apex sets apexsession to the immutable session id. APEX_SESSION is a
	// user-facing default and may only be a reusable label, so it is not an
	// originating session identity.
	session = getenv("apexsession")
	if session == "" {
		return "", "", ""
	}
	socket = apexSocketPath(getenv)
	window = getenv("winid")
	if id, err := strconv.Atoi(window); err != nil || id < 0 {
		window = ""
	}
	return socket, session, window
}
