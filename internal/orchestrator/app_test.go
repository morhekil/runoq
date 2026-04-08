package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

type fakeExitError struct {
	code int
}

func (e fakeExitError) Error() string {
	return "command failed"
}

func (e fakeExitError) ExitCode() int {
	return e.code
}

func TestParseStateFromCommentsExtractsLatest(t *testing.T) {
	comments := `[
		{"body": "<!-- runoq:event:init -->\n<!-- runoq:state:{\"phase\":\"INIT\",\"pr_number\":87} -->\n> Posted by orchestrator"},
		{"body": "<!-- runoq:event:develop -->\n<!-- runoq:state:{\"phase\":\"DEVELOP\",\"round\":1,\"pr_number\":87} -->\n> Posted by orchestrator"}
	]`
	stateJSON, err := parseStateFromComments(comments)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stateJSON, `"phase":"DEVELOP"`) {
		t.Fatalf("expected latest state (DEVELOP), got %q", stateJSON)
	}
	if !strings.Contains(stateJSON, `"round":1`) {
		t.Fatalf("expected round in state, got %q", stateJSON)
	}
}

func TestParseStateFromCommentsNoStateBlock(t *testing.T) {
	comments := `[
		{"body": "<!-- runoq:event:init -->\n> Posted by orchestrator — init phase\n\nOrchestrator initialized."}
	]`
	stateJSON, err := parseStateFromComments(comments)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stateJSON != "" {
		t.Fatalf("expected empty state for old-format comments, got %q", stateJSON)
	}
}

func TestAuditCommentFormatsStateBlock(t *testing.T) {
	// formatAuditComment should embed state JSON when provided
	body := formatAuditComment("develop", `{"phase":"DEVELOP","round":1}`, "## Develop\n| Field | Value |")
	if !strings.Contains(body, `<!-- runoq:event:develop -->`) {
		t.Fatalf("expected event marker, got %q", body)
	}
	if !strings.Contains(body, `<!-- runoq:state:{"phase":"DEVELOP","round":1} -->`) {
		t.Fatalf("expected state block, got %q", body)
	}
	if !strings.Contains(body, "## Develop") {
		t.Fatalf("expected body content, got %q", body)
	}
}

func TestAuditCommentOmitsStateBlockWhenEmpty(t *testing.T) {
	body := formatAuditComment("init", "", "Orchestrator initialized.")
	if strings.Contains(body, "runoq:state") {
		t.Fatalf("expected no state block for empty state, got %q", body)
	}
	if !strings.Contains(body, `<!-- runoq:event:init -->`) {
		t.Fatalf("expected event marker, got %q", body)
	}
}

func TestAuditCommentRoundTrip(t *testing.T) {
	stateJSON := `{"phase":"REVIEW","round":2,"pr_number":87,"verdict":"PASS"}`
	body := formatAuditComment("review", stateJSON, "Review verdict table")

	// Wrap in comments array and parse back
	commentsJSON := `[{"body":` + strconv.Quote(body) + `}]`
	parsed, err := parseStateFromComments(commentsJSON)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if parsed != stateJSON {
		t.Fatalf("round-trip failed: got %q, want %q", parsed, stateJSON)
	}
}

func TestDeriveStateFromGitHubFindsLinkedPR(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout, stderr bytes.Buffer
	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)

	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, `closes #42`):
			_, _ = io.WriteString(req.Stdout, `[{"number":87,"headRefName":"runoq/42-implement-queue"}]`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "comments"):
			_, _ = io.WriteString(req.Stdout, `{"comments":[{"body":"<!-- runoq:event:init -->\n<!-- runoq:state:{\"phase\":\"INIT\",\"pr_number\":87,\"branch\":\"runoq/42-implement-queue\",\"worktree\":\"/tmp/runoq-wt-42\"} -->\n> Posted by orchestrator"},{"body":"<!-- runoq:event:develop -->\n<!-- runoq:state:{\"phase\":\"DEVELOP\",\"round\":1,\"pr_number\":87,\"branch\":\"runoq/42-implement-queue\",\"worktree\":\"/tmp/runoq-wt-42\",\"cumulative_tokens\":12} -->\n> Posted by orchestrator"}]}`)
			return nil
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
			return nil
		}
	})

	stateJSON, prNumber, found, err := app.deriveStateFromGitHub(ctx, app.env, "owner/repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected state to be found")
	}
	if prNumber != 87 {
		t.Fatalf("expected PR 87, got %d", prNumber)
	}
	if !strings.Contains(stateJSON, `"phase":"DEVELOP"`) {
		t.Fatalf("expected DEVELOP phase, got %q", stateJSON)
	}
	if !strings.Contains(stateJSON, `"round":1`) {
		t.Fatalf("expected round 1, got %q", stateJSON)
	}
}

func TestDeriveStateFromGitHubNoPR(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout, stderr bytes.Buffer
	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)

	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, `closes #42`):
			_, _ = io.WriteString(req.Stdout, `[]`)
			return nil
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
			return nil
		}
	})

	_, _, found, err := app.deriveStateFromGitHub(ctx, app.env, "owner/repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected no state found for issue without PR")
	}
}

func TestDeriveStateFromGitHubOldFormatComments(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout, stderr bytes.Buffer
	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)

	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, `closes #42`):
			_, _ = io.WriteString(req.Stdout, `[{"number":87,"headRefName":"runoq/42-implement-queue"}]`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "comments"):
			_, _ = io.WriteString(req.Stdout, `{"comments":[{"body":"<!-- runoq:event:init -->\n> Posted by orchestrator — init phase\n\nOrchestrator initialized. Branch: runoq/42"}]}`)
			return nil
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
			return nil
		}
	})

	stateJSON, prNumber, found, err := app.deriveStateFromGitHub(ctx, app.env, "owner/repo", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected state found (PR exists even without state blocks)")
	}
	if prNumber != 87 {
		t.Fatalf("expected PR 87, got %d", prNumber)
	}
	// Old format: no state block, so derive phase from event marker
	if !strings.Contains(stateJSON, `"phase":"INIT"`) {
		t.Fatalf("expected INIT phase derived from event marker, got %q", stateJSON)
	}
}

func TestParseStateFromCommentsEmpty(t *testing.T) {
	stateJSON, err := parseStateFromComments("[]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stateJSON != "" {
		t.Fatalf("expected empty state for no comments, got %q", stateJSON)
	}
}

func TestRunProgramTeesOutputToLogWriter(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()

	var logBuf bytes.Buffer
	var stderr bytes.Buffer
	app := New(nil, []string{"RUNOQ_ROOT=" + root, "TARGET_ROOT=" + root}, root, io.Discard, &stderr)
	app.SetLogWriter(&logBuf)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		_, _ = io.WriteString(req.Stdout, "stdout-data\n")
		_, _ = io.WriteString(req.Stderr, "stderr-data\n")
		return nil
	})

	// Caller passes io.Discard — output should still reach the log writer
	err := app.runProgram(ctx, app.env, "test-program", nil, nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("runProgram: %v", err)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "stdout-data") {
		t.Fatalf("expected stdout in log, got %q", logOutput)
	}
	if !strings.Contains(logOutput, "stderr-data") {
		t.Fatalf("expected stderr in log, got %q", logOutput)
	}
}

func TestReplaceMarkerContent(t *testing.T) {
	body := "## Summary\n<!-- runoq:summary:start -->\nPending.\n<!-- runoq:summary:end -->\n\n## Linked Issue\nCloses #42\n"
	updated := replaceMarkerContent(body, "<!-- runoq:summary:start -->", "<!-- runoq:summary:end -->", "Implemented queue processing.")
	if !strings.Contains(updated, "Implemented queue processing.") {
		t.Fatalf("expected updated summary, got %q", updated)
	}
	if strings.Contains(updated, "Pending.") {
		t.Fatalf("expected old summary removed, got %q", updated)
	}
	if !strings.Contains(updated, "Closes #42") {
		t.Fatalf("expected linked issue preserved, got %q", updated)
	}
}

func TestReplaceMarkerContentNoMarkers(t *testing.T) {
	body := "No markers here."
	updated := replaceMarkerContent(body, "<!-- start -->", "<!-- end -->", "replacement")
	if updated != body {
		t.Fatalf("expected unchanged body, got %q", updated)
	}
}

func TestMetadataFromIssueViewFallsBackToBodyBlock(t *testing.T) {
	meta := metadataFromIssueView(issueView{
		Number: 42,
		Title:  "Implement queue",
		Body: `<!-- runoq:meta
estimated_complexity: high
complexity_rationale: touches queue scheduling
type: epic
-->

## Acceptance Criteria

- [ ] Works.`,
		URL: "https://example.test/issues/42",
	})

	if meta.EstimatedComplexity != "high" {
		t.Fatalf("expected fallback complexity, got %q", meta.EstimatedComplexity)
	}
	if meta.ComplexityRationale == nil || *meta.ComplexityRationale != "touches queue scheduling" {
		t.Fatalf("expected fallback rationale, got %#v", meta.ComplexityRationale)
	}
	if meta.Type != "epic" {
		t.Fatalf("expected fallback type, got %q", meta.Type)
	}
}

func TestRunIssueDryRunDoesNotForceShellForMigratedHelpers(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	app := New([]string{"run", "owner/repo", "--issue", "42", "--dry-run"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_identity"):
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_remote"):
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "reconcile owner/repo":
			assertEnvNotValue(t, req.Env, "RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION", "shell")
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json title":
			_, _ = io.WriteString(req.Stdout, `{"title":"Implement queue"}`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json number,title,body,labels,url":
			_, _ = io.WriteString(req.Stdout, `{"number":42,"title":"Implement queue","body":"<!-- runoq:meta\nestimated_complexity: low\ntype: task\n-->\n","labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "list owner/repo runoq:ready":
			assertEnvNotValue(t, req.Env, "RUNOQ_ISSUE_QUEUE_IMPLEMENTATION", "shell")
			_, _ = io.WriteString(req.Stdout, `[{"number":42,"title":"Implement queue","body":"body","url":"https://example.test/issues/42","estimated_complexity":"low","type":"task"}]`)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "eligibility owner/repo 42":
			assertEnvNotValue(t, req.Env, "RUNOQ_DISPATCH_SAFETY_IMPLEMENTATION", "shell")
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-implement-queue","reasons":[]}`)
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) != `{"branch":"runoq/42-implement-queue","dry_run":true,"issue":42,"phase":"INIT"}` {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestPhaseInitPRCreateFailureRollsBackAndCleansUp(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var calls []string

	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
	}, root, io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "eligibility owner/repo 42":
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-implement-queue","reasons":[]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 in-progress":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "create 42 Implement queue":
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42"}`)
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_remote"):
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 commit --allow-empty -m runoq: begin work on #42":
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 push -u origin runoq/42-implement-queue":
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "create owner/repo runoq/42-implement-queue 42 Implement queue":
			_, _ = io.WriteString(req.Stderr, "aborted: push first")
			return errors.New("pr create failed")
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 ready":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "remove 42":
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	_, err := app.phaseInit(ctx, root, app.env, "owner/repo", 42, false, "Implement queue")
	if err == nil {
		t.Fatal("expected init failure")
	}
	if !containsCall(calls, "set-status owner/repo 42 ready") {
		t.Fatalf("expected ready rollback call, got %v", calls)
	}
	if !containsCall(calls, "worktree.sh remove 42") {
		t.Fatalf("expected worktree removal call, got %v", calls)
	}
}

func TestRunLowComplexityDevelopFailureCompletesNeedsReviewHandoff(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var calls []string

	app := New([]string{"run", "owner/repo", "--issue", "42"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_identity"):
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_remote"):
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "runoq::claude_stream"):
			if len(req.Args) < 7 {
				t.Fatalf("expected claude invocation args, got %v", req.Args)
			}
			reviewOutputPath := req.Args[4]
			if err := os.WriteFile(reviewOutputPath, []byte("REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 42\nCHECKLIST:\n- Acceptance criteria satisfied.\n"), 0o644); err != nil {
				t.Fatalf("write review output: %v", err)
			}
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "reconcile owner/repo":
			return nil
		case req.Name == "gh" && strings.Contains(strings.Join(req.Args, " "), "pr list") && strings.Contains(strings.Join(req.Args, " "), "closes #42"):
			_, _ = io.WriteString(req.Stdout, `[]`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json title":
			_, _ = io.WriteString(req.Stdout, `{"title":"Implement queue"}`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json number,title,body,labels,url":
			_, _ = io.WriteString(req.Stdout, `{"number":42,"title":"Implement queue","body":"<!-- runoq:meta\nestimated_complexity: low\ntype: task\n-->\n\n## Acceptance Criteria\n\n- [ ] Works.","labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "list owner/repo runoq:ready":
			_, _ = io.WriteString(req.Stdout, `[{"number":42,"title":"Implement queue","body":"body","url":"https://example.test/issues/42","estimated_complexity":"low","type":"task"}]`)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "eligibility owner/repo 42":
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-implement-queue","reasons":[]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 in-progress":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "create 42 Implement queue":
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42"}`)
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 commit --allow-empty -m runoq: begin work on #42":
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 push -u origin runoq/42-implement-queue":
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "create owner/repo runoq/42-implement-queue 42 Implement queue":
			_, _ = io.WriteString(req.Stdout, `{"url":"https://example.test/pull/87","number":87}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "comment owner/repo 87 "):
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json body":
			_, _ = io.WriteString(req.Stdout, `{"body":"## Acceptance Criteria\n\n- [ ] Works."}`)
			return nil
		case strings.HasSuffix(req.Name, "/issue-runner.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "run "):
			_, _ = io.WriteString(req.Stdout, `{"status":"fail","logDir":"log/issue-42","baselineHash":"base","headHash":"head","commitRange":"base..head","reviewLogPath":"log/issue-42/round-1-diff-review.md","specRequirements":"## Acceptance Criteria","changedFiles":[],"relatedFiles":[],"cumulativeTokens":0,"verificationPassed":false,"verificationFailures":["no new commits were created"],"caveats":["verification failed"],"summary":"Verification failed after round 1"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "finalize owner/repo 87 needs-review --reviewer username":
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 needs-review":
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatal("expected final state on stdout")
	}
	if !strings.Contains(stdout.String(), `"phase":"DONE"`) {
		t.Fatalf("expected DONE state on stdout, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"finalize_verdict":"needs-review"`) {
		t.Fatalf("expected needs-review finalize verdict, got %q", stdout.String())
	}
	if !containsCall(calls, "gh-pr-lifecycle.sh finalize owner/repo 87 needs-review --reviewer username") {
		t.Fatalf("expected needs-review finalize call, got %v", calls)
	}
	if !containsCall(calls, "gh-issue-queue.sh set-status owner/repo 42 needs-review") {
		t.Fatalf("expected needs-review status update, got %v", calls)
	}
	if !strings.Contains(stderr.String(), "DEVELOP: issue #42 requires deterministic needs-review handoff") {
		t.Fatalf("expected develop handoff log, got %q", stderr.String())
	}
}

func TestRunLowComplexityReviewReadyAutoMergesAndCleansUp(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var calls []string
	var updatedPRBody string
	var capturedCommentBodies []string

	app := New([]string{"run", "owner/repo", "--issue", "42"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_identity"):
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_remote"):
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "runoq::claude_stream"):
			if len(req.Args) < 7 {
				t.Fatalf("expected claude invocation args, got %v", req.Args)
			}
			reviewOutputPath := req.Args[4]
			if err := os.WriteFile(reviewOutputPath, []byte("REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 42\nCHECKLIST:\n- Acceptance criteria satisfied.\n"), 0o644); err != nil {
				t.Fatalf("write review output: %v", err)
			}
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "reconcile owner/repo":
			return nil
		case req.Name == "gh" && strings.Contains(strings.Join(req.Args, " "), "pr list") && strings.Contains(strings.Join(req.Args, " "), "closes #42"):
			_, _ = io.WriteString(req.Stdout, `[]`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json title":
			_, _ = io.WriteString(req.Stdout, `{"title":"Implement queue"}`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json number,title,body,labels,url":
			_, _ = io.WriteString(req.Stdout, `{"number":42,"title":"Implement queue","body":"<!-- runoq:meta\nestimated_complexity: low\ntype: task\n-->\n\n## Acceptance Criteria\n\n- [ ] Works.","labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "list owner/repo runoq:ready":
			_, _ = io.WriteString(req.Stdout, `[{"number":42,"title":"Implement queue","body":"body","url":"https://example.test/issues/42","estimated_complexity":"low","type":"task"}]`)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "eligibility owner/repo 42":
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-implement-queue","reasons":[]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 in-progress":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "create 42 Implement queue":
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42"}`)
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 commit --allow-empty -m runoq: begin work on #42":
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 push -u origin runoq/42-implement-queue":
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "create owner/repo runoq/42-implement-queue 42 Implement queue":
			_, _ = io.WriteString(req.Stdout, `{"url":"https://example.test/pull/87","number":87}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "comment owner/repo 87 "):
			commentFile := req.Args[len(req.Args)-1]
			body, err := os.ReadFile(commentFile)
			if err == nil {
				capturedCommentBodies = append(capturedCommentBodies, string(body))
			}
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json body":
			_, _ = io.WriteString(req.Stdout, `{"body":"## Acceptance Criteria\n\n- [ ] Works."}`)
			return nil
		case strings.HasSuffix(req.Name, "/issue-runner.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "run "):
			_, _ = io.WriteString(req.Stdout, `{"status":"review_ready","logDir":"log/issue-42","baselineHash":"base","headHash":"head","commitRange":"base..head","reviewLogPath":"log/issue-42/round-1-diff-review.md","specRequirements":"## Acceptance Criteria","changedFiles":["src/queue.ts"],"relatedFiles":["src/queue.ts"],"cumulativeTokens":12,"verificationPassed":true,"verificationFailures":[],"caveats":[],"summary":"Verification passed on round 1; ready for review"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "finalize owner/repo 87 auto-merge":
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 done":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "remove 42":
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "pr view 87 --repo owner/repo --json body":
			_, _ = io.WriteString(req.Stdout, `{"body":"## Summary\n<!-- runoq:summary:start -->\nPending.\n<!-- runoq:summary:end -->\n\n## Linked Issue\nCloses #42\n"}`)
			return nil
		case req.Name == "gh" && len(req.Args) >= 5 && strings.Join(req.Args[:5], " ") == "pr edit 87 --repo owner/repo" && strings.HasPrefix(strings.Join(req.Args[5:], " "), "--body-file"):
			bodyFile := req.Args[6]
			body, err := os.ReadFile(bodyFile)
			if err != nil {
				t.Fatalf("read PR body file: %v", err)
			}
			updatedPRBody = string(body)
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) == "" {
		t.Fatal("expected final state on stdout")
	}
	if !strings.Contains(stdout.String(), `"phase":"DONE"`) {
		t.Fatalf("expected DONE state on stdout, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"status":"review_ready"`) {
		t.Fatalf("expected review_ready status on stdout, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"finalize_verdict":"auto-merge"`) {
		t.Fatalf("expected auto-merge finalize verdict, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"issue_status":"done"`) {
		t.Fatalf("expected done issue status, got %q", stdout.String())
	}
	if !containsCall(calls, "gh-pr-lifecycle.sh finalize owner/repo 87 auto-merge") {
		t.Fatalf("expected auto-merge finalize call, got %v", calls)
	}
	if !containsCall(calls, "gh-issue-queue.sh set-status owner/repo 42 done") {
		t.Fatalf("expected done status update, got %v", calls)
	}
	if !containsCall(calls, "worktree.sh remove 42") {
		t.Fatalf("expected worktree removal call, got %v", calls)
	}
	if !strings.Contains(stderr.String(), "REVIEW: verdict=PASS score=42") {
		t.Fatalf("expected parsed review verdict log, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "FINALIZE: removing worktree for issue #42 (auto-merged)") {
		t.Fatalf("expected finalize cleanup log, got %q", stderr.String())
	}
	if !strings.Contains(updatedPRBody, "Verification passed on round 1; ready for review") {
		t.Fatalf("expected summary in updated PR body, got %q", updatedPRBody)
	}
	if strings.Contains(updatedPRBody, "Pending.") {
		t.Fatalf("expected Pending placeholder replaced in PR body, got %q", updatedPRBody)
	}
	if !strings.Contains(updatedPRBody, "## Final Status") {
		t.Fatalf("expected Final Status section in PR body, got %q", updatedPRBody)
	}
	if !strings.Contains(updatedPRBody, "PASS") {
		t.Fatalf("expected verdict in PR body, got %q", updatedPRBody)
	}
	if !strings.Contains(updatedPRBody, "Closes #42") {
		t.Fatalf("expected linked issue preserved in PR body, got %q", updatedPRBody)
	}
	// Verify audit comments contain state blocks
	stateBlockCount := 0
	for _, body := range capturedCommentBodies {
		if strings.Contains(body, "<!-- runoq:state:") {
			stateBlockCount++
		}
	}
	if stateBlockCount == 0 {
		t.Fatalf("expected audit comments to contain state blocks, got %d comments: %v", len(capturedCommentBodies), capturedCommentBodies)
	}
}

func TestRunLowComplexityIterateReentersDevelopWithChecklist(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var calls []string
	var runnerPayloads []string
	reviewCalls := 0

	app := New([]string{"run", "owner/repo", "--issue", "42"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_identity"):
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "configure_git_bot_remote"):
			return nil
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "runoq::claude_stream"):
			if len(req.Args) < 5 {
				t.Fatalf("expected claude invocation args, got %v", req.Args)
			}
			reviewCalls++
			reviewOutputPath := req.Args[4]
			body := "REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 42\nCHECKLIST:\n- Final checklist item.\n"
			if reviewCalls == 1 {
				body = "REVIEW-TYPE: diff-review\nVERDICT: ITERATE\nSCORE: 21\nCHECKLIST:\n- First checklist item.\n"
			}
			if err := os.WriteFile(reviewOutputPath, []byte(body), 0o644); err != nil {
				t.Fatalf("write review output: %v", err)
			}
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "reconcile owner/repo":
			return nil
		case req.Name == "gh" && strings.Contains(strings.Join(req.Args, " "), "pr list") && strings.Contains(strings.Join(req.Args, " "), "closes #42"):
			_, _ = io.WriteString(req.Stdout, `[]`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json title":
			_, _ = io.WriteString(req.Stdout, `{"title":"Implement queue"}`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json number,title,body,labels,url":
			_, _ = io.WriteString(req.Stdout, `{"number":42,"title":"Implement queue","body":"<!-- runoq:meta\nestimated_complexity: low\ntype: task\n-->\n\n## Acceptance Criteria\n\n- [ ] Works.","labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "list owner/repo runoq:ready":
			_, _ = io.WriteString(req.Stdout, `[{"number":42,"title":"Implement queue","body":"body","url":"https://example.test/issues/42","estimated_complexity":"low","type":"task"}]`)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "eligibility owner/repo 42":
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-implement-queue","reasons":[]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 in-progress":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "create 42 Implement queue":
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42"}`)
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 commit --allow-empty -m runoq: begin work on #42":
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 push -u origin runoq/42-implement-queue":
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "create owner/repo runoq/42-implement-queue 42 Implement queue":
			_, _ = io.WriteString(req.Stdout, `{"url":"https://example.test/pull/87","number":87}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "comment owner/repo 87 "):
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json body":
			_, _ = io.WriteString(req.Stdout, `{"body":"## Acceptance Criteria\n\n- [ ] Works."}`)
			return nil
		case strings.HasSuffix(req.Name, "/issue-runner.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "run "):
			payloadFile := req.Args[1]
			payloadBody, err := os.ReadFile(payloadFile)
			if err != nil {
				t.Fatalf("read payload: %v", err)
			}
			runnerPayloads = append(runnerPayloads, string(payloadBody))
			if len(runnerPayloads) == 1 {
				_, _ = io.WriteString(req.Stdout, `{"status":"review_ready","logDir":"log/issue-42","baselineHash":"base-1","headHash":"head-1","commitRange":"base-1..head-1","reviewLogPath":"log/issue-42/round-1-diff-review.md","specRequirements":"## Acceptance Criteria","changedFiles":["src/queue.ts"],"relatedFiles":["src/queue.ts"],"cumulativeTokens":12,"verificationPassed":true,"verificationFailures":[],"caveats":[],"summary":"Round 1 ready for review"}`)
				return nil
			}
			_, _ = io.WriteString(req.Stdout, `{"status":"review_ready","logDir":"log/issue-42","baselineHash":"base-2","headHash":"head-2","commitRange":"base-2..head-2","reviewLogPath":"log/issue-42/round-2-diff-review.md","specRequirements":"## Acceptance Criteria","changedFiles":["src/queue.ts"],"relatedFiles":["src/queue.ts"],"cumulativeTokens":24,"verificationPassed":true,"verificationFailures":[],"caveats":[],"summary":"Round 2 ready for review"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "finalize owner/repo 87 auto-merge":
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 done":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "remove 42":
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "pr view 87 --repo owner/repo --json body":
			_, _ = io.WriteString(req.Stdout, `{"body":"## Summary\n<!-- runoq:summary:start -->\nPending.\n<!-- runoq:summary:end -->\n\n## Linked Issue\nCloses #42\n"}`)
			return nil
		case req.Name == "gh" && len(req.Args) >= 5 && strings.Join(req.Args[:5], " ") == "pr edit 87 --repo owner/repo" && strings.HasPrefix(strings.Join(req.Args[5:], " "), "--body-file"):
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if len(runnerPayloads) != 2 {
		t.Fatalf("expected two issue-runner rounds, got %d payloads", len(runnerPayloads))
	}
	if strings.Contains(runnerPayloads[0], `"previousChecklist"`) {
		t.Fatalf("did not expect previousChecklist in first round payload: %s", runnerPayloads[0])
	}
	if !strings.Contains(runnerPayloads[1], `"previousChecklist":"- First checklist item."`) {
		t.Fatalf("expected checklist carry-over in second round payload, got %s", runnerPayloads[1])
	}
	if !strings.Contains(stdout.String(), `"phase":"DONE"`) {
		t.Fatalf("expected DONE state on stdout, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"round":2`) {
		t.Fatalf("expected final round 2 on stdout, got %q", stdout.String())
	}
	if !containsCall(calls, "gh-pr-lifecycle.sh finalize owner/repo 87 auto-merge") {
		t.Fatalf("expected auto-merge finalize call, got %v", calls)
	}
	if !containsCall(calls, "gh-issue-queue.sh set-status owner/repo 42 done") {
		t.Fatalf("expected done status update, got %v", calls)
	}
	if !strings.Contains(stderr.String(), "DECIDE: verdict=ITERATE round=1/5") {
		t.Fatalf("expected iterate decide log, got %q", stderr.String())
	}
}

func TestRunMediumComplexityGoesToDevelopAndAutoMerges(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var calls []string

	app := New([]string{"run", "owner/repo", "--issue", "42"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		args := strings.Join(req.Args, " ")
		switch {
		case req.Name == "bash" && strings.Contains(args, "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case req.Name == "bash" && strings.Contains(args, "configure_git_bot_identity"):
			return nil
		case req.Name == "bash" && strings.Contains(args, "configure_git_bot_remote"):
			return nil
		case req.Name == "bash" && strings.Contains(args, "runoq::claude_stream"):
			reviewOutputPath := req.Args[4]
			_ = os.WriteFile(reviewOutputPath, []byte("REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 38\nCHECKLIST:\n- Looks good.\n"), 0o644)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Contains(args, "reconcile"):
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, "closes #42"):
			_, _ = io.WriteString(req.Stdout, `[]`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "issue view 42") && strings.Contains(args, "json title"):
			_, _ = io.WriteString(req.Stdout, `{"title":"Coordinate migration"}`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "issue view 42") && strings.Contains(args, "number,title,body"):
			_, _ = io.WriteString(req.Stdout, `{"number":42,"title":"Coordinate migration","body":"<!-- runoq:meta\nestimated_complexity: medium\ntype: task\n-->\n\n## Acceptance Criteria\n\n- [ ] Coordinate migration.","labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Contains(args, "list"):
			_, _ = io.WriteString(req.Stdout, `[{"number":42,"title":"Coordinate migration","body":"body","url":"https://example.test/issues/42","estimated_complexity":"medium","type":"task"}]`)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Contains(args, "eligibility"):
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-coordinate-migration","reasons":[]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Contains(args, "set-status"):
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Contains(args, "create"):
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/42-coordinate-migration","worktree":"/tmp/runoq-wt-42"}`)
			return nil
		case req.Name == "git":
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Contains(args, "create"):
			_, _ = io.WriteString(req.Stdout, `{"url":"https://example.test/pull/87","number":87}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Contains(args, "comment"):
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Contains(args, "finalize"):
			return nil
		case req.Name == "gh" && strings.Contains(args, "issue view 42") && strings.Contains(args, "--json body"):
			_, _ = io.WriteString(req.Stdout, `{"body":"## AC\n\n- [ ] Coordinate migration."}`)
			return nil
		case strings.HasSuffix(req.Name, "/issue-runner.sh"):
			_, _ = io.WriteString(req.Stdout, `{"status":"review_ready","logDir":"log","baselineHash":"b","headHash":"h","commitRange":"b..h","reviewLogPath":"log/r.md","specRequirements":"AC","changedFiles":["f"],"relatedFiles":["f"],"cumulativeTokens":10,"verificationPassed":true,"verificationFailures":[],"caveats":[],"summary":"Done"}`)
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Contains(args, "remove"):
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "--json body"):
			_, _ = io.WriteString(req.Stdout, `{"body":"## Summary\n<!-- runoq:summary:start -->\nP.\n<!-- runoq:summary:end -->\n\nCloses #42\n"}`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr edit 87") && strings.Contains(args, "--body-file"):
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"phase":"DONE"`) {
		t.Fatalf("expected DONE state, got %q", stdout.String())
	}
	// Medium complexity + PASS → auto-merge (complexity gate removed, reviewer verdict is PASS)
	if !strings.Contains(stdout.String(), `"finalize_verdict":"auto-merge"`) {
		t.Fatalf("expected auto-merge finalize verdict, got %q", stdout.String())
	}
	// Must have invoked issue-runner (went through DEVELOP)
	if !containsCall(calls, "issue-runner.sh run") {
		t.Fatalf("expected issue-runner invocation for medium complexity, got %v", calls)
	}
	// Must have gone through REVIEW
	if !strings.Contains(stderr.String(), "REVIEW:") {
		t.Fatalf("expected REVIEW phase, got %q", stderr.String())
	}
}

func TestRunCommandEntryRequiresIssueFlag(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stderr bytes.Buffer
	app := New([]string{"run", "owner/repo"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, io.Discard, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case req.Name == "bash":
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh"):
			return nil
		default:
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 1 {
		t.Fatalf("expected exit code 1 without --issue, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--issue is required") {
		t.Fatalf("expected --issue required error, got %q", stderr.String())
	}
}

func TestPhaseIntegratePendingPersistsIntegratePendingDecision(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var calls []string

	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
	}, root, io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "epic-status owner/repo 41":
			_, _ = io.WriteString(req.Stdout, `{"all_done":false,"children":[42],"pending":[42]}`)
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	stateJSON, err := app.phaseIntegrate(ctx, root, app.env, "owner/repo", 41, `{"phase":"DECIDE","next_phase":"INTEGRATE"}`, "Coordinate migration")
	if err != nil {
		t.Fatalf("phaseIntegrate returned error: %v", err)
	}
	if !strings.Contains(stateJSON, `"phase":"DECIDE"`) {
		t.Fatalf("expected DECIDE phase, got %s", stateJSON)
	}
	if !strings.Contains(stateJSON, `"decision":"integrate-pending"`) {
		t.Fatalf("expected integrate-pending decision, got %s", stateJSON)
	}
	if containsCall(calls, "set-status owner/repo 41") {
		t.Fatalf("did not expect set-status mutation for pending integrate, got %v", calls)
	}
}

func TestPhaseIntegrateSuccessWithCriteriaCommitMarksDone(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var calls []string

	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
	}, root, io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "epic-status owner/repo 41":
			_, _ = io.WriteString(req.Stdout, `{"all_done":true,"children":[42],"pending":[]}`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 41 --repo owner/repo --json title":
			_, _ = io.WriteString(req.Stdout, `{"title":"Coordinate migration"}`)
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "create 41 Coordinate migration-integrate":
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/41-coordinate-migration-integrate","worktree":"/tmp/runoq-wt-41-integrate"}`)
			return nil
		case strings.HasSuffix(req.Name, "/verify.sh") && strings.Join(req.Args, " ") == "integrate /tmp/runoq-wt-41-integrate criteria-abc":
			_, _ = io.WriteString(req.Stdout, `{"ok":true}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 41 done":
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	stateJSON, err := app.phaseIntegrate(ctx, root, app.env, "owner/repo", 41, `{"phase":"DECIDE","next_phase":"INTEGRATE","criteria_commit":"criteria-abc"}`, "Coordinate migration")
	if err != nil {
		t.Fatalf("phaseIntegrate returned error: %v", err)
	}
	if !strings.Contains(stateJSON, `"phase":"DONE"`) {
		t.Fatalf("expected DONE phase, got %s", stateJSON)
	}
	if !containsCall(calls, "verify.sh integrate /tmp/runoq-wt-41-integrate criteria-abc") {
		t.Fatalf("expected verify integrate call, got %v", calls)
	}
	if !containsCall(calls, "gh-issue-queue.sh set-status owner/repo 41 done") {
		t.Fatalf("expected done status update, got %v", calls)
	}
}

func TestPhaseIntegrateFailureMarksNeedsReviewAndFailed(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var calls []string

	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
	}, root, io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "epic-status owner/repo 41":
			_, _ = io.WriteString(req.Stdout, `{"all_done":true,"children":[42],"pending":[]}`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 41 --repo owner/repo --json title":
			_, _ = io.WriteString(req.Stdout, `{"title":"Coordinate migration"}`)
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "create 41 Coordinate migration-integrate":
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/41-coordinate-migration-integrate","worktree":"/tmp/runoq-wt-41-integrate"}`)
			return nil
		case strings.HasSuffix(req.Name, "/verify.sh") && strings.Join(req.Args, " ") == "integrate /tmp/runoq-wt-41-integrate criteria-abc":
			_, _ = io.WriteString(req.Stdout, `{"ok":false,"failures":["criteria drift","tests failed"]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 41 needs-review":
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	stateJSON, err := app.phaseIntegrate(ctx, root, app.env, "owner/repo", 41, `{"phase":"DECIDE","next_phase":"INTEGRATE","criteria_commit":"criteria-abc"}`, "Coordinate migration")
	if err != nil {
		t.Fatalf("phaseIntegrate returned error: %v", err)
	}
	if !strings.Contains(stateJSON, `"phase":"FAILED"`) {
		t.Fatalf("expected FAILED phase, got %s", stateJSON)
	}
	if !containsCall(calls, "gh-issue-queue.sh set-status owner/repo 41 needs-review") {
		t.Fatalf("expected needs-review status update, got %v", calls)
	}
}

func TestMentionTriageReturnsEmptyStdoutWhenPollMentionsIsEmpty(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var calls []string

	app := New([]string{"mention-triage", "owner/repo", "87"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "poll-mentions owner/repo runoq":
			_, _ = io.WriteString(req.Stdout, "[]\n")
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout when no mentions are found, got %q", stdout.String())
	}
	if !containsCall(calls, "gh-pr-lifecycle.sh poll-mentions owner/repo runoq") {
		t.Fatalf("expected poll-mentions call, got %v", calls)
	}
	if !strings.Contains(stderr.String(), "Token mint failed or skipped") {
		t.Fatalf("expected auth log on stderr, got %q", stderr.String())
	}
}

func TestMentionTriageReturnsNotImplementedWhenMentionsExist(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := New([]string{"mention-triage", "owner/repo", "87"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "poll-mentions owner/repo runoq":
			_, _ = io.WriteString(req.Stdout, "[{\"comment_id\":3001}]\n")
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout when mentions exist, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "mention-triage with mentions not implemented") {
		t.Fatalf("expected not-implemented error, got %q", stderr.String())
	}
}

func TestMentionTriagePropagatesScriptExitCodeAndStderr(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	app := New([]string{"mention-triage", "owner/repo", "87"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		switch {
		case req.Name == "bash" && strings.Contains(strings.Join(req.Args, " "), "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "poll-mentions owner/repo runoq":
			_, _ = io.WriteString(req.Stderr, "poll failed\n")
			return fakeExitError{code: 23}
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 23 {
		t.Fatalf("expected exit code 23, got %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout on error, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "poll failed") {
		t.Fatalf("expected script stderr, got %q", stderr.String())
	}
}

func TestSetupReturnsAuthedEnvAndConfiguresIdentity(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var identityCalled, remoteCalled bool
	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"TARGET_ROOT=" + root,
	}, root, io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		cmd := commandLine(req)
		switch {
		case req.Name == "bash" && strings.Contains(cmd, "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "ok\ntest-token-123")
			return nil
		case req.Name == "bash" && strings.Contains(cmd, "configure_git_bot_identity"):
			identityCalled = true
			return nil
		case req.Name == "bash" && strings.Contains(cmd, "configure_git_bot_remote"):
			remoteCalled = true
			return nil
		default:
			return nil
		}
	})

	env := app.Setup(ctx, "owner/repo")
	if env == nil {
		t.Fatal("Setup returned nil env")
	}

	// Should have GH_TOKEN from auth
	token, ok := shell.EnvLookup(env, "GH_TOKEN")
	if !ok || token != "test-token-123" {
		t.Fatalf("expected GH_TOKEN=test-token-123, got %q (ok=%v)", token, ok)
	}

	if !identityCalled {
		t.Error("expected configureGitBotIdentity to be called")
	}
	if !remoteCalled {
		t.Error("expected configureGitBotRemote to be called")
	}
}

func TestRunIssueExportedMethodSkipsQueueSelection(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		switch {
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Contains(commandLine(req), "eligibility"):
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-implement-queue","reasons":[]}`)
			return nil
		default:
			return nil
		}
	})

	meta := IssueMetadata{
		Number:              42,
		Title:               "Implement queue",
		EstimatedComplexity: "low",
		Type:                "task",
	}
	stateJSON, err := app.RunIssue(ctx, "owner/repo", 42, true, "Implement queue", meta)
	if err != nil {
		t.Fatalf("RunIssue failed: %v", err)
	}
	if !strings.Contains(stateJSON, `"issue":42`) || !strings.Contains(stateJSON, `"dry_run":true`) {
		t.Fatalf("unexpected state: %s", stateJSON)
	}
}

func TestRunIssueDoesNotCallStateSave(t *testing.T) {
	// After M6, state.sh save should never be called — state is on GitHub
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout, stderr bytes.Buffer
	var calls []string

	app := New([]string{"run", "owner/repo", "--issue", "42"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		args := strings.Join(req.Args, " ")
		switch {
		case req.Name == "bash" && strings.Contains(args, "gh-auth.sh"):
			_, _ = io.WriteString(req.Stdout, "fail\n")
			return nil
		case req.Name == "bash" && strings.Contains(args, "configure_git_bot_identity"):
			return nil
		case req.Name == "bash" && strings.Contains(args, "configure_git_bot_remote"):
			return nil
		case req.Name == "bash" && strings.Contains(args, "runoq::claude_stream"):
			reviewOutputPath := req.Args[4]
			_ = os.WriteFile(reviewOutputPath, []byte("REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 42\nCHECKLIST:\n- OK.\n"), 0o644)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Contains(args, "reconcile"):
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, "closes #42"):
			_, _ = io.WriteString(req.Stdout, `[]`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "issue view 42") && strings.Contains(args, "title"):
			_, _ = io.WriteString(req.Stdout, `{"title":"Implement queue"}`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "issue view 42") && strings.Contains(args, "number,title,body"):
			_, _ = io.WriteString(req.Stdout, `{"number":42,"title":"Implement queue","body":"<!-- runoq:meta\nestimated_complexity: low\ntype: task\n-->\n\n## Acceptance Criteria\n\n- [ ] Works.","labels":[{"name":"runoq:ready"}],"url":"u"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Contains(args, "list"):
			_, _ = io.WriteString(req.Stdout, `[{"number":42,"title":"Implement queue","body":"body","url":"u","estimated_complexity":"low","type":"task"}]`)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Contains(args, "eligibility"):
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-implement-queue","reasons":[]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Contains(args, "set-status"):
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Contains(args, "create"):
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42"}`)
			return nil
		case req.Name == "git":
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Contains(args, "create"):
			_, _ = io.WriteString(req.Stdout, `{"url":"https://example.test/pull/87","number":87}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Contains(args, "comment"):
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Contains(args, "finalize"):
			return nil
		case req.Name == "gh" && strings.Contains(args, "issue view 42") && strings.Contains(args, "--json body"):
			_, _ = io.WriteString(req.Stdout, `{"body":"## AC"}`)
			return nil
		case strings.HasSuffix(req.Name, "/issue-runner.sh"):
			_, _ = io.WriteString(req.Stdout, `{"status":"review_ready","logDir":"log","baselineHash":"b","headHash":"h","commitRange":"b..h","reviewLogPath":"log/r.md","specRequirements":"AC","changedFiles":["f"],"relatedFiles":["f"],"cumulativeTokens":1,"verificationPassed":true,"verificationFailures":[],"caveats":[],"summary":"ok"}`)
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Contains(args, "remove"):
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "--json body"):
			_, _ = io.WriteString(req.Stdout, `{"body":"## Summary\n<!-- runoq:summary:start -->\nP.\n<!-- runoq:summary:end -->\n\nCloses #42\n"}`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr edit 87") && strings.Contains(args, "--body-file"):
			return nil
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Contains(args, "save"):
			t.Fatalf("state.sh save should not be called, got: %s", commandLine(req))
			return nil
		default:
			return nil
		}
	})

	code := app.Run(ctx)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr: %s", code, stderr.String())
	}
	// Double check no state.sh calls
	for _, call := range calls {
		if strings.Contains(call, "state.sh") && strings.Contains(call, "save") {
			t.Fatalf("state.sh save was called: %s", call)
		}
	}
}

func TestRunIssueResumesFromDevelopState(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var calls []string

	// State as it would appear in a develop-phase audit comment
	developState := `{"phase":"DEVELOP","issue":42,"pr_number":87,"branch":"runoq/42-implement-queue","worktree":"/tmp/runoq-wt-42","round":1,"cumulative_tokens":12,"baseline_hash":"base","head_hash":"head","commit_range":"base..head","review_log_path":"log/issue-42/round-1-diff-review.md","spec_requirements":"## AC","changed_files":["src/queue.ts"],"related_files":["src/queue.ts"],"verification_passed":true,"verification_failures":[],"caveats":[],"summary":"Ready for review","complexity":"low","issue_type":"task","status":"review_ready"}`

	app := New(nil, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		calls = append(calls, commandLine(req))
		args := strings.Join(req.Args, " ")
		switch {
		// deriveStateFromGitHub: find linked PR
		case req.Name == "gh" && strings.Contains(args, "pr list") && strings.Contains(args, `closes #42`):
			_, _ = io.WriteString(req.Stdout, `[{"number":87,"headRefName":"runoq/42-implement-queue"}]`)
			return nil
		// deriveStateFromGitHub: fetch PR comments with state blocks
		case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "comments"):
			_, _ = io.WriteString(req.Stdout, `{"comments":[{"body":"<!-- runoq:event:init -->\n<!-- runoq:state:{\"phase\":\"INIT\",\"pr_number\":87} -->\n> init"},{"body":"<!-- runoq:event:develop -->\n<!-- runoq:state:`+strings.ReplaceAll(developState, `"`, `\"`)+` -->\n> develop"}]}`)
			return nil
		// Review phase: claude_stream for diff-reviewer
		case req.Name == "bash" && strings.Contains(args, "runoq::claude_stream"):
			reviewOutputPath := req.Args[4]
			if err := os.WriteFile(reviewOutputPath, []byte("REVIEW-TYPE: diff-review\nVERDICT: PASS\nSCORE: 42\nCHECKLIST:\n- OK.\n"), 0o644); err != nil {
				t.Fatalf("write review output: %v", err)
			}
			return nil
		// PR comment posting
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(args, "comment owner/repo 87 "):
			return nil
		// Finalize
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(args, "finalize owner/repo 87"):
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.HasPrefix(args, "set-status owner/repo 42"):
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && args == "remove 42":
			return nil
		// PR body update
		case req.Name == "gh" && strings.Contains(args, "pr view 87") && strings.Contains(args, "--json body"):
			_, _ = io.WriteString(req.Stdout, `{"body":"## Summary\n<!-- runoq:summary:start -->\nPending.\n<!-- runoq:summary:end -->\n\n## Linked Issue\nCloses #42\n"}`)
			return nil
		case req.Name == "gh" && strings.Contains(args, "pr edit 87") && strings.Contains(args, "--body-file"):
			return nil
		default:
			t.Fatalf("unexpected command: %s", commandLine(req))
			return nil
		}
	})

	meta := IssueMetadata{
		Number:              42,
		Title:               "Implement queue",
		EstimatedComplexity: "low",
		Type:                "task",
	}
	stateJSON, err := app.RunIssue(ctx, "owner/repo", 42, false, "Implement queue", meta)
	if err != nil {
		t.Fatalf("RunIssue failed: %v", err)
	}
	if !strings.Contains(stateJSON, `"phase":"DONE"`) {
		t.Fatalf("expected DONE, got %s", stateJSON)
	}
	// Must NOT have called phaseInit (no PR creation, no worktree creation, no eligibility check)
	for _, call := range calls {
		if strings.Contains(call, "dispatch-safety.sh eligibility") {
			t.Fatalf("should not have called eligibility check on resume, got: %v", calls)
		}
		if strings.Contains(call, "gh-pr-lifecycle.sh create") {
			t.Fatalf("should not have created PR on resume, got: %v", calls)
		}
		if strings.Contains(call, "worktree.sh create") {
			t.Fatalf("should not have created worktree on resume, got: %v", calls)
		}
	}
	// Should have gone through REVIEW phase
	if !strings.Contains(stderr.String(), "REVIEW:") {
		t.Fatalf("expected REVIEW phase log on resume, got %q", stderr.String())
	}
}

func writeRuntimeConfig(t *testing.T, root string) {
	t.Helper()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "runoq.json"), []byte(`{"labels":{"ready":"runoq:ready"},"identity":{"handle":"runoq"},"autoMerge":{"enabled":true,"maxComplexity":"low"},"reviewers":["username"],"maxRounds":5,"maxTokenBudget":500000}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func commandLine(req shell.CommandRequest) string {
	return req.Name + " " + strings.Join(req.Args, " ")
}

func containsCall(calls []string, needle string) bool {
	for _, call := range calls {
		if strings.Contains(call, needle) {
			return true
		}
	}
	return false
}

func assertEnvNotValue(t *testing.T, env []string, key string, disallowed string) {
	t.Helper()
	if value, ok := shell.EnvLookup(env, key); ok && value == disallowed {
		t.Fatalf("expected %s to not be %q, got %q", key, disallowed, value)
	}
}
