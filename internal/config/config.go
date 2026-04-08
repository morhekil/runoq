// Package config loads the runoq.json config file once at startup.
// Inner packages never import this — the CLI entry point loads config
// and passes typed values to each package's constructor.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// LoadFile reads runoq.json and returns top-level sections as raw JSON.
// Each consumer unmarshals its own section into its own typed struct.
func LoadFile(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return raw, nil
}

// ResolvePath returns the config file path. If configPath is non-empty, uses it directly.
// Otherwise derives from runoqRoot.
func ResolvePath(configPath string, runoqRoot string) string {
	if configPath != "" {
		return configPath
	}
	return filepath.Join(runoqRoot, "config", "runoq.json")
}
