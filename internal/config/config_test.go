package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFileReturnsRawSections(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "runoq.json")
	os.WriteFile(configPath, []byte(`{
		"labels": {"ready": "runoq:ready", "inProgress": "runoq:in-progress"},
		"maxRounds": 5,
		"branchPrefix": "runoq/",
		"verification": {"testCommand": "npm test"}
	}`), 0o644)

	raw, err := LoadFile(configPath)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if raw["labels"] == nil {
		t.Fatal("expected labels section")
	}
	if raw["maxRounds"] == nil {
		t.Fatal("expected maxRounds section")
	}
	if raw["verification"] == nil {
		t.Fatal("expected verification section")
	}
}

func TestLoadFileMissing(t *testing.T) {
	t.Parallel()

	_, err := LoadFile("/nonexistent/runoq.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolvePathFromExplicit(t *testing.T) {
	t.Parallel()

	got := ResolvePath("/explicit/runoq.json", "/root")
	if got != "/explicit/runoq.json" {
		t.Fatalf("expected explicit path, got %q", got)
	}
}

func TestResolvePathFromRoot(t *testing.T) {
	t.Parallel()

	got := ResolvePath("", "/runoq")
	if got != "/runoq/config/runoq.json" {
		t.Fatalf("expected root-derived path, got %q", got)
	}
}
