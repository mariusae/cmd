package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	dashboardPollInterval     = 500 * time.Millisecond
	dashboardRecentEventAge   = 2 * time.Hour
	dashboardRecentEventLimit = 20
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

type dashboardRender struct {
	text             string
	recentEvents     string
	newestEventStart int
	newestEventEnd   int
}

type dashboardWindowEditor interface {
	Replace(q0, q1 int, text string) error
	Select(q0, q1 int) error
	Show(at int) error
}

type dashboardEdit struct {
	q0, q1 int
	text   string
}

func launchDashboard(
	b backend,
	repo repository,
	store *stateStore,
	now func() time.Time,
) error {
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
	socket := apexSocketPath(store.getenv)

	// Render while the remote UI creates and marks the window live. The render
	// is local work, so this hides it behind those network round trips.
	renders := make(chan struct {
		view dashboardRender
		err  error
	}, 1)
	liveTitles := liveAgentTitlesFromTool(repo, tool)
	go func() {
		view, renderErr := renderDashboardView(repo, store, now(), liveTitles)
		renders <- struct {
			view dashboardRender
			err  error
		}{view: view, err: renderErr}
	}()

	dashboardName := filepath.Join(repo.MainRoot, "-work")
	dashboardWindow, err := tool.New(dashboardName)
	if err != nil {
		return err
	}
	if err := dashboardWindow.SetLive(true); err != nil {
		return err
	}
	renderResult := <-renders
	if renderResult.err != nil {
		return renderResult.err
	}
	rendered := renderResult.view

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
	if err := dashboardWindow.Replace(0, apexapi.End, rendered.text); err != nil {
		return err
	}
	if err := revealNewestDashboardEvent(dashboardWindow, rendered); err != nil {
		return err
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(signals)
	ticker := time.NewTicker(dashboardPollInterval)
	defer ticker.Stop()

	lastRender := rendered
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
					rendered, err = renderDashboardView(repo, store, now(), liveAgentTitlesFromTool(repo, tool))
					if err != nil {
						requestErr = err
						break
					}
					if err := updateDashboardWindow(dashboardWindow, lastRender, rendered); err != nil {
						requestErr = err
						break
					}
					lastRender = rendered
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
		rendered, err = renderDashboardView(repo, store, now(), liveAgentTitlesFromTool(repo, tool))
		if err != nil {
			continue
		}
		if rendered.text == lastRender.text {
			continue
		}
		if err := updateDashboardWindow(dashboardWindow, lastRender, rendered); err != nil {
			return err
		}
		lastRender = rendered
	}
}

func updateDashboardWindow(
	window dashboardWindowEditor,
	previous, next dashboardRender,
) error {
	edit, changed := minimalDashboardEdit(previous.text, next.text)
	if !changed {
		return nil
	}
	if err := window.Replace(edit.q0, edit.q1, edit.text); err != nil {
		return err
	}
	if next.recentEvents == "" || next.recentEvents == previous.recentEvents {
		return nil
	}
	return revealNewestDashboardEvent(window, next)
}

func revealNewestDashboardEvent(
	window dashboardWindowEditor,
	rendered dashboardRender,
) error {
	if rendered.recentEvents == "" {
		return nil
	}
	if err := window.Show(rendered.newestEventStart); err != nil {
		return err
	}
	// Select does not alter Apex's viewport, so this keeps the status visible
	// while highlighting the status and the complete assistant output.
	return window.Select(rendered.newestEventStart, rendered.newestEventEnd)
}

func minimalDashboardEdit(before, after string) (dashboardEdit, bool) {
	if before == after {
		return dashboardEdit{}, false
	}
	oldRunes := []rune(before)
	newRunes := []rune(after)
	prefix := 0
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldRunes)-prefix && suffix < len(newRunes)-prefix &&
		oldRunes[len(oldRunes)-1-suffix] == newRunes[len(newRunes)-1-suffix] {
		suffix++
	}
	return dashboardEdit{
		q0:   prefix,
		q1:   len(oldRunes) - suffix,
		text: string(newRunes[prefix : len(newRunes)-suffix]),
	}, true
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
	if agent != "" && state.Agent != agent {
		return apexAgentLocation{}, false
	}
	target, ok := resolveAgentLocation(state, path, socket)
	if !ok {
		return apexAgentLocation{}, false
	}
	window := strconv.Itoa(target.window)
	if window != state.ApexWindow {
		// Native Apex terminals do not inherit winid: Apex starts their shell
		// before the UI allocates its window. Cache the concrete window once
		// the target session resolves it so subsequent navigation has the ID.
		_ = store.rememberApexWindow(path, state.ApexSocket, target.session, window)
	}
	return target, true
}

func dashboardAgentForPlumb(text string, event apexapi.Plumb, worktrees []worktree) (string, string, bool) {
	if event.At == nil {
		return "", "", false
	}
	switch event.Verb {
	case "Agent":
		path, ok := dashboardWorktreeAtPoint(text, event.At.Q0, worktrees)
		return path, "", ok
	case "plumb":
		path, agent, ok := dashboardAgentAtPoint(text, event.At.Q0, worktrees)
		if !ok {
			return "", "", false
		}
		if agent != event.Text {
			return "", "", false
		}
		return path, agent, true
	default:
		return "", "", false
	}
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

func resolveAgentLocation(state agentState, path, socket string) (apexAgentLocation, bool) {
	if state.ApexSession == "" || state.ApexSocket == "" ||
		filepath.Clean(state.ApexSocket) != filepath.Clean(socket) {
		return apexAgentLocation{}, false
	}
	window, ok := discoverApexAgentWindow(state.ApexSocket, state.ApexSession, path)
	if !ok {
		return matchingAgentLocation(state, state.Agent, socket)
	}
	id, err := strconv.Atoi(window)
	if err != nil || id <= 0 {
		return apexAgentLocation{}, false
	}
	return apexAgentLocation{session: state.ApexSession, window: id}, true
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

func renderDashboard(repo repository, store *stateStore, now time.Time, liveTitles map[string]string) (string, error) {
	rendered, err := renderDashboardView(repo, store, now, liveTitles)
	return rendered.text, err
}

func renderDashboardView(repo repository, store *stateStore, now time.Time, liveTitles map[string]string) (dashboardRender, error) {
	states, err := store.list(repo.MainRoot)
	if err != nil {
		return dashboardRender{}, err
	}
	expanded, err := store.expandedWorktrees()
	if err != nil {
		return dashboardRender{}, err
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
		if err := writeWorktreeDetails(&output, worktree, state, store, now); err != nil {
			return dashboardRender{}, err
		}
	}
	recentEvents, newestEventLength, err := renderRecentEvents(repo.MainRoot, store, now)
	if err != nil {
		return dashboardRender{}, err
	}
	newestEventStart := 0
	newestEventEnd := 0
	if recentEvents != "" {
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		newestEventStart = utf8.RuneCountInString(output.String())
		newestEventEnd = newestEventStart + newestEventLength
		output.WriteString(recentEvents)
	}
	return dashboardRender{
		text:             output.String(),
		recentEvents:     recentEvents,
		newestEventStart: newestEventStart,
		newestEventEnd:   newestEventEnd,
	}, nil
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
	if err := writeAgentEvents(&output, worktree.Path, state, store, now); err != nil {
		return "", err
	}
	return output.String(), nil
}

func writeWorktreeDetails(output *strings.Builder, worktree worktree, state agentState, store *stateStore, now time.Time) error {
	note, err := ensureNoteFile(store.home, worktree, now)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "\t%s\n", note)
	return writeAgentEvents(output, worktree.Path, state, store, now)
}

func writeAgentEvents(output *strings.Builder, worktreePath string, state agentState, store *stateStore, now time.Time) error {
	events, err := store.listSessionEvents(worktreePath, state.Agent, state.SessionID)
	if err != nil {
		return err
	}
	for _, event := range events {
		fmt.Fprintf(output, "\t%s %s\n", formatEventTime(event.CreatedAt, now), dashboardEventStatus(event))
		writeEventTranscript(output, event, store, "\t\t")
	}
	return nil
}

func renderRecentEvents(repositoryRoot string, store *stateStore, now time.Time) (string, int, error) {
	events, err := store.listRecentEvents(
		repositoryRoot,
		now.Add(-dashboardRecentEventAge).Unix(),
		dashboardRecentEventLimit,
	)
	if err != nil {
		return "", 0, err
	}
	if len(events) == 0 {
		return "", 0, nil
	}
	var output strings.Builder
	newestEventLength := 0
	for index, event := range events {
		fmt.Fprintf(
			&output,
			"%s\t%s\t%s\t%s\n",
			directoryPath(event.WorktreePath),
			dashboardEventStatus(event),
			formatEventTime(event.CreatedAt, now),
			dashboardField(event.Agent),
		)
		writeEventTranscript(&output, event, store, "\t")
		if index == 0 {
			newestEventLength = utf8.RuneCountInString(output.String())
		}
	}
	return output.String(), newestEventLength, nil
}

func dashboardEventStatus(event agentEvent) string {
	status := event.Status
	if status == "done" {
		status = "complete"
		if event.StartedAt > 0 {
			status += " (" + formatOperationDuration(event.StartedAt, event.CreatedAt) + ")"
		}
	}
	return status
}

func writeEventTranscript(output *strings.Builder, event agentEvent, store *stateStore, indent string) {
	if event.Status != "done" {
		return
	}
	messages, _ := store.transcriptMessages(event)
	if transcript := transcriptForEvent(messages, event.CreatedAt); transcript != "" {
		writeIndentedTranscript(output, transcript, indent)
	}
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

func writeIndentedTranscript(output *strings.Builder, transcript, indent string) {
	for _, line := range strings.Split(transcript, "\n") {
		output.WriteString(indent)
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
		return "", false
	}
	return "", false
}
