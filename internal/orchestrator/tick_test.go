package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/comments"
	"github.com/saruman/runoq/internal/shell"
	"github.com/saruman/runoq/planning"
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
				_, _ = req.Stdout.Write([]byte(r.stdout))
			}
			return r.err
		}
	}
	return nil
}

// countingStub tracks call counts per substring and can return different
// results for successive calls to the same pattern.
type countingStub struct {
	counts map[string]int
	rules  []countingRule
	calls  []string
}

type countingRule struct {
	contains  string
	failUntil int    // fail this many times before succeeding
	stdout    string // output on success
	failErr   error  // error to return on failure
}

func (s *countingStub) exec(_ context.Context, req shell.CommandRequest) error {
	cmd := req.Name + " " + strings.Join(req.Args, " ")
	s.calls = append(s.calls, cmd)
	if s.counts == nil {
		s.counts = make(map[string]int)
	}
	for _, r := range s.rules {
		if strings.Contains(cmd, r.contains) {
			s.counts[r.contains]++
			if s.counts[r.contains] <= r.failUntil {
				return r.failErr
			}
			if req.Stdout != nil && r.stdout != "" {
				_, _ = req.Stdout.Write([]byte(r.stdout))
			}
			return nil
		}
	}
	return nil
}

func TestFetchDependenciesRetriesOnFailure(t *testing.T) {
	t.Parallel()

	graphqlResponse := `{"data":{"repository":{"issues":{"nodes":[
		{"number":1,"blockedBy":{"nodes":[]},"issueType":{"name":"Epic"}},
		{"number":2,"blockedBy":{"nodes":[{"number":1}]},"issueType":{"name":"Task"}}
	]}}}}`

	stub := &countingStub{
		rules: []countingRule{
			{contains: "api graphql", failUntil: 2, stdout: graphqlResponse, failErr: fmt.Errorf("GraphQL: Could not resolve")},
		},
	}

	runner := &tickRunner{
		cfg: TickConfig{
			Repo:        "owner/repo",
			Stderr:      io.Discard,
			Stdout:      io.Discard,
			ExecCommand: stub.exec,
		},
		issues: []issue{
			{Number: 1, State: "OPEN"},
			{Number: 2, State: "OPEN"},
		},
	}

	if err := runner.fetchDependencies(t.Context()); err != nil {
		t.Fatalf("fetchDependencies: %v", err)
	}

	if runner.issues[0].IssueType != "epic" {
		t.Errorf("issue 1: expected IssueType 'epic', got %q", runner.issues[0].IssueType)
	}
	if runner.issues[1].IssueType != "task" {
		t.Errorf("issue 2: expected IssueType 'task', got %q", runner.issues[1].IssueType)
	}
	if len(runner.issues[1].BlockedBy) != 1 || runner.issues[1].BlockedBy[0] != 1 {
		t.Errorf("issue 2: expected BlockedBy [1], got %v", runner.issues[1].BlockedBy)
	}
	// Should have retried 3 times (2 failures + 1 success)
	graphqlCalls := 0
	for _, c := range stub.calls {
		if strings.Contains(c, "api graphql") {
			graphqlCalls++
		}
	}
	if graphqlCalls != 3 {
		t.Errorf("expected 3 GraphQL calls (2 retries + success), got %d", graphqlCalls)
	}
}

func TestFetchDependenciesFailsAfterRetries(t *testing.T) {
	t.Parallel()

	stub := &countingStub{
		rules: []countingRule{
			{contains: "api graphql", failUntil: 99, failErr: fmt.Errorf("GraphQL: Could not resolve")},
		},
	}

	runner := &tickRunner{
		cfg: TickConfig{
			Repo:        "owner/repo",
			Stderr:      io.Discard,
			Stdout:      io.Discard,
			ExecCommand: stub.exec,
		},
		issues: []issue{
			{Number: 1, State: "OPEN"},
		},
	}

	err := runner.fetchDependencies(t.Context())
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	if !strings.Contains(err.Error(), "dependency fetch failed") {
		t.Errorf("expected 'dependency fetch failed' error, got: %v", err)
	}
}

func TestRunTickNoEpicsBootstraps(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "config"), 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	configPath := filepath.Join(tmpDir, "config", "runoq.json")
	if err := os.WriteFile(configPath, []byte(`{"labels":{"ready":"runoq:ready","inProgress":"runoq:in-progress","done":"runoq:done","needsReview":"runoq:needs-human-review","blocked":"runoq:blocked","planApproved":"runoq:plan-approved"},"branchPrefix":"runoq/","worktreePrefix":"runoq-wt-"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var issueCreateCalled bool
	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: "[]"},
			{contains: "issue create", stdout: "https://github.com/owner/repo/issues/1"},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:      "owner/repo",
		PlanFile:  "docs/plan.md",
		RunoqRoot: tmpDir,
		Env:       []string{"RUNOQ_CONFIG=" + configPath},
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
			{contains: "issue list", stdout: `[{"number":1,"title":"M1","state":"CLOSED","body":"","labels":[],"url":"u"}]`},
			{contains: "api graphql", stdout: `{"data":{"repository":{"issues":{"nodes":[{"number":1,"blockedBy":{"nodes":[]},"issueType":{"name":"Epic"}}]}}}}`},
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
		{Number: 9, Title: "Epic", State: "OPEN", IssueType: "epic"},
		// Task #11: no deps
		{Number: 11, Title: "Second task", State: "OPEN", IssueType: "task", ParentEpic: 9},
		// Task #10: blocked by #12 which is OPEN
		{Number: 10, Title: "First task", State: "OPEN", IssueType: "task", ParentEpic: 9, BlockedBy: []int{12}},
		// Task #12: no deps — should be selected (deeper chain: #10 depends on it)
		{Number: 12, Title: "Third task", State: "OPEN", IssueType: "task", ParentEpic: 9},
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
		{Number: 9, Title: "Epic", State: "OPEN", IssueType: "epic"},
		// Task #10: no ready label — skip
		{Number: 10, Title: "Needs review", State: "OPEN", IssueType: "task", ParentEpic: 9, Labels: []label{{Name: "runoq:needs-human-review"}}},
		// Task #11: has ready label
		{Number: 11, Title: "Ready task", State: "OPEN", IssueType: "task", ParentEpic: 9, Labels: []label{{Name: "runoq:ready"}}},
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
		{Number: 9, Title: "Epic", State: "OPEN", IssueType: "epic"},
		{Number: 10, Title: "Task A", State: "OPEN", IssueType: "task", ParentEpic: 9, BlockedBy: []int{11}},
		{Number: 11, Title: "Task B", State: "OPEN", IssueType: "task", ParentEpic: 9, BlockedBy: []int{10}},
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
		{Number: 9, Title: "Epic", State: "OPEN", IssueType: "epic"},
		{Number: 10, Title: "Done task", State: "CLOSED", IssueType: "task", ParentEpic: 9},
		{Number: 11, Title: "Planning", State: "OPEN", IssueType: "task", ParentEpic: 9, Labels: []label{{Name: "runoq:planning"}}},
		{Number: 12, Title: "Ready task", State: "OPEN", IssueType: "task", ParentEpic: 9, BlockedBy: []int{10}},
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
			{Number: 9, Title: "Epic", State: "OPEN", IssueType: "epic"},
			// Ready task
			{Number: 10, Title: "Ready task", State: "OPEN", IssueType: "task", ParentEpic: 9, Labels: []label{{Name: "runoq:ready"}}},
			// In-progress task
			{Number: 11, Title: "In-progress task", State: "OPEN", IssueType: "task", ParentEpic: 9, Labels: []label{{Name: "runoq:in-progress"}}},
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
			{Number: 9, Title: "Epic", State: "OPEN", IssueType: "epic"},
			{Number: 10, Title: "Ready task", State: "OPEN", IssueType: "task", ParentEpic: 9, Labels: []label{{Name: "runoq:ready"}}},
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

func TestDepGraphUsesIssueTypeField(t *testing.T) {
	t.Parallel()

	// Issues with IssueType and ParentEpic fields set — no body metadata needed
	issues := []issue{
		{Number: 9, Title: "Epic", State: "OPEN", Body: "## AC", IssueType: "epic"},
		{Number: 10, Title: "Task", State: "OPEN", Body: "## AC", IssueType: "task", ParentEpic: 9, Labels: []label{{Name: "runoq:ready"}}},
	}
	g := BuildDepGraph(issues, 9, "runoq:ready")
	task := g.Next()
	if task == nil {
		t.Fatal("expected task #10")
	}
	if task.Number != 10 {
		t.Fatalf("expected #10, got #%d", task.Number)
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
			makeIssue(10, "OPEN", nil),
			makeIssue(11, "OPEN", []int{10}),
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

func TestHandleActiveConversationsFindsAndResponds(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Task #10 is in-progress with a linked PR that has an unprocessed comment
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:            "owner/repo",
			RunoqRoot:       tmpDir,
			InProgressLabel: "runoq:in-progress",
			ReadyLabel:      "runoq:ready",
			Env:             []string{"RUNOQ_ROOT=" + tmpDir},
			Stdout:          io.Discard,
			Stderr:          io.Discard,
		},
		issues: []issue{
			{Number: 10, Title: "Task", State: "OPEN", IssueType: "task", Labels: []label{{Name: "runoq:in-progress"}}},
		},
	}

	var prCommentPosted bool
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "pr list") && strings.Contains(args, "closes #10"):
			_, _ = io.WriteString(req.Stdout, `[{"number":87}]`)
		case strings.Contains(args, "api") && strings.Contains(args, "issues/87/comments") && !strings.Contains(args, "reactions"):
			_, _ = io.WriteString(req.Stdout, `[{"id":300,"body":"Fix this please","user":{"login":"human1"},"created_at":"2026-01-01T00:00:00Z","reactions":{"+1":0}}]`)
		case strings.Contains(args, "pr comment 87"):
			prCommentPosted = true
		case strings.Contains(args, "api") && strings.Contains(args, "reactions"):
			// +1 reaction
		}
		return nil
	}

	result := runner.handleActiveConversations(t.Context())
	if result != 0 {
		t.Fatalf("expected result 0, got %d", result)
	}
	if !prCommentPosted {
		t.Fatal("expected PR comment to be posted")
	}
}

func TestHandleActiveConversationsNoInProgressTasks(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		cfg: TickConfig{
			InProgressLabel: "runoq:in-progress",
			Stdout:          io.Discard,
			Stderr:          io.Discard,
		},
		issues: []issue{
			{Number: 10, Title: "Task", State: "OPEN", IssueType: "task", Labels: []label{{Name: "runoq:ready"}}},
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		return nil
	}

	result := runner.handleActiveConversations(t.Context())
	if result != -1 {
		t.Fatalf("expected -1 (no active conversations), got %d", result)
	}
}

func TestHandleActiveConversationsFailsWhenPRLookupFails(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		cfg: TickConfig{
			Repo:            "owner/repo",
			InProgressLabel: "runoq:in-progress",
			Stdout:          io.Discard,
			Stderr:          io.Discard,
		},
		issues: []issue{
			{Number: 10, Title: "Task", State: "OPEN", IssueType: "task", Labels: []label{{Name: "runoq:in-progress"}}},
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		if strings.Contains(args, "pr list") && strings.Contains(args, "closes #10") {
			return errors.New("pr list failed")
		}
		t.Fatalf("unexpected command: %s %s", req.Name, args)
		return nil
	}

	result := runner.handleActiveConversations(t.Context())
	if result != 1 {
		t.Fatalf("expected result 1, got %d", result)
	}
}

func TestHandleActiveConversationsFailsWhenCommentLookupFails(t *testing.T) {
	t.Parallel()

	runner := &tickRunner{
		cfg: TickConfig{
			Repo:            "owner/repo",
			InProgressLabel: "runoq:in-progress",
			IdentityHandle:  "runoq",
			Stdout:          io.Discard,
			Stderr:          io.Discard,
		},
		issues: []issue{
			{Number: 10, Title: "Task", State: "OPEN", IssueType: "task", Labels: []label{{Name: "runoq:in-progress"}}},
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "pr list") && strings.Contains(args, "closes #10"):
			_, _ = io.WriteString(req.Stdout, `[{"number":87}]`)
			return nil
		case strings.Contains(args, "api") && strings.Contains(args, "issues/87/comments") && !strings.Contains(args, "reactions"):
			return errors.New("comment lookup failed")
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
			return nil
		}
	}

	result := runner.handleActiveConversations(t.Context())
	if result != 1 {
		t.Fatalf("expected result 1, got %d", result)
	}
}

func TestTickSelectsTaskAndCallsRunIssue(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "runoq.json")
	if err := os.WriteFile(configPath, []byte(`{"labels":{"ready":"runoq:ready","inProgress":"runoq:in-progress","done":"runoq:done","needsReview":"runoq:needs-human-review","blocked":"runoq:blocked","planApproved":"runoq:plan-approved"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Epic #9 with task children #10 and #11
	issueList := `[
		{"number":9,"title":"M1","state":"OPEN","body":"","labels":[],"url":"u"},
		{"number":10,"title":"Second task","state":"OPEN","body":"","labels":[],"url":"u"},
		{"number":11,"title":"First task","state":"OPEN","body":"","labels":[],"url":"u"}
	]`

	graphqlResponse := `{"data":{"repository":{"issues":{"nodes":[
		{"number":9,"blockedBy":{"nodes":[]},"issueType":{"name":"Epic"}},
		{"number":10,"blockedBy":{"nodes":[]},"issueType":{"name":"Task"}},
		{"number":11,"blockedBy":{"nodes":[]},"issueType":{"name":"Task"}}
	]}}}}`

	var eligibilityIssue string
	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: issueList},
			// GraphQL: fetch blockedBy and issueType
			{contains: "api graphql", stdout: graphqlResponse},
			// Sub-issues: epic #9 has children #10 and #11
			{contains: "sub_issues", stdout: "10\n11\n"},
			// CheckEligibility: issue view (from dispatchsafety via runoq::gh)
			{contains: "issue view 11", stdout: `{"number":11,"title":"First task","body":"## Acceptance Criteria\n\n- [ ] Works.","labels":[],"url":"u"}`},
			// CheckEligibility: pr list (open PR check)
			{contains: "pr list", stdout: `[]`},
			// issuequeue set-status (via ghClient)
			{contains: "issue edit", stdout: ""},
			// worktree creation (git commands)
			{contains: "remote show", stdout: "HEAD branch: main\n"},
			{contains: "fetch", stdout: ""},
			{contains: "worktree", stdout: ""},
			{contains: "branch -D", stdout: ""},
			{contains: "commit --allow-empty", stdout: ""},
			{contains: "push", stdout: ""},
			{contains: "config user.", stdout: ""},
			// PR lifecycle (now via gh directly)
			{contains: "pr create", stdout: "https://example.test/pull/87\n"},
			{contains: "pr comment", stdout: ""},
			{contains: "pr ready", stdout: ""},
			{contains: "pr merge", stdout: ""},
			// issue view (orchestrator calls)
			{contains: "issue view", stdout: `{"number":11,"title":"First task","body":"## AC","labels":[],"url":"u"}`},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:      "owner/repo",
		PlanFile:  "docs/plan.md",
		RunoqRoot: tmpDir,
		Env:       []string{"RUNOQ_CONFIG=" + configPath, "RUNOQ_ROOT=" + tmpDir, "TARGET_ROOT=" + tmpDir},
		ExecCommand: func(ctx context.Context, req shell.CommandRequest) error {
			cmd := req.Name + " " + strings.Join(req.Args, " ")
			// Capture eligibility by detecting issue view calls with specific issue numbers from dispatchsafety
			if strings.Contains(cmd, "issue view") && (strings.Contains(cmd, " 11 ") || strings.Contains(cmd, " 11\n")) {
				eligibilityIssue = "11"
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
}

func TestRunTickTargetIssueDispatchesSpecificTask(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "runoq.json")
	if err := os.WriteFile(configPath, []byte(`{"labels":{"ready":"runoq:ready","inProgress":"runoq:in-progress","done":"runoq:done","needsReview":"runoq:needs-human-review","blocked":"runoq:blocked","planApproved":"runoq:plan-approved"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	issueList := `[
		{"number":42,"title":"Target task","state":"OPEN","body":"## Acceptance Criteria\n\n- [ ] Works.","labels":[{"name":"runoq:ready"}],"url":"u"},
		{"number":99,"title":"Other epic","state":"OPEN","body":"","labels":[],"url":"u"}
	]`

	graphqlResponse := `{"data":{"repository":{"issues":{"nodes":[
		{"number":42,"blockedBy":{"nodes":[]},"issueType":{"name":"Task"}},
		{"number":99,"blockedBy":{"nodes":[]},"issueType":{"name":"Epic"}}
	]}}}}`

	var stdout, stderr bytes.Buffer
	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: issueList},
			{contains: "api graphql", stdout: graphqlResponse},
			{contains: "pr list --repo owner/repo --state open --head", stdout: `[]`},
			{contains: "pr list --repo owner/repo --search closes #42", stdout: `[]`},
			{contains: "issue view 42", stdout: `{"number":42,"title":"Target task","body":"## Acceptance Criteria\n\n- [ ] Works.","labels":[{"name":"runoq:ready"}],"url":"u"}`},
			{contains: "issue edit 42", stdout: ""},
			{contains: "ls-remote --symref origin HEAD", stdout: "ref: refs/heads/main\tHEAD\n"},
			{contains: "remote show", stdout: "HEAD branch: main\n"},
			{contains: "fetch", stdout: ""},
			{contains: "worktree", stdout: ""},
			{contains: "branch -D", stdout: ""},
			{contains: "commit --allow-empty", stdout: ""},
			{contains: "push", stdout: ""},
			{contains: "config user.", stdout: ""},
			{contains: "pr create", stdout: "https://example.test/pull/87\n"},
			{contains: "pr comment", stdout: ""},
		},
	}

	result := RunTick(t.Context(), TickConfig{
		Repo:           "owner/repo",
		PlanFile:       "docs/plan.md",
		RunoqRoot:      tmpDir,
		TargetIssue:    42,
		BranchPrefix:   "runoq/",
		WorktreePrefix: "runoq-wt-",
		Env:            []string{"RUNOQ_CONFIG=" + configPath, "RUNOQ_ROOT=" + tmpDir, "TARGET_ROOT=" + tmpDir},
		ExecCommand:    stub.exec,
		Stdout:         &stdout,
		Stderr:         &stderr,
	})

	if result != 0 {
		t.Fatalf("RunTick = %d, want 0; stderr=%s", result, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Issue #42 — phase: INIT") {
		t.Fatalf("expected targeted issue output, got %q", stdout.String())
	}
	for _, call := range stub.calls {
		if strings.Contains(call, "sub_issues") {
			t.Fatalf("targeted issue tick should not walk epic hierarchy, got calls: %v", stub.calls)
		}
	}
}

func TestDispatchTaskRespectsIssueStatusFromState(t *testing.T) {
	t.Parallel()

	// When RunIssue returns state with issue_status="needs-review" and phase="DONE",
	// dispatchTask must NOT override it with "done". The issue_status field set by
	// phaseFinalize/phaseDevelopNeedsReview is authoritative.

	tests := []struct {
		name          string
		stateJSON     string
		wantSetStatus string // expected status arg to issueSetStatus, "" means no call
	}{
		{
			name:          "needs-review state should not become done",
			stateJSON:     `{"phase":"DONE","issue_status":"needs-review"}`,
			wantSetStatus: "", // phaseDevelopNeedsReview already set it
		},
		{
			name:          "done state sets done",
			stateJSON:     `{"phase":"DONE","issue_status":"done"}`,
			wantSetStatus: "done",
		},
		{
			name:          "legacy state without issue_status sets done",
			stateJSON:     `{"phase":"DONE"}`,
			wantSetStatus: "done",
		},
		{
			name:          "non-terminal phase skips set-status",
			stateJSON:     `{"phase":"DEVELOP"}`,
			wantSetStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := issueStatusFromDoneState(tt.stateJSON)
			if got != tt.wantSetStatus {
				t.Errorf("issueStatusFromDoneState() = %q, want %q", got, tt.wantSetStatus)
			}
		})
	}
}

func TestDispatchTaskUsesImplementationDefaultsForIterateDecision(t *testing.T) {
	t.Parallel()

	reviewState := `{"phase":"REVIEW","issue":42,"pr_number":87,"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42","round":1,"verdict":"ITERATE","score":"21","review_checklist":"- Add error handling","baseline_hash":"base","head_hash":"head","summary":"Needs another round"}`

	var decisionComment string
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:   "owner/repo",
			Env:    []string{"RUNOQ_ROOT=/tmp/runoq", "TARGET_ROOT=/tmp/target"},
			Stdout: io.Discard,
			Stderr: io.Discard,
			ExecCommand: func(_ context.Context, req shell.CommandRequest) error {
				args := strings.Join(req.Args, " ")
				switch {
				case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, "closes #42"):
					_, _ = io.WriteString(req.Stdout, `[{"number":87,"headRefName":"runoq/42-implement-queue"}]`)
					return nil
				case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "comments"):
					_, _ = io.WriteString(req.Stdout, `{"comments":[{"body":"<!-- runoq:bot:orchestrator:review -->\n<!-- runoq:state:`+strings.ReplaceAll(reviewState, `"`, `\"`)+` -->\n> review"}]}`)
					return nil
				case req.Name == "gh" && strings.Contains(args, "api repos/owner/repo/issues/87/comments"):
					_, _ = io.WriteString(req.Stdout, `[]`)
					return nil
				case req.Name == "gh" && strings.Contains(args, "pr comment 87"):
					for i, arg := range req.Args {
						if arg == "--body-file" && i+1 < len(req.Args) {
							data, err := os.ReadFile(req.Args[i+1])
							if err != nil {
								t.Fatalf("read decision body: %v", err)
							}
							decisionComment = string(data)
							break
						}
					}
					return nil
				default:
					t.Fatalf("unexpected command: %s %s", req.Name, args)
					return nil
				}
			},
		},
	}

	result := runner.dispatchTask(t.Context(), &issue{
		Number: 42,
		Title:  "Implement queue",
		State:  "OPEN",
		Body:   "## Acceptance Criteria\n\n- [ ] Works.",
		URL:    "https://example.test/issues/42",
	})
	if result != 0 {
		t.Fatalf("dispatchTask = %d, want 0", result)
	}
	if !strings.Contains(decisionComment, "Decision: iterate.") {
		t.Fatalf("expected iterate decision comment, got %q", decisionComment)
	}
	if strings.Contains(decisionComment, "finalize-needs-review") {
		t.Fatalf("tick dispatch should not force finalize-needs-review for round 1 iterate state, got %q", decisionComment)
	}
}

func TestDispatchTaskReturnsWaitingForWaitingDevelopState(t *testing.T) {
	t.Parallel()

	waitingState := `{"phase":"DEVELOP","issue":42,"pr_number":87,"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42","round":1,"waiting":true,"waiting_reason":"transient_backoff","transient_retry_after":"2099-01-01T00:00:00Z"}`

	runner := &tickRunner{
		cfg: TickConfig{
			Repo:   "owner/repo",
			Env:    []string{"RUNOQ_ROOT=/tmp/runoq", "TARGET_ROOT=/tmp/target"},
			Stdout: io.Discard,
			Stderr: io.Discard,
			ExecCommand: func(_ context.Context, req shell.CommandRequest) error {
				args := strings.Join(req.Args, " ")
				switch {
				case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, "closes #42"):
					_, _ = io.WriteString(req.Stdout, `[{"number":87,"headRefName":"runoq/42-implement-queue"}]`)
					return nil
				case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "comments"):
					_, _ = io.WriteString(req.Stdout, `{"comments":[{"body":"<!-- runoq:bot:orchestrator:develop -->\n<!-- runoq:state:`+strings.ReplaceAll(waitingState, `"`, `\"`)+` -->\n> develop"}]}`)
					return nil
				case req.Name == "gh" && strings.Contains(args, "api repos/owner/repo/issues/87/comments"):
					_, _ = io.WriteString(req.Stdout, `[]`)
					return nil
				default:
					t.Fatalf("unexpected command: %s %s", req.Name, args)
					return nil
				}
			},
		},
	}

	result := runner.dispatchTask(t.Context(), &issue{
		Number: 42,
		Title:  "Implement queue",
		State:  "OPEN",
		Body:   "## Acceptance Criteria\n\n- [ ] Works.",
		URL:    "https://example.test/issues/42",
	})
	if result != 2 {
		t.Fatalf("dispatchTask = %d, want 2", result)
	}
}

func TestDispatchTaskIncludesWaitingReasonInOutput(t *testing.T) {
	t.Parallel()

	waitingState := `{"phase":"DEVELOP","issue":42,"pr_number":87,"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42","round":1,"waiting":true,"waiting_reason":"transient_backoff","summary":"transient codex error: Selected model is at capacity","transient_retry_after":"2099-01-01T00:00:00Z"}`

	var stdout bytes.Buffer
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:   "owner/repo",
			Env:    []string{"RUNOQ_ROOT=/tmp/runoq", "TARGET_ROOT=/tmp/target"},
			Stdout: &stdout,
			Stderr: io.Discard,
			ExecCommand: func(_ context.Context, req shell.CommandRequest) error {
				args := strings.Join(req.Args, " ")
				switch {
				case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, "closes #42"):
					_, _ = io.WriteString(req.Stdout, `[{"number":87,"headRefName":"runoq/42-implement-queue"}]`)
					return nil
				case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "comments"):
					_, _ = io.WriteString(req.Stdout, `{"comments":[{"body":"<!-- runoq:bot:orchestrator:develop -->\n<!-- runoq:state:`+strings.ReplaceAll(waitingState, `"`, `\"`)+` -->\n> develop"}]}`)
					return nil
				case req.Name == "gh" && strings.Contains(args, "api repos/owner/repo/issues/87/comments"):
					_, _ = io.WriteString(req.Stdout, `[]`)
					return nil
				default:
					t.Fatalf("unexpected command: %s %s", req.Name, args)
					return nil
				}
			},
		},
	}

	result := runner.dispatchTask(t.Context(), &issue{
		Number: 42,
		Title:  "Implement queue",
		State:  "OPEN",
		Body:   "## Acceptance Criteria\n\n- [ ] Works.",
		URL:    "https://example.test/issues/42",
	})
	if result != 2 {
		t.Fatalf("dispatchTask = %d, want 2", result)
	}
	if !strings.Contains(stdout.String(), "Selected model is at capacity") {
		t.Fatalf("expected waiting output to include transient reason, got %q", stdout.String())
	}
}

func TestHandleApprovedAdjustmentSeedsNextPlanningIssueOnce(t *testing.T) {
	t.Parallel()

	reviewView := "{\"body\":\"## Adjustment Review\\n\\n```json\\n{\\\"milestone_number\\\":9,\\\"milestone_title\\\":\\\"Current milestone\\\",\\\"summary\\\":\\\"done\\\",\\\"recommended_verdict\\\":\\\"APPROVE\\\",\\\"proposed_adjustments\\\":[]}\\n```\"}"

	refreshedIssues := `[
		{"number":20,"title":"Next milestone","state":"OPEN","body":"","labels":[],"url":"u"},
		{"number":21,"title":"Break down Next milestone into tasks","state":"OPEN","body":"","labels":[{"name":"runoq:planning"}],"url":"u"}
	]`

	var createCalls []string
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:              "owner/repo",
			PlanApprovedLabel: "runoq:plan-approved",
			ReadyLabel:        "runoq:ready",
			InProgressLabel:   "runoq:in-progress",
			DoneLabel:         "runoq:done",
			NeedsReviewLabel:  "runoq:needs-human-review",
			BlockedLabel:      "runoq:blocked",
			Stdout:            io.Discard,
			Stderr:            io.Discard,
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[{"name":"runoq:adjustment"}]}`)
			return nil
		case strings.Contains(args, "issue view 9") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[]}`)
			return nil
		case strings.Contains(args, "issue edit 88"), strings.Contains(args, "issue edit 9"):
			return nil
		case strings.Contains(args, "issue close 88"), strings.Contains(args, "issue close 9"):
			return nil
		case strings.Contains(args, "issue list") && strings.Contains(args, "--state all"):
			_, _ = io.WriteString(req.Stdout, refreshedIssues)
			return nil
		case strings.Contains(args, "api graphql"):
			_, _ = io.WriteString(req.Stdout, `{"data":{"repository":{"issues":{"nodes":[
				{"number":20,"blockedBy":{"nodes":[]},"issueType":{"name":"Epic"}},
				{"number":21,"blockedBy":{"nodes":[]},"issueType":{"name":"Task"}}
			]}}}}`)
			return nil
		case strings.Contains(args, "sub_issues") && strings.Contains(args, "/issues/20/sub_issues"):
			_, _ = io.WriteString(req.Stdout, "21\n")
			return nil
		case strings.Contains(args, "issue create"):
			createCalls = append(createCalls, args)
			_, _ = io.WriteString(req.Stdout, "https://example.test/issues/99")
			return nil
		default:
			return nil
		}
	}

	result := runner.handleApprovedAdjustment(t.Context(), reviewView, 88, "9", comments.ItemSelection{})
	if result != 0 {
		t.Fatalf("handleApprovedAdjustment = %d, want 0", result)
	}
	if len(createCalls) != 0 {
		t.Fatalf("expected no duplicate planning issue to be created, got calls %v", createCalls)
	}
}

func TestHandleApprovedAdjustmentSeedsNextPlanningIssueWhenMissing(t *testing.T) {
	t.Parallel()

	reviewView := "{\"body\":\"## Adjustment Review\\n\\n```json\\n{\\\"milestone_number\\\":9,\\\"milestone_title\\\":\\\"Current milestone\\\",\\\"summary\\\":\\\"done\\\",\\\"recommended_verdict\\\":\\\"APPROVE\\\",\\\"proposed_adjustments\\\":[]}\\n```\"}"

	refreshedIssues := `[
		{"number":20,"title":"Next milestone","state":"OPEN","body":"","labels":[],"url":"u"}
	]`

	var createCalls []string
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:              "owner/repo",
			PlanApprovedLabel: "runoq:plan-approved",
			ReadyLabel:        "runoq:ready",
			InProgressLabel:   "runoq:in-progress",
			DoneLabel:         "runoq:done",
			NeedsReviewLabel:  "runoq:needs-human-review",
			BlockedLabel:      "runoq:blocked",
			Stdout:            io.Discard,
			Stderr:            io.Discard,
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[{"name":"runoq:adjustment"}]}`)
			return nil
		case strings.Contains(args, "issue view 9") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[]}`)
			return nil
		case strings.Contains(args, "issue edit 88"), strings.Contains(args, "issue edit 9"):
			return nil
		case strings.Contains(args, "issue close 88"), strings.Contains(args, "issue close 9"):
			return nil
		case strings.Contains(args, "issue list") && strings.Contains(args, "--state all"):
			_, _ = io.WriteString(req.Stdout, refreshedIssues)
			return nil
		case strings.Contains(args, "api graphql"):
			_, _ = io.WriteString(req.Stdout, `{"data":{"repository":{"issues":{"nodes":[
				{"number":20,"blockedBy":{"nodes":[]},"issueType":{"name":"Epic"}}
			]}}}}`)
			return nil
		case strings.Contains(args, "sub_issues") && strings.Contains(args, "/issues/20/sub_issues"):
			_, _ = io.WriteString(req.Stdout, "")
			return nil
		case strings.Contains(args, "issue create"):
			createCalls = append(createCalls, args)
			_, _ = io.WriteString(req.Stdout, "https://example.test/issues/99")
			return nil
		default:
			return nil
		}
	}

	result := runner.handleApprovedAdjustment(t.Context(), reviewView, 88, "9", comments.ItemSelection{})
	if result != 0 {
		t.Fatalf("handleApprovedAdjustment = %d, want 0", result)
	}
	if len(createCalls) != 1 {
		t.Fatalf("expected one planning issue to be seeded, got calls %v", createCalls)
	}
	if !strings.Contains(createCalls[0], "Break down Next milestone into tasks") {
		t.Fatalf("expected planning issue creation for next epic, got %v", createCalls)
	}
}

func TestHandleApprovedAdjustmentReportsAdjustmentOutcome(t *testing.T) {
	t.Parallel()

	reviewView := "{\"body\":\"## Adjustment Review\\n\\n```json\\n{\\\"milestone_number\\\":9,\\\"milestone_title\\\":\\\"Current milestone\\\",\\\"summary\\\":\\\"done\\\",\\\"recommended_verdict\\\":\\\"APPROVE\\\",\\\"proposed_adjustments\\\":[]}\\n```\"}"

	refreshedIssues := `[]`
	var stdout bytes.Buffer
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:             "owner/repo",
			ReadyLabel:       "runoq:ready",
			InProgressLabel:  "runoq:in-progress",
			DoneLabel:        "runoq:done",
			NeedsReviewLabel: "runoq:needs-human-review",
			BlockedLabel:     "runoq:blocked",
			Stdout:           &stdout,
			Stderr:           io.Discard,
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[{"name":"runoq:adjustment"}]}`)
			return nil
		case strings.Contains(args, "issue view 9") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[]}`)
			return nil
		case strings.Contains(args, "issue edit 88"), strings.Contains(args, "issue edit 9"):
			return nil
		case strings.Contains(args, "issue close 88"), strings.Contains(args, "issue close 9"):
			return nil
		case strings.Contains(args, "issue list") && strings.Contains(args, "--state all"):
			_, _ = io.WriteString(req.Stdout, refreshedIssues)
			return nil
		default:
			return nil
		}
	}

	result := runner.handleApprovedAdjustment(t.Context(), reviewView, 88, "9", comments.ItemSelection{})
	if result != 0 {
		t.Fatalf("handleApprovedAdjustment = %d, want 0", result)
	}
	if strings.Contains(stdout.String(), "created issues") {
		t.Fatalf("expected adjustment output to avoid stale 'created issues' wording, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Applied adjustments from #88") {
		t.Fatalf("expected adjustment-specific output, got %q", stdout.String())
	}
}

func TestHandleApprovedPlanningFailsWhenTaskCreationFails(t *testing.T) {
	t.Parallel()

	reviewView := "{\"body\":\"<!-- runoq:payload:plan-proposal -->\\n```json\\n{\\\"items\\\":[{\\\"title\\\":\\\"Task A\\\",\\\"type\\\":\\\"implementation\\\",\\\"body\\\":\\\"A body\\\"}]}\\n```\"}"

	var closedReview bool
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:             "owner/repo",
			ReadyLabel:       "runoq:ready",
			InProgressLabel:  "runoq:in-progress",
			DoneLabel:        "runoq:done",
			NeedsReviewLabel: "runoq:needs-human-review",
			BlockedLabel:     "runoq:blocked",
			Stdout:           io.Discard,
			Stderr:           io.Discard,
		},
		issues: []issue{{Number: 9, Title: "Milestone", State: "OPEN", IssueType: "epic"}},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issue view 9") && strings.Contains(args, "--json title"):
			_, _ = io.WriteString(req.Stdout, `{"title":"Milestone"}`)
			return nil
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[{"name":"runoq:planning"}]}`)
			return nil
		case strings.Contains(args, "issue create"):
			return errors.New("create failed")
		case strings.Contains(args, "issue edit 88"), strings.Contains(args, "issue close 88"):
			closedReview = true
			return nil
		default:
			return nil
		}
	}

	result := runner.handleApprovedPlanning(t.Context(), reviewView, 88, "9", comments.ItemSelection{})
	if result != 1 {
		t.Fatalf("handleApprovedPlanning = %d, want 1", result)
	}
	if closedReview {
		t.Fatal("expected review to remain open when task creation fails")
	}
}

func TestHandleApprovedPlanningLinksDependenciesCreatedLater(t *testing.T) {
	t.Parallel()

	reviewView := "{\"body\":\"<!-- runoq:payload:plan-proposal -->\\n```json\\n{\\\"items\\\":[{\\\"key\\\":\\\"a\\\",\\\"title\\\":\\\"Task A\\\",\\\"type\\\":\\\"implementation\\\",\\\"body\\\":\\\"A body\\\",\\\"depends_on_keys\\\":[\\\"b\\\"]},{\\\"key\\\":\\\"b\\\",\\\"title\\\":\\\"Task B\\\",\\\"type\\\":\\\"implementation\\\",\\\"body\\\":\\\"B body\\\"}]}\\n```\"}"

	var addBlockedByQueries []string
	var stderr bytes.Buffer
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:             "owner/repo",
			ReadyLabel:       "runoq:ready",
			InProgressLabel:  "runoq:in-progress",
			DoneLabel:        "runoq:done",
			NeedsReviewLabel: "runoq:needs-human-review",
			BlockedLabel:     "runoq:blocked",
			Stdout:           io.Discard,
			Stderr:           &stderr,
		},
		issues: []issue{{Number: 9, Title: "Milestone", State: "OPEN", IssueType: "epic"}},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issue view 9") && strings.Contains(args, "--json title"):
			_, _ = io.WriteString(req.Stdout, `{"title":"Milestone"}`)
			return nil
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json labels"):
			_, _ = io.WriteString(req.Stdout, `{"labels":[{"name":"runoq:planning"}]}`)
			return nil
		case strings.Contains(args, "issue create") && strings.Contains(args, "Task A"):
			_, _ = io.WriteString(req.Stdout, "https://example.test/issues/101")
			return nil
		case strings.Contains(args, "issue create") && strings.Contains(args, "Task B"):
			_, _ = io.WriteString(req.Stdout, "https://example.test/issues/102")
			return nil
		case strings.Contains(args, "api repos/owner/repo/issues/101") && strings.Contains(args, "node_id"):
			_, _ = io.WriteString(req.Stdout, "NODE_A\n")
			return nil
		case strings.Contains(args, "api repos/owner/repo/issues/102") && strings.Contains(args, "node_id"):
			_, _ = io.WriteString(req.Stdout, "NODE_B\n")
			return nil
		case strings.Contains(args, "api graphql") && strings.Contains(args, "addBlockedBy"):
			addBlockedByQueries = append(addBlockedByQueries, args)
			return nil
		case strings.Contains(args, "issue edit 88"), strings.Contains(args, "issue close 88"):
			return nil
		default:
			return nil
		}
	}

	result := runner.handleApprovedPlanning(t.Context(), reviewView, 88, "9", comments.ItemSelection{})
	if result != 0 {
		t.Fatalf("handleApprovedPlanning = %d, want 0; stderr=%q", result, stderr.String())
	}
	if len(addBlockedByQueries) != 1 {
		t.Fatalf("expected one addBlockedBy mutation, got %v", addBlockedByQueries)
	}
	if !strings.Contains(addBlockedByQueries[0], "issueId: \"NODE_A\"") || !strings.Contains(addBlockedByQueries[0], "blockingIssueId: \"NODE_B\"") {
		t.Fatalf("unexpected dependency link mutation: %q", addBlockedByQueries[0])
	}
}

func TestHandlePendingReviewFailsWhenCommentHandlingFails(t *testing.T) {
	t.Parallel()

	issueView := `{"number":88,"title":"Review","body":"<!-- runoq:payload:plan-proposal -->","state":"OPEN","labels":[{"name":"runoq:planning"}],"comments":[{"id":"IC1","author":{"login":"human"},"body":"Please revise","reactionGroups":[]}]}`

	runner := &tickRunner{
		cfg: TickConfig{
			Repo:             "owner/repo",
			ReadyLabel:       "runoq:ready",
			InProgressLabel:  "runoq:in-progress",
			DoneLabel:        "runoq:done",
			NeedsReviewLabel: "runoq:needs-review",
			BlockedLabel:     "runoq:blocked",
			Stdout:           io.Discard,
			Stderr:           io.Discard,
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json number,title,body,comments,labels,state"):
			_, _ = io.WriteString(req.Stdout, issueView)
			return nil
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json number,title,body,comments"):
			_, _ = io.WriteString(req.Stdout, issueView)
			return nil
		case strings.Contains(args, "api graphql") && strings.Contains(args, "addReaction"):
			return errors.New("reaction failed")
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
			return nil
		}
	}

	result := runner.handlePendingReview(t.Context(), &issue{
		Number: 88,
		Title:  "Review",
		State:  "OPEN",
		Body:   "<!-- runoq:payload:plan-proposal -->",
		Labels: []label{{Name: "runoq:planning"}},
	})
	if result != 1 {
		t.Fatalf("handlePendingReview = %d, want 1", result)
	}
}

func TestHandlePendingReviewRedispatchesWhenProposalMarkerExistsOnlyInComments(t *testing.T) {
	t.Parallel()

	issueView := `{"number":88,"title":"Review","body":"## Acceptance Criteria\n\n- [ ] Review milestones.","state":"OPEN","labels":[{"name":"runoq:planning"}],"comments":[{"id":"IC1","author":{"login":"human"},"body":"I pasted <!-- runoq:payload:plan-proposal --> here for reference","reactionGroups":[{"content":"THUMBS_UP","users":{"totalCount":1}}]}]}`

	runner := &tickRunner{
		cfg: TickConfig{
			Repo:   "owner/repo",
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		if strings.Contains(args, "issue view 88") && strings.Contains(args, "--json number,title,body,comments,labels,state") {
			_, _ = io.WriteString(req.Stdout, issueView)
			return nil
		}
		t.Fatalf("unexpected command: %s %s", req.Name, args)
		return nil
	}

	result := runner.handlePendingReview(t.Context(), &issue{
		Number: 88,
		Title:  "Review",
		State:  "OPEN",
		Body:   "## Acceptance Criteria\n\n- [ ] Review milestones.",
		Labels: []label{{Name: "runoq:planning"}},
	})
	if result != 1 {
		t.Fatalf("handlePendingReview = %d, want 1", result)
	}
}

func TestHandleApprovedAdjustmentFailsOnUnsupportedType(t *testing.T) {
	t.Parallel()

	reviewView := "{\"body\":\"## Adjustment Review\\n\\n```json\\n{\\\"milestone_number\\\":9,\\\"milestone_title\\\":\\\"Current milestone\\\",\\\"summary\\\":\\\"done\\\",\\\"recommended_verdict\\\":\\\"APPROVE\\\",\\\"proposed_adjustments\\\":[{\\\"type\\\":\\\"archive\\\",\\\"description\\\":\\\"Archive this milestone\\\"}]}\\n```\"}"

	var closed bool
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:   "owner/repo",
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		if strings.Contains(args, "issue edit 88") || strings.Contains(args, "issue close 88") || strings.Contains(args, "issue edit 9") || strings.Contains(args, "issue close 9") {
			closed = true
			return nil
		}
		t.Fatalf("unexpected command: %s %s", req.Name, args)
		return nil
	}

	result := runner.handleApprovedAdjustment(t.Context(), reviewView, 88, "9", comments.ItemSelection{})
	if result != 1 {
		t.Fatalf("handleApprovedAdjustment = %d, want 1", result)
	}
	if closed {
		t.Fatal("expected review and parent to remain open on unsupported adjustment type")
	}
}

func TestHandleApprovedAdjustmentFailsOnModifyWithoutTarget(t *testing.T) {
	t.Parallel()

	reviewView := "{\"body\":\"## Adjustment Review\\n\\n```json\\n{\\\"proposed_adjustments\\\":[{\\\"type\\\":\\\"modify\\\",\\\"description\\\":\\\"Tighten milestone scope\\\"}]}\\n```\"}"

	var closed bool
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:   "owner/repo",
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
	}
	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		if strings.Contains(args, "issue edit 88") || strings.Contains(args, "issue close 88") || strings.Contains(args, "issue edit 9") || strings.Contains(args, "issue close 9") {
			closed = true
			return nil
		}
		t.Fatalf("unexpected command: %s %s", req.Name, args)
		return nil
	}

	result := runner.handleApprovedAdjustment(t.Context(), reviewView, 88, "9", comments.ItemSelection{})
	if result != 1 {
		t.Fatalf("handleApprovedAdjustment = %d, want 1", result)
	}
	if closed {
		t.Fatal("expected review and parent to remain open when modify adjustment is missing target milestone")
	}
}

func TestHandleBootstrapUsesConfiguredPlanningMaxRounds(t *testing.T) {
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:              "owner/repo",
			PlanFile:          "docs/plan.md",
			RunoqRoot:         t.TempDir(),
			ReadyLabel:        "runoq:ready",
			InProgressLabel:   "runoq:in-progress",
			DoneLabel:         "runoq:done",
			NeedsReviewLabel:  "runoq:needs-human-review",
			BlockedLabel:      "runoq:blocked",
			MaxPlanningRounds: 7,
			Stdout:            io.Discard,
			Stderr:            io.Discard,
		},
	}

	previous := runPlanningDispatch
	t.Cleanup(func() {
		runPlanningDispatch = previous
	})

	var gotMaxRounds int
	runPlanningDispatch = func(_ context.Context, cfg planning.DispatchConfig) (planning.DispatchResult, error) {
		gotMaxRounds = cfg.MaxRounds
		return planning.DispatchResult{FormattedBody: "<!-- runoq:payload:plan-proposal -->\n"}, nil
	}

	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "api user --jq .login"):
			_, _ = io.WriteString(req.Stdout, "operator\n")
		case strings.Contains(args, "api repos/owner/repo/issues/") && strings.Contains(args, "--jq .node_id"):
			_, _ = io.WriteString(req.Stdout, "NODE_ID\n")
		case strings.Contains(args, "api repos/owner/repo/issues/") && strings.Contains(args, "--jq .id"):
			_, _ = io.WriteString(req.Stdout, "1\n")
		case strings.Contains(args, "api repos/owner/repo/issues/") && strings.Contains(args, "/sub_issues") && strings.Contains(args, "--method POST"):
			return nil
		case strings.Contains(args, "api graphql"):
			return nil
		case strings.Contains(args, "issue edit") && strings.Contains(args, "--add-assignee"):
			return nil
		case strings.Contains(args, "issue view") && strings.Contains(args, "--jq .body //"):
			_, _ = io.WriteString(req.Stdout, "")
		case strings.Contains(args, "issue create"):
			_, _ = io.WriteString(req.Stdout, "https://example.test/issues/1")
		case strings.Contains(args, "issue edit"), strings.Contains(args, "issue comment"), strings.Contains(args, "issue view"):
			return nil
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
		}
		return nil
	}

	if result := runner.handleBootstrap(t.Context()); result != 0 {
		t.Fatalf("handleBootstrap = %d, want 0", result)
	}
	if gotMaxRounds != 7 {
		t.Fatalf("dispatch MaxRounds = %d, want 7", gotMaxRounds)
	}
}

func TestHandlePlanningDispatchUsesConfiguredPlanningMaxRounds(t *testing.T) {
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:              "owner/repo",
			PlanFile:          "docs/plan.md",
			RunoqRoot:         t.TempDir(),
			ReadyLabel:        "runoq:ready",
			InProgressLabel:   "runoq:in-progress",
			DoneLabel:         "runoq:done",
			NeedsReviewLabel:  "runoq:needs-human-review",
			BlockedLabel:      "runoq:blocked",
			MaxPlanningRounds: 9,
			Stdout:            io.Discard,
			Stderr:            io.Discard,
		},
	}

	previous := runPlanningDispatch
	t.Cleanup(func() {
		runPlanningDispatch = previous
	})

	var gotMaxRounds int
	runPlanningDispatch = func(_ context.Context, cfg planning.DispatchConfig) (planning.DispatchResult, error) {
		gotMaxRounds = cfg.MaxRounds
		return planning.DispatchResult{FormattedBody: "<!-- runoq:payload:plan-proposal -->\n"}, nil
	}

	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "api user --jq .login"):
			_, _ = io.WriteString(req.Stdout, "operator\n")
		case strings.Contains(args, "issue edit") && strings.Contains(args, "--add-assignee"):
			return nil
		case strings.Contains(args, "issue view 88") && strings.Contains(args, "--json body"):
			_, _ = io.WriteString(req.Stdout, `{"body":""}`)
		case strings.Contains(args, "issue edit"), strings.Contains(args, "issue comment"), strings.Contains(args, "api repos/owner/repo/issues/9/sub_issues"), strings.Contains(args, "api graphql"):
			return nil
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
		}
		return nil
	}

	planningChild := &issue{Number: 88, Title: "Break down milestone", State: "OPEN", IssueType: "planning", ParentEpic: 9}
	epic := &issue{Number: 9, Title: "Milestone", State: "OPEN", IssueType: "epic"}

	if result := runner.handlePlanningDispatch(t.Context(), planningChild, epic, epic.Title); result != 0 {
		t.Fatalf("handlePlanningDispatch = %d, want 0", result)
	}
	if gotMaxRounds != 9 {
		t.Fatalf("dispatch MaxRounds = %d, want 9", gotMaxRounds)
	}
}

func TestHandleBootstrapFailsWhenPlanningIssueAssignmentFails(t *testing.T) {
	var stdout bytes.Buffer
	runner := &tickRunner{
		cfg: TickConfig{
			Repo:              "owner/repo",
			PlanFile:          "docs/plan.md",
			RunoqRoot:         t.TempDir(),
			ReadyLabel:        "runoq:ready",
			InProgressLabel:   "runoq:in-progress",
			DoneLabel:         "runoq:done",
			NeedsReviewLabel:  "runoq:needs-human-review",
			BlockedLabel:      "runoq:blocked",
			MaxPlanningRounds: 7,
			Stdout:            &stdout,
			Stderr:            io.Discard,
		},
	}

	previous := runPlanningDispatch
	t.Cleanup(func() {
		runPlanningDispatch = previous
	})

	runPlanningDispatch = func(_ context.Context, cfg planning.DispatchConfig) (planning.DispatchResult, error) {
		return planning.DispatchResult{FormattedBody: "<!-- runoq:payload:plan-proposal -->\n"}, nil
	}

	runner.cfg.ExecCommand = func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "api user --jq .login"):
			_, _ = io.WriteString(req.Stdout, "operator\n")
		case strings.Contains(args, "api repos/owner/repo/issues/") && strings.Contains(args, "--jq .node_id"):
			_, _ = io.WriteString(req.Stdout, "NODE_ID\n")
		case strings.Contains(args, "api repos/owner/repo/issues/") && strings.Contains(args, "--jq .id"):
			_, _ = io.WriteString(req.Stdout, "1\n")
		case strings.Contains(args, "api repos/owner/repo/issues/") && strings.Contains(args, "/sub_issues") && strings.Contains(args, "--method POST"):
			return nil
		case strings.Contains(args, "api graphql"):
			return nil
		case strings.Contains(args, "issue edit 1") && strings.Contains(args, "--add-assignee"):
			return errors.New("assignment failed")
		case strings.Contains(args, "issue view") && strings.Contains(args, "--jq .body //"):
			_, _ = io.WriteString(req.Stdout, "")
		case strings.Contains(args, "issue create"):
			_, _ = io.WriteString(req.Stdout, "https://example.test/issues/1")
		case strings.Contains(args, "issue edit"), strings.Contains(args, "issue comment"), strings.Contains(args, "issue view"):
			return nil
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
		}
		return nil
	}

	if result := runner.handleBootstrap(t.Context()); result != 1 {
		t.Fatalf("handleBootstrap = %d, want 1", result)
	}
	if strings.Contains(stdout.String(), "Created planning milestone") {
		t.Fatalf("expected bootstrap to fail close before success output, got %q", stdout.String())
	}
}

func TestExtractJSONFromCodeFence(t *testing.T) {
	t.Parallel()
	body := "## Title\n\n<details>\n<summary>Raw JSON payload</summary>\n\n```json\n{\"key\":\"value\"}\n```\n\n</details>\n"
	got := extractJSONFromCodeFence(body)
	if got != `{"key":"value"}` {
		t.Errorf("expected {\"key\":\"value\"}, got %q", got)
	}
}

func TestExtractJSONFromCodeFenceNoFence(t *testing.T) {
	t.Parallel()
	body := "plain text"
	got := extractJSONFromCodeFence(body)
	if got != body {
		t.Errorf("expected body unchanged, got %q", got)
	}
}
