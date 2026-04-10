package issuerunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

func mustWriteReqStdout(t *testing.T, req shell.CommandRequest, value string) {
	t.Helper()
	if req.Stdout == nil {
		t.Fatal("stdout writer is nil")
	}
	if _, err := req.Stdout.Write([]byte(value)); err != nil {
		t.Fatalf("write stdout: %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func codexPayloadOutput(status string, testsRun bool, testsPassed bool, testSummary string, buildPassed bool, blockers []string, notes string) string {
	payload := map[string]any{
		"status":       status,
		"tests_run":    testsRun,
		"tests_passed": testsPassed,
		"test_summary": testSummary,
		"build_passed": buildPassed,
		"blockers":     blockers,
		"notes":        notes,
	}
	data, _ := json.Marshal(payload)
	return "<!-- runoq:payload:codex-return -->\n```json\n" + string(data) + "\n```\n"
}

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

	if output.Status != "completed" {
		t.Errorf("status = %q, want %q", output.Status, "completed")
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
	if output.Round != 1 {
		t.Errorf("round default: got %d, want 1", output.Round)
	}
}

func TestValidatePayloadUsesStatePackageWithoutScript(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	mustMkdirAll(t, worktree)
	runoqRoot := filepath.Join(dir, "runoq")
	mustMkdirAll(t, runoqRoot)

	lastMsgFile := filepath.Join(dir, "last-message.md")
	mustWriteFile(t, lastMsgFile, []byte(codexPayloadOutput("completed", true, true, "ok", true, []string{}, "")))
	payloadFile := filepath.Join(dir, "payload.json")

	var calls []string
	app := New(nil, []string{"RUNOQ_ROOT=" + runoqRoot}, dir, io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, req.Name+" "+strings.Join(req.Args, " "))
		if req.Name != "git" {
			t.Fatalf("unexpected command: %s %v", req.Name, req.Args)
		}

		switch {
		case slices.Contains(req.Args, "log"):
			mustWriteReqStdout(t, req, "abc123 first commit\n")
		case slices.Contains(req.Args, "diff"):
			mustWriteReqStdout(t, req, "")
		default:
			mustWriteReqStdout(t, req, "abc123\n")
		}
		return nil
	})

	if ok := app.validatePayload(t.Context(), worktree, "base", lastMsgFile, payloadFile); !ok {
		t.Fatal("validatePayload returned false, want true")
	}

	data, err := os.ReadFile(payloadFile)
	if err != nil {
		t.Fatalf("read payload file: %v", err)
	}
	if !strings.Contains(string(data), `"payload_schema_valid": true`) {
		t.Fatalf("expected normalized payload output, got %s", data)
	}
	for _, call := range calls {
		if strings.Contains(call, "state.sh") {
			t.Fatalf("did not expect shell script call, got %q", call)
		}
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
		mustWriteReqStdout(fe.t, req, "deadbeef123456\n")
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
	mustMkdirAll(t, worktree)
	logDir := filepath.Join(dir, "logs")
	mustMkdirAll(t, logDir)
	runoqRoot := filepath.Join(dir, "runoq")
	mustMkdirAll(t, filepath.Join(runoqRoot, "scripts"))

	specFile := filepath.Join(dir, "spec.md")
	mustWriteFile(t, specFile, []byte("implement X"))

	verifyCount := 0
	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, "abc123\n")
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			// Write a fake events JSONL with thread_id.
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"type":"thread.started","thread_id":"thread-42"}`+"\n")
				mustWriteReqStdout(t, req, `{"tokens": 500}`+"\n")
			}
			// Write fake last-message to the -o path.
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					mustWriteFile(t, req.Args[i+1], []byte("fake codex output"))
					break
				}
			}
			return nil
		},
		"state.sh": func(req shell.CommandRequest) error {
			// validate-payload: return valid schema.
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"payload_schema_valid":true}`)
			}
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			verifyCount++
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

	if out.Status != "completed" {
		t.Errorf("status = %q, want %q", out.Status, "completed")
	}
	if out.Round != 1 {
		t.Errorf("round = %d, want 1", out.Round)
	}
	if verifyCount != 0 {
		t.Errorf("verify.sh called %d times, want 0", verifyCount)
	}
	if out.VerificationPassed {
		t.Error("verificationPassed should remain false until VERIFY tick")
	}
	if len(out.VerificationFailures) != 0 {
		t.Errorf("verificationFailures = %v, want empty", out.VerificationFailures)
	}
}

func TestRequiredPayloadSchemaBlockOmitsReconstructableGitFields(t *testing.T) {
	schema := requiredPayloadSchemaBlock()

	for _, field := range []string{"tests_run", "tests_passed", "test_summary", "build_passed", "blockers", "notes"} {
		if !strings.Contains(schema, field) {
			t.Fatalf("expected schema to contain %q, got:\n%s", field, schema)
		}
	}
	for _, field := range []string{"commits_pushed", "commit_range", "files_changed", "files_added", "files_deleted"} {
		if strings.Contains(schema, field) {
			t.Fatalf("expected schema to omit %q, got:\n%s", field, schema)
		}
	}
}

func TestDevelopmentLoop_PersistsVerificationPayload(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	mustMkdirAll(t, worktree)
	logDir := filepath.Join(dir, "logs")
	mustMkdirAll(t, logDir)
	runoqRoot := filepath.Join(dir, "runoq")
	mustMkdirAll(t, filepath.Join(runoqRoot, "scripts"))

	specFile := filepath.Join(dir, "spec.md")
	mustWriteFile(t, specFile, []byte("implement X"))

	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, "abc123\n")
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"type":"thread.started","thread_id":"thread-42"}`+"\n")
				mustWriteReqStdout(t, req, `{"tokens": 500}`+"\n")
			}
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					mustWriteFile(t, req.Args[i+1], []byte(codexPayloadOutput("completed", true, true, "ok", true, []string{}, "")))
					break
				}
			}
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"review_allowed":true,"failures":[],"actual":{"files_changed":["main.go"],"files_added":[],"files_deleted":[]}}`)
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
	if out.VerificationPayload == nil {
		t.Fatal("expected verification payload to be persisted in output")
	}
	if got, _ := out.VerificationPayload["payload_schema_valid"].(bool); !got {
		t.Fatalf("expected payload_schema_valid=true in verification payload, got %+v", out.VerificationPayload)
	}
}

func TestDevelopmentLoop_LogsProgressToStderr(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	mustMkdirAll(t, worktree)
	logDir := filepath.Join(dir, "logs")
	mustMkdirAll(t, logDir)
	runoqRoot := filepath.Join(dir, "runoq")
	mustMkdirAll(t, filepath.Join(runoqRoot, "scripts"))

	specFile := filepath.Join(dir, "spec.md")
	mustWriteFile(t, specFile, []byte("implement X"))

	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, "abc123\n")
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"type":"thread.started","thread_id":"thread-42"}`+"\n")
				mustWriteReqStdout(t, req, `{"tokens": 500}`+"\n")
			}
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					mustWriteFile(t, req.Args[i+1], []byte("fake codex output"))
					break
				}
			}
			return nil
		},
		"state.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"payload_schema_valid":true}`)
			}
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"review_allowed":true,"failures":[],"actual":{"files_changed":["main.go"],"files_added":[],"files_deleted":[]}}`)
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
	if strings.Contains(stderrOutput, "[verifier]") {
		t.Errorf("stderr should not log verifier activity during DEVELOP, got:\n%s", stderrOutput)
	}
}

func TestDevelopmentLoop_BudgetExhausted(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	mustMkdirAll(t, worktree)
	logDir := filepath.Join(dir, "logs")
	mustMkdirAll(t, logDir)

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
			mustWriteReqStdout(t, req, "deadbeef\n")
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

func TestDevelopmentLoop_NoVerificationLoop(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	mustMkdirAll(t, worktree)
	logDir := filepath.Join(dir, "logs")
	mustMkdirAll(t, logDir)
	runoqRoot := filepath.Join(dir, "runoq")
	mustMkdirAll(t, filepath.Join(runoqRoot, "scripts"))

	specFile := filepath.Join(dir, "spec.md")
	mustWriteFile(t, specFile, []byte("spec"))

	verifyCount := 0
	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, "deadbeef\n")
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"type":"thread.started","thread_id":"t1"}`+"\n")
			}
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					mustWriteFile(t, req.Args[i+1], []byte("output"))
					break
				}
			}
			return nil
		},
		"state.sh": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"payload_schema_valid":true}`)
			}
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			verifyCount++
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

	if out.Status != "completed" {
		t.Errorf("status = %q, want %q", out.Status, "completed")
	}
	if out.Round != 1 {
		t.Errorf("round = %d, want 1", out.Round)
	}
	if verifyCount != 0 {
		t.Errorf("verify.sh called %d times, want 0", verifyCount)
	}
	if len(out.VerificationFailures) != 0 {
		t.Errorf("verificationFailures = %v, want empty", out.VerificationFailures)
	}
}

func TestDevelopmentLoop_SchemaRetryUsesResumeWithSameThreadID(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	mustMkdirAll(t, worktree)
	logDir := filepath.Join(dir, "logs")
	mustMkdirAll(t, logDir)
	runoqRoot := filepath.Join(dir, "runoq")
	mustMkdirAll(t, filepath.Join(runoqRoot, "scripts"))

	specFile := filepath.Join(dir, "spec.md")
	mustWriteFile(t, specFile, []byte("implement X"))

	validateCount := 0
	var codexCalls [][]string
	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, "abc123\n")
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			codexCalls = append(codexCalls, append([]string(nil), req.Args...))
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"type":"thread.started","thread_id":"thread-42"}`+"\n")
				mustWriteReqStdout(t, req, `{"tokens": 500}`+"\n")
			}
			for i, arg := range req.Args {
				if arg == "-o" && i+1 < len(req.Args) {
					mustWriteFile(t, req.Args[i+1], []byte("fake codex output"))
					break
				}
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
	app.validatePayloadFn = func(_ context.Context, _ string, _ string, _ string, payloadFile string) bool {
		validateCount++
		mustWriteFile(t, payloadFile, []byte("{\n  \"payload_schema_valid\": false\n}\n"))
		return false
	}

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}

	var out outputPayload
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("parse output: %v; raw=%s", err, stdout.String())
	}
	if out.Status != "completed" {
		t.Fatalf("status = %q, want completed", out.Status)
	}
	if validateCount != 2 {
		t.Fatalf("validate-payload called %d times, want 2", validateCount)
	}
	if len(codexCalls) != 2 {
		t.Fatalf("codex called %d times, want 2", len(codexCalls))
	}
	if len(codexCalls[0]) < 1 || codexCalls[0][0] != "exec" {
		t.Fatalf("initial codex call = %v, want exec", codexCalls[0])
	}
	if len(codexCalls[0]) > 1 && codexCalls[0][1] == "resume" {
		t.Fatalf("initial codex call should not be a resume, got %v", codexCalls[0])
	}
	if len(codexCalls[1]) < 3 || codexCalls[1][0] != "exec" || codexCalls[1][1] != "resume" || codexCalls[1][2] != "thread-42" {
		t.Fatalf("schema retry call = %v, want exec resume thread-42", codexCalls[1])
	}
	if !strings.Contains(out.Summary, "schema issues") {
		t.Fatalf("summary = %q, want schema issue warning", out.Summary)
	}
	if !slices.Contains(out.Caveats, "codex payload schema invalid after 1 resume attempt(s)") {
		t.Fatalf("caveats = %v, want single retry caveat", out.Caveats)
	}
}

func TestClassifyTransientError(t *testing.T) {
	app := newTestApp(t, nil)
	dir := t.TempDir()

	t.Run("capacity error in event log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "capacity-events.jsonl")
		mustWriteFile(t, eventsPath, []byte(`{"type":"turn.failed","error":"Selected model is at capacity"}`+"\n"))

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
		mustWriteFile(t, eventsPath, []byte(`{"type":"turn.failed","error":"Rate limit exceeded, status 429"}`+"\n"))

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
		mustWriteFile(t, eventsPath, []byte(`{"type":"thread.started","thread_id":"t1"}`+"\n"+`{"type":"turn.completed"}`+"\n"))

		isTransient, reason := app.classifyTransientError(eventsPath, nil)
		if isTransient {
			t.Fatalf("expected transient=false for normal log, got reason=%q", reason)
		}
	})

	t.Run("exec error with empty log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "empty-events.jsonl")
		mustWriteFile(t, eventsPath, []byte{})

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
		mustWriteFile(t, eventsPath, []byte(`{"type":"thread.started","thread_id":"t1"}`+"\n"+`{"type":"turn.completed"}`+"\n"))

		isTransient, _ := app.classifyTransientError(eventsPath, fmt.Errorf("exit status 1"))
		if isTransient {
			t.Fatal("expected transient=false when log has valid output despite exec error")
		}
	})

	t.Run("503 error in event log", func(t *testing.T) {
		eventsPath := filepath.Join(dir, "503-events.jsonl")
		mustWriteFile(t, eventsPath, []byte(`{"type":"turn.failed","error":"Service unavailable (503)"}`+"\n"))

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

func TestDevelopmentLoop_TransientErrorShortCircuits(t *testing.T) {
	dir := t.TempDir()
	worktree := filepath.Join(dir, "wt")
	mustMkdirAll(t, worktree)
	logDir := filepath.Join(dir, "logs")
	mustMkdirAll(t, logDir)
	runoqRoot := filepath.Join(dir, "runoq")
	mustMkdirAll(t, filepath.Join(runoqRoot, "scripts"))

	specFile := filepath.Join(dir, "spec.md")
	mustWriteFile(t, specFile, []byte("implement X"))

	verifyCount := 0
	fe := &fakeExecutor{t: t, handlers: map[string]func(shell.CommandRequest) error{
		"git": func(req shell.CommandRequest) error {
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, "abc123\n")
			}
			return nil
		},
		"codex": func(req shell.CommandRequest) error {
			// Simulate a transient capacity error.
			if req.Stdout != nil {
				mustWriteReqStdout(t, req, `{"type":"turn.failed","error":"Selected model is at capacity"}`+"\n")
			}
			return fmt.Errorf("exit status 1")
		},
		"state.sh": func(req shell.CommandRequest) error {
			t.Error("state.sh should not be called on transient error")
			return nil
		},
		"verify.sh": func(req shell.CommandRequest) error {
			verifyCount++
			t.Error("verify.sh should not be called on transient error")
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

	if out.Status != "transient_error" {
		t.Errorf("status = %q, want %q", out.Status, "transient_error")
	}
	if out.Round != 1 {
		t.Errorf("round = %d, want 1 (should not burn rounds)", out.Round)
	}
	if verifyCount != 0 {
		t.Errorf("verify.sh called %d times, want 0", verifyCount)
	}
	if out.Summary == "" {
		t.Error("summary should describe the transient error")
	}
}
