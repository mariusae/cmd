package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeJSONHooksPreservesExistingHooksAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	existing := `{
  "theme": "dark",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "notify-send done"}]}]
  }
}`
	if err := os.WriteFile(path, []byte(existing), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := mergeJSONHooks(path, claudeHooks()); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := mergeJSONHooks(path, claudeHooks()); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("second hook installation changed the file")
	}

	var root map[string]any
	if err := json.Unmarshal(second, &root); err != nil {
		t.Fatal(err)
	}
	if root["theme"] != "dark" {
		t.Fatalf("existing setting was not preserved: %#v", root)
	}
	stop := root["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("Stop hooks = %#v", stop)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
	}
}

func TestInstallAgentHooks(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{".claude", ".codex", ".gemini", filepath.Join(".config", "opencode")} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"test\"\n\n[features]\nhooks = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	getenv := func(string) string { return "" }
	installed, err := installAgentHooks(home, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 4 {
		t.Fatalf("installed = %#v", installed)
	}
	for _, path := range []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "hooks.json"),
		filepath.Join(home, ".gemini", "settings.json"),
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "work agent-status") {
			t.Fatalf("%s does not contain work hooks", path)
		}
	}
	codexConfig, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexConfig), "hooks = true") || strings.Contains(string(codexConfig), "hooks = false") {
		t.Fatalf("Codex config = %q", codexConfig)
	}
	openCodePluginPath := filepath.Join(home, ".config", "opencode", "plugins", "work-status.ts")
	plugin, err := os.ReadFile(openCodePluginPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plugin), "work agent-status") {
		t.Fatalf("OpenCode plugin = %q", plugin)
	}
	if !strings.Contains(string(plugin), "opencode ${sessionID}") {
		t.Fatalf("OpenCode plugin does not forward the session ID: %q", plugin)
	}
}

func TestSetTOMLFeatureAddsFeaturesSection(t *testing.T) {
	input := "model = \"test\"\n\n[projects.\"/repo\"]\ntrust_level = \"trusted\"\n"
	want := input + "\n[features]\nhooks = true\n"
	if got := setTOMLFeature(input, "hooks", true); got != want {
		t.Fatalf("setTOMLFeature = %q, want %q", got, want)
	}
}
