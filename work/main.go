package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const helpText = `work manages linked Sapling worktrees.

Usage:
  work
  work ls
  work new NAME
  work rm [PATH|LABEL]
  work note [PATH|LABEL]
  work status
  work dash
  work install-hooks
  work -help

work uses the worktree group for the current Sapling repository. Outside a
worktree-capable repository, it uses the default repository from
~/.config/work/config.yaml.

Commands:
  work            Show details for the current worktree, or list the default.
  ls              Print linked worktree paths. The main worktree is omitted.
  new NAME        Create YYYY-MM-DD-NAME beside the main worktree.
  rm              Remove the current linked worktree.
  rm PATH|LABEL   Remove the selected linked worktree.
  note            Create and open the current worktree's notes file in Apex.
  note PATH|LABEL Create and open another worktree's notes file in Apex.
  status          Show the latest agent status for each linked worktree.
  dash            Open a live worktree and agent dashboard in Apex.
  install-hooks   Install status hooks for supported agents.

The ls and new commands print paths with a trailing slash, making their output
convenient to use from Apex, Acme, and shells.

Configuration:
  default: ~/fbsource
  pre_remove:
    - /path/to/pre-remove-cleanup.sh

pre_remove commands run with the worktree as their working directory. They
receive WORK_HANDLE, WORK_WORKTREE_PATH, and WORK_PROJECT_ROOT. A failing hook
stops removal.
`

type command struct {
	backend     backend
	findBackend func() (backend, error)
	getwd       func() (string, error)
	chdir       func(string) error
	getenv      func(string) string
	homeDir     func() (string, error)
	now         func() time.Time
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	plumbFile   func(string) error
}

func main() {
	c := command{
		findBackend: findSaplingBackend,
		getwd:       os.Getwd,
		chdir:       os.Chdir,
		getenv:      os.Getenv,
		homeDir:     os.UserHomeDir,
		now:         time.Now,
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}
	os.Exit(c.run(os.Args[1:]))
}

func (c command) run(args []string) int {
	parsed, ok := parseArgs(args)
	if !ok {
		fmt.Fprintln(c.stderr, "usage: work [ls] | work new NAME | work rm [PATH|LABEL] | work note [PATH|LABEL] | work status | work dash | work install-hooks (try 'work -help' for help)")
		return 2
	}
	if parsed.action == "help" {
		fmt.Fprint(c.stdout, helpText)
		return 0
	}

	home, err := c.homeDir()
	if err != nil {
		return c.fail(fmt.Errorf("finding home directory: %w", err))
	}
	getenv := c.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if parsed.action == "install-hooks" {
		store, err := newStateStore(home, getenv)
		if err != nil {
			return c.fail(err)
		}
		if err := store.initialize(); err != nil {
			return c.fail(err)
		}
		installed, err := installAgentHooks(home, getenv)
		if err != nil {
			return c.fail(err)
		}
		for _, result := range installed {
			fmt.Fprintln(c.stdout, result)
		}
		fmt.Fprintf(c.stdout, "State database: %s\n", store.path)
		return 0
	}

	if c.backend == nil {
		finder := c.findBackend
		if finder == nil {
			finder = findSaplingBackend
		}
		c.backend, err = finder()
		if err != nil {
			return c.fail(err)
		}
	}

	cwd, err := c.getwd()
	if err != nil {
		return c.fail(fmt.Errorf("finding current directory: %w", err))
	}
	if parsed.action == "agent-status" {
		return c.recordAgentStatus(parsed, cwd, home, getenv)
	}

	cfg, err := loadConfig(home)
	if err != nil {
		return c.fail(err)
	}
	resolved, err := c.resolveRepository(cwd, home, cfg)
	if err != nil {
		return c.fail(err)
	}

	switch parsed.action {
	case "default":
		if !resolved.fromCurrent {
			printWorktrees(c.stdout, resolved.repo)
			return 0
		}
		target, err := currentWorktree(resolved.repo)
		if err != nil {
			return c.fail(err)
		}
		store, err := newStateStore(home, getenv)
		if err != nil {
			return c.fail(err)
		}
		state, exists, err := store.get(target.Path)
		if err != nil {
			return c.fail(err)
		}
		title := liveAgentTitles(resolved.repo, getenv)[filepath.Clean(target.Path)]
		if title == "" {
			title = state.Title
		}
		changeTitle, err := c.backend.latestChangeTitle(target.Path)
		if err != nil {
			return c.fail(err)
		}
		text, err := renderWorktreeSummary(target, state, exists, title, store, c.now(), changeTitle)
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprint(c.stdout, text)
		return 0
	case "ls":
		printWorktrees(c.stdout, resolved.repo)
		return 0
	case "status":
		store, err := newStateStore(home, getenv)
		if err != nil {
			return c.fail(err)
		}
		states, err := store.list(resolved.repo.MainRoot)
		if err != nil {
			return c.fail(err)
		}
		byWorktree := statesByWorktree(states)
		liveTitles := liveAgentTitles(resolved.repo, getenv)
		for _, worktree := range resolved.repo.Worktrees {
			if worktree.Main {
				continue
			}
			state, exists := byWorktree[filepath.Clean(worktree.Path)]
			title := liveTitles[filepath.Clean(worktree.Path)]
			if title == "" {
				title = state.Title
			}
			fmt.Fprintln(c.stdout, worktreeStatusLine(worktree.Path, state, exists, title, c.now()))
		}
		return 0
	case "dash":
		if err := launchDashboard(resolved.repo, c.stdout, c.stderr); err != nil {
			return c.fail(err)
		}
		return 0
	case "dash-live":
		store, err := newStateStore(home, getenv)
		if err != nil {
			return c.fail(err)
		}
		window := getenv("winid")
		if window == "" {
			return c.fail(fmt.Errorf("dash-live must run inside an Apex win tool"))
		}
		if err := runDashboardLive(c.backend, resolved.repo, store, window, c.stdin, c.stdout, c.now); err != nil {
			return c.fail(err)
		}
		return 0
	case "dash-expand":
		store, err := newStateStore(home, getenv)
		if err != nil {
			return c.fail(err)
		}
		if err := expandDashboardAtPoint(parsed.argument, resolved.repo, store); err != nil {
			return c.fail(err)
		}
		return 0
	case "note":
		target, err := noteTarget(resolved.repo, parsed.argument, cwd)
		if err != nil {
			return c.fail(err)
		}
		path, err := ensureNoteFile(home, target, c.now())
		if err != nil {
			return c.fail(err)
		}
		plumb := c.plumbFile
		if plumb == nil {
			plumb = plumbNote
		}
		if err := plumb(path); err != nil {
			return c.fail(err)
		}
		return 0
	case "new":
		path, label, err := nextWorktreePath(resolved.repo, parsed.argument, c.now())
		if err != nil {
			return c.fail(err)
		}
		if err := c.backend.add(resolved.repo, path, label, c.stderr); err != nil {
			return c.fail(fmt.Errorf("creating %s: %w", path, err))
		}
		fmt.Fprintln(c.stdout, directoryPath(path))
		return 0
	case "rm":
		target, err := removalTarget(resolved, parsed.argument, cwd)
		if err != nil {
			return c.fail(err)
		}
		if target.Main {
			return c.fail(fmt.Errorf("refusing to remove the main worktree at %s", target.Path))
		}
		if err := runPreRemoveHooks(cfg.PreRemove, resolved.repo, target, c.stderr); err != nil {
			return c.fail(err)
		}
		if resolved.fromCurrent && pathsEqual(target.Path, resolved.repo.CurrentRoot) {
			chdir := c.chdir
			if chdir == nil {
				chdir = os.Chdir
			}
			if err := chdir(resolved.repo.MainRoot); err != nil {
				return c.fail(fmt.Errorf("changing to main worktree %s before removal: %w", resolved.repo.MainRoot, err))
			}
		}
		if err := c.backend.remove(resolved.repo, target.Path, c.stderr); err != nil {
			return c.fail(fmt.Errorf("removing %s: %w", target.Path, err))
		}
		if store, err := newStateStore(home, getenv); err == nil {
			if err := store.delete(target.Path); err != nil {
				fmt.Fprintf(c.stderr, "work: warning: %v\n", err)
			}
		}
		return 0
	default:
		panic("unreachable action")
	}
}

type parsedArguments struct {
	action   string
	argument string
	agent    string
	session  string
}

func parseArgs(args []string) (parsedArguments, bool) {
	if len(args) == 0 {
		return parsedArguments{action: "default"}, true
	}
	if len(args) == 1 && isHelp(args[0]) {
		return parsedArguments{action: "help"}, true
	}
	if len(args) == 1 && (args[0] == "ls" || args[0] == "list") {
		return parsedArguments{action: "ls"}, true
	}
	if len(args) == 1 && args[0] == "status" {
		return parsedArguments{action: "status"}, true
	}
	if len(args) == 1 && args[0] == "dash" {
		return parsedArguments{action: "dash"}, true
	}
	if len(args) >= 1 && len(args) <= 2 && args[0] == "note" {
		parsed := parsedArguments{action: "note"}
		if len(args) == 2 {
			parsed.argument = args[1]
		}
		return parsed, true
	}
	if len(args) == 1 && args[0] == "dash-live" {
		return parsedArguments{action: "dash-live"}, true
	}
	if len(args) == 1 && args[0] == "install-hooks" {
		return parsedArguments{action: "install-hooks"}, true
	}
	if len(args) == 2 && (args[0] == "new" || args[0] == "add") {
		return parsedArguments{action: "new", argument: args[1]}, true
	}
	if len(args) >= 1 && len(args) <= 2 && (args[0] == "rm" || args[0] == "remove") {
		if len(args) == 2 {
			return parsedArguments{action: "rm", argument: args[1]}, true
		}
		return parsedArguments{action: "rm"}, true
	}
	if (len(args) == 3 || len(args) == 4) && args[0] == "agent-status" {
		if validateAgentStatus(args[1]) != nil || args[2] == "" {
			return parsedArguments{}, false
		}
		parsed := parsedArguments{action: "agent-status", argument: args[1], agent: args[2]}
		if len(args) == 4 {
			parsed.session = args[3]
		}
		return parsed, true
	}
	if len(args) == 2 && args[0] == "dash-expand" && args[1] != "" {
		return parsedArguments{action: "dash-expand", argument: args[1]}, true
	}
	return parsedArguments{}, false
}

func printWorktrees(output io.Writer, repo repository) {
	for _, worktree := range repo.Worktrees {
		if !worktree.Main {
			fmt.Fprintln(output, directoryPath(worktree.Path))
		}
	}
}

func isHelp(arg string) bool {
	return arg == "-help" || arg == "--help" || arg == "-h" || arg == "help"
}

func (c command) recordAgentStatus(parsed parsedArguments, cwd, home string, getenv func(string) string) int {
	reader := c.stdin
	if reader == nil {
		reader = strings.NewReader("")
	}
	input, err := readHookInput(reader)
	if err != nil {
		return c.fail(err)
	}
	payload := parseHookPayload(input)
	workdir := cwd
	if payload.CWD != "" {
		workdir = payload.CWD
	}
	repo, err := c.backend.open(workdir)
	if errors.Is(err, errNotWorktreeRepository) {
		return 0
	}
	if err != nil {
		return c.fail(err)
	}
	target, err := currentWorktree(repo)
	if err != nil {
		return c.fail(err)
	}
	store, err := newStateStore(home, getenv)
	if err != nil {
		return c.fail(err)
	}
	sessionID := payload.SessionID
	if sessionID == "" {
		sessionID = parsed.session
	}
	title := sanitizeAgentTitle(payload.Title, target.handle(), filepath.Base(repo.MainRoot))
	if liveTitle := liveAgentTitles(repo, getenv)[filepath.Clean(target.Path)]; liveTitle != "" {
		title = liveTitle
	}
	transcriptPath := resolveTranscriptPath(parsed.agent, sessionID, target.Path, payload.TranscriptPath, store)
	if title == "" && parsed.agent == "opencode" {
		title = sanitizeAgentTitle(
			openCodeSessionTitle(transcriptPath, sessionID, store.executable),
			target.handle(), filepath.Base(repo.MainRoot),
		)
	}
	apexSocket, apexSession, apexWindow := apexHookLocation(getenv)
	if err := store.update(agentState{
		WorktreePath:   target.Path,
		RepositoryRoot: repo.MainRoot,
		Status:         parsed.argument,
		Agent:          parsed.agent,
		SessionID:      sessionID,
		Title:          title,
		TranscriptPath: transcriptPath,
		ApexSocket:     apexSocket,
		ApexSession:    apexSession,
		ApexWindow:     apexWindow,
		UpdatedAt:      c.now().Unix(),
	}); err != nil {
		return c.fail(err)
	}
	return 0
}

func currentWorktree(repo repository) (worktree, error) {
	for _, candidate := range repo.Worktrees {
		if candidate.Current || pathsEqual(candidate.Path, repo.CurrentRoot) {
			return candidate, nil
		}
	}
	return worktree{}, fmt.Errorf("current worktree %s is not registered", repo.CurrentRoot)
}

type resolvedRepository struct {
	repo        repository
	fromCurrent bool
}

func (c command) resolveRepository(cwd, home string, cfg config) (resolvedRepository, error) {
	repo, err := c.backend.open(cwd)
	if err == nil {
		return resolvedRepository{repo: repo, fromCurrent: true}, nil
	}
	if !errors.Is(err, errNotWorktreeRepository) {
		return resolvedRepository{}, err
	}
	if cfg.Default == "" {
		return resolvedRepository{}, fmt.Errorf(
			"not in a %s worktree repository and no default is configured in %s",
			c.backend.name(), configFile(home),
		)
	}

	defaultPath, pathErr := expandConfiguredPath(cfg.Default, home)
	if pathErr != nil {
		return resolvedRepository{}, pathErr
	}
	repo, err = c.backend.open(defaultPath)
	if err != nil {
		return resolvedRepository{}, fmt.Errorf("opening default repository %s: %w", defaultPath, err)
	}
	return resolvedRepository{repo: repo}, nil
}

func nextWorktreePath(repo repository, requestedName string, now time.Time) (string, string, error) {
	name := strings.Join(strings.Fields(requestedName), "-")
	if name == "" || name == "." || name == ".." {
		return "", "", fmt.Errorf("worktree name must not be empty")
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
		return "", "", fmt.Errorf("worktree name %q must not contain path separators", requestedName)
	}

	parent := filepath.Dir(repo.MainRoot)
	project := filepath.Base(repo.MainRoot)
	baseLabel := now.Format("2006-01-02") + "-" + name
	for sequence := 1; ; sequence++ {
		label := baseLabel
		if sequence > 1 {
			label = fmt.Sprintf("%s-%d", baseLabel, sequence)
		}
		path := filepath.Join(parent, project+"-"+label)
		if worktreeIdentityExists(repo.Worktrees, path, label) {
			continue
		}
		if _, err := os.Lstat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("checking %s: %w", path, err)
		}
		return path, label, nil
	}
}

func worktreeIdentityExists(worktrees []worktree, path, label string) bool {
	for _, existing := range worktrees {
		if existing.Label == label || pathsEqual(existing.Path, path) {
			return true
		}
	}
	return false
}

func removalTarget(resolved resolvedRepository, argument, cwd string) (worktree, error) {
	if argument == "" {
		if !resolved.fromCurrent {
			return worktree{}, fmt.Errorf("rm without a path must be run from a linked worktree")
		}
		for _, candidate := range resolved.repo.Worktrees {
			if candidate.Current || pathsEqual(candidate.Path, resolved.repo.CurrentRoot) {
				return candidate, nil
			}
		}
		return worktree{}, fmt.Errorf("current worktree %s is not registered", resolved.repo.CurrentRoot)
	}

	argumentPath := argument
	if !filepath.IsAbs(argumentPath) {
		argumentPath = filepath.Join(cwd, argumentPath)
	}
	for _, candidate := range resolved.repo.Worktrees {
		if pathsEqual(candidate.Path, argumentPath) {
			return candidate, nil
		}
	}
	for _, candidate := range resolved.repo.Worktrees {
		if candidate.Label == argument || candidate.handle() == argument {
			return candidate, nil
		}
	}
	return worktree{}, fmt.Errorf("worktree %q not found (use 'work ls' to list worktrees)", argument)
}

func noteTarget(repo repository, argument, cwd string) (worktree, error) {
	if argument == "" {
		return currentWorktree(repo)
	}
	argumentPath := argument
	if !filepath.IsAbs(argumentPath) {
		argumentPath = filepath.Join(cwd, argumentPath)
	}
	for _, candidate := range repo.Worktrees {
		if pathsEqual(candidate.Path, argumentPath) || candidate.Label == argument || candidate.handle() == argument {
			return candidate, nil
		}
	}
	return worktree{}, fmt.Errorf("worktree %q not found (use 'work ls' to list worktrees)", argument)
}

func runPreRemoveHooks(hooks []string, repo repository, target worktree, diagnostics io.Writer) error {
	if len(hooks) == 0 {
		return nil
	}

	environment := os.Environ()
	values := map[string]string{
		"WORK_HANDLE":        target.handle(),
		"WORK_WORKTREE_PATH": target.Path,
		"WORK_PROJECT_ROOT":  repo.MainRoot,
	}
	for key, value := range values {
		environment = setEnvironment(environment, key, value)
	}

	for _, hook := range hooks {
		cmd := exec.Command("bash", "-c", hook)
		cmd.Dir = target.Path
		cmd.Env = environment
		cmd.Stdout = diagnostics
		cmd.Stderr = diagnostics
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pre_remove hook failed (%s): %w", hook, err)
		}
	}
	return nil
}

func setEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func pathsEqual(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil && rightErr == nil {
		return leftResolved == rightResolved
	}
	return left == right
}

func directoryPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasSuffix(path, string(filepath.Separator)) {
		return path
	}
	return path + string(filepath.Separator)
}

func (c command) fail(err error) int {
	fmt.Fprintf(c.stderr, "work: %v\n", err)
	return 1
}
