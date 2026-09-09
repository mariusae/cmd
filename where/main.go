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

type queryComponent struct {
	text  string
	exact bool
}

const helpText = `where finds files and directories when you know part of their path.

Usage:
  where QUERY
  where -help

where searches the immediate children of every directory in WHEREPATH for names
containing QUERY. Searches are case-sensitive. Directory results end in a slash.

A query may contain slash-separated components. Each component narrows the set of
directories searched for the next component, without recursively scanning every
directory below a root. Components match substrings unless prefixed with " or =.
Repeating either marker makes that component and every component after it exact.

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

  where "lib/rc
  where =lib/rc
      Find names containing "rc" directly inside the exact directory "lib".

  where lib/"rc
  where lib/=rc
      Find the exact name "rc" inside directories containing "lib".

  where ""lib/rc
  where ==lib/rc
  where "lib/"rc
  where =lib/=rc
      Match both "lib" and "rc" exactly.

  where project/src/main
      Traverse three partially specified path components.

In shells that consume double quotes, use the = form or escape the quotes so
they reach where. rc passes double quotes through literally.
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
	components, err := parseQuery(query)
	if err != nil {
		return nil, err
	}

	roots, err := expandWherePath(wherePath)
	if err != nil {
		return nil, err
	}

	parents := roots
	var readErrors []error

	var matches []match
	for componentIndex, component := range components {
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
				if component.exact && name != component.text {
					continue
				}
				if !component.exact && !strings.Contains(name, component.text) {
					continue
				}

				path := filepath.Join(parent, name)
				isDir := entry.IsDir()
				if !isDir && entry.Type()&os.ModeSymlink != 0 {
					if info, err := os.Stat(path); err == nil {
						isDir = info.IsDir()
					}
				}
				if componentIndex < len(components)-1 && !isDir {
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

func parseQuery(query string) ([]queryComponent, error) {
	rawComponents := strings.Split(query, "/")
	components := make([]queryComponent, 0, len(rawComponents))
	exactRest := false
	for _, raw := range rawComponents {
		component := queryComponent{text: raw, exact: exactRest}
		switch {
		case strings.HasPrefix(component.text, `""`):
			component.exact = true
			exactRest = true
			component.text = component.text[2:]
		case strings.HasPrefix(component.text, "=="):
			component.exact = true
			exactRest = true
			component.text = component.text[2:]
		case strings.HasPrefix(component.text, `"`):
			component.exact = true
			component.text = component.text[1:]
		case strings.HasPrefix(component.text, "="):
			component.exact = true
			component.text = component.text[1:]
		}

		if strings.Contains(component.text, `"`) {
			return nil, fmt.Errorf("query %q contains a quote outside a component prefix", query)
		}

		if component.text == "" {
			return nil, fmt.Errorf("query %q contains an empty path component", query)
		}
		components = append(components, component)
	}
	return components, nil
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
