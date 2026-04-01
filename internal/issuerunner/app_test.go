package issuerunner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/common"
)

func newTestApp(t *testing.T, args []string) *App {
	t.Helper()
	var stdout, stderr bytes.Buffer
	return New(args, nil, t.TempDir(), &stdout, &stderr)
}

func TestRunNoArgs(t *testing.T) {
	app := newTestApp(t, nil)
	code := app.Run(t.Context())
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	got := app.stderr.(*bytes.Buffer).String()
	if !strings.Contains(got, "Usage") {
		t.Errorf("expected usage in stderr, got %q", got)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	app := newTestApp(t, []string{"bogus"})
	code := app.Run(t.Context())
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	got := app.stderr.(*bytes.Buffer).String()
	if !strings.Contains(got, "unknown command") {
		t.Errorf("expected 'unknown command' in stderr, got %q", got)
	}
}

func TestRunMissingPayload(t *testing.T) {
	app := newTestApp(t, []string{"run"})
	code := app.Run(t.Context())
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	got := app.stderr.(*bytes.Buffer).String()
	if !strings.Contains(got, "usage:") {
		t.Errorf("expected usage hint in stderr, got %q", got)
	}
}

func TestRunInvalidPayload(t *testing.T) {
	app := newTestApp(t, []string{"run", "/no/such/file.json"})
	code := app.Run(t.Context())
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	got := app.stderr.(*bytes.Buffer).String()
	if !strings.Contains(got, "failed to read payload") {
		t.Errorf("expected read error in stderr, got %q", got)
	}
}

func TestRunValidPayload(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	specFile := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(specFile, []byte("implement feature X"), 0o644); err != nil {
		t.Fatal(err)
	}

	payload := inputPayload{
		IssueNumber: 42,
		Worktree:    worktree,
		Branch:      "feature-x",
		SpecPath:    specFile,
		Repo:        "owner/repo",
		MaxRounds:   5,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadFile := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(payloadFile, payloadBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	app := New([]string{"run", payloadFile}, nil, dir, &stdout, &stderr)

	fakeHash := "abc123def456"
	app.SetCommandExecutor(func(_ context.Context, req common.CommandRequest) error {
		if req.Name == "git" {
			_, _ = req.Stdout.Write([]byte(fakeHash + "\n"))
		}
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", code, stderr.String())
	}

	var output outputPayload
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nraw=%s", err, stdout.String())
	}

	if output.Status != "fail" {
		t.Errorf("status = %q, want %q", output.Status, "fail")
	}
	if output.BaselineHash != fakeHash {
		t.Errorf("baselineHash = %q, want %q", output.BaselineHash, fakeHash)
	}
	if output.HeadHash != fakeHash {
		t.Errorf("headHash = %q, want %q", output.HeadHash, fakeHash)
	}
	if output.TotalRounds != 5 {
		t.Errorf("totalRounds = %d, want 5", output.TotalRounds)
	}
	if output.Round != 1 {
		t.Errorf("round = %d, want 1", output.Round)
	}
	if output.SpecRequirements != "implement feature X" {
		t.Errorf("specRequirements = %q, want %q", output.SpecRequirements, "implement feature X")
	}
	if output.LogDir == "" {
		t.Error("logDir should not be empty")
	}
}

func TestPayloadDefaults(t *testing.T) {
	dir := t.TempDir()

	payload := inputPayload{
		IssueNumber: 1,
		Worktree:    dir,
		Repo:        "owner/repo",
		// MaxRounds and Round intentionally omitted (zero values)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	payloadFile := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(payloadFile, payloadBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	app := New([]string{"run", payloadFile}, nil, dir, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req common.CommandRequest) error {
		if req.Name == "git" {
			_, _ = req.Stdout.Write([]byte("deadbeef\n"))
		}
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", code, stderr.String())
	}

	var output outputPayload
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}

	if output.TotalRounds != 3 {
		t.Errorf("maxRounds default: got %d, want 3", output.TotalRounds)
	}
	if output.Round != 1 {
		t.Errorf("round default: got %d, want 1", output.Round)
	}
}
