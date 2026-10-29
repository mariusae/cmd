package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Listing every Manifold space takes about half a second, so the panel polls
// less eagerly than the Apex window.
const manifoldDashboardPollInterval = 2 * time.Second

type widget map[string]any

// launchManifoldDashboard shows the dashboard as a Manifold panel: a row of
// buttons for each linked worktree, the expanded histories, and the recent
// summaries. It returns once the panel is closed.
func launchManifoldDashboard(
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
	client, err := newManiClient(store.getenv)
	if err != nil {
		return err
	}
	render := func(message string) ([]widget, error) {
		titles := map[string]string{}
		if layout, err := client.layout(); err == nil {
			titles = agentTitlesFromManifold(repo, layout)
		}
		return renderManifoldDashboard(repo, store, now(), titles, message)
	}
	widgets, err := render("")
	if err != nil {
		return err
	}
	panel, err := client.newPanel("work · "+filepath.Base(repo.MainRoot), widgets)
	if err != nil {
		return err
	}
	last, _ := json.Marshal(widgets)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan maniEvent)
	waitErrors := make(chan error, 1)
	go func() {
		for {
			event, err := client.waitPanel(ctx, panel)
			if err != nil {
				if ctx.Err() == nil {
					waitErrors <- err
				}
				return
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(signals)
	ticker := time.NewTicker(manifoldDashboardPollInterval)
	defer ticker.Stop()

	message := ""
	for {
		select {
		case <-signals:
			return nil
		case err := <-waitErrors:
			return fmt.Errorf("serving Manifold dashboard: %w", err)
		case event := <-events:
			if event.Event != "click" {
				continue
			}
			message = ""
			if err := handleManifoldDashboardClick(client, b, &repo, store, now, event.ID); err != nil {
				message = err.Error()
			}
		case <-ticker.C:
			shown, err := client.onScreen(panel)
			if err != nil {
				return err
			}
			if !shown {
				return nil
			}
			if refreshed, err := b.open(repo.MainRoot); err == nil {
				repo = refreshed
			}
		}
		widgets, err := render(message)
		if err != nil {
			continue
		}
		encoded, _ := json.Marshal(widgets)
		if bytes.Equal(encoded, last) {
			continue
		}
		if err := client.setPanel(panel, widgets); err != nil {
			return err
		}
		last = encoded
	}
}

func handleManifoldDashboardClick(
	client *maniClient,
	b backend,
	repo *repository,
	store *stateStore,
	now func() time.Time,
	id string,
) error {
	if id == "refresh" {
		refreshed, err := b.open(repo.MainRoot)
		if err != nil {
			return err
		}
		*repo = refreshed
		return nil
	}
	action, handle, ok := strings.Cut(id, ":")
	if !ok {
		return nil
	}
	var target worktree
	found := false
	for _, candidate := range repo.Worktrees {
		if !candidate.Main && candidate.handle() == handle {
			target, found = candidate, true
			break
		}
	}
	if !found {
		return fmt.Errorf("worktree %s no longer exists", handle)
	}
	switch action {
	case "space":
		recorded, _, err := store.worktreeSpace(target.Path)
		if err != nil {
			return err
		}
		return switchManifoldSpace(client, store, target, worktreeStart(target, recorded.StartDirectory))
	case "agent":
		return showManifoldAgent(client, store, target.Path)
	case "note":
		path, err := ensureNoteFile(store.home, target, now())
		if err != nil {
			return err
		}
		return client.open(path)
	case "expand":
		return store.toggleExpanded(target.Path)
	}
	return errors.New("unknown dashboard action " + action)
}

func renderManifoldDashboard(
	repo repository,
	store *stateStore,
	now time.Time,
	liveTitles map[string]string,
	message string,
) ([]widget, error) {
	states, err := store.list(repo.MainRoot)
	if err != nil {
		return nil, err
	}
	expanded, err := store.expandedWorktrees()
	if err != nil {
		return nil, err
	}
	byWorktree := statesByWorktree(states)

	widgets := []widget{{
		"type": "row",
		"items": []widget{
			{"type": "label", "text": filepath.Base(repo.MainRoot), "style": "heading"},
			{"type": "button", "id": "refresh", "label": "Refresh"},
		},
	}}
	if message != "" {
		widgets = append(widgets, widget{"type": "label", "id": "message", "text": message, "style": "secondary"})
	}
	for _, worktree := range repo.Worktrees {
		if worktree.Main {
			continue
		}
		path := filepath.Clean(worktree.Path)
		handle := worktree.handle()
		state, exists := byWorktree[path]
		title := liveTitles[path]
		if title == "" {
			title = state.Title
		}
		status := "idle"
		if exists {
			status = strings.Join([]string{
				state.Status, formatEventTime(state.UpdatedAt, now), dashboardField(state.Agent),
			}, " · ")
		}
		items := []widget{
			{"type": "label", "text": handle, "tip": directoryPath(path)},
			{"type": "label", "text": status},
		}
		if title = dashboardField(title); title != "" {
			items = append(items, widget{"type": "label", "text": title, "style": "secondary"})
		}
		expandLabel := "Expand"
		if expanded[path] {
			expandLabel = "Collapse"
		}
		items = append(items,
			widget{"type": "button", "id": "space:" + handle, "label": "Space", "style": "primary"},
			widget{"type": "button", "id": "agent:" + handle, "label": "Agent", "disabled": !exists},
			widget{"type": "button", "id": "note:" + handle, "label": "Note"},
			widget{"type": "button", "id": "expand:" + handle, "label": expandLabel},
		)
		widgets = append(widgets, widget{"type": "row", "items": items})
		if !expanded[path] {
			continue
		}
		history, err := manifoldWorktreeHistory(worktree, state, store, now)
		if err != nil {
			return nil, err
		}
		widgets = append(widgets, widget{"type": "markdown", "id": "history:" + handle, "text": history})
	}

	recent, err := manifoldRecentSummaries(repo, store, now)
	if err != nil {
		return nil, err
	}
	if recent != "" {
		widgets = append(widgets,
			widget{"type": "divider"},
			widget{"type": "markdown", "id": "recent", "text": recent},
		)
	}
	return widgets, nil
}

func manifoldWorktreeHistory(worktree worktree, state agentState, store *stateStore, now time.Time) (string, error) {
	note, err := ensureNoteFile(store.home, worktree, now)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "`%s`\n", note)
	events, err := store.listSessionSummaries(worktree.Path, state.Agent, state.SessionID)
	if err != nil {
		return "", err
	}
	for _, event := range events {
		fmt.Fprintf(&output, "\n**%s** %s\n", formatEventTime(event.CreatedAt, now), dashboardEventStatus(event))
		if transcript := eventTranscript(event, store); transcript != "" {
			fmt.Fprintf(&output, "\n%s\n", transcript)
		}
	}
	return output.String(), nil
}

func manifoldRecentSummaries(repo repository, store *stateStore, now time.Time) (string, error) {
	handles := make(map[string]string, len(repo.Worktrees))
	for _, worktree := range repo.Worktrees {
		handles[filepath.Clean(worktree.Path)] = worktree.handle()
	}
	events, err := store.listRecentSummaries(
		repo.MainRoot,
		now.Add(-dashboardRecentEventAge).Unix(),
		dashboardRecentEventLimit,
	)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	for index, event := range events {
		if index > 0 {
			output.WriteString("\n---\n\n")
		}
		handle := handles[filepath.Clean(event.WorktreePath)]
		if handle == "" {
			handle = filepath.Base(event.WorktreePath)
		}
		fmt.Fprintf(
			&output,
			"**%s** · %s · %s · %s\n",
			handle,
			dashboardEventStatus(event),
			formatEventTime(event.CreatedAt, now),
			dashboardField(event.Agent),
		)
		if transcript := eventTranscript(event, store); transcript != "" {
			fmt.Fprintf(&output, "\n%s\n", transcript)
		}
	}
	return output.String(), nil
}

func eventTranscript(event agentEvent, store *stateStore) string {
	if event.Status != "done" {
		return ""
	}
	messages, _ := store.transcriptMessages(event)
	return transcriptForEvent(messages, event.CreatedAt)
}
