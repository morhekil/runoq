package orchestrator

import (
	"bytes"
	"context"
	"io"
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

func TestRunTickAllClosedReturnsComplete(t *testing.T) {
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

	if result != 3 {
		t.Errorf("RunTick = %d, want 3 (complete); stderr = %s", result, stderr.String())
	}
	if !strings.Contains(stdout.String(), "All milestones complete") {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestGraphPrioritySortAndDependencyFiltering(t *testing.T) {
	t.Parallel()

	issues := []issue{
		{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
		// Task #11: priority 2, no deps
		{Number: 11, Title: "Second task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\n-->"},
		// Task #10: priority 1, blocked by #12 which is OPEN
		{Number: 10, Title: "First task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\n-->", BlockedBy: []int{12}},
		// Task #12: priority 3, no deps — should be selected (deeper chain: #10 depends on it)
		{Number: 12, Title: "Third task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 3\n-->"},
	}
	g := BuildDepGraph(issues, 9, "")
	task := g.Next()
	if task == nil {
		t.Fatal("Next returned nil")
	}
	// #12 has effective priority 1 (from #10 which depends on it) and deeper chain
	// #11 has effective priority 2
	// So #12 should be selected
	if task.Number != 12 {
		t.Errorf("Next selected #%d, expected #12 (effective pri=1 from #10)", task.Number)
	}
}

func TestGraphSkipsNonReadyLabels(t *testing.T) {
	t.Parallel()

	issues := []issue{
		{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
		// Task #10: no ready label — skip
		{Number: 10, Title: "Needs review", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\n-->", Labels: []label{{Name: "runoq:needs-human-review"}}},
		// Task #11: has ready label
		{Number: 11, Title: "Ready task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\n-->", Labels: []label{{Name: "runoq:ready"}}},
	}
	g := BuildDepGraph(issues, 9, "runoq:ready")
	task := g.Next()
	if task == nil {
		t.Fatal("Next returned nil, expected #11")
	}
	if task.Number != 11 {
		t.Errorf("Next selected #%d, expected #11 (only ready task)", task.Number)
	}
}

func TestGraphAllBlockedCycle(t *testing.T) {
	t.Parallel()

	issues := []issue{
		{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
		{Number: 10, Title: "Task A", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\n-->", BlockedBy: []int{11}},
		{Number: 11, Title: "Task B", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\n-->", BlockedBy: []int{10}},
	}
	g := BuildDepGraph(issues, 9, "")
	task := g.Next()
	if task != nil {
		t.Errorf("Next returned #%d, expected nil (cycle)", task.Number)
	}
	if !g.HasCycle() {
		t.Error("expected HasCycle() true")
	}
}

func TestGraphSkipsClosedAndNonTaskTypes(t *testing.T) {
	t.Parallel()

	issues := []issue{
		{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
		{Number: 10, Title: "Done task", State: "CLOSED", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\n-->"},
		{Number: 11, Title: "Planning", State: "OPEN", Body: "<!-- runoq:meta\ntype: planning\nparent_epic: 9\npriority: 1\n-->"},
		{Number: 12, Title: "Ready task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\n-->", BlockedBy: []int{10}},
	}
	g := BuildDepGraph(issues, 9, "")
	task := g.Next()
	if task == nil {
		t.Fatal("Next returned nil, expected #12")
	}
	if task.Number != 12 {
		t.Errorf("Next selected #%d, expected #12", task.Number)
	}
}

func TestFindInProgressTaskReturnsInProgressIssue(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		cfg: TickConfig{InProgressLabel: "runoq:in-progress"},
		issues: []issue{
			{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
			// Ready task
			{Number: 10, Title: "Ready task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\n-->", Labels: []label{{Name: "runoq:ready"}}},
			// In-progress task
			{Number: 11, Title: "In-progress task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 2\n-->", Labels: []label{{Name: "runoq:in-progress"}}},
		},
	}

	task := runner.findInProgressTask(9)
	if task == nil {
		t.Fatal("findInProgressTask returned nil, expected #11")
	}
	if task.Number != 11 {
		t.Errorf("findInProgressTask selected #%d, expected #11", task.Number)
	}
}

func TestFindInProgressTaskReturnsNilWhenNone(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		cfg: TickConfig{InProgressLabel: "runoq:in-progress"},
		issues: []issue{
			{Number: 9, Title: "Epic", State: "OPEN", Body: "<!-- runoq:meta\ntype: epic\npriority: 1\n-->"},
			{Number: 10, Title: "Ready task", State: "OPEN", Body: "<!-- runoq:meta\ntype: task\nparent_epic: 9\npriority: 1\n-->", Labels: []label{{Name: "runoq:ready"}}},
		},
	}

	task := runner.findInProgressTask(9)
	if task != nil {
		t.Fatalf("findInProgressTask returned #%d, expected nil", task.Number)
	}
}

func TestTickOutputIncludesTimestamps(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	runner := &tickRunner{
		cfg: TickConfig{Stderr: &stderr},
	}
	runner.step("Test step")
	output := stderr.String()
	// Should contain a timestamp like [15:04:05]
	if !strings.Contains(output, "[") || !strings.Contains(output, "]") {
		t.Fatalf("expected timestamp in step output, got %q", output)
	}
	if !strings.Contains(output, "Test step") {
		t.Fatalf("expected step message, got %q", output)
	}
}

func TestTickSuccessIncludesElapsed(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	runner := &tickRunner{
		cfg: TickConfig{Stderr: &stderr},
	}
	runner.step("First")
	stderr.Reset()
	runner.success("Done")
	output := stderr.String()
	if !strings.Contains(output, "Done") {
		t.Fatalf("expected success message, got %q", output)
	}
	// Should contain elapsed time
	if !strings.Contains(output, "s)") {
		t.Fatalf("expected elapsed time in success output, got %q", output)
	}
}

func TestPostDAGCommentUsesTickConfigLabels(t *testing.T) {
	t.Parallel()

	// If postDAGComment reads labels from TickConfig (not env/loadConfig),
	// it should work without RUNOQ_CONFIG set at all.
	var stderr bytes.Buffer
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:            "owner/repo",
			InProgressLabel: "runoq:in-progress",
			DoneLabel:       "runoq:done",
			Stderr:          &stderr,
			Env:             []string{}, // deliberately no RUNOQ_CONFIG
			ExecCommand: func(_ context.Context, req shell.CommandRequest) error {
				// Mock gh calls — just succeed
				if strings.Contains(strings.Join(req.Args, " "), "issue view") {
					_, _ = io.WriteString(req.Stdout, `[]`)
				}
				return nil
			},
		},
		issues: []issue{
			makeIssue(10, 1, "OPEN", nil),
			makeIssue(11, 1, "OPEN", []int{10}),
		},
	}

	graph := BuildDepGraph(runner.issues, 1, "runoq:ready")
	// This should not panic or fail even without RUNOQ_CONFIG
	runner.postDAGComment(context.Background(), 1, graph)
	// If it tried loadConfig from env, it would fail; if it uses TickConfig, it succeeds
}

func TestResolveDependsOnMapsKeysToNumbers(t *testing.T) {
	t.Parallel()

	keyToNumber := map[string]string{"store": "10", "types": "9"}

	result := resolveDependsOn([]string{"types", "store"}, keyToNumber)
	if result != "9,10" {
		t.Fatalf("expected '9,10', got %q", result)
	}
}

func TestResolveDependsOnSkipsUnknownKeys(t *testing.T) {
	t.Parallel()

	keyToNumber := map[string]string{"store": "10"}

	result := resolveDependsOn([]string{"store", "unknown"}, keyToNumber)
	if result != "10" {
		t.Fatalf("expected '10', got %q", result)
	}
}

func TestResolveDependsOnReturnsEmptyForNoDeps(t *testing.T) {
	t.Parallel()

	result := resolveDependsOn(nil, map[string]string{"store": "10"})
	if result != "" {
		t.Fatalf("expected empty, got %q", result)
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
