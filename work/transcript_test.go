package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadCodexTranscript(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"timestamp":"2026-09-09T12:00:00Z","type":"session_meta","payload":{}}`,
		`{"timestamp":"2026-09-09T12:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"First"}]}}`,
		`{"timestamp":"2026-09-09T12:02:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Ignore"}]}}`,
		`{"timestamp":"2026-09-09T12:03:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Final\n\n- done"}]}}`,
	}, "\n"))
	messages, err := readCodexTranscript(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Text != "Final\n\n- done" {
		t.Fatalf("messages = %#v", messages)
	}
	if got := transcriptForEvent(messages, time.Date(2026, 9, 9, 12, 3, 0, 0, time.UTC).Unix()); got != "Final\n\n- done" {
		t.Fatalf("transcriptForEvent = %q", got)
	}
}

func TestReadClaudeTranscript(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`{"timestamp":"2026-09-09T12:00:00Z","type":"user","message":{"role":"user","content":"Question"}}`,
		`{"timestamp":"2026-09-09T12:00:10Z","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Answer"},{"type":"tool_use"}]}}`,
	}, "\n"))
	messages, err := readClaudeTranscript(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Text != "Answer" {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestEncodeClaudeProjectPath(t *testing.T) {
	if got, want := encodeClaudeProjectPath("/home/user/.config/my_app"), "-home-user--config-my-app"; got != want {
		t.Fatalf("encoded path = %q, want %q", got, want)
	}
}

func TestResolveCodexTranscriptPath(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "codex")
	path := filepath.Join(configDir, "sessions", "2026", "09", "09", "rollout-session-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "CODEX_CONFIG_DIR" {
			return configDir
		}
		return ""
	}
	store, err := newStateStore(home, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolveTranscriptPath("codex", "session-1", "/repo", "", store); got != path {
		t.Fatalf("transcript path = %q, want %q", got, path)
	}
}
