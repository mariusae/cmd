package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const stateSchema = `
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
CREATE TABLE IF NOT EXISTS agent_status (
  worktree_path TEXT PRIMARY KEY,
  repository_root TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('working', 'waiting', 'done')),
  agent TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  transcript_path TEXT NOT NULL DEFAULT '',
  apex_socket TEXT NOT NULL DEFAULT '',
  apex_session TEXT NOT NULL DEFAULT '',
  apex_window TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS agent_status_repository
  ON agent_status (repository_root, updated_at DESC);
CREATE TABLE IF NOT EXISTS agent_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  worktree_path TEXT NOT NULL,
  repository_root TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('working', 'waiting', 'done')),
  agent TEXT NOT NULL,
  session_id TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  transcript_path TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS agent_events_worktree
  ON agent_events (worktree_path, created_at DESC, id DESC);
CREATE TABLE IF NOT EXISTS dashboard_expansion (
  worktree_path TEXT PRIMARY KEY
);
`

const stateBootstrap = `
INSERT INTO agent_events (
  worktree_path, repository_root, status, agent, session_id, transcript_path, created_at
)
SELECT s.worktree_path, s.repository_root, s.status, s.agent,
       s.session_id, s.transcript_path, s.updated_at
FROM agent_status AS s
WHERE NOT EXISTS (
  SELECT 1 FROM agent_events AS e WHERE e.worktree_path = s.worktree_path
);
`

type agentState struct {
	WorktreePath   string `json:"worktree_path"`
	RepositoryRoot string `json:"repository_root"`
	Status         string `json:"status"`
	Agent          string `json:"agent"`
	SessionID      string `json:"session_id"`
	Title          string `json:"title"`
	TranscriptPath string `json:"transcript_path"`
	ApexSocket     string `json:"apex_socket"`
	ApexSession    string `json:"apex_session"`
	ApexWindow     string `json:"apex_window"`
	UpdatedAt      int64  `json:"updated_at"`
}

type agentEvent struct {
	ID             int64  `json:"id"`
	WorktreePath   string `json:"worktree_path"`
	RepositoryRoot string `json:"repository_root"`
	Status         string `json:"status"`
	Agent          string `json:"agent"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	StartedAt      int64  `json:"started_at"`
	CreatedAt      int64  `json:"created_at"`
}

type stateStore struct {
	path            string
	executable      string
	home            string
	getenv          func(string) string
	mu              sync.Mutex
	initialized     bool
	transcriptCache map[string]cachedTranscript
}

func stateDatabasePath(home string, getenv func(string) string) string {
	base := ""
	if getenv != nil {
		base = getenv("XDG_STATE_HOME")
	}
	if base == "" {
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "work", "work.db")
}

func newStateStore(home string, getenv func(string) string) (*stateStore, error) {
	executable, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("sqlite3 is required for agent status tracking")
	}
	return &stateStore{
		path:       stateDatabasePath(home, getenv),
		executable: executable,
		home:       home,
		getenv:     getenv,
	}, nil
}

func (s *stateStore) initialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	if _, err := s.execute(stateSchema); err != nil {
		return fmt.Errorf("initializing agent state database: %w", err)
	}
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{"agent_status", "title", "TEXT NOT NULL DEFAULT ''"},
		{"agent_status", "transcript_path", "TEXT NOT NULL DEFAULT ''"},
		{"agent_status", "apex_socket", "TEXT NOT NULL DEFAULT ''"},
		{"agent_status", "apex_session", "TEXT NOT NULL DEFAULT ''"},
		{"agent_status", "apex_window", "TEXT NOT NULL DEFAULT ''"},
		{"agent_events", "transcript_path", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := s.execute(stateBootstrap); err != nil {
		return fmt.Errorf("migrating agent state database: %w", err)
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("securing agent state database: %w", err)
	}
	s.initialized = true
	return nil
}

func (s *stateStore) update(state agentState) error {
	if err := validateAgentStatus(state.Status); err != nil {
		return err
	}
	if err := s.initialize(); err != nil {
		return err
	}
	existing, found, err := s.get(state.WorktreePath)
	if err != nil {
		return err
	}
	if found {
		sameStatus := state.Status == existing.Status && state.Agent == existing.Agent &&
			(state.SessionID == "" || state.SessionID == existing.SessionID)
		if state.SessionID == "" {
			state.SessionID = existing.SessionID
		}
		if state.Title == "" {
			state.Title = existing.Title
		}
		if state.TranscriptPath == "" {
			state.TranscriptPath = existing.TranscriptPath
		}
		if sameStatus {
			state.UpdatedAt = existing.UpdatedAt
		}
	}

	sql := fmt.Sprintf(`
BEGIN IMMEDIATE;
UPDATE agent_events
SET transcript_path = %s
WHERE worktree_path = %s
  AND session_id = %s
  AND transcript_path = ''
  AND %s != '';
INSERT INTO agent_events (
  worktree_path, repository_root, status, agent, session_id, transcript_path, created_at
)
SELECT %s, %s, %s, %s, %s, %s, %d
WHERE NOT EXISTS (
  SELECT 1 FROM agent_status
  WHERE worktree_path = %s
    AND status = %s
    AND agent = %s
    AND session_id = %s
);
INSERT INTO agent_status (
  worktree_path, repository_root, status, agent, session_id, title, transcript_path,
  apex_socket, apex_session, apex_window, updated_at
) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %d)
ON CONFLICT(worktree_path) DO UPDATE SET
  repository_root = excluded.repository_root,
  status = excluded.status,
  agent = excluded.agent,
  session_id = excluded.session_id,
  title = excluded.title,
  transcript_path = excluded.transcript_path,
  apex_socket = excluded.apex_socket,
  apex_session = excluded.apex_session,
  apex_window = excluded.apex_window,
  updated_at = excluded.updated_at;

COMMIT;
`, sqlString(state.TranscriptPath), sqlString(state.WorktreePath),
		sqlString(state.SessionID), sqlString(state.TranscriptPath),
		sqlString(state.WorktreePath), sqlString(state.RepositoryRoot),
		sqlString(state.Status), sqlString(state.Agent), sqlString(state.SessionID),
		sqlString(state.TranscriptPath), state.UpdatedAt,
		sqlString(state.WorktreePath), sqlString(state.Status), sqlString(state.Agent),
		sqlString(state.SessionID),
		sqlString(state.WorktreePath), sqlString(state.RepositoryRoot),
		sqlString(state.Status), sqlString(state.Agent), sqlString(state.SessionID),
		sqlString(state.Title), sqlString(state.TranscriptPath),
		sqlString(state.ApexSocket), sqlString(state.ApexSession), sqlString(state.ApexWindow),
		state.UpdatedAt)
	_, err = s.execute(sql)
	if err != nil {
		return fmt.Errorf("updating agent status: %w", err)
	}
	return nil
}

func (s *stateStore) get(worktreePath string) (agentState, bool, error) {
	query := fmt.Sprintf(`
SELECT worktree_path, repository_root, status, agent, session_id, title, transcript_path,
       apex_socket, apex_session, apex_window, updated_at
FROM agent_status WHERE worktree_path = %s LIMIT 1;
`, sqlString(filepath.Clean(worktreePath)))
	output, err := s.executeWithArgs([]string{"-json"}, query)
	if err != nil {
		return agentState{}, false, fmt.Errorf("reading agent status: %w", err)
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return agentState{}, false, nil
	}
	var states []agentState
	if err := json.Unmarshal(output, &states); err != nil {
		return agentState{}, false, fmt.Errorf("parsing agent status database output: %w", err)
	}
	if len(states) == 0 {
		return agentState{}, false, nil
	}
	return states[0], true, nil
}

func (s *stateStore) listEvents(worktreePath string, limit int) ([]agentEvent, error) {
	if err := s.initialize(); err != nil {
		return nil, err
	}
	if limit < 1 {
		return nil, nil
	}
	if limit > 100 {
		limit = 100
	}
	query := fmt.Sprintf(`
SELECT e.id, e.worktree_path, e.repository_root, e.status, e.agent, e.session_id,
       e.transcript_path,
       CASE WHEN e.status = 'done' THEN COALESCE((
         SELECT MIN(w.created_at)
         FROM agent_events AS w
         WHERE w.worktree_path = e.worktree_path
           AND w.agent = e.agent
           AND w.session_id = e.session_id
           AND w.status = 'working'
           AND w.id < e.id
           AND w.id > COALESCE((
             SELECT MAX(d.id)
             FROM agent_events AS d
             WHERE d.worktree_path = e.worktree_path
               AND d.agent = e.agent
               AND d.session_id = e.session_id
               AND d.status = 'done'
               AND d.id < e.id
           ), 0)
       ), 0) ELSE 0 END AS started_at,
       e.created_at
FROM agent_events AS e
WHERE e.worktree_path = %s
ORDER BY created_at DESC, id DESC
LIMIT %d;
`, sqlString(filepath.Clean(worktreePath)), limit)
	output, err := s.executeWithArgs([]string{"-json"}, query)
	if err != nil {
		return nil, fmt.Errorf("reading agent event history: %w", err)
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, nil
	}
	var events []agentEvent
	if err := json.Unmarshal(output, &events); err != nil {
		return nil, fmt.Errorf("parsing agent event history: %w", err)
	}
	for left, right := 0, len(events)-1; left < right; left, right = left+1, right-1 {
		events[left], events[right] = events[right], events[left]
	}
	return events, nil
}

func (s *stateStore) expandedWorktrees() (map[string]bool, error) {
	if err := s.initialize(); err != nil {
		return nil, err
	}
	output, err := s.executeWithArgs([]string{"-json"}, `
SELECT worktree_path FROM dashboard_expansion ORDER BY worktree_path;
`)
	if err != nil {
		return nil, fmt.Errorf("reading dashboard expansion state: %w", err)
	}
	type row struct {
		WorktreePath string `json:"worktree_path"`
	}
	var rows []row
	if len(bytes.TrimSpace(output)) != 0 {
		if err := json.Unmarshal(output, &rows); err != nil {
			return nil, fmt.Errorf("parsing dashboard expansion state: %w", err)
		}
	}
	result := make(map[string]bool, len(rows))
	for _, row := range rows {
		result[filepath.Clean(row.WorktreePath)] = true
	}
	return result, nil
}

func (s *stateStore) toggleExpanded(worktreePath string) error {
	if err := s.initialize(); err != nil {
		return err
	}
	path := sqlString(filepath.Clean(worktreePath))
	query := fmt.Sprintf(`
BEGIN IMMEDIATE;
DELETE FROM dashboard_expansion WHERE worktree_path = %s;
INSERT INTO dashboard_expansion (worktree_path)
SELECT %s WHERE changes() = 0;
COMMIT;
`, path, path)
	if _, err := s.execute(query); err != nil {
		return fmt.Errorf("updating dashboard expansion state: %w", err)
	}
	return nil
}

func (s *stateStore) list(repositoryRoot string) ([]agentState, error) {
	if err := s.initialize(); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
SELECT worktree_path, repository_root, status, agent, session_id, title, transcript_path,
       apex_socket, apex_session, apex_window, updated_at
FROM agent_status
WHERE repository_root = %s
ORDER BY updated_at DESC;
`, sqlString(repositoryRoot))
	output, err := s.executeWithArgs([]string{"-json"}, query)
	if err != nil {
		return nil, fmt.Errorf("reading agent status: %w", err)
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, nil
	}
	var states []agentState
	if err := json.Unmarshal(output, &states); err != nil {
		return nil, fmt.Errorf("parsing agent status database output: %w", err)
	}
	return states, nil
}

func (s *stateStore) ensureColumn(table, name, definition string) error {
	type columnInfo struct {
		Name string `json:"name"`
	}
	output, err := s.executeWithArgs([]string{"-json"}, "PRAGMA table_info("+table+");\n")
	if err != nil {
		return fmt.Errorf("inspecting %s schema: %w", table, err)
	}
	var columns []columnInfo
	if len(bytes.TrimSpace(output)) != 0 {
		if err := json.Unmarshal(output, &columns); err != nil {
			return fmt.Errorf("parsing %s schema: %w", table, err)
		}
	}
	for _, column := range columns {
		if column.Name == name {
			return nil
		}
	}
	if _, err := s.execute(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s;\n", table, name, definition)); err != nil {
		return fmt.Errorf("adding %s.%s: %w", table, name, err)
	}
	return nil
}

func (s *stateStore) delete(worktreePath string) error {
	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("checking agent state database: %w", err)
	}
	if err := s.initialize(); err != nil {
		return err
	}
	path := sqlString(filepath.Clean(worktreePath))
	query := fmt.Sprintf(`
BEGIN IMMEDIATE;
DELETE FROM agent_status WHERE worktree_path = %s;
DELETE FROM agent_events WHERE worktree_path = %s;
DELETE FROM dashboard_expansion WHERE worktree_path = %s;
COMMIT;
`, path, path, path)
	if _, err := s.execute(query); err != nil {
		return fmt.Errorf("deleting agent status: %w", err)
	}
	return nil
}

func (s *stateStore) execute(sql string) ([]byte, error) {
	return s.executeWithArgs(nil, sql)
}

func (s *stateStore) executeWithArgs(args []string, sql string) ([]byte, error) {
	commandArgs := []string{"-cmd", ".timeout 5000"}
	commandArgs = append(commandArgs, args...)
	commandArgs = append(commandArgs, s.path)
	cmd := exec.Command(s.executable, commandArgs...)
	cmd.Stdin = strings.NewReader(sql)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, errors.New(detail)
	}
	return stdout.Bytes(), nil
}

func sqlString(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func validateAgentStatus(status string) error {
	switch status {
	case "working", "waiting", "done":
		return nil
	default:
		return fmt.Errorf("invalid agent status %q (expected working, waiting, or done)", status)
	}
}

func statesByWorktree(states []agentState) map[string]agentState {
	result := make(map[string]agentState, len(states))
	for _, state := range states {
		result[filepath.Clean(state.WorktreePath)] = state
	}
	return result
}

func formatEventTime(timestamp int64, now time.Time) string {
	created := time.Unix(timestamp, 0).In(now.Location())
	layout := time.Kitchen
	switch duration := now.Sub(created); {
	case duration > 7*24*time.Hour:
		layout = "2Jan06"
	case duration > 24*time.Hour:
		layout = "Mon3:04PM"
	}
	return created.Format(layout)
}

func formatOperationDuration(startedAt, completedAt int64) string {
	duration := time.Duration(completedAt-startedAt) * time.Second
	if duration < 0 {
		duration = 0
	}
	days := int(duration / (24 * time.Hour))
	duration %= 24 * time.Hour
	hours := int(duration / time.Hour)
	duration %= time.Hour
	minutes := int(duration / time.Minute)
	duration %= time.Minute
	seconds := int(duration / time.Second)

	switch {
	case days > 0:
		if hours > 0 {
			return fmt.Sprintf("%dd%dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		if minutes > 0 {
			return fmt.Sprintf("%dh%dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	case minutes > 0:
		if seconds > 0 {
			return fmt.Sprintf("%dm%ds", minutes, seconds)
		}
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

type hookPayload struct {
	CWD            string
	SessionID      string
	Title          string
	TranscriptPath string
}

func parseHookPayload(input []byte) hookPayload {
	if len(bytes.TrimSpace(input)) == 0 {
		return hookPayload{}
	}
	var value any
	if json.Unmarshal(input, &value) != nil {
		return hookPayload{}
	}
	return hookPayload{
		CWD: firstJSONText(value, []string{
			"cwd", "workdir", "working_directory", "workingDirectory",
		}),
		SessionID: firstJSONText(value, []string{
			"session_id", "sessionId", "conversation_id", "thread_id",
		}),
		Title: firstJSONText(value, []string{
			"title", "session_title", "sessionTitle", "conversation_title", "conversationTitle",
		}),
		TranscriptPath: firstJSONText(value, []string{
			"transcript_path", "transcriptPath", "session_path", "sessionPath",
		}),
	}
}

func firstJSONText(value any, keys []string) string {
	keyRank := make(map[string]int, len(keys))
	for index, key := range keys {
		keyRank[key] = index
	}
	type candidate struct {
		rank  int
		value string
	}
	var candidates []candidate
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if rank, ok := keyRank[key]; ok {
					if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
						candidates = append(candidates, candidate{rank: rank, value: text})
					}
				}
				visit(child)
			}
		case []any:
			for _, child := range typed {
				visit(child)
			}
		}
	}
	visit(value)
	if len(candidates) == 0 {
		return ""
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].rank < candidates[j].rank })
	return candidates[0].value
}

func readHookInput(reader io.Reader) ([]byte, error) {
	if file, ok := reader.(*os.File); ok {
		info, err := file.Stat()
		if err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return nil, nil
		}
	}
	input, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading hook input: %w", err)
	}
	return input, nil
}
