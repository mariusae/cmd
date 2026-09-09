package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type match struct {
	path  string
	isDir bool
}

const helpText = `where finds files and directories when you know part of their path.

Usage:
  where QUERY
  where -help

where searches the immediate children of every directory in WHEREPATH for names
containing QUERY. Searches are case-sensitive. Directory results end in a slash.

A query may contain slash-separated components. Each component narrows the set of
directories searched for the next component, without recursively scanning every
directory below a root.

Configuration:
  Set WHEREPATH to a colon-separated list of directories or glob patterns:

    export WHEREPATH="$HOME/src:$HOME/work/*"

  Put the export in your shell startup file (for example, ~/.bashrc or ~/.zshrc)
  to make it persistent. Glob patterns are expanded by where, so quote WHEREPATH
  when assigning it.

Examples:
  where mon
      Find immediate entries containing "mon" in each WHEREPATH root.

  where mon/BUCK
      Find entries containing "BUCK" inside matching "mon" directories.

  where project/src/main
      Traverse three partially specified path components.
`

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "-help" || args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, helpText)
		return 0
	}

	if len(args) != 1 || args[0] == "" {
		fmt.Fprintln(stderr, "usage: where QUERY (try 'where -help' for help)")
		return 2
	}

	wherePath := getenv("WHEREPATH")
	if wherePath == "" {
		fmt.Fprintln(stderr, "where: WHEREPATH is not set")
		return 2
	}

	matches, err := findMatches(args[0], wherePath)
	for _, match := range matches {
		path := match.path
		if match.isDir {
			path += string(filepath.Separator)
		}
		fmt.Fprintln(stdout, path)
	}

	if err != nil {
		fmt.Fprintf(stderr, "where: %v\n", err)
		return 1
	}
	return 0
}

func findMatches(query, wherePath string) ([]match, error) {
	roots, err := expandWherePath(wherePath)
	if err != nil {
		return nil, err
	}
	terms := strings.Split(query, "/")
	for _, term := range terms {
		if term == "" {
			return nil, fmt.Errorf("query %q contains an empty path component", query)
		}
	}

	parents := roots
	var readErrors []error

	var matches []match
	for termIndex, term := range terms {
		matches = nil
		seen := make(map[string]struct{})
		for _, parent := range parents {
			entries, err := os.ReadDir(parent)
			if err != nil {
				readErrors = append(readErrors, err)
				continue
			}

			for _, entry := range entries {
				name := entry.Name()
				if !strings.Contains(name, term) {
					continue
				}

				path := filepath.Join(parent, name)
				isDir := entry.IsDir()
				if !isDir && entry.Type()&os.ModeSymlink != 0 {
					if info, err := os.Stat(path); err == nil {
						isDir = info.IsDir()
					}
				}
				if termIndex < len(terms)-1 && !isDir {
					continue
				}

				key, err := filepath.Abs(path)
				if err != nil {
					key = filepath.Clean(path)
				}
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				matches = append(matches, match{
					path:  filepath.Clean(path),
					isDir: isDir,
				})
			}
		}

		parents = make([]string, len(matches))
		for i, match := range matches {
			parents[i] = match.path
		}
	}

	return matches, errors.Join(readErrors...)
}

func expandWherePath(wherePath string) ([]string, error) {
	seen := make(map[string]struct{})
	var roots []string

	for _, pattern := range filepath.SplitList(wherePath) {
		if pattern == "" {
			continue
		}

		expanded, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid WHEREPATH pattern %q: %w", pattern, err)
		}
		for _, root := range expanded {
			root = filepath.Clean(root)
			key, err := filepath.Abs(root)
			if err != nil {
				key = root
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			roots = append(roots, root)
		}
	}

	return roots, nil
}
