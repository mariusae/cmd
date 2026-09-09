package main

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

func looksLikeGitURL(value string) bool {
	parsed, err := url.Parse(value)
	if err == nil {
		switch parsed.Scheme {
		case "git", "http", "https", "ssh", "file":
			return true
		}
	}

	colon := strings.IndexByte(value, ':')
	return colon > 0 && strings.Contains(value[:colon], "@") && colon < len(value)-1
}

func nameFromGitURL(remote string) (string, error) {
	repositoryPath := ""
	parsed, err := url.Parse(remote)
	if err == nil && parsed.Scheme != "" {
		repositoryPath = parsed.Path
	} else if colon := strings.IndexByte(remote, ':'); colon >= 0 {
		repositoryPath = remote[colon+1:]
	}

	repositoryPath = strings.TrimRight(repositoryPath, "/")
	repositoryPath = strings.TrimSuffix(repositoryPath, ".git")
	repositoryPath = strings.Trim(repositoryPath, "/")
	if repositoryPath == "" {
		return "", fmt.Errorf("cannot derive a name from Git URL %q", remote)
	}

	parts := strings.Split(repositoryPath, "/")
	repository := path.Base(repositoryPath)
	if len(parts) == 1 {
		return repository, nil
	}
	owner := parts[len(parts)-2]
	if owner == "" || repository == "" || owner == "." || repository == "." {
		return "", fmt.Errorf("cannot derive a name from Git URL %q", remote)
	}
	return owner + "-" + repository, nil
}
