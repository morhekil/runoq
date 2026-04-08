package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestNoEnvLeakageInInternalPackages(t *testing.T) {
	t.Parallel()

	// Packages that are allowed to access env vars (entry points / config)
	allowed := map[string]bool{
		"internal/config": true,
		"internal/cli":    true,
	}

	// Packages with standalone CLI entry points (shell wrappers for operator use).
	// Their New() constructors read env vars for that path; NewDirect() does not.
	// The orchestrator uses NewDirect() — these exceptions are for the CLI path only.
	scriptEntryPoints := map[string]bool{
		"internal/worktree":       true, // worktree.sh — standalone worktree management
		"internal/verify":         true, // verify.sh — standalone verification
		"internal/state":          true, // state.sh — standalone state operations
		"internal/report":         true, // report subcommand
		"internal/issuequeue":     true, // gh-issue-queue.sh — standalone queue management
		"internal/dispatchsafety": true, // dispatch-safety.sh — standalone eligibility check
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Walk up to repo root
	for !fileExists(filepath.Join(root, "go.mod")) {
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("could not find repo root")
		}
		root = parent
	}

	internalDir := filepath.Join(root, "internal")
	var violations []string

	filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		pkg := filepath.Dir(rel)
		if allowed[pkg] || scriptEntryPoints[pkg] {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)

		for _, pattern := range []string{"os.Getenv", "os.LookupEnv", "os.Environ()"} {
			if strings.Contains(content, pattern) {
				violations = append(violations, fmt.Sprintf("%s: contains %s", rel, pattern))
			}
		}
		return nil
	})

	if len(violations) > 0 {
		t.Fatalf("env access found in internal packages:\n%s", strings.Join(violations, "\n"))
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestResolvePathFromRoot(t *testing.T) {
	t.Parallel()

	got := ResolvePath("", "/runoq")
	if got != "/runoq/config/runoq.json" {
		t.Fatalf("expected root-derived path, got %q", got)
	}
}
