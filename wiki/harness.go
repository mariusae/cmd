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
)

// A harness runs a coding agent over a directory, read-only, and returns its
// answer as JSON conforming to schema. Going through the agents' own command
// lines leaves their authentication, models and billing to them.
type harness interface {
	// ask runs prompt in dir and decodes the answer into out. It returns what
	// the run cost in dollars, when the agent says.
	ask(ctx context.Context, dir, prompt string, schema json.RawMessage, out any) (float64, error)
}

// newHarness makes the harness of the given name. commands are commands the
// agent may run besides, as "git show", which must be read-only.
func newHarness(name, model string, commands []string) (harness, error) {
	switch name {
	case "claude":
		return claudeHarness{model: model, commands: commands}, nil
	case "codex":
		return codexHarness{model: model}, nil
	}
	return nil, fmt.Errorf("unknown harness %q: use claude or codex", name)
}

type claudeHarness struct {
	model    string
	commands []string
}

// claudeTools are all the agent is given: enough to find and read source, and
// nothing that writes or runs anything.
const claudeTools = "Read,Glob,Grep"

func (h claudeHarness) args(schema json.RawMessage) []string {
	tools, allowed := claudeTools, claudeTools
	if len(h.commands) > 0 {
		// Bash is given, but only these commands are allowed to run; in print
		// mode any other is refused.
		tools += ",Bash"
		for _, c := range h.commands {
			allowed += ",Bash(" + c + ":*)"
		}
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", string(schema),
		"--tools", tools,
		"--allowedTools", allowed,
		"--no-session-persistence",
		// The user's hooks are for their own sessions, not these.
		"--settings", `{"disableAllHooks":true}`,
	}
	if h.model != "" {
		args = append(args, "--model", h.model)
	}
	return args
}

func (h claudeHarness) ask(ctx context.Context, dir, prompt string, schema json.RawMessage, out any) (float64, error) {
	cmd := exec.CommandContext(ctx, "claude", h.args(schema)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	return decodeClaude(stdout.Bytes(), stderr.String(), runErr, out)
}

// decodeClaude reads the result of claude -p --output-format json.
func decodeClaude(stdout []byte, stderr string, runErr error, out any) (float64, error) {
	var result struct {
		IsError          bool            `json:"is_error"`
		Subtype          string          `json:"subtype"`
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Cost             float64         `json:"total_cost_usd"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		if runErr != nil {
			return 0, fmt.Errorf("claude: %v: %s", runErr, firstLine(stderr))
		}
		return 0, fmt.Errorf("claude: unreadable output: %v", err)
	}
	if result.IsError || len(result.StructuredOutput) == 0 || string(result.StructuredOutput) == "null" {
		why := firstLine(result.Result)
		if why == "" {
			why = result.Subtype
		}
		return result.Cost, fmt.Errorf("claude: no answer: %s", why)
	}
	if err := json.Unmarshal(result.StructuredOutput, out); err != nil {
		return result.Cost, fmt.Errorf("claude: answer does not fit: %v", err)
	}
	return result.Cost, nil
}

// codexHarness runs Codex in its read-only sandbox, where it may run any
// command that only reads.
type codexHarness struct {
	model string
}

func (h codexHarness) ask(ctx context.Context, dir, prompt string, schema json.RawMessage, out any) (float64, error) {
	tmp, err := os.MkdirTemp("", "wiki-codex-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	schemaPath := filepath.Join(tmp, "schema.json")
	answerPath := filepath.Join(tmp, "answer.json")
	if err := os.WriteFile(schemaPath, schema, 0o644); err != nil {
		return 0, err
	}
	args := []string{
		"exec",
		"--sandbox", "read-only",
		"--cd", dir,
		"--skip-git-repo-check",
		"--ephemeral",
		"--output-schema", schemaPath,
		"--output-last-message", answerPath,
	}
	if h.model != "" {
		args = append(args, "--model", h.model)
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	answer, err := os.ReadFile(answerPath)
	if runErr != nil || err != nil || len(bytes.TrimSpace(answer)) == 0 {
		if runErr == nil {
			runErr = errors.New("no answer")
		}
		return 0, fmt.Errorf("codex: %v: %s", runErr, lastLine(stderr.String()))
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return 0, fmt.Errorf("codex: answer does not fit: %v", err)
	}
	return 0, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
