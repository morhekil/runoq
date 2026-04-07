package orchestrator

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

// stubExecutor returns a CommandExecutor that responds to gh commands with
// predefined outputs based on substring matching.
type ghStub struct {
	rules []ghStubRule
	calls []string
}

type ghStubRule struct {
	contains string
	stdout   string
	err      error
}

func (s *ghStub) exec(_ context.Context, req shell.CommandRequest) error {
	cmd := req.Name + " " + strings.Join(req.Args, " ")
	s.calls = append(s.calls, cmd)
	for _, r := range s.rules {
		if strings.Contains(cmd, r.contains) {
			if req.Stdout != nil && r.stdout != "" {
				req.Stdout.Write([]byte(r.stdout))
			}
			return r.err
		}
	}
	return nil
}

func TestRunTickNoEpicsBootstraps(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "runoq.json")
	os.WriteFile(configPath, []byte(`{"labels":{"ready":"runoq:ready","inProgress":"runoq:in-progress","done":"runoq:done","needsReview":"runoq:needs-human-review","blocked":"runoq:blocked","planApproved":"runoq:plan-approved"}}`), 0o644)

	var issueCreateCalled bool
	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: "[]"},
			{contains: "issue create", stdout: "https://github.com/owner/repo/issues/1"},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:        "owner/repo",
		PlanFile:    "docs/plan.md",
		RunoqRoot:   tmpDir,
		Env:         []string{"RUNOQ_CONFIG=" + configPath},
		ExecCommand: func(ctx context.Context, req shell.CommandRequest) error {
			if strings.Contains(req.Name+" "+strings.Join(req.Args, " "), "issue create") {
				issueCreateCalled = true
			}
			return stub.exec(ctx, req)
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	// Bootstrap creates issues — may fail on plan-dispatch (no script), but should have called create
	_ = result
	if !issueCreateCalled {
		t.Error("expected issue create to be called during bootstrap")
	}
}

func TestRunTickAllClosedReturnsWaiting(t *testing.T) {
	t.Parallel()

	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: `[{"number":1,"title":"M1","state":"CLOSED","body":"<!-- runoq:meta\ntype: epic\n-->","labels":[],"url":"u"}]`},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:        "owner/repo",
		PlanFile:    "docs/plan.md",
		RunoqRoot:   t.TempDir(),
		ExecCommand: stub.exec,
		Stdout:      &stdout,
		Stderr:      &stderr,
	})

	if result != 2 {
		t.Errorf("RunTick = %d, want 2 (waiting); stderr = %s", result, stderr.String())
	}
	if !strings.Contains(stdout.String(), "All milestones complete") {
		t.Errorf("stdout = %q", stdout.String())
	}
}
