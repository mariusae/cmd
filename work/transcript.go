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
	"time"
)

type transcriptMessage struct {
	Timestamp time.Time
	Text      string
}

type cachedTranscript struct {
	fingerprint string
	messages    []transcriptMessage
}

func resolveTranscriptPath(agent, sessionID, worktreePath, hint string, store *stateStore) string {
	if hint != "" {
		if filepath.IsAbs(hint) {
			return filepath.Clean(hint)
		}
		return filepath.Clean(filepath.Join(worktreePath, hint))
	}
	if sessionID == "" {
		return ""
	}
	getenv := store.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	var patterns []string
	switch agent {
	case "claude":
		configDir := configuredDirectory(getenv("CLAUDE_CONFIG_DIR"), filepath.Join(store.home, ".claude"))
		project := encodeClaudeProjectPath(worktreePath)
		patterns = []string{filepath.Join(configDir, "projects", project, sessionID+".jsonl")}
	case "codex":
		configDir := configuredDirectory(getenv("CODEX_CONFIG_DIR"), filepath.Join(store.home, ".codex"))
		patterns = []string{filepath.Join(configDir, "sessions", "*", "*", "*", "*"+sessionID+"*.jsonl")}
	case "gemini":
		configDir := configuredDirectory(getenv("GEMINI_CONFIG_DIR"), filepath.Join(store.home, ".gemini"))
		patterns = []string{
			filepath.Join(configDir, "tmp", "*", "chats", "*"+sessionID+"*.json"),
			filepath.Join(configDir, "tmp", "*", "chats", "*"+sessionID+"*.jsonl"),
		}
	case "opencode":
		return openCodeDatabasePath(store.home, getenv)
	}
	return newestMatchingPath(patterns)
}

func encodeClaudeProjectPath(path string) string {
	return strings.Map(func(character rune) rune {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' {
			return character
		}
		return '-'
	}, path)
}

func newestMatchingPath(patterns []string) string {
	var matches []string
	for _, pattern := range patterns {
		found, err := filepath.Glob(pattern)
		if err == nil {
			matches = append(matches, found...)
		}
	}
	sort.SliceStable(matches, func(left, right int) bool {
		leftInfo, leftErr := os.Stat(matches[left])
		rightInfo, rightErr := os.Stat(matches[right])
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return leftInfo.ModTime().After(rightInfo.ModTime())
	})
	if len(matches) == 0 {
		return ""
	}
	return filepath.Clean(matches[0])
}

func openCodeDatabasePath(home string, getenv func(string) string) string {
	if configured := getenv("OPENCODE_DATA_DIR"); configured != "" {
		return filepath.Join(configured, "opencode.db")
	}
	base := getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "opencode", "opencode.db")
}

func (s *stateStore) transcriptMessages(event agentEvent) ([]transcriptMessage, error) {
	path := resolveTranscriptPath(event.Agent, event.SessionID, event.WorktreePath, event.TranscriptPath, s)
	key := event.Agent + "\x00" + event.SessionID + "\x00" + path
	fingerprint := transcriptFingerprint(path, event.Agent == "opencode")
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.transcriptCache[key]; ok && cached.fingerprint == fingerprint {
		return cached.messages, nil
	}
	messages, err := readTranscriptMessages(event, path, s)
	if err != nil {
		return nil, err
	}
	if s.transcriptCache == nil {
		s.transcriptCache = make(map[string]cachedTranscript)
	}
	s.transcriptCache[key] = cachedTranscript{fingerprint: fingerprint, messages: messages}
	return messages, nil
}

func transcriptFingerprint(path string, includeWAL bool) string {
	if path == "" {
		return ""
	}
	paths := []string{path}
	if includeWAL {
		paths = append(paths, path+"-wal")
	}
	var fingerprint strings.Builder
	for _, candidate := range paths {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		fmt.Fprintf(&fingerprint, "%s:%d:%d;", candidate, info.Size(), info.ModTime().UnixNano())
	}
	return fingerprint.String()
}

func readTranscriptMessages(event agentEvent, path string, store *stateStore) ([]transcriptMessage, error) {
	if path == "" {
		return nil, nil
	}
	if event.Agent == "opencode" {
		return readOpenCodeTranscript(path, event.SessionID, store.executable)
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	switch event.Agent {
	case "claude":
		return readClaudeTranscript(file)
	case "codex":
		return readCodexTranscript(file)
	case "gemini":
		return readGeminiTranscript(file)
	default:
		return nil, nil
	}
}

func readCodexTranscript(reader io.Reader) ([]transcriptMessage, error) {
	decoder := json.NewDecoder(reader)
	var messages []transcriptMessage
	for {
		var record struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if record.Type != "response_item" {
			continue
		}
		var payload struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(record.Payload, &payload) != nil || payload.Type != "message" || payload.Role != "assistant" {
			continue
		}
		appendTranscriptMessage(&messages, record.Timestamp, textContent(payload.Content))
	}
	return messages, nil
}

func readClaudeTranscript(reader io.Reader) ([]transcriptMessage, error) {
	decoder := json.NewDecoder(reader)
	var messages []transcriptMessage
	for {
		var record struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Message   struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if record.Type != "assistant" || record.Message.Role != "assistant" {
			continue
		}
		appendTranscriptMessage(&messages, record.Timestamp, textContent(record.Message.Content))
	}
	return messages, nil
}

func readGeminiTranscript(reader io.Reader) ([]transcriptMessage, error) {
	contents, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	var document any
	if err := json.Unmarshal(contents, &document); err != nil {
		return readGenericJSONLines(bytes.NewReader(contents))
	}
	var messages []transcriptMessage
	collectGenericAssistantMessages(document, &messages)
	sort.SliceStable(messages, func(left, right int) bool {
		return messages[left].Timestamp.Before(messages[right].Timestamp)
	})
	return messages, nil
}

func readGenericJSONLines(reader io.Reader) ([]transcriptMessage, error) {
	decoder := json.NewDecoder(reader)
	var messages []transcriptMessage
	for {
		var value any
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		collectGenericAssistantMessages(value, &messages)
	}
	return messages, nil
}

func collectGenericAssistantMessages(value any, messages *[]transcriptMessage) {
	object, ok := value.(map[string]any)
	if !ok {
		if array, ok := value.([]any); ok {
			for _, child := range array {
				collectGenericAssistantMessages(child, messages)
			}
		}
		return
	}
	role, _ := object["role"].(string)
	typeName, _ := object["type"].(string)
	if role == "assistant" || typeName == "gemini" || typeName == "assistant" {
		content, _ := json.Marshal(object["content"])
		text := textContent(content)
		if text == "" {
			if raw, ok := object["text"].(string); ok {
				text = raw
			}
		}
		timestamp, _ := object["timestamp"].(string)
		appendTranscriptMessage(messages, timestamp, text)
	}
	for key, child := range object {
		if key == "content" {
			continue
		}
		collectGenericAssistantMessages(child, messages)
	}
}

func textContent(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var selected []string
		for _, part := range parts {
			if part.Text != "" && (part.Type == "" || part.Type == "text" || part.Type == "output_text") {
				selected = append(selected, part.Text)
			}
		}
		return strings.TrimSpace(strings.Join(selected, "\n"))
	}
	var object struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return strings.TrimSpace(object.Text)
	}
	return ""
}

func appendTranscriptMessage(messages *[]transcriptMessage, timestamp, text string) {
	text = cleanTranscriptText(text)
	if text == "" {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return
	}
	*messages = append(*messages, transcriptMessage{Timestamp: at, Text: text})
}

func cleanTranscriptText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(text)
}

func readOpenCodeTranscript(path, sessionID, sqliteExecutable string) ([]transcriptMessage, error) {
	if sessionID == "" {
		return nil, nil
	}
	query := fmt.Sprintf(`
SELECT m.id AS message_id, m.time_created, m.data AS message_data, p.data AS part_data
FROM message AS m
JOIN part AS p ON p.message_id = m.id
WHERE m.session_id = %s
ORDER BY m.time_created, m.id, p.id;
`, sqlString(sessionID))
	output, err := executeSQLiteFile(sqliteExecutable, path, query)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type row struct {
		MessageID   string `json:"message_id"`
		TimeCreated int64  `json:"time_created"`
		MessageData string `json:"message_data"`
		PartData    string `json:"part_data"`
	}
	var rows []row
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, err
	}
	var messages []transcriptMessage
	lastMessageID := ""
	for _, row := range rows {
		var messageData struct {
			Role string `json:"role"`
		}
		var partData struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(row.MessageData), &messageData) != nil || messageData.Role != "assistant" {
			continue
		}
		if json.Unmarshal([]byte(row.PartData), &partData) != nil || partData.Type != "text" || partData.Text == "" {
			continue
		}
		at := time.UnixMilli(row.TimeCreated)
		text := cleanTranscriptText(partData.Text)
		if len(messages) > 0 && row.MessageID == lastMessageID {
			messages[len(messages)-1].Text += "\n" + text
		} else {
			messages = append(messages, transcriptMessage{Timestamp: at, Text: text})
			lastMessageID = row.MessageID
		}
	}
	return messages, nil
}

func openCodeSessionTitle(path, sessionID, sqliteExecutable string) string {
	if sessionID == "" {
		return ""
	}
	query := fmt.Sprintf("SELECT title FROM session WHERE id = %s LIMIT 1;\n", sqlString(sessionID))
	output, err := executeSQLiteFile(sqliteExecutable, path, query)
	if err != nil {
		return ""
	}
	var rows []struct {
		Title string `json:"title"`
	}
	if json.Unmarshal(output, &rows) != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Title
}

func executeSQLiteFile(executable, path, query string) ([]byte, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, "-json", path)
	cmd.Stdin = strings.NewReader(query)
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

func transcriptForEvent(messages []transcriptMessage, createdAt int64) string {
	cutoff := time.Unix(createdAt, 0).Add(time.Second)
	text := ""
	for _, message := range messages {
		if message.Timestamp.After(cutoff) {
			break
		}
		text = message.Text
	}
	return text
}
