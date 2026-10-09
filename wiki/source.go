package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A tree is what wiki knows of a source directory: its files, each with a
// hash of its contents, and where its files can be linked to.
type tree struct {
	files    map[string]string // slash-separated path → content hash
	revision string            // the checked-out commit, when under version control
	linkBase string            // the URL of the files on GitHub, if they are there
}

// scanTree lists and hashes the files of dir. Under git or Sapling the files
// are the ones the repository keeps, so that build output and the like stay
// out; elsewhere they are every file not hidden.
func scanTree(dir string) (*tree, error) {
	paths, vcs := listFiles(dir)
	t := &tree{files: make(map[string]string, len(paths))}
	for _, p := range paths {
		h, err := hashFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			continue // deleted, unreadable, or a directory (a submodule)
		}
		t.files[p] = h
	}
	switch vcs {
	case "git":
		t.revision = command(dir, "git", "rev-parse", "HEAD")
		prefix := command(dir, "git", "rev-parse", "--show-prefix")
		if base := githubBase(command(dir, "git", "remote", "get-url", "origin")); base != "" && t.revision != "" {
			t.linkBase = base + "/blob/" + t.revision + "/" + prefix
		}
	case "sl":
		t.revision = command(dir, "sl", "log", "-r", ".", "-T", "{node}")
	}
	return t, nil
}

func listFiles(dir string) ([]string, string) {
	if out, err := output(dir, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"); err == nil {
		return splitNonEmpty(out, "\x00"), "git"
	}
	if _, err := output(dir, "sl", "root"); err == nil {
		if out, err := output(dir, "sl", "files", "-0", "."); err == nil {
			return splitNonEmpty(out, "\x00"), "sl"
		}
	}
	return walkFiles(dir), ""
}

var skipDirs = map[string]bool{"node_modules": true, "__pycache__": true}

func walkFiles(dir string) []string {
	var paths []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if path != dir && (strings.HasPrefix(name, ".") || skipDirs[name]) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, path)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	return paths
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// changedFiles returns the paths added, removed, or changed between two
// hashings of a tree, in order.
func changedFiles(before, after map[string]string) []string {
	var changed []string
	for p, h := range after {
		if before[p] != h {
			changed = append(changed, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return changed
}

// sortedPaths returns a tree's paths in order.
func (t *tree) sortedPaths() []string {
	paths := make([]string, 0, len(t.files))
	for p := range t.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// has reports whether p names a file of the tree or a directory holding one.
func (t *tree) has(p string) bool {
	return hasPath(t.files, p)
}

func hasPath(files map[string]string, p string) bool {
	if _, ok := files[p]; ok {
		return true
	}
	prefix := strings.TrimSuffix(p, "/") + "/"
	for f := range files {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

// covers reports whether the file f is, or is under, one of paths.
func covers(paths []string, f string) bool {
	for _, p := range paths {
		p = strings.TrimSuffix(p, "/")
		if f == p || strings.HasPrefix(f, p+"/") {
			return true
		}
	}
	return false
}

var githubRemote = regexp.MustCompile(`^(?:https://|ssh://git@|git@)github\.com[:/]([^/]+)/([^/]+?)(?:\.git)?/?$`)

// githubBase turns a GitHub remote into the URL of its repository on the web,
// or returns "" for any other remote.
func githubBase(remote string) string {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(remote))
	if m == nil {
		return ""
	}
	return "https://github.com/" + m[1] + "/" + m[2]
}

func output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.String(), err
}

// command runs a command for its one line of output, or "" if it fails.
func command(dir, name string, args ...string) string {
	out, err := output(dir, name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func splitNonEmpty(s, sep string) []string {
	var parts []string
	for _, p := range strings.Split(s, sep) {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}
