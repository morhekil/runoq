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

func TestSelectNextTaskPrioritySortAndDependencyFiltering(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		issues: []issue{
			{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
			// Task #11: priority 2, no deps — should be selected
			{Number: 11, Title: "Second task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\n-->"},
			// Task #10: priority 1, depends on #12 which is OPEN — blocked
			{Number: 10, Title: "First task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\ndepends_on: [12]\n-->"},
			// Task #12: priority 3, no deps
			{Number: 12, Title: "Third task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 3\n-->"},
		},
	}

	task := runner.selectNextTask(9)
	if task == nil {
		t.Fatal("selectNextTask returned nil, expected #11")
	}
	if task.Number != 11 {
		t.Errorf("selectNextTask selected #%d, expected #11 (highest unblocked priority)", task.Number)
	}
}

func TestSelectNextTaskAllBlocked(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		issues: []issue{
			{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
			// Task #10 depends on #11 which is OPEN
			{Number: 10, Title: "Task A", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\ndepends_on: [11]\n-->"},
			{Number: 11, Title: "Task B", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\ndepends_on: [10]\n-->"},
		},
	}

	task := runner.selectNextTask(9)
	if task != nil {
		t.Errorf("selectNextTask returned #%d, expected nil (all blocked)", task.Number)
	}
}

func TestSelectNextTaskSkipsClosedAndNonTaskTypes(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		issues: []issue{
			{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
			// Closed task — skip
			{Number: 10, Title: "Done task", State: "CLOSED", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\n-->"},
			// Planning type — skip
			{Number: 11, Title: "Planning", State: "OPEN", Body: "<!-- runoq:meta\ntype: planning\nparent_epic: 9\npriority: 1\n-->"},
			// Open task with met dependency (#10 is CLOSED)
			{Number: 12, Title: "Ready task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\ndepends_on: [10]\n-->"},
		},
	}

	task := runner.selectNextTask(9)
	if task == nil {
		t.Fatal("selectNextTask returned nil, expected #12")
	}
	if task.Number != 12 {
		t.Errorf("selectNextTask selected #%d, expected #12", task.Number)
	}
}

func TestTickSelectsTaskAndCallsRunIssue(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "runoq.json")
	os.WriteFile(configPath, []byte(`{"labels":{"ready":"runoq:ready","inProgress":"runoq:in-progress","done":"runoq:done","needsReview":"runoq:needs-human-review","blocked":"runoq:blocked","planApproved":"runoq:plan-approved"}}`), 0o644)

	// Epic #9 with task children #10 (priority 2) and #11 (priority 1)
	issueList := `[
		{"number":9,"title":"M1","state":"OPEN","body":"<!-- runoq:meta\ntype: epic\npriority: 1\n-->","labels":[],"url":"u"},
		{"number":10,"title":"Second task","state":"OPEN","body":"<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\nestimated_complexity: low\n-->","labels":[],"url":"u"},
		{"number":11,"title":"First task","state":"OPEN","body":"<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\nestimated_complexity: low\n-->","labels":[],"url":"u"}
	]`

	var eligibilityIssue string
	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: issueList},
			{contains: "eligibility", stdout: `{"allowed":true,"issue":11,"branch":"runoq/11-first-task","reasons":[]}`},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:      "owner/repo",
		PlanFile:  "docs/plan.md",
		RunoqRoot: tmpDir,
		Env:       []string{"RUNOQ_CONFIG=" + configPath, "RUNOQ_ROOT=" + tmpDir},
		ExecCommand: func(ctx context.Context, req shell.CommandRequest) error {
			cmd := req.Name + " " + strings.Join(req.Args, " ")
			if strings.Contains(cmd, "eligibility") {
				// Capture which issue the phase machine was called with
				for i, arg := range req.Args {
					if i > 0 && req.Args[i-1] == "owner/repo" {
						eligibilityIssue = arg
						break
					}
				}
			}
			return stub.exec(ctx, req)
		},
		Stdout: &stdout,
		Stderr: &stderr,
	})

	_ = result

	// Tick should have selected #11 (priority 1) and called RunIssue with it
	if eligibilityIssue != "11" {
		t.Errorf("expected RunIssue called with issue 11, got eligibility for issue %q; stderr:\n%s", eligibilityIssue, stderr.String())
	}

	// Should NOT have called gh-issue-queue.sh next (no queue selection)
	for _, call := range stub.calls {
		if strings.Contains(call, "gh-issue-queue.sh next") {
			t.Errorf("tick should not call queue next, but did: %s", call)
		}
	}
}
