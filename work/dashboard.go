package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	apexapi "github.com/mariusae/apex/go/apex"
)

const (
	dashboardPollInterval = 500 * time.Millisecond
	dashboardEventLimit   = 5
)

type dashboardRequestKind int

const (
	dashboardExpandRequest dashboardRequestKind = iota
	dashboardNavigationRequest
	dashboardRefreshRequest
)

type dashboardRequest struct {
	kind     dashboardRequestKind
	plumb    apexapi.Plumb
	response chan bool
}

type apexAgentLocation struct {
	session string
	window  int
}

func launchDashboard(repo repository, stdout, stderr io.Writer) error {
	executable, err := exec.LookPath("apex")
	if err != nil {
		return fmt.Errorf("apex is required for 'work dash'")
	}
	workExecutable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating work executable: %w", err)
	}
	// Apex's win host owns the window and marks it live, so Del closes the
	// window and terminates this same work executable cleanly.
	cmd := exec.Command(executable, "tool", "win", workExecutable, "dash-live")
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
	windowID, err := strconv.Atoi(window)
	if err != nil || windowID < 0 {
		return fmt.Errorf("invalid Apex window %q", window)
	}
	if now == nil {
		now = time.Now
	}
	if err := store.initialize(); err != nil {
		return err
	}

	tool, err := attachApexTool(fmt.Sprintf("work-dash-%d", os.Getpid()), store.getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = tool.Close()
	}()
	dashboardWindow := tool.Window(windowID)
	socket := apexSocketPath(store.getenv)

	repo := initial
	text, err := renderDashboard(repo, store, now(), liveAgentTitlesFromTool(repo, tool))
	if err != nil {
		return err
	}

	requests := make(chan dashboardRequest)
	handler := func(kind dashboardRequestKind) func(apexapi.Plumb) bool {
		return func(plumb apexapi.Plumb) bool {
			response := make(chan bool, 1)
			select {
			case requests <- dashboardRequest{kind: kind, plumb: plumb, response: response}:
			case <-ctx.Done():
				return false
			}
			select {
			case accepted := <-response:
				return accepted
			case <-ctx.Done():
				return false
			}
		}
	}
	if _, err := tool.Offer(apexapi.Rule{
		Verb: "Expand", Window: dashboardWindow, Priority: 100,
	}, handler(dashboardExpandRequest)); err != nil {
		return err
	}
	if _, err := tool.Offer(apexapi.Rule{
		Text: "^[[:alnum:]_.+-]+$", Window: dashboardWindow, Priority: 100,
	}, handler(dashboardNavigationRequest)); err != nil {
		return err
	}
	if _, err := tool.Offer(apexapi.Rule{
		Verb: "Agent", Window: dashboardWindow, Priority: 100,
	}, handler(dashboardNavigationRequest)); err != nil {
		return err
	}
	if _, err := tool.Offer(apexapi.Rule{
		Verb: "Get", Window: dashboardWindow, Priority: 100,
	}, handler(dashboardRefreshRequest)); err != nil {
		return err
	}

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- tool.Serve(ctx)
	}()
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
	waitForDashboardText(dashboardWindow, text, time.Second)
	_ = dashboardWindow.Select(0, 0)

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
		case serveErr := <-serveErrors:
			if serveErr == nil || errors.Is(serveErr, context.Canceled) {
				return nil
			}
			return fmt.Errorf("serving Apex dashboard: %w", serveErr)
		case request := <-requests:
			accepted := false
			var requestErr error
			switch request.kind {
			case dashboardExpandRequest:
				if request.plumb.Window != nil && request.plumb.Window.ID == dashboardWindow.ID {
					accepted = expandDashboardWindowAtPoint(dashboardWindow, repo, store) == nil
				}
			case dashboardRefreshRequest:
				if request.plumb.Window != nil && request.plumb.Window.ID == dashboardWindow.ID {
					accepted = true
					refreshed, err := b.open(repo.MainRoot)
					if err != nil {
						requestErr = err
						break
					}
					repo = refreshed
					text, err = renderDashboard(repo, store, now(), liveAgentTitlesFromTool(repo, tool))
					if err != nil {
						requestErr = err
						break
					}
					if err := dashboardWindow.Replace(0, apexapi.End, text); err != nil {
						requestErr = err
						break
					}
					lastText = text
				}
			case dashboardNavigationRequest:
				var target apexAgentLocation
				target, accepted = dashboardAgentLocation(dashboardWindow, request.plumb, repo, store, socket)
				if accepted {
					requestErr = tool.Switch(target.session, target.window)
				}
			}
			request.response <- accepted
			if requestErr != nil {
				return requestErr
			}
			continue
		case <-ticker.C:
		}

		exists, err := apexWindowExists(tool, dashboardWindow.ID)
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
		text, err = renderDashboard(repo, store, now(), liveAgentTitlesFromTool(repo, tool))
		if err != nil {
			continue
		}
		if text == lastText {
			continue
		}
		if err := dashboardWindow.Replace(0, apexapi.End, text); err != nil {
			return err
		}
		lastText = text
	}
}

func apexWindowExists(tool *apexapi.Tool, id int) (bool, error) {
	windows, err := tool.Windows()
	if err != nil {
		return false, err
	}
	for _, window := range windows {
		if window.ID == id {
			return true, nil
		}
	}
	return false, nil
}

func dashboardAgentLocation(
	dashboardWindow *apexapi.Window,
	event apexapi.Plumb,
	repo repository,
	store *stateStore,
	socket string,
) (apexAgentLocation, bool) {
	if event.At == nil || event.Window == nil || event.Window.ID != dashboardWindow.ID {
		return apexAgentLocation{}, false
	}
	text, err := dashboardWindow.Read()
	if err != nil {
		return apexAgentLocation{}, false
	}
	path, agent, ok := dashboardAgentForPlumb(text, event, repo.Worktrees)
	if !ok {
		return apexAgentLocation{}, false
	}
	state, found, err := store.get(path)
	if err != nil || !found {
		return apexAgentLocation{}, false
	}
	return matchingAgentLocation(state, agent, socket)
}

func dashboardAgentForPlumb(text string, event apexapi.Plumb, worktrees []worktree) (string, string, bool) {
	if event.At == nil {
		return "", "", false
	}
	path, agent, ok := dashboardAgentAtPoint(text, event.At.Q0, worktrees)
	if !ok {
		return "", "", false
	}
	switch event.Verb {
	case "Agent":
	case "plumb":
		if agent != event.Text {
			return "", "", false
		}
	default:
		return "", "", false
	}
	return path, agent, true
}

func matchingAgentLocation(state agentState, agent, socket string) (apexAgentLocation, bool) {
	if state.Agent != agent || state.ApexWindow == "" || state.ApexSession == "" ||
		state.ApexSocket == "" || filepath.Clean(state.ApexSocket) != filepath.Clean(socket) {
		return apexAgentLocation{}, false
	}
	window, err := strconv.Atoi(state.ApexWindow)
	if err != nil || window <= 0 {
		return apexAgentLocation{}, false
	}
	return apexAgentLocation{session: state.ApexSession, window: window}, true
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

func waitForDashboardText(window *apexapi.Window, want string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if text, err := window.Read(); err == nil && text == want {
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
		if err := writeWorktreeDetails(&output, worktree, store, now); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

func renderWorktreeSummary(worktree worktree, state agentState, exists bool, title string, store *stateStore, now time.Time, changeTitle string) (string, error) {
	var output strings.Builder
	output.WriteString(worktreeStatusLine(worktree.Path, state, exists, title, now))
	output.WriteByte('\n')
	note, err := ensureNoteFile(store.home, worktree, now)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&output, "note: %s\n", note)
	fmt.Fprintf(&output, "last change: %s\n", dashboardField(changeTitle))
	output.WriteString("agent:\n")
	if err := writeAgentEvents(&output, worktree.Path, store, now); err != nil {
		return "", err
	}
	return output.String(), nil
}

func writeWorktreeDetails(output *strings.Builder, worktree worktree, store *stateStore, now time.Time) error {
	note, err := ensureNoteFile(store.home, worktree, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "\t%s\n", note)
	return writeAgentEvents(output, worktree.Path, store, now)
}

func writeAgentEvents(output *strings.Builder, worktreePath string, store *stateStore, now time.Time) error {
	events, err := store.listEvents(worktreePath, dashboardEventLimit)
	if err != nil {
		return err
	}
	for _, event := range events {
		status := event.Status
		if status == "done" {
			status = "complete"
			if event.StartedAt > 0 {
				status += " (" + formatOperationDuration(event.StartedAt, event.CreatedAt) + ")"
			}
		}
		fmt.Fprintf(output, "\t%s %s\n", formatEventTime(event.CreatedAt, now), status)
		if event.Status != "done" {
			continue
		}
		messages, _ := store.transcriptMessages(event)
		if transcript := transcriptForEvent(messages, event.CreatedAt); transcript != "" {
			writeIndentedTranscript(output, transcript)
		}
	}
	return nil
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
	windowID, err := strconv.Atoi(window)
	if err != nil || windowID < 0 {
		return fmt.Errorf("invalid Apex window %q", window)
	}
	tool, err := attachApexTool(fmt.Sprintf("work-expand-%d", os.Getpid()), store.getenv)
	if err != nil {
		return err
	}
	defer tool.Close()
	return expandDashboardWindowAtPoint(tool.Window(windowID), repo, store)
}

func expandDashboardWindowAtPoint(window *apexapi.Window, repo repository, store *stateStore) error {
	q0, _, err := window.Selection()
	if err != nil {
		return err
	}
	text, err := window.Read()
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
