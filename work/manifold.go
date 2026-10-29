package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maniTimeout = 15 * time.Second

// inManifold reports whether work runs in a Manifold block. Manifold and Apex
// are never both active, so Manifold's block variable selects the environment.
func inManifold(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv("MANIFOLD_BLOCK") != ""
}

type maniClient struct {
	executable string
	getenv     func(string) string
}

func newManiClient(getenv func(string) string) (*maniClient, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if path, err := exec.LookPath("mani"); err == nil {
		return &maniClient{executable: path, getenv: getenv}, nil
	}
	if bin := getenv("MANIFOLD_BIN"); bin != "" {
		path := filepath.Join(bin, "mani")
		if _, err := os.Stat(path); err == nil {
			return &maniClient{executable: path, getenv: getenv}, nil
		}
	}
	return nil, fmt.Errorf("mani is not available on PATH")
}

type maniLayout struct {
	Spaces []maniSpace `json:"spaces"`
}

type maniSpace struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Columns []struct {
		Tiles []maniTile `json:"tiles"`
	} `json:"columns"`
}

type maniTile struct {
	Block string     `json:"block"`
	Kind  string     `json:"kind"`
	Title string     `json:"title"`
	Cwd   string     `json:"cwd"`
	Path  string     `json:"path"`
	Space string     `json:"space"`
	Shown *bool      `json:"shown"`
	Stack []maniTile `json:"stack"`
}

// tiles returns the space's tiles, including the ones stacked under others.
func (s maniSpace) tiles() []maniTile {
	var result []maniTile
	for _, column := range s.Columns {
		for _, tile := range column.Tiles {
			result = append(result, tile)
			result = append(result, tile.Stack...)
		}
	}
	return result
}

type maniEvent struct {
	Block string `json:"block"`
	Event string `json:"event"`
	ID    string `json:"id"`
}

func (m *maniClient) run(ctx context.Context, input string, args ...string) ([]byte, error) {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), maniTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, m.executable, args...)
	cmd.Stdin = strings.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err == nil {
		return output, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("mani %s: %w", args[0], ctx.Err())
	}
	detail := strings.TrimSpace(stderr.String())
	if detail == "" {
		detail = err.Error()
	}
	return nil, fmt.Errorf("mani %s: %s", strings.Join(args, " "), detail)
}

func (m *maniClient) id(args ...string) (string, error) {
	output, err := m.run(nil, "", args...)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(output))
	if id == "" {
		return "", fmt.Errorf("mani %s printed no id", args[0])
	}
	return id, nil
}

func (m *maniClient) layout() (maniLayout, error) {
	output, err := m.run(nil, "", "ls", "--all", "--json")
	if err != nil {
		return maniLayout{}, err
	}
	var layout maniLayout
	if err := json.Unmarshal(output, &layout); err != nil {
		return maniLayout{}, fmt.Errorf("parsing mani ls output: %w", err)
	}
	return layout, nil
}

func (m *maniClient) spaceLayout(space string) (maniSpace, error) {
	output, err := m.run(nil, "", "ls", "--json", "--space", space)
	if err != nil {
		return maniSpace{}, err
	}
	var result maniSpace
	if err := json.Unmarshal(output, &result); err != nil {
		return maniSpace{}, fmt.Errorf("parsing mani ls output: %w", err)
	}
	return result, nil
}

func (m *maniClient) info(name string) (maniTile, error) {
	output, err := m.run(nil, "", "info", name)
	if err != nil {
		return maniTile{}, err
	}
	var tile maniTile
	if err := json.Unmarshal(output, &tile); err != nil {
		return maniTile{}, fmt.Errorf("parsing mani info output: %w", err)
	}
	return tile, nil
}

// onScreen reports whether a block is still on a space. Manifold keeps closed
// blocks for a while, reporting them as no longer shown.
func (m *maniClient) onScreen(name string) (bool, error) {
	tile, err := m.info(name)
	if err != nil {
		return false, err
	}
	return tile.Shown == nil || *tile.Shown, nil
}

func (m *maniClient) show(name string) error {
	_, err := m.run(nil, "", "show", name)
	return err
}

func (m *maniClient) open(path string) error {
	_, err := m.run(nil, "", "open", path)
	return err
}

func (m *maniClient) newPanel(title string, widgets any) (string, error) {
	text, err := json.Marshal(widgets)
	if err != nil {
		return "", err
	}
	return m.id("ui", "new", "--title", title, "--text", string(text))
}

func (m *maniClient) setPanel(panel string, widgets any) error {
	text, err := json.Marshal(widgets)
	if err != nil {
		return err
	}
	_, err = m.run(nil, "", "ui", "set", "--text", string(text), panel)
	return err
}

// waitPanel blocks until something is done in the panel or ctx ends.
func (m *maniClient) waitPanel(ctx context.Context, panel string) (maniEvent, error) {
	output, err := m.run(ctx, "", "ui", "wait", panel)
	if err != nil {
		return maniEvent{}, err
	}
	var event maniEvent
	if err := json.Unmarshal(output, &event); err != nil {
		return maniEvent{}, fmt.Errorf("parsing mani ui wait output: %w", err)
	}
	return event, nil
}

// agentTitlesFromManifold reads live agent titles from Manifold's agent blocks.
func agentTitlesFromManifold(repo repository, layout maniLayout) map[string]string {
	var windows []titledWindow
	for _, space := range layout.Spaces {
		for _, tile := range space.tiles() {
			if tile.Kind == "agent" && tile.Cwd != "" {
				windows = append(windows, titledWindow{path: tile.Cwd, title: tile.Title})
			}
		}
	}
	return agentTitlesFromTitledWindows(repo, windows)
}

// manifoldAgentBlock finds a live agent block working in the worktree.
func manifoldAgentBlock(layout maniLayout, worktreePath string) (string, bool) {
	for _, space := range layout.Spaces {
		for _, tile := range space.tiles() {
			if tile.Kind == "agent" && tile.Cwd != "" && windowInWorktree(tile.Cwd, worktreePath) {
				return tile.Block, true
			}
		}
	}
	return "", false
}

// showManifoldAgent focuses the agent recorded for the worktree, falling back
// to any live agent block in it once the recorded one has been closed.
func showManifoldAgent(client *maniClient, store *stateStore, worktreePath string) error {
	state, found, err := store.get(worktreePath)
	if err != nil {
		return err
	}
	if found && state.ManifoldBlock != "" {
		if shown, err := client.onScreen(state.ManifoldBlock); err == nil && shown {
			return client.show(state.ManifoldBlock)
		}
	}
	layout, err := client.layout()
	if err != nil {
		return err
	}
	block, ok := manifoldAgentBlock(layout, worktreePath)
	if !ok {
		return fmt.Errorf("no Manifold agent is running in %s", worktreePath)
	}
	if found && block != state.ManifoldBlock {
		_ = store.rememberManifoldBlock(worktreePath, block)
	}
	return client.show(block)
}

// switchManifoldSpace shows the worktree's space, creating it when it does not
// exist: a terminal, a folder and a smartlog on the left, and an agent on the
// right, all starting in start.
func switchManifoldSpace(client *maniClient, store *stateStore, target worktree, start string) error {
	layout, err := client.layout()
	if err != nil {
		return err
	}
	recorded, _, err := store.worktreeSpace(target.Path)
	if err != nil {
		return err
	}
	title := manifoldSpaceTitle(target.handle())
	if space, ok := findManifoldSpace(layout, recorded.ManifoldSpace, title, target.handle()); ok {
		if space.Title == target.handle() && title != space.Title {
			// Spaces made before titles dropped the date keep the full handle.
			_, _ = client.run(nil, "", "rename", space.ID, title)
		}
		if space.ID != recorded.ManifoldSpace {
			recorded.ManifoldSpace = space.ID
			if err := store.setWorktreeSpace(target.Path, recorded); err != nil {
				return err
			}
		}
		// Showing a space by its id places it as a tile here, so focus one of
		// its blocks instead, which shows the space itself.
		if block, ok := spaceFocusBlock(space); ok {
			return client.show(block)
		}
	}

	space, err := client.id("new", "space", "--title", title, "--cwd", start)
	if err != nil {
		return err
	}
	recorded.ManifoldSpace = space
	if err := store.setWorktreeSpace(target.Path, recorded); err != nil {
		return err
	}
	created, err := client.spaceLayout(space)
	if err != nil {
		return err
	}
	tiles := created.tiles()
	if len(tiles) == 0 {
		return fmt.Errorf("Manifold space %s has no terminal", space)
	}
	terminal := tiles[0].Block
	agent, err := client.id("new", "agent", "--space", space, "--at", terminal, "--right", "--size", "0.6", "--cwd", start)
	if err != nil {
		return err
	}
	folder, err := client.id("new", "folder", start, "--space", space, "--at", terminal, "--below")
	if err != nil {
		return err
	}
	if _, err := client.id("new", "smartlog", "--space", space, "--at", folder, "--below", "--cwd", start); err != nil {
		return err
	}
	return client.show(agent)
}

// manifoldSpaceTitle drops the date from a dated worktree handle, which the
// space list would otherwise repeat on every row.
func manifoldSpaceTitle(handle string) string {
	if len(handle) > len("2006-01-02-") && handle[len("2006-01-02")] == '-' {
		if _, err := time.Parse("2006-01-02", handle[:len("2006-01-02")]); err == nil {
			return handle[len("2006-01-02-"):]
		}
	}
	return handle
}

func findManifoldSpace(layout maniLayout, id string, titles ...string) (maniSpace, bool) {
	if id != "" {
		for _, space := range layout.Spaces {
			if space.ID == id {
				return space, true
			}
		}
	}
	for _, title := range titles {
		for _, space := range layout.Spaces {
			if space.Title == title {
				return space, true
			}
		}
	}
	return maniSpace{}, false
}

// spaceFocusBlock picks the block to focus when showing a space: its agent,
// else its first tile.
func spaceFocusBlock(space maniSpace) (string, bool) {
	tiles := space.tiles()
	for _, tile := range tiles {
		if tile.Kind == "agent" {
			return tile.Block, true
		}
	}
	if len(tiles) == 0 {
		return "", false
	}
	return tiles[0].Block, true
}

var errNoStartDirectory = errors.New("start directory does not exist")

// startDirectory resolves a worktree-relative start directory requested for
// add. Relative directories are taken from the repository root; absolute ones
// must lie within one of the repository's worktrees.
func startDirectory(repo repository, requested string) (string, error) {
	if requested == "" {
		return "", nil
	}
	relative := filepath.Clean(requested)
	if filepath.IsAbs(relative) {
		base := ""
		for _, candidate := range repo.Worktrees {
			path := filepath.Clean(candidate.Path)
			if windowInWorktree(relative, path) && len(path) > len(base) {
				base = path
			}
		}
		if base == "" {
			return "", fmt.Errorf("%s is not inside a worktree of %s", requested, repo.MainRoot)
		}
		relative, _ = filepath.Rel(base, relative)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("start directory %q is outside the repository", requested)
	}
	if relative == "." {
		return "", nil
	}
	info, err := os.Stat(filepath.Join(repo.CurrentRoot, relative))
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: %s in %s", errNoStartDirectory, relative, repo.CurrentRoot)
	}
	return relative, nil
}

// worktreeStart returns where a worktree's session starts: its recorded
// subdirectory when that still exists, else the worktree itself.
func worktreeStart(target worktree, relative string) string {
	if relative == "" {
		return target.Path
	}
	path := filepath.Join(target.Path, relative)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return target.Path
}
