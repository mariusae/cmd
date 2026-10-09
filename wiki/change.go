package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// A change is a diff under review: one commit, or a linear stack of them, and
// the patch from the parent of the first to the last.
type change struct {
	VCS     string   `json:"vcs"`  // git or sl
	Spec    string   `json:"spec"` // the revset or commit as given
	Base    string   `json:"base"` // the parent of the first commit; "" for none
	Head    string   `json:"head"` // the last commit
	Commits []commit `json:"commits"`

	atHead bool        // the working copy is checked out at Head
	parts  []filePatch // the patch, file by file
}

type commit struct {
	Hash    string `json:"hash"`
	Parent  string `json:"-"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	Message string `json:"message"`
}

// A filePatch is the part of a patch that changes one file.
type filePatch struct {
	Path           string
	Text           string
	Added, Removed int
}

// gitEmptyTree is the hash of the empty tree, which a root commit is diffed
// against.
const gitEmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// resolveChange finds the change spec names in the repository holding dir —
// the one commit it names when single, else the commits of the revset — and
// returns it with the repository's root.
func resolveChange(dir, spec string, single bool) (*change, string, error) {
	if root, err := vcsOutput(dir, "git", "rev-parse", "--show-toplevel"); err == nil {
		root = strings.TrimSpace(root)
		c, err := resolveGit(root, spec, single)
		return c, root, err
	}
	if root, err := vcsOutput(dir, "sl", "root"); err == nil {
		root = strings.TrimSpace(root)
		c, err := resolveSapling(root, spec, single)
		return c, root, err
	}
	return nil, "", fmt.Errorf("%s is not in a git or Sapling repository", dir)
}

// gitRange turns a revset into arguments for git rev-list. A lone revision R
// stands for the commits since it, R..HEAD, as in git log R..; anything with
// range syntax is taken as it is.
func gitRange(spec string, single bool) []string {
	if single {
		return []string{spec + "^!"}
	}
	fields := strings.Fields(spec)
	if len(fields) == 1 && !strings.Contains(spec, "..") && !strings.HasPrefix(spec, "^") &&
		!strings.HasSuffix(spec, "^!") && !strings.HasSuffix(spec, "^@") && !strings.Contains(spec, "^-") {
		return []string{spec + "..HEAD"}
	}
	return fields
}

func resolveGit(root, spec string, single bool) (*change, error) {
	args := append([]string{"rev-list", "--parents"}, gitRange(spec, single)...)
	out, err := vcsOutput(root, "git", append(args, "--")...)
	if err != nil {
		return nil, err
	}
	var commits []commit
	for _, line := range splitNonEmpty(out, "\n") {
		f := strings.Fields(line)
		c := commit{Hash: f[0]}
		if len(f) > 1 {
			c.Parent = f[1]
		}
		commits = append(commits, c)
	}
	if commits, err = orderStack(spec, commits); err != nil {
		return nil, err
	}
	hashes := make([]string, len(commits))
	for i, c := range commits {
		hashes[i] = c.Hash
	}
	args = append([]string{"log", "--no-walk=unsorted", "--format=%H%x00%an <%ae>%x00%aI%x00%B%x1e"}, hashes...)
	out, err = vcsOutput(root, "git", args...)
	if err != nil {
		return nil, err
	}
	for i, rec := range splitNonEmpty(strings.TrimSpace(out), "\x1e") {
		f := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 4)
		if len(f) == 4 && i < len(commits) && f[0] == commits[i].Hash {
			commits[i].Author, commits[i].Date, commits[i].Message = f[1], f[2], strings.TrimSpace(f[3])
		}
	}
	c := &change{VCS: "git", Spec: spec, Base: commits[0].Parent, Head: commits[len(commits)-1].Hash, Commits: commits}
	base := c.Base
	if base == "" {
		base = gitEmptyTree
	}
	patch, err := vcsOutput(root, "git", "diff", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", base, c.Head, "--")
	if err != nil {
		return nil, err
	}
	c.parts = splitPatch(patch)
	c.atHead = command(root, "git", "rev-parse", "HEAD") == c.Head
	return c, c.check()
}

func resolveSapling(root, spec string, single bool) (*change, error) {
	out, err := vcsOutput(root, "sl", "log", "-r", spec, "-T", "json")
	if err != nil {
		return nil, err
	}
	var logged []struct {
		Node    string   `json:"node"`
		User    string   `json:"user"`
		Date    []int64  `json:"date"`
		Desc    string   `json:"desc"`
		Parents []string `json:"parents"`
	}
	if err := json.Unmarshal([]byte(out), &logged); err != nil {
		return nil, fmt.Errorf("sl log: %v", err)
	}
	if single && len(logged) != 1 {
		return nil, fmt.Errorf("%s names %d commits, not one", spec, len(logged))
	}
	var commits []commit
	for _, l := range logged {
		c := commit{Hash: l.Node, Author: l.User, Message: strings.TrimSpace(l.Desc)}
		if len(l.Parents) > 0 {
			c.Parent = l.Parents[0]
		}
		if len(l.Date) == 2 {
			c.Date = time.Unix(l.Date[0], 0).In(time.FixedZone("", int(-l.Date[1]))).Format(time.RFC3339)
		}
		commits = append(commits, c)
	}
	if commits, err = orderStack(spec, commits); err != nil {
		return nil, err
	}
	c := &change{VCS: "sl", Spec: spec, Base: commits[0].Parent, Head: commits[len(commits)-1].Hash, Commits: commits}
	base := c.Base
	if base == "" {
		base = "null"
	}
	patch, err := vcsOutput(root, "sl", "diff", "--git", "-r", base, "-r", c.Head)
	if err != nil {
		return nil, err
	}
	c.parts = splitPatch(patch)
	c.atHead = command(root, "sl", "log", "-r", ".", "-T", "{node}") == c.Head
	return c, c.check()
}

func (c *change) check() error {
	if len(c.parts) == 0 {
		return fmt.Errorf("%s changes no files", c.Spec)
	}
	return nil
}

// orderStack puts commits in order from first to last, each the parent of the
// next, or fails if they are not such a stack.
func orderStack(spec string, commits []commit) ([]commit, error) {
	if len(commits) == 0 {
		return nil, fmt.Errorf("%s names no commits", spec)
	}
	in := map[string]bool{}
	child := map[string]int{}
	for i, c := range commits {
		in[c.Hash] = true
		if _, ok := child[c.Parent]; ok {
			return nil, fmt.Errorf("%s is not a linear stack of commits: it branches at %s", spec, shortRevision(c.Parent))
		}
		child[c.Parent] = i
	}
	first := -1
	for i, c := range commits {
		if !in[c.Parent] {
			if first >= 0 {
				return nil, fmt.Errorf("%s is not a linear stack of commits: %s and %s both start one", spec, shortRevision(commits[first].Hash), shortRevision(c.Hash))
			}
			first = i
		}
	}
	if first < 0 {
		return nil, fmt.Errorf("%s is not a linear stack of commits", spec)
	}
	ordered := []commit{commits[first]}
	for len(ordered) < len(commits) {
		i, ok := child[ordered[len(ordered)-1].Hash]
		if !ok {
			return nil, fmt.Errorf("%s is not a linear stack of commits", spec)
		}
		ordered = append(ordered, commits[i])
	}
	return ordered, nil
}

// splitPatch cuts a patch in git's format into its files.
func splitPatch(patch string) []filePatch {
	var parts []filePatch
	var chunks []string
	for _, line := range strings.SplitAfter(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") || len(chunks) == 0 {
			chunks = append(chunks, "")
		}
		chunks[len(chunks)-1] += line
	}
	for _, chunk := range chunks {
		if !strings.HasPrefix(chunk, "diff --git ") {
			continue
		}
		p := filePatch{Path: patchPath(chunk), Text: chunk}
		inHunk := false
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "@@"):
				inHunk = true
			case inHunk && strings.HasPrefix(line, "+"):
				p.Added++
			case inHunk && strings.HasPrefix(line, "-"):
				p.Removed++
			}
		}
		if p.Path != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// patchPath returns the path of the file a part of a patch changes: its new
// path, or its old one when it is deleted.
func patchPath(chunk string) string {
	lines := strings.Split(chunk, "\n")
	var old string
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "@@") {
			break
		}
		if p, ok := strings.CutPrefix(line, "+++ "); ok && p != "/dev/null" {
			return stripPrefix(p)
		}
		if p, ok := strings.CutPrefix(line, "--- "); ok && p != "/dev/null" {
			old = stripPrefix(p)
		}
		for _, prefix := range []string{"rename to ", "copy to "} {
			if p, ok := strings.CutPrefix(line, prefix); ok {
				return p
			}
		}
	}
	if old != "" {
		return old
	}
	if i := strings.LastIndex(lines[0], " b/"); i >= 0 {
		return lines[0][i+3:]
	}
	return ""
}

func stripPrefix(p string) string {
	p, _, _ = strings.Cut(p, "\t")
	if len(p) > 2 && (p[:2] == "a/" || p[:2] == "b/") {
		return p[2:]
	}
	return p
}

// fileHashes hashes each file's part of the patch: what changes between two
// versions of a change is the files whose hash does.
func (c *change) fileHashes() map[string]string {
	files := make(map[string]string, len(c.parts))
	for _, p := range c.parts {
		h := sha256.Sum256([]byte(p.Text))
		files[p.Path] = hex.EncodeToString(h[:])[:16]
	}
	return files
}

// tree is what wiki knows of the source for a change: the files it changes,
// and the rest of the repository at root, on disk.
func (c *change) tree(root string) *tree {
	t := &tree{files: c.fileHashes(), revision: c.Head, root: root}
	if c.VCS == "git" {
		if base := githubBase(command(root, "git", "remote", "get-url", "origin")); base != "" {
			t.linkBase = base + "/blob/" + c.Head + "/"
		}
	}
	return t
}

// sameMessages reports whether two versions of a change say the same of
// themselves.
func sameMessages(a, b *change) bool {
	if a == nil || b == nil || len(a.Commits) != len(b.Commits) {
		return false
	}
	for i := range a.Commits {
		if a.Commits[i].Message != b.Commits[i].Message {
			return false
		}
	}
	return true
}

// commands are the read-only commands of the change's VCS the agent may run,
// to see the code as of a revision that is not checked out.
func (c *change) commands() []string {
	if c.VCS == "sl" {
		return []string{"sl cat", "sl diff", "sl log", "sl show", "sl blame"}
	}
	return []string{"git show", "git diff", "git log", "git blame"}
}

var specJunk = regexp.MustCompile(`[^A-Za-z0-9._~^@+-]+`)

// specName makes a revset or commit fit to name a directory.
func specName(spec string) string {
	if name := strings.Trim(specJunk.ReplaceAllString(spec, "-"), "-"); name != "" {
		return name
	}
	return "diff"
}

// vcsOutput runs a command of a VCS, and on failure returns what it said.
func vcsOutput(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if name == "sl" {
		cmd.Env = append(os.Environ(), "HGPLAIN=1")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return "", errors.New(name + ": " + msg)
		}
		return "", fmt.Errorf("%s: %v", name, err)
	}
	return stdout.String(), nil
}
