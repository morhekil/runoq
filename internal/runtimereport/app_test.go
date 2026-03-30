package runtimereport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummaryAggregatesStateFiles(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	stateDir := filepath.Join(targetRoot, ".runoq", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(stateDir, "42.json"), []byte(`{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100,
  "rounds": [
    { "tokens": { "input": 50, "cached_input": 10, "output": 40 } }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write state 42: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "43.json"), []byte(`{
  "phase": "FAILED",
  "outcome": { "verdict": "FAIL" },
  "tokens_used": 200,
  "rounds": [
    { "tokens": { "input": 100, "cached_input": 20, "output": 80 } }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write state 43: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"summary"},
		[]string{"TARGET_ROOT=" + targetRoot},
		targetRoot,
		&stdout,
		&stderr,
	)

	code := app.Run()
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%q", code, stderr.String())
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if int(out["issues"].(float64)) != 2 {
		t.Fatalf("issues mismatch: %#v", out["issues"])
	}
	if int(out["pass"].(float64)) != 1 {
		t.Fatalf("pass mismatch: %#v", out["pass"])
	}
	if int(out["fail"].(float64)) != 1 {
		t.Fatalf("fail mismatch: %#v", out["fail"])
	}
	tokens := out["tokens"].(map[string]any)
	if int(tokens["total"].(float64)) != 300 {
		t.Fatalf("tokens.total mismatch: %#v", tokens["total"])
	}
}

func TestIssueMissingFileFailsWithContractMessage(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"issue", "999"},
		[]string{"TARGET_ROOT=" + targetRoot},
		targetRoot,
		&stdout,
		&stderr,
	)

	code := app.Run()
	if code == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if !strings.Contains(stderr.String(), "runoq: No state file found for issue 999") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestIssueReturnsStoredStatePrettyPrintedLikeShell(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	stateDir := filepath.Join(targetRoot, ".runoq", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}

	stored := "{\"phase\":\"DONE\",\"outcome\":{\"verdict\":\"PASS\"},\"tokens_used\":100}\n"
	if err := os.WriteFile(filepath.Join(stateDir, "42.json"), []byte(stored), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	expected := `{
  "phase": "DONE",
  "outcome": {
    "verdict": "PASS"
  },
  "tokens_used": 100
}
`

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"issue", "42"},
		[]string{"TARGET_ROOT=" + targetRoot},
		targetRoot,
		&stdout,
		&stderr,
	)

	code := app.Run()
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%q", code, stderr.String())
	}
	if stdout.String() != expected {
		t.Fatalf("expected pretty-printed output %q, got %q", expected, stdout.String())
	}
}

func TestCostUsesConfigRates(t *testing.T) {
	t.Parallel()

	targetRoot := t.TempDir()
	stateDir := filepath.Join(targetRoot, ".runoq", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	configPath := filepath.Join(targetRoot, "config.json")

	if err := os.WriteFile(filepath.Join(stateDir, "42.json"), []byte(`{
  "phase": "DONE",
  "outcome": { "verdict": "PASS" },
  "tokens_used": 100,
  "rounds": [
    { "tokens": { "input": 1000000, "cached_input": 0, "output": 500000 } }
  ]
}`), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(`{
  "tokenCost": {
    "inputPerMillion": 1,
    "cachedInputPerMillion": 0,
    "outputPerMillion": 2
  }
}`), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(
		[]string{"cost"},
		[]string{"TARGET_ROOT=" + targetRoot, "RUNOQ_CONFIG=" + configPath},
		targetRoot,
		&stdout,
		&stderr,
	)

	code := app.Run()
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr=%q", code, stderr.String())
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if out["estimated_cost"].(float64) != 2 {
		t.Fatalf("estimated_cost mismatch: %#v", out["estimated_cost"])
	}
}
