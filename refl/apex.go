package main

// Finding the Apex session this command was started in.

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	apexapi "github.com/mariusae/apex/go/apex"
)

func apexSession(getenv func(string) string) string {
	if name := getenv("apexsession"); name != "" {
		return name
	}
	if name := getenv("APEX_SESSION"); name != "" {
		return name
	}
	return "default"
}

func apexSocket(getenv func(string) string) string {
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

func attachApex(name string, getenv func(string) string) (*apexapi.Tool, error) {
	return apexapi.Attach(name, &apexapi.Options{
		Socket:  apexSocket(getenv),
		Session: apexSession(getenv),
	})
}
