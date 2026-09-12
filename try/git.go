package main

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// defaultGitHost is the host assumed for OWNER/REPOSITORY shorthand.
const defaultGitHost = "github.com"

// webPathVerbs are the components GitHub inserts after OWNER/REPOSITORY in the
// URLs it shows in a browser. They are dropped so that a URL copied from a
// browser clones the repository it points into.
var webPathVerbs = map[string]bool{
	"actions":     true,
	"blob":        true,
	"branches":    true,
	"commit":      true,
	"commits":     true,
	"compare":     true,
	"discussions": true,
	"issues":      true,
	"pull":        true,
	"pulls":       true,
	"releases":    true,
	"tags":        true,
	"tree":        true,
	"wiki":        true,
}

// gitSource is a repository that try clones into a new directory.
type gitSource struct {
	remote string // passed to git clone
	name   string // directory name, derived from owner and repository
}

// isGitSource reports whether the argument to -n names a repository to clone
// rather than a plain try name.
func isGitSource(value string) bool {
	return looksLikeGitURL(value) || looksLikeShorthand(value)
}

// gitSourceFor derives the clone URL and directory name for an argument
// accepted by isGitSource.
func gitSourceFor(value string) (gitSource, error) {
	if looksLikeShorthand(value) {
		host, repositoryPath := splitShorthand(value)
		name, err := nameFromRepositoryPath(repositoryPath)
		if err != nil {
			return gitSource{}, fmt.Errorf("cannot derive a name from repository %q", value)
		}
		return gitSource{remote: sshRemote(host, repositoryPath), name: name}, nil
	}

	remote := value
	repositoryPath := ""
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		repositoryPath = cleanRepositoryPath(parsed.Host, parsed.Path)
		switch {
		case isGitHubHost(parsed.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https"):
			// GitHub's web URLs are not clonable over SSH as given, and
			// cloning them over HTTPS reaches only public repositories.
			remote = sshRemote(defaultGitHost, repositoryPath)
		case truncatedTo(parsed.Path, repositoryPath):
			shortened := *parsed
			shortened.Path = "/" + repositoryPath
			shortened.RawQuery = ""
			shortened.Fragment = ""
			remote = shortened.String()
		}
	} else if colon := strings.IndexByte(value, ':'); colon >= 0 {
		repositoryPath = cleanRepositoryPath(value[:colon], value[colon+1:])
		if truncatedTo(value[colon+1:], repositoryPath) {
			remote = value[:colon+1] + repositoryPath
		}
	}

	name, err := nameFromRepositoryPath(repositoryPath)
	if err != nil {
		return gitSource{}, fmt.Errorf("cannot derive a name from Git URL %q", value)
	}
	return gitSource{remote: remote, name: name}, nil
}

func sshRemote(host, repositoryPath string) string {
	return "git@" + host + ":" + repositoryPath + ".git"
}

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

// looksLikeShorthand reports whether value is a bare repository reference such
// as mariusae/over or github.com/mariusae/over.
func looksLikeShorthand(value string) bool {
	parts := strings.Split(strings.TrimSuffix(value, "/"), "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if !isShorthandComponent(part) {
			return false
		}
	}
	return true
}

func isShorthandComponent(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// splitShorthand separates the host, which is only present when the shorthand
// has more than two components, from the repository path.
func splitShorthand(value string) (host, repositoryPath string) {
	parts := strings.Split(strings.Trim(value, "/"), "/")
	host = defaultGitHost
	if len(parts) > 2 && strings.Contains(parts[0], ".") {
		host, parts = parts[0], parts[1:]
	}
	return host, cleanRepositoryPath(host, strings.Join(parts, "/"))
}

// cleanRepositoryPath strips the decoration around the OWNER/REPOSITORY part of
// a URL path: leading and trailing slashes, a .git suffix, and the trailing
// components of a URL copied from a browser.
func cleanRepositoryPath(host, repositoryPath string) string {
	parts := strings.Split(strings.Trim(repositoryPath, "/"), "/")
	for i := 2; i < len(parts); i++ {
		// GitLab separates a repository path from the rest of a web URL with
		// a "-" component; GitHub follows OWNER/REPOSITORY with a verb.
		if parts[i] == "-" || (i == 2 && isGitHubHost(host) && webPathVerbs[parts[i]]) {
			parts = parts[:i]
			break
		}
	}
	return strings.TrimSuffix(strings.Join(parts, "/"), ".git")
}

// truncatedTo reports whether cleaning dropped path components, rather than
// only the slashes and .git suffix that git itself ignores.
func truncatedTo(original, cleaned string) bool {
	original = strings.TrimSuffix(strings.Trim(original, "/"), ".git")
	return cleaned != original
}

func isGitHubHost(host string) bool {
	if at := strings.IndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	host, _, _ = strings.Cut(host, ":")
	return host == defaultGitHost
}

// nameFromRepositoryPath joins the owner and repository of a repository path
// into a single directory name.
func nameFromRepositoryPath(repositoryPath string) (string, error) {
	if repositoryPath == "" {
		return "", fmt.Errorf("empty repository path")
	}

	parts := strings.Split(repositoryPath, "/")
	repository := path.Base(repositoryPath)
	if len(parts) == 1 {
		if repository == "." || repository == "/" {
			return "", fmt.Errorf("empty repository name")
		}
		return repository, nil
	}
	owner := parts[len(parts)-2]
	if owner == "" || repository == "" || owner == "." || repository == "." {
		return "", fmt.Errorf("empty owner or repository name")
	}
	return owner + "-" + repository, nil
}
