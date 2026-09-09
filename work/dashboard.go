package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	dashboardPollInterval = 500 * time.Millisecond
	dashboardEventLimit   = 5
)

type apexClient struct {
	executable string
}

func newApexClient() (apexClient, error) {
	executable, err := exec.LookPath("apex")
	if err != nil {
		return apexClient{}, fmt.Errorf("apex is required for 'work dash'")
	}
	return apexClient{executable: executable}, nil
}

func (a apexClient) output(input string, args ...string) (string, error) {
	cmd := exec.Command(a.executable, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("apex %s: %s", strings.Join(args, " "), detail)
	}
	return stdout.String(), nil
}

func (a apexClient) addExpandRule(windowName, workExecutable string) (string, error) {
	run := shellQuote(workExecutable) + " dash-expand $win"
	output, err := a.output("", "plumb", "rule", "add",
		"-verb=Expand",
		"-file=^"+regexp.QuoteMeta(windowName)+"$",
		"-kind=file",
		"-run="+run,
		"-priority=100",
	)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(output)
	if id == "" {
		return "", fmt.Errorf("apex plumb rule add returned no rule id")
	}
	return id, nil
}

func (a apexClient) addAgentNavigationRule(windowName, toolName string) (string, error) {
	output, err := a.output("", "plumb", "rule", "add",
		"-text=^[[:alnum:]_.+-]+$",
		"-file=^"+regexp.QuoteMeta(windowName)+"$",
		"-kind=file",
		"-tool="+toolName,
		"-priority=100",
	)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(output)
	if id == "" {
		return "", fmt.Errorf("apex plumb rule add returned no rule id")
	}
	return id, nil
}

func (a apexClient) addRefreshRule(windowName, toolName string) (string, error) {
	output, err := a.output("", "plumb", "rule", "add",
		"-verb=Get",
		"-file=^"+regexp.QuoteMeta(windowName)+"$",
		"-kind=file",
		"-tool="+toolName,
		"-priority=100",
	)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(output)
	if id == "" {
		return "", fmt.Errorf("apex plumb rule add returned no rule id")
	}
	return id, nil
}

func (a apexClient) windowName(id string) (string, error) {
	windows, err := a.windows()
	if err != nil {
		return "", err
	}
	for _, window := range windows {
		if window.ID == id {
			return window.Name, nil
		}
	}
	return "", fmt.Errorf("Apex window %s no longer exists", id)
}

func (a apexClient) removeRule(id string) {
	if id != "" {
		_, _ = a.output("", "plumb", "rule", "rm", id)
	}
}

func (a apexClient) windowExists(id string) (bool, error) {
	output, err := a.output("", "win", "list")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == id {
			return true, nil
		}
	}
	return false, nil
}

func (a apexClient) selection(id string) (int, int, error) {
	output, err := a.output("", "sel", id)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(output)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("apex sel returned %q", strings.TrimSpace(output))
	}
	q0, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, fmt.Errorf("parsing Apex selection: %w", err)
	}
	q1, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, fmt.Errorf("parsing Apex selection: %w", err)
	}
	return q0, q1, nil
}

func (a apexClient) text(id string) (string, error) {
	return a.output("", "text", "read", id)
}

func (a apexClient) replaceText(id, text string) error {
	q0, q1, selectionErr := a.selection(id)
	if _, err := a.output("", "edit", id, apexReplaceProgram(text)); err != nil {
		return err
	}
	if selectionErr == nil {
		length := utf8.RuneCountInString(text)
		q0 = min(q0, length)
		q1 = min(q1, length)
		if _, err := a.output("", "sel", id, strconv.Itoa(q0), strconv.Itoa(q1)); err != nil {
			return err
		}
	}
	return nil
}

func apexReplaceProgram(text string) string {
	text = strings.NewReplacer(
		`\`, `\\`,
		`|`, `\|`,
		"\n", `\n`,
	).Replace(text)
	return ",c|" + text + "|"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func launchDashboard(repo repository, stdout, stderr io.Writer) error {
	apex, err := newApexClient()
	if err != nil {
		return err
	}
	workExecutable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating work executable: %w", err)
	}
	// Apex's win host owns the window and marks it live, so Del closes the
	// window and terminates this same work executable cleanly.
	cmd := exec.Command(apex.executable, "tool", "win", workExecutable, "dash-live")
	cmd.Dir = repo.MainRoot
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting Apex dashboard: %w", err)
	}
	return nil
}

func runDashboardLive(
	b backend,
	initial repository,
	store *stateStore,
	window string,
	stdin io.Reader,
	stdout io.Writer,
	now func() time.Time,
) error {
	apex, err := newApexClient()
	if err != nil {
		return err
	}
	if now == nil {
		now = time.Now
	}
	if err := store.initialize(); err != nil {
		return err
	}

	repo := initial
	text, err := renderDashboard(repo, store, now(), liveAgentTitles(repo, os.Getenv))
	if err != nil {
		return err
	}
	windowName, err := apex.windowName(window)
	if err != nil {
		return err
	}

	workExecutable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating work executable: %w", err)
	}
	rule, err := apex.addExpandRule(windowName, workExecutable)
	if err != nil {
		return err
	}
	defer apex.removeRule(rule)

	socket := apexSocketPath(store.getenv)
	session := apexSessionName(store.getenv)
	toolName := fmt.Sprintf("work-dash-%d", os.Getpid())
	navigator, err := connectApexTool(socket, session, toolName)
	if err != nil {
		return err
	}
	defer navigator.Close()
	navigationRule, err := apex.addAgentNavigationRule(windowName, toolName)
	if err != nil {
		return err
	}
	defer apex.removeRule(navigationRule)
	refreshRule, err := apex.addRefreshRule(windowName, toolName)
	if err != nil {
		return err
	}
	defer apex.removeRule(refreshRule)
	navigationEvents := navigator.events
	if stdin != nil {
		// The win host treats edits to its body as process input. Dashboard
		// refreshes replace that body, so keep its pseudo-terminal drained.
		go func() {
			_, _ = io.Copy(io.Discard, stdin)
		}()
	}
	if _, err := fmt.Fprint(stdout, text); err != nil {
		return fmt.Errorf("writing initial dashboard: %w", err)
	}
	waitForDashboardText(apex, window, text, time.Second)
	_, _ = apex.output("", "sel", window, "0", "0")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(signals)
	ticker := time.NewTicker(dashboardPollInterval)
	defer ticker.Stop()

	lastText := text
	for {
		select {
		case <-signals:
			return nil
		case event, ok := <-navigationEvents:
			if !ok {
				return fmt.Errorf("Apex dashboard navigation connection closed")
			}
			if event.Verb == "Get" {
				accepted := event.Window == window
				if err := navigator.acknowledge(event.ID, accepted); err != nil {
					return err
				}
				if !accepted {
					continue
				}
				refreshed, err := b.open(repo.MainRoot)
				if err != nil {
					return err
				}
				repo = refreshed
				text, err = renderDashboard(repo, store, now(), liveAgentTitles(repo, os.Getenv))
				if err != nil {
					return err
				}
				if err := apex.replaceText(window, text); err != nil {
					return err
				}
				lastText = text
				continue
			}
			if event.Verb != "plumb" {
				if err := navigator.acknowledge(event.ID, false); err != nil {
					return err
				}
				continue
			}
			target, accepted := dashboardAgentWindow(apex, window, event, repo, store, socket, session)
			if err := navigator.acknowledge(event.ID, accepted); err != nil {
				return err
			}
			if accepted {
				if err := navigator.gotoWindow(target); err != nil {
					return err
				}
			}
			continue
		case <-ticker.C:
		}

		exists, err := apex.windowExists(window)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		refreshed, err := b.open(repo.MainRoot)
		if err != nil {
			continue
		}
		repo = refreshed
		text, err = renderDashboard(repo, store, now(), liveAgentTitles(repo, os.Getenv))
		if err != nil {
			continue
		}
		if text == lastText {
			continue
		}
		if err := apex.replaceText(window, text); err != nil {
			return err
		}
		lastText = text
	}
}

func dashboardAgentWindow(
	apex apexClient,
	dashboardWindow string,
	event apexPlumb,
	repo repository,
	store *stateStore,
	socket, session string,
) (string, bool) {
	if !event.HasAt || event.Window != dashboardWindow {
		return "", false
	}
	text, err := apex.text(dashboardWindow)
	if err != nil {
		return "", false
	}
	path, agent, ok := dashboardAgentAtPoint(text, event.Point, repo.Worktrees)
	if !ok || agent != event.Text {
		return "", false
	}
	state, found, err := store.get(path)
	if err != nil || !found {
		return "", false
	}
	windows, err := apex.windows()
	if err != nil {
		return "", false
	}
	return matchingAgentWindow(state, agent, path, socket, session, windows)
}

func matchingAgentWindow(state agentState, agent, path, socket, session string, windows []apexWindow) (string, bool) {
	if state.Agent != agent || state.ApexWindow == "" || state.ApexSession != session ||
		state.ApexSocket == "" || filepath.Clean(state.ApexSocket) != filepath.Clean(socket) {
		return "", false
	}
	for _, candidate := range windows {
		if candidate.ID == state.ApexWindow && candidate.Live && windowInWorktree(candidate.Name, path) {
			return candidate.Name, true
		}
	}
	return "", false
}

func dashboardAgentAtPoint(text string, point int, worktrees []worktree) (string, string, bool) {
	if point < 0 {
		return "", "", false
	}
	lines := strings.Split(text, "\n")
	offset := 0
	for _, line := range lines {
		lineLength := utf8.RuneCountInString(line)
		if point > offset+lineLength {
			offset += lineLength + 1
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			return "", "", false
		}
		agentStart := utf8.RuneCountInString(strings.Join(fields[:3], "\t")) + 1
		agentEnd := agentStart + utf8.RuneCountInString(fields[3])
		withinLine := point - offset
		// Apex's word expansion also takes the word immediately to the left
		// when the click lands on its trailing boundary.
		if withinLine < agentStart || withinLine > agentEnd {
			return "", "", false
		}
		registered := make(map[string]string, len(worktrees))
		for _, worktree := range worktrees {
			if !worktree.Main {
				registered[directoryPath(worktree.Path)] = filepath.Clean(worktree.Path)
			}
		}
		path, found := registered[fields[0]]
		return path, fields[3], found
	}
	return "", "", false
}

func waitForDashboardText(apex apexClient, window, want string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, err := apex.text(window); err == nil && text == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func renderDashboard(repo repository, store *stateStore, now time.Time, liveTitles map[string]string) (string, error) {
	states, err := store.list(repo.MainRoot)
	if err != nil {
		return "", err
	}
	expanded, err := store.expandedWorktrees()
	if err != nil {
		return "", err
	}
	byWorktree := statesByWorktree(states)

	var output strings.Builder
	for _, worktree := range repo.Worktrees {
		if worktree.Main {
			continue
		}
		path := filepath.Clean(worktree.Path)
		state, exists := byWorktree[path]
		title := liveTitles[path]
		if title == "" {
			title = state.Title
		}
		output.WriteString(worktreeStatusLine(path, state, exists, title, now))
		output.WriteByte('\n')

		if !expanded[path] {
			continue
		}
		events, err := store.listEvents(path, dashboardEventLimit)
		if err != nil {
			return "", err
		}
		for _, event := range events {
			status := event.Status
			if status == "done" {
				status = "complete"
				if event.StartedAt > 0 {
					status += " (" + formatOperationDuration(event.StartedAt, event.CreatedAt) + ")"
				}
			}
			fmt.Fprintf(&output, "\t%s %s\n", formatEventTime(event.CreatedAt, now), status)
			if event.Status != "done" {
				continue
			}
			messages, _ := store.transcriptMessages(event)
			if transcript := transcriptForEvent(messages, event.CreatedAt); transcript != "" {
				writeIndentedTranscript(&output, transcript)
			}
		}
	}
	return output.String(), nil
}

func worktreeStatusLine(path string, state agentState, exists bool, title string, now time.Time) string {
	fields := []string{directoryPath(path)}
	if exists {
		fields = append(fields, state.Status, formatEventTime(state.UpdatedAt, now), dashboardField(state.Agent))
	} else {
		fields = append(fields, "idle")
	}
	if title = dashboardField(title); title != "" {
		fields = append(fields, title)
	}
	return strings.Join(fields, "\t")
}

func writeIndentedTranscript(output *strings.Builder, transcript string) {
	for _, line := range strings.Split(transcript, "\n") {
		output.WriteString("\t\t")
		output.WriteString(line)
		output.WriteByte('\n')
	}
}

func dashboardField(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func expandDashboardAtPoint(window string, repo repository, store *stateStore) error {
	apex, err := newApexClient()
	if err != nil {
		return err
	}
	q0, _, err := apex.selection(window)
	if err != nil {
		return err
	}
	text, err := apex.text(window)
	if err != nil {
		return err
	}
	path, ok := dashboardWorktreeAtPoint(text, q0, repo.Worktrees)
	if !ok {
		return fmt.Errorf("Expand: point is not on a worktree or its activity")
	}
	return store.toggleExpanded(path)
}

func dashboardWorktreeAtPoint(text string, point int, worktrees []worktree) (string, bool) {
	lines := strings.Split(text, "\n")
	lineIndex := 0
	offset := 0
	for index, line := range lines {
		end := offset + utf8.RuneCountInString(line)
		if point <= end {
			lineIndex = index
			break
		}
		lineIndex = index
		offset = end + 1
	}

	registered := make(map[string]string, len(worktrees))
	for _, worktree := range worktrees {
		if !worktree.Main {
			registered[directoryPath(worktree.Path)] = filepath.Clean(worktree.Path)
		}
	}
	for index := lineIndex; index >= 0; index-- {
		line := lines[index]
		if strings.HasPrefix(line, "\t") || line == "" {
			continue
		}
		path := strings.SplitN(line, "\t", 2)[0]
		if registeredPath, ok := registered[path]; ok {
			return registeredPath, true
		}
		if index != lineIndex {
			break
		}
	}
	return "", false
}
