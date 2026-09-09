package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type hookGroup map[string]any
type hookSet map[string][]hookGroup

func statusCommand(status, agent string) string {
	return fmt.Sprintf("work agent-status %s %s >/dev/null 2>&1 || true", status, agent)
}

func commandHook(status, agent string) map[string]string {
	return map[string]string{
		"type":    "command",
		"command": statusCommand(status, agent),
	}
}

func plainHook(status, agent string) hookGroup {
	return hookGroup{"hooks": []any{commandHook(status, agent)}}
}

func matchedHook(matcher, status, agent string) hookGroup {
	return hookGroup{
		"matcher": matcher,
		"hooks":   []any{commandHook(status, agent)},
	}
}

func claudeHooks() hookSet {
	return hookSet{
		"UserPromptSubmit": {plainHook("working", "claude")},
		"Notification":     {matchedHook("permission_prompt|elicitation_dialog", "waiting", "claude")},
		"PostToolUse":      {plainHook("working", "claude")},
		"Stop":             {plainHook("done", "claude")},
	}
}

func codexHooks() hookSet {
	return hookSet{
		"UserPromptSubmit":  {plainHook("working", "codex")},
		"PermissionRequest": {plainHook("waiting", "codex")},
		"PostToolUse":       {plainHook("working", "codex")},
		"SubagentStart":     {plainHook("working", "codex")},
		"SubagentStop":      {plainHook("working", "codex")},
		"Stop":              {plainHook("done", "codex")},
	}
}

func geminiHooks() hookSet {
	return hookSet{
		"BeforeAgent":  {plainHook("working", "gemini")},
		"Notification": {matchedHook("ToolPermission", "waiting", "gemini")},
		"AfterTool":    {plainHook("working", "gemini")},
		"AfterAgent":   {plainHook("done", "gemini")},
		"SessionEnd":   {plainHook("done", "gemini")},
	}
}

const openCodePlugin = `import type { Plugin } from '@opencode-ai/plugin';

export const WorkStatusPlugin: Plugin = async ({ $ }) => {
  const lastStatusBySession = new Map<string, string>();
  const acceptBusyBySession = new Map<string, boolean>();

  async function setStatus(sessionID: string | undefined, status: string) {
    if (!sessionID) {
      return;
    }
    if (status === 'working' && acceptBusyBySession.get(sessionID) === false) {
      return;
    }
    if (lastStatusBySession.get(sessionID) === status) {
      return;
    }
    lastStatusBySession.set(sessionID, status);
    acceptBusyBySession.set(sessionID, status !== 'done');
    await $` + "`work agent-status ${status} opencode ${sessionID}`" + `.quiet();
  }

  return {
    event: async ({ event }) => {
      if (event.type === 'message.updated' && event.properties.info.role === 'user') {
        acceptBusyBySession.set(event.properties.sessionID, true);
      }
      switch (event.type) {
        case 'session.status':
          if (event.properties.status.type === 'busy') {
            await setStatus(event.properties.sessionID, 'working');
          } else if (event.properties.status.type === 'idle') {
            await setStatus(event.properties.sessionID, 'done');
          }
          break;
        case 'permission.asked':
        case 'question.asked':
          await setStatus(event.properties.sessionID, 'waiting');
          break;
        case 'permission.replied':
        case 'question.replied':
          await setStatus(event.properties.sessionID, 'working');
          break;
        case 'session.idle':
          await setStatus(event.properties.sessionID, 'done');
          break;
      }
    },
  };
};
`

type agentInstaller struct {
	name       string
	executable string
	configDir  string
	install    func() (string, error)
}

func installAgentHooks(home string, getenv func(string) string) ([]string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	claudeDir := configuredDirectory(getenv("CLAUDE_CONFIG_DIR"), filepath.Join(home, ".claude"))
	codexDir := configuredDirectory(getenv("CODEX_CONFIG_DIR"), filepath.Join(home, ".codex"))
	geminiDir := configuredDirectory(getenv("GEMINI_CONFIG_DIR"), filepath.Join(home, ".gemini"))
	openCodeDir := configuredDirectory(getenv("OPENCODE_CONFIG"), filepath.Join(configHome(home, getenv), "opencode"))

	installers := []agentInstaller{
		{
			name:       "Claude Code",
			executable: "claude",
			configDir:  claudeDir,
			install: func() (string, error) {
				path := filepath.Join(claudeDir, "settings.json")
				return path, mergeJSONHooks(path, claudeHooks())
			},
		},
		{
			name:       "Codex",
			executable: "codex",
			configDir:  codexDir,
			install: func() (string, error) {
				hooksPath := filepath.Join(codexDir, "hooks.json")
				if err := mergeJSONHooks(hooksPath, codexHooks()); err != nil {
					return "", err
				}
				if err := enableCodexHooks(filepath.Join(codexDir, "config.toml")); err != nil {
					return "", err
				}
				return hooksPath, nil
			},
		},
		{
			name:       "Gemini CLI",
			executable: "gemini",
			configDir:  geminiDir,
			install: func() (string, error) {
				path := filepath.Join(geminiDir, "settings.json")
				return path, mergeJSONHooks(path, geminiHooks())
			},
		},
		{
			name:       "OpenCode",
			executable: "opencode",
			configDir:  openCodeDir,
			install: func() (string, error) {
				return installOpenCodeHook(openCodeDir)
			},
		},
	}

	var installed []string
	for _, installer := range installers {
		if !directoryExists(installer.configDir) {
			if _, err := exec.LookPath(installer.executable); err != nil {
				continue
			}
		}
		path, err := installer.install()
		if err != nil {
			return installed, fmt.Errorf("installing %s hooks: %w", installer.name, err)
		}
		installed = append(installed, fmt.Sprintf("%s: %s", installer.name, path))
	}
	if len(installed) == 0 {
		return nil, fmt.Errorf("no supported agents found (Claude Code, Codex, Gemini CLI, or OpenCode)")
	}
	return installed, nil
}

func configuredDirectory(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

func configHome(home string, getenv func(string) string) string {
	if configured := getenv("XDG_CONFIG_HOME"); configured != "" {
		return configured
	}
	return filepath.Join(home, ".config")
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func mergeJSONHooks(path string, additions hookSet) error {
	root := map[string]any{}
	if contents, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(contents, &root); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		if _, exists := root["hooks"]; exists {
			return fmt.Errorf("parsing %s: hooks must be an object", path)
		}
		hooks = make(map[string]any)
		root["hooks"] = hooks
	}

	for event, groups := range additions {
		existing, ok := hooks[event].([]any)
		if !ok {
			if _, exists := hooks[event]; exists {
				return fmt.Errorf("parsing %s: hooks.%s must be an array", path, event)
			}
			existing = nil
		}
		for _, group := range groups {
			command := hookGroupCommand(group)
			if command == "" || hookCommandExists(existing, command) {
				continue
			}
			existing = append(existing, map[string]any(group))
		}
		hooks[event] = existing
	}
	return writeJSON(path, root)
}

func hookGroupCommand(group hookGroup) string {
	hooks, _ := group["hooks"].([]any)
	if len(hooks) == 0 {
		return ""
	}
	hook, _ := hooks[0].(map[string]string)
	return hook["command"]
}

func hookCommandExists(groups []any, command string) bool {
	for _, value := range groups {
		group, _ := value.(map[string]any)
		hooks, _ := group["hooks"].([]any)
		for _, hookValue := range hooks {
			hook, _ := hookValue.(map[string]any)
			if hook["command"] == command {
				return true
			}
		}
	}
	return false
}

func writeJSON(path string, value any) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(contents, '\n'), 0o600)
}

func writeFile(path string, contents []byte, defaultMode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	mode := defaultMode
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".work-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func enableCodexHooks(path string) error {
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	updated := setTOMLFeature(string(contents), "hooks", true)
	if updated == string(contents) {
		return nil
	}
	if err := writeFile(path, []byte(updated), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func setTOMLFeature(contents, key string, enabled bool) string {
	lines := strings.Split(contents, "\n")
	section := ""
	featuresLine := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
			if section == "features" {
				featuresLine = index
			}
			continue
		}
		if section != "features" {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			value := "false"
			if enabled {
				value = "true"
			}
			lines[index] = key + " = " + value
			return strings.Join(lines, "\n")
		}
	}

	setting := key + " = true"
	if !enabled {
		setting = key + " = false"
	}
	if featuresLine >= 0 {
		lines = append(lines[:featuresLine+1], append([]string{setting}, lines[featuresLine+1:]...)...)
		return strings.Join(lines, "\n")
	}
	contents = strings.TrimRight(contents, "\n")
	if contents != "" {
		contents += "\n\n"
	}
	return contents + "[features]\n" + setting + "\n"
}

func installOpenCodeHook(configDir string) (string, error) {
	pluginPath := filepath.Join(configDir, "plugins", "work-status.ts")
	packagePath := filepath.Join(configDir, "package.json")
	root := map[string]any{}
	if contents, err := os.ReadFile(packagePath); err == nil {
		if err := json.Unmarshal(contents, &root); err != nil {
			return "", fmt.Errorf("parsing %s: %w", packagePath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	root["private"] = true
	dependencies, ok := root["dependencies"].(map[string]any)
	if !ok {
		if _, exists := root["dependencies"]; exists {
			return "", fmt.Errorf("parsing %s: dependencies must be an object", packagePath)
		}
		dependencies = make(map[string]any)
		root["dependencies"] = dependencies
	}
	if _, exists := dependencies["@opencode-ai/plugin"]; !exists {
		dependencies["@opencode-ai/plugin"] = "1.4.3"
	}
	if err := writeJSON(packagePath, root); err != nil {
		return "", err
	}
	if err := writeFile(pluginPath, []byte(openCodePlugin), 0o644); err != nil {
		return "", fmt.Errorf("writing OpenCode plugin: %w", err)
	}
	return pluginPath, nil
}
