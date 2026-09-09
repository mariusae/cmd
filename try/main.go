package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const recentTryLimit = 20

const helpText = `try finds and creates short-lived project directories.

Usage:
  try
  try QUERY
  try -n NAME
  try -n GIT_URL
  try -help

Tries live in $TRY_PATH, or $HOME/src/tries when TRY_PATH is not set. Each try
is a directory whose name starts with its creation date. Output contains one
full path per line, with a trailing slash so editors can recognize directories.

Finding tries:
  QUERY is matched as a case-sensitive substring of each directory name.
  Exact name components rank before partial matches, then newer tries rank first.
  With no QUERY, try prints the 20 most recent directories.

    try md
    try newproject

Creating tries:
  -n creates a directory named YYYY-MM-DD-NAME and prints its path.
  Whitespace in NAME becomes hyphens. If that name already exists, try adds a
  numeric suffix such as -2 so every invocation creates a new directory.

    try -n newproject

  When the argument to -n is a Git URL, try derives NAME from the repository's
  owner and name, then clones the repository into the new directory.

    try -n git@github.com:mariusae/cmd.git
    try -n https://github.com/mariusae/cmd.git

Configuration:
  Override the tries directory by setting TRY_PATH in your shell startup file:

    export TRY_PATH="$HOME/src/tries"
`

type command struct {
	getenv      func(string) string
	userHomeDir func() (string, error)
	now         func() time.Time
	clone       func(string, string, io.Writer) error
	stdout      io.Writer
	stderr      io.Writer
}

func main() {
	c := command{
		getenv:      os.Getenv,
		userHomeDir: os.UserHomeDir,
		now:         time.Now,
		clone:       cloneGitRepository,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}
	os.Exit(c.run(os.Args[1:]))
}

func (c command) run(args []string) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprint(c.stdout, helpText)
		return 0
	}

	if len(args) == 2 && args[0] == "-n" {
		base, err := triesPath(c.getenv, c.userHomeDir)
		if err != nil {
			return c.fail(err)
		}

		path, err := createTry(base, args[1], c.now(), c.clone, c.stderr)
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintln(c.stdout, directoryPath(path))
		return 0
	}

	if len(args) == 0 || (len(args) == 1 && args[0] != "" && !strings.HasPrefix(args[0], "-")) {
		base, err := triesPath(c.getenv, c.userHomeDir)
		if err != nil {
			return c.fail(err)
		}

		query := ""
		if len(args) == 1 {
			query = args[0]
		}
		matches, err := findTries(base, query)
		if err != nil {
			return c.fail(err)
		}
		if len(matches) == 0 {
			if query == "" {
				return c.fail(fmt.Errorf("no tries found in %s", base))
			}
			return c.fail(fmt.Errorf("no tries match %q", query))
		}
		if query == "" && len(matches) > recentTryLimit {
			matches = matches[:recentTryLimit]
		}
		for _, match := range matches {
			fmt.Fprintln(c.stdout, directoryPath(match))
		}
		return 0
	}

	fmt.Fprintln(c.stderr, "usage: try [QUERY] | try -n NAME_OR_GIT_URL (try -help for help)")
	return 2
}

func (c command) fail(err error) int {
	fmt.Fprintf(c.stderr, "try: %v\n", err)
	return 1
}

func isHelp(arg string) bool {
	return arg == "-help" || arg == "--help" || arg == "-h"
}

func triesPath(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	base := getenv("TRY_PATH")
	if base == "" {
		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding home directory: %w", err)
		}
		base = filepath.Join(home, "src", "tries")
	}

	base, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolving tries directory: %w", err)
	}
	return filepath.Clean(base), nil
}

func findTries(base, query string) ([]string, error) {
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading tries directory: %w", err)
	}

	var matches []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !strings.Contains(entry.Name(), query) {
			continue
		}

		path := filepath.Join(base, entry.Name())
		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			if info, statErr := os.Stat(path); statErr == nil {
				isDir = info.IsDir()
			}
		}
		if isDir {
			matches = append(matches, path)
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		nameI := filepath.Base(matches[i])
		nameJ := filepath.Base(matches[j])
		exactI := exactQueryMatch(nameI, query)
		exactJ := exactQueryMatch(nameJ, query)
		if exactI != exactJ {
			return exactI
		}

		dateI := datePrefix(nameI)
		dateJ := datePrefix(nameJ)
		if dateI != dateJ {
			return dateI > dateJ
		}
		return nameI < nameJ
	})
	return matches, nil
}

func exactQueryMatch(name, query string) bool {
	if query == "" {
		return false
	}
	if name == query {
		return true
	}
	if prefix := datePrefix(name); prefix != "" && len(name) > len(prefix)+1 {
		name = name[len(prefix)+1:]
	}
	return name == query || strings.HasSuffix(name, "-"+query)
}

func datePrefix(name string) string {
	if len(name) < len("2006-01-02-") || name[4] != '-' || name[7] != '-' || name[10] != '-' {
		return ""
	}
	date := name[:10]
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return ""
	}
	return date
}

func createTry(
	base, value string,
	now time.Time,
	clone func(string, string, io.Writer) error,
	cloneOutput io.Writer,
) (string, error) {
	name := value
	isGitURL := looksLikeGitURL(value)
	if isGitURL {
		var err error
		name, err = nameFromGitURL(value)
		if err != nil {
			return "", err
		}
	} else {
		name = normalizeName(name)
	}
	if err := validateName(name); err != nil {
		return "", err
	}

	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("creating tries directory: %w", err)
	}

	stem := now.Format("2006-01-02") + "-" + name
	path, err := createUniqueDirectory(base, stem)
	if err != nil {
		return "", err
	}

	if isGitURL {
		if err := clone(value, path, cloneOutput); err != nil {
			if removeErr := os.RemoveAll(path); removeErr != nil {
				return "", fmt.Errorf("%w (also could not remove incomplete clone: %v)", err, removeErr)
			}
			return "", err
		}
	}
	return path, nil
}

func createUniqueDirectory(base, stem string) (string, error) {
	for sequence := 1; ; sequence++ {
		name := stem
		if sequence > 1 {
			name = fmt.Sprintf("%s-%d", stem, sequence)
		}
		path := filepath.Join(base, name)
		if err := os.Mkdir(path, 0o755); err == nil {
			return path, nil
		} else if !os.IsExist(err) {
			return "", fmt.Errorf("creating try: %w", err)
		}
	}
}

func normalizeName(name string) string {
	return strings.Join(strings.Fields(name), "-")
}

func validateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("name must not be empty")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return fmt.Errorf("name %q must not contain path separators", name)
	}
	return nil
}

func cloneGitRepository(remote, destination string, output io.Writer) error {
	cmd := exec.Command("git", "clone", "--", remote, destination)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}
	return nil
}

func directoryPath(path string) string {
	return filepath.Clean(path) + string(filepath.Separator)
}
