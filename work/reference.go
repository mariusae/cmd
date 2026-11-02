package main

import (
	"fmt"
	"regexp"
	"strings"
)

// reference is a change named on the command line for checkout: the revset
// Sapling resolves, fetching it if necessary, and the name its worktree is
// labelled with.
type reference struct {
	revset string
	name   string
}

var (
	diffPattern        = regexp.MustCompile(`^[Dd]([0-9]+)$`)
	diffURLPattern     = regexp.MustCompile(`^https?://[^/]+/(?:diff/)?[Dd]([0-9]+)(?:[/?#].*)?$`)
	pullRequestPattern = regexp.MustCompile(`^(?i:pr|#)([0-9]+)$`)
	pullURLPattern     = regexp.MustCompile(`^https?://[^/]+/[^/]+/[^/]+/pull/([0-9]+)(?:[/?#].*)?$`)
	hashPattern        = regexp.MustCompile(`^[0-9a-f]{12,40}$`)
	unsafeNamePattern  = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// parseReference recognizes Phabricator diffs and GitHub pull requests, by
// number or URL, and otherwise passes the argument through as a revset.
func parseReference(argument string) (reference, error) {
	argument = strings.TrimSpace(argument)
	if argument == "" {
		return reference{}, fmt.Errorf("reference must not be empty")
	}
	for _, pattern := range []*regexp.Regexp{diffPattern, diffURLPattern} {
		if match := pattern.FindStringSubmatch(argument); match != nil {
			return reference{revset: "D" + match[1], name: "D" + match[1]}, nil
		}
	}
	for _, pattern := range []*regexp.Regexp{pullRequestPattern, pullURLPattern} {
		if match := pattern.FindStringSubmatch(argument); match != nil {
			return reference{revset: "PR" + match[1], name: "pr" + match[1]}, nil
		}
	}
	if hashPattern.MatchString(argument) {
		return reference{revset: argument, name: argument[:12]}, nil
	}
	name := strings.Trim(unsafeNamePattern.ReplaceAllString(argument, "-"), "-.")
	if name == "" {
		return reference{}, fmt.Errorf("cannot name a worktree after %q", argument)
	}
	return reference{revset: argument, name: name}, nil
}
