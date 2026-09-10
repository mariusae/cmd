package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	apexapi "github.com/mariusae/apex/go/apex"
)

func apexSessionName(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if name := getenv("apexsession"); name != "" {
		return name
	}
	if name := getenv("APEX_SESSION"); name != "" {
		return name
	}
	return "default"
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
	return apexapi.Attach(name, &apexapi.Options{
		Socket:  apexSocketPath(getenv),
		Session: apexSessionName(getenv),
	})
}

func apexHookLocation(getenv func(string) string) (socket, session, window string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	window = getenv("winid")
	if id, err := strconv.Atoi(window); err != nil || id < 0 {
		return "", "", ""
	}
	return apexSocketPath(getenv), apexSessionName(getenv), window
}
