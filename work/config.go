package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type config struct {
	Default   string     `yaml:"default"`
	PreRemove stringList `yaml:"pre_remove"`
}

// stringList accepts both the convenient single-hook form and a YAML list.
type stringList []string

func (s *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var value string
		if err := node.Decode(&value); err != nil {
			return err
		}
		*s = []string{value}
		return nil
	case yaml.SequenceNode:
		var values []string
		if err := node.Decode(&values); err != nil {
			return err
		}
		*s = values
		return nil
	case yaml.AliasNode:
		return s.UnmarshalYAML(node.Alias)
	default:
		return fmt.Errorf("must be a command string or a list of command strings")
	}
}

func configFile(home string) string {
	return filepath.Join(home, ".config", "work", "config.yaml")
}

func loadConfig(home string) (config, error) {
	path := configFile(home)
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config{}, nil
	}
	if err != nil {
		return config{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var result config
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&result); err != nil && !errors.Is(err, io.EOF) {
		return config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, hook := range result.PreRemove {
		if strings.TrimSpace(hook) == "" {
			return config{}, fmt.Errorf("parsing %s: pre_remove commands must not be empty", path)
		}
	}
	return result, nil
}

func expandConfiguredPath(path, home string) (string, error) {
	switch {
	case path == "~":
		path = home
	case strings.HasPrefix(path, "~/"):
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	case !filepath.IsAbs(path):
		path = filepath.Join(home, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving path %q: %w", path, err)
	}
	return filepath.Clean(abs), nil
}
