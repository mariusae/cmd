package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfigAcceptsHookList(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "default: ~/fbsource\npre_remove:\n  - first\n  - second\n")

	cfg, err := loadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != "~/fbsource" {
		t.Fatalf("default = %q", cfg.Default)
	}
	if want := (stringList{"first", "second"}); !reflect.DeepEqual(cfg.PreRemove, want) {
		t.Fatalf("pre_remove = %#v, want %#v", cfg.PreRemove, want)
	}
}

func TestLoadConfigAcceptsSingleHook(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "pre_remove: /path/to/hook\n")

	cfg, err := loadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if want := (stringList{"/path/to/hook"}); !reflect.DeepEqual(cfg.PreRemove, want) {
		t.Fatalf("pre_remove = %#v, want %#v", cfg.PreRemove, want)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "defualt: ~/fbsource\n")

	_, err := loadConfig(home)
	if err == nil || !strings.Contains(err.Error(), "field defualt not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadConfigAllowsMissingFile(t *testing.T) {
	cfg, err := loadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != "" || len(cfg.PreRemove) != 0 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestLoadConfigAllowsEmptyFile(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "")
	cfg, err := loadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != "" || len(cfg.PreRemove) != 0 {
		t.Fatalf("config = %#v", cfg)
	}
}

func TestExpandConfiguredPath(t *testing.T) {
	home := t.TempDir()
	path, err := expandConfiguredPath("~/fbsource", home)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "fbsource"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	relative, err := expandConfiguredPath("repos/fbsource", home)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "repos", "fbsource"); relative != want {
		t.Fatalf("relative path = %q, want %q", relative, want)
	}
}

func TestEmptyHookIsRejected(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "pre_remove:\n  - '   '\n")
	_, err := loadConfig(home)
	if err == nil {
		t.Fatal("loadConfig returned nil error")
	}
}

func TestConfigFileUsesRequestedHome(t *testing.T) {
	home := filepath.Join(string(os.PathSeparator), "users", "someone")
	want := filepath.Join(home, ".config", "work", "config.yaml")
	if got := configFile(home); got != want {
		t.Fatalf("configFile = %q, want %q", got, want)
	}
}
