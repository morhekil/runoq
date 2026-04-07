package orchestrator

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
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

func TestHandleImplementationPassesRepoToOrchestrator(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "runoq.json")
	os.WriteFile(configPath, []byte(`{"labels":{"ready":"runoq:ready","inProgress":"runoq:in-progress","done":"runoq:done","needsReview":"runoq:needs-human-review","blocked":"runoq:blocked","planApproved":"runoq:plan-approved"}}`), 0o644)

	// Epic #9 with a task child #10 — tick should reach handleImplementation
	issueList := `[
		{"number":9,"title":"M1","state":"OPEN","body":"<!-- runoq:meta\ntype: epic\npriority: 1\n-->","labels":[],"url":"u"},
		{"number":10,"title":"Do thing","state":"OPEN","body":"<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\nestimated_complexity: low\n-->","labels":[],"url":"u"}
	]`

	var queueNextArgs []string
	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: issueList},
			// The queue "next" call from the orchestrator's runCommandEntry
			{contains: "gh-issue-queue.sh next", stdout: `{"issue":null,"skipped":[]}`},
			// list for sweep
			{contains: "gh-issue-queue.sh list", stdout: issueList},
			// epic-status for sweep
			{contains: "epic-status", stdout: `{"all_done":false,"pending":[10]}`},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:      "owner/repo",
		PlanFile:  "docs/plan.md",
		RunoqRoot: tmpDir,
		Env:       []string{"RUNOQ_CONFIG=" + configPath, "RUNOQ_ROOT=" + tmpDir},
		ExecCommand: func(ctx context.Context, req shell.CommandRequest) error {
			if slices.Contains(req.Args, "next") {
				queueNextArgs = req.Args
			}
			return stub.exec(ctx, req)
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	_ = result

	if len(queueNextArgs) == 0 {
		t.Fatalf("orchestrator run was never invoked (gh-issue-queue.sh next not called); stderr:\n%s", stderr.String())
	}

	if !slices.Contains(queueNextArgs, "owner/repo") {
		t.Errorf("orchestrator did not receive repo; queue next args = %v", queueNextArgs)
	}
}
