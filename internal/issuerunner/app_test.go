package issuerunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
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
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
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
	// Loop runs through all maxRounds when verification never passes.
	if output.Round != 5 {
		t.Errorf("round = %d, want 5", output.Round)
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
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
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
	// Loop exhausts all rounds when verification never passes.
	if output.Round != 3 {
		t.Errorf("round default: got %d, want 3", output.Round)
	}
}

// fakeExecutor routes command calls to handler functions.
type fakeExecutor struct {
	t        *testing.T
	handlers map[string]func(shell.CommandRequest) error
}

func (fe *fakeExecutor) exec(_ context.Context, req shell.CommandRequest) error {
	// Try exact name match first, then base name.
	if h, ok := fe.handlers[req.Name]; ok {
		return h(req)
	}
	base := filepath.Base(req.Name)
	if h, ok := fe.handlers[base]; ok {
		return h(req)
	}
	// Default: write empty output for git.
	if req.Name == "git" && req.Stdout != nil {
		req.Stdout.Write([]byte("deadbeef123456\n"))
	}
	return nil
}

func writePayloadFile(t *testing.T, dir string, input inputPayload) string {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDevelopmentLoop_SingleRoundSuccess(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	os.MkdirAll(worktree, 0o755)
	logDir := filepath.Join(dir, "logs")
	os.MkdirAll(logDir, 0o755)
	runoqRoot := filepath.Join(dir, "runoq")
	os.MkdirAll(filepath.Join(runoqRoot, "scripts"), 0o755)

	specFile := filepath.Join(dir, "spec.md")
	os.WriteFile(specFile, []byte("implement X"), 0o644)

	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte("abc123\n"))
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			// Write a fake events JSONL with thread_id.
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"type":"thread.started","thread_id":"thread-42"}` + "\n"))
				req.Stdout.Write([]byte(`{"tokens": 500}` + "\n"))
			}
			// Write fake last-message to the -o path.
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					os.WriteFile(req.Args[i+1], []byte("fake codex output"), 0o644)
					break
				}
			}
			return nil
		},
		"state.sh": func(req shell.CommandRequest) error {
			// validate-payload: return valid schema.
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"payload_schema_valid":true}`))
			}
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"review_allowed":true,"failures":[],"actual":{"files_changed":["main.go"],"files_added":[],"files_deleted":[]}}`))
			}
			return nil
		},
	}}

	payloadFile := writePayloadFile(t, dir, inputPayload{
		IssueNumber:    1,
		PRNumber:       10,
		Worktree:       worktree,
		Branch:         "feat-x",
		SpecPath:       specFile,
		Repo:           "owner/repo",
		MaxRounds:      3,
		MaxTokenBudget: 100000,
		LogDir:         logDir,
	})

	var stdout, stderr bytes.Buffer
	app := New([]string{"run", payloadFile},
		[]string{"RUNOQ_ROOT=" + runoqRoot, "RUNOQ_CODEX_BIN=codex"},
		dir, &stdout, &stderr)
	app.SetCommandExecutor(fe.exec)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}

	var out outputPayload
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("parse output: %v; raw=%s", err, stdout.String())
	}

	if out.Status != "review_ready" {
		t.Errorf("status = %q, want %q", out.Status, "review_ready")
	}
	if out.Round != 1 {
		t.Errorf("round = %d, want 1", out.Round)
	}
	if !out.VerificationPassed {
		t.Error("verificationPassed should be true")
	}
	if len(out.ChangedFiles) == 0 {
		t.Error("changedFiles should not be empty")
	}
}

func TestDevelopmentLoop_LogsProgressToStderr(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	os.MkdirAll(worktree, 0o755)
	logDir := filepath.Join(dir, "logs")
	os.MkdirAll(logDir, 0o755)
	runoqRoot := filepath.Join(dir, "runoq")
	os.MkdirAll(filepath.Join(runoqRoot, "scripts"), 0o755)

	specFile := filepath.Join(dir, "spec.md")
	os.WriteFile(specFile, []byte("implement X"), 0o644)

	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte("abc123\n"))
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"type":"thread.started","thread_id":"thread-42"}` + "\n"))
				req.Stdout.Write([]byte(`{"tokens": 500}` + "\n"))
			}
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					os.WriteFile(req.Args[i+1], []byte("fake codex output"), 0o644)
					break
				}
			}
			return nil
		},
		"state.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"payload_schema_valid":true}`))
			}
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"review_allowed":true,"failures":[],"actual":{"files_changed":["main.go"],"files_added":[],"files_deleted":[]}}`))
			}
			return nil
		},
	}}

	payloadFile := writePayloadFile(t, dir, inputPayload{
		IssueNumber:    42,
		PRNumber:       10,
		Worktree:       worktree,
		Branch:         "feat-x",
		SpecPath:       specFile,
		Repo:           "owner/repo",
		MaxRounds:      3,
		MaxTokenBudget: 100000,
		LogDir:         logDir,
	})

	var stdout, stderr bytes.Buffer
	app := New([]string{"run", payloadFile},
		[]string{"RUNOQ_ROOT=" + runoqRoot, "RUNOQ_CODEX_BIN=codex"},
		dir, &stdout, &stderr)
	app.SetCommandExecutor(fe.exec)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}

	stderrOutput := stderr.String()

	// Should include issue number and round in progress output
	if !strings.Contains(stderrOutput, "#42") {
		t.Errorf("stderr should mention issue #42, got:\n%s", stderrOutput)
	}
	if !strings.Contains(stderrOutput, "round 1") {
		t.Errorf("stderr should mention round number, got:\n%s", stderrOutput)
	}
	// Log lines must name the specific agent performing the action
	if !strings.Contains(stderrOutput, "[codex]") {
		t.Errorf("stderr should tag codex invocation with [codex], got:\n%s", stderrOutput)
	}
	if !strings.Contains(stderrOutput, "[verifier]") {
		t.Errorf("stderr should tag verification with [verifier], got:\n%s", stderrOutput)
	}
}

func TestDevelopmentLoop_BudgetExhausted(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	os.MkdirAll(worktree, 0o755)
	logDir := filepath.Join(dir, "logs")
	os.MkdirAll(logDir, 0o755)

	payloadFile := writePayloadFile(t, dir, inputPayload{
		IssueNumber:      1,
		Worktree:         worktree,
		Branch:           "feat-x",
		Repo:             "owner/repo",
		MaxRounds:        3,
		MaxTokenBudget:   1000,
		CumulativeTokens: 1500, // Already over budget.
		LogDir:           logDir,
	})

	var stdout, stderr bytes.Buffer
	app := New([]string{"run", payloadFile}, nil, dir, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		if req.Name == "git" && req.Stdout != nil {
			req.Stdout.Write([]byte("deadbeef\n"))
		}
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}

	var out outputPayload
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("parse output: %v; raw=%s", err, stdout.String())
	}

	if out.Status != "budget_exhausted" {
		t.Errorf("status = %q, want %q", out.Status, "budget_exhausted")
	}
	if !strings.Contains(out.Summary, "budget") {
		t.Errorf("summary should mention budget, got %q", out.Summary)
	}
}

func TestDevelopmentLoop_MaxRoundsExhausted(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	os.MkdirAll(worktree, 0o755)
	logDir := filepath.Join(dir, "logs")
	os.MkdirAll(logDir, 0o755)
	runoqRoot := filepath.Join(dir, "runoq")
	os.MkdirAll(filepath.Join(runoqRoot, "scripts"), 0o755)

	specFile := filepath.Join(dir, "spec.md")
	os.WriteFile(specFile, []byte("spec"), 0o644)

	roundCount := 0
	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte("deadbeef\n"))
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"type":"thread.started","thread_id":"t1"}` + "\n"))
			}
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					os.WriteFile(req.Args[i+1], []byte("output"), 0o644)
					break
				}
			}
			return nil
		},
		"state.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"payload_schema_valid":true}`))
			}
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			roundCount++
			if req.Stdout != nil {
				req.Stdout.Write([]byte(`{"review_allowed":false,"failures":["test failed"],"actual":{"files_changed":[],"files_added":[],"files_deleted":[]}}`))
			}
			return nil
		},
		"gh-pr-lifecycle.sh": func(req shell.CommandRequest) error {
			return nil
		},
	}}

	payloadFile := writePayloadFile(t, dir, inputPayload{
		IssueNumber:    1,
		PRNumber:       5,
		Worktree:       worktree,
		Branch:         "feat-x",
		SpecPath:       specFile,
		Repo:           "owner/repo",
		MaxRounds:      2,
		MaxTokenBudget: 999999,
		LogDir:         logDir,
	})

	var stdout, stderr bytes.Buffer
	app := New([]string{"run", payloadFile},
		[]string{"RUNOQ_ROOT=" + runoqRoot, "RUNOQ_CODEX_BIN=codex"},
		dir, &stdout, &stderr)
	app.SetCommandExecutor(fe.exec)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}

	var out outputPayload
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("parse output: %v; raw=%s", err, stdout.String())
	}

	if out.Status != "fail" {
		t.Errorf("status = %q, want %q", out.Status, "fail")
	}
	if out.Round != 2 {
		t.Errorf("round = %d, want 2 (maxRounds)", out.Round)
	}
	if roundCount != 2 {
		t.Errorf("verify.sh called %d times, want 2", roundCount)
	}
	if len(out.VerificationFailures) == 0 {
		t.Error("verificationFailures should not be empty")
	}
}

func TestClassifyTransientError(t *testing.T) {
	app := newTestApp(t, nil)
	dir := t.TempDir()

	t.Run("capacity error in event log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "capacity-events.jsonl")
		os.WriteFile(eventsPath, []byte(`{"type":"turn.failed","error":"Selected model is at capacity"}`+"\n"), 0o644)

		isTransient, reason := app.classifyTransientError(eventsPath, nil)
		if !isTransient {
			t.Fatal("expected transient=true for capacity error")
		}
		if !strings.Contains(reason, "capacity") && !strings.Contains(reason, "at capacity") {
			t.Errorf("reason should mention capacity, got %q", reason)
		}
	})

	t.Run("rate limit error in event log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "ratelimit-events.jsonl")
		os.WriteFile(eventsPath, []byte(`{"type":"turn.failed","error":"Rate limit exceeded, status 429"}`+"\n"), 0o644)

		isTransient, reason := app.classifyTransientError(eventsPath, nil)
		if !isTransient {
			t.Fatal("expected transient=true for rate limit error")
		}
		if reason == "" {
			t.Error("reason should not be empty")
		}
	})

	t.Run("normal event log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "normal-events.jsonl")
		os.WriteFile(eventsPath, []byte(`{"type":"thread.started","thread_id":"t1"}`+"\n"+`{"type":"turn.completed"}`+"\n"), 0o644)

		isTransient, reason := app.classifyTransientError(eventsPath, nil)
		if isTransient {
			t.Fatalf("expected transient=false for normal log, got reason=%q", reason)
		}
	})

	t.Run("exec error with empty log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "empty-events.jsonl")
		os.WriteFile(eventsPath, []byte{}, 0o644)

		isTransient, reason := app.classifyTransientError(eventsPath, fmt.Errorf("exit status 1"))
		if !isTransient {
			t.Fatal("expected transient=true for exec error + empty log")
		}
		if reason == "" {
			t.Error("reason should not be empty")
		}
	})

	t.Run("exec error with valid output", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "valid-events.jsonl")
		os.WriteFile(eventsPath, []byte(`{"type":"thread.started","thread_id":"t1"}`+"\n"+`{"type":"turn.completed"}`+"\n"), 0o644)

		isTransient, _ := app.classifyTransientError(eventsPath, fmt.Errorf("exit status 1"))
		if isTransient {
			t.Fatal("expected transient=false when log has valid output despite exec error")
		}
	})

	t.Run("503 error in event log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "503-events.jsonl")
		os.WriteFile(eventsPath, []byte(`{"type":"turn.failed","error":"Service unavailable (503)"}`+"\n"), 0o644)

		isTransient, _ := app.classifyTransientError(eventsPath, nil)
		if !isTransient {
			t.Fatal("expected transient=true for 503 error")
		}
	})

	t.Run("missing event log file", func(t *testing.T) {
		isTransient, reason := app.classifyTransientError(filepath.Join(dir, "nonexistent.jsonl"), fmt.Errorf("exit status 1"))
		if !isTransient {
			t.Fatal("expected transient=true for missing log + exec error")
		}
		if reason == "" {
			t.Error("reason should not be empty")
		}
	})
}
