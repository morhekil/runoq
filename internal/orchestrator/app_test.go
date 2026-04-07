package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

func TestPhaseInitPRCreateFailureWritesRollbackStateAndCleansUp(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var savedState string
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
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 42":
			payload, _ := io.ReadAll(req.Stdin)
			savedState = string(payload)
			return nil
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
	if !strings.Contains(savedState, `"phase":"FAILED"`) {
		t.Fatalf("expected failed phase in saved state, got %s", savedState)
	}
	if !strings.Contains(savedState, `"failure_stage":"INIT"`) {
		t.Fatalf("expected init failure stage in saved state, got %s", savedState)
	}
	if !strings.Contains(savedState, `"worktree":"/tmp/runoq-wt-42"`) {
		t.Fatalf("expected worktree in saved state, got %s", savedState)
	}
	if strings.Contains(savedState, `"pr_number"`) {
		t.Fatalf("did not expect pr_number in rollback state, got %s", savedState)
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
	var savedStates []string
	var calls []string
	var runnerPayload string

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
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 42":
			payload, _ := io.ReadAll(req.Stdin)
			savedStates = append(savedStates, string(payload))
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
			runnerPayload = string(payloadBody)
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
	if !strings.Contains(runnerPayload, `"issueNumber":42`) || !strings.Contains(runnerPayload, `"prNumber":87`) {
		t.Fatalf("unexpected issue-runner payload: %s", runnerPayload)
	}
	for _, phase := range []string{`"phase":"INIT"`, `"phase":"CRITERIA"`, `"phase":"DEVELOP"`, `"phase":"REVIEW"`, `"phase":"DECIDE"`, `"phase":"FINALIZE"`, `"phase":"DONE"`} {
		if !containsAny(savedStates, phase) {
			t.Fatalf("expected saved state containing %s, got %v", phase, savedStates)
		}
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
	var savedStates []string
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
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 42":
			payload, _ := io.ReadAll(req.Stdin)
			savedStates = append(savedStates, string(payload))
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "comment owner/repo 87 "):
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
	for _, phase := range []string{`"phase":"DEVELOP"`, `"phase":"REVIEW"`, `"phase":"DECIDE"`, `"phase":"FINALIZE"`, `"phase":"DONE"`} {
		if !containsAny(savedStates, phase) {
			t.Fatalf("expected saved state containing %s, got %v", phase, savedStates)
		}
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
}

func TestRunLowComplexityIterateReentersDevelopWithChecklist(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var savedStates []string
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
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 42":
			payload, _ := io.ReadAll(req.Stdin)
			savedStates = append(savedStates, string(payload))
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
	if !containsAny(savedStates, `"decision":"iterate"`) || !containsAny(savedStates, `"next_phase":"DEVELOP"`) {
		t.Fatalf("expected iterate decision state, got %v", savedStates)
	}
	if !containsAny(savedStates, `"previous_checklist":"- First checklist item."`) {
		t.Fatalf("expected saved previous_checklist state, got %v", savedStates)
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

func TestRunNonLowComplexityCriteriaNeedsReviewHandoffSkipsDevelop(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var savedStates []string
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
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "reconcile owner/repo":
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json title":
			_, _ = io.WriteString(req.Stdout, `{"title":"Coordinate migration"}`)
			return nil
		case req.Name == "gh" && strings.Join(req.Args, " ") == "issue view 42 --repo owner/repo --json number,title,body,labels,url":
			_, _ = io.WriteString(req.Stdout, `{"number":42,"title":"Coordinate migration","body":"<!-- runoq:meta\nestimated_complexity: medium\ntype: task\n-->\n\n## Acceptance Criteria\n\n- [ ] Coordinate migration.","labels":[{"name":"runoq:ready"}],"url":"https://example.test/issues/42"}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "list owner/repo runoq:ready":
			_, _ = io.WriteString(req.Stdout, `[{"number":42,"title":"Coordinate migration","body":"body","url":"https://example.test/issues/42","estimated_complexity":"medium","type":"task"}]`)
			return nil
		case strings.HasSuffix(req.Name, "/dispatch-safety.sh") && strings.Join(req.Args, " ") == "eligibility owner/repo 42":
			_, _ = io.WriteString(req.Stdout, `{"allowed":true,"issue":42,"branch":"runoq/42-coordinate-migration","reasons":[]}`)
			return nil
		case strings.HasSuffix(req.Name, "/gh-issue-queue.sh") && strings.Join(req.Args, " ") == "set-status owner/repo 42 in-progress":
			return nil
		case strings.HasSuffix(req.Name, "/worktree.sh") && strings.Join(req.Args, " ") == "create 42 Coordinate migration":
			_, _ = io.WriteString(req.Stdout, `{"branch":"runoq/42-coordinate-migration","worktree":"/tmp/runoq-wt-42"}`)
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 commit --allow-empty -m runoq: begin work on #42":
			return nil
		case req.Name == "git" && strings.Join(req.Args, " ") == "-C /tmp/runoq-wt-42 push -u origin runoq/42-coordinate-migration":
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.Join(req.Args, " ") == "create owner/repo runoq/42-coordinate-migration 42 Coordinate migration":
			_, _ = io.WriteString(req.Stdout, `{"url":"https://example.test/pull/87","number":87}`)
			return nil
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 42":
			payload, _ := io.ReadAll(req.Stdin)
			savedStates = append(savedStates, string(payload))
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "comment owner/repo 87 "):
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
	if !strings.Contains(stdout.String(), `"issue_status":"needs-review"`) {
		t.Fatalf("expected needs-review issue status, got %q", stdout.String())
	}
	for _, phase := range []string{`"phase":"INIT"`, `"phase":"CRITERIA"`, `"phase":"REVIEW"`, `"phase":"DECIDE"`, `"phase":"FINALIZE"`, `"phase":"DONE"`} {
		if !containsAny(savedStates, phase) {
			t.Fatalf("expected saved state containing %s, got %v", phase, savedStates)
		}
	}
	if containsAny(savedStates, `"phase":"DEVELOP"`) {
		t.Fatalf("did not expect DEVELOP state for non-low complexity criteria handoff, got %v", savedStates)
	}
	if containsCall(calls, "issue-runner.sh run") {
		t.Fatalf("did not expect issue-runner invocation, got %v", calls)
	}
	if !containsCall(calls, "gh-pr-lifecycle.sh finalize owner/repo 87 needs-review --reviewer username") {
		t.Fatalf("expected needs-review finalize call, got %v", calls)
	}
	if !containsCall(calls, "gh-issue-queue.sh set-status owner/repo 42 needs-review") {
		t.Fatalf("expected needs-review status update, got %v", calls)
	}
	if !strings.Contains(stderr.String(), "CRITERIA: issue #42 criteria for complexity=medium type=task requires human review in the current runtime slice") {
		t.Fatalf("expected criteria handoff log, got %q", stderr.String())
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

	var savedStates []string
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
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 41":
			payload, _ := io.ReadAll(req.Stdin)
			savedStates = append(savedStates, string(payload))
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
	if !containsAny(savedStates, `"decision":"integrate-pending"`) {
		t.Fatalf("expected integrate-pending saved state, got %v", savedStates)
	}
	if containsCall(calls, "set-status owner/repo 41") {
		t.Fatalf("did not expect set-status mutation for pending integrate, got %v", calls)
	}
}

func TestPhaseIntegrateSuccessWithCriteriaCommitMarksDone(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var savedStates []string
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
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 41":
			payload, _ := io.ReadAll(req.Stdin)
			savedStates = append(savedStates, string(payload))
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
	if !containsAny(savedStates, `"phase":"INTEGRATE"`) || !containsAny(savedStates, `"phase":"DONE"`) {
		t.Fatalf("expected INTEGRATE and DONE states, got %v", savedStates)
	}
}

func TestPhaseIntegrateFailureMarksNeedsReviewAndFailed(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var savedStates []string
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
		case strings.HasSuffix(req.Name, "/state.sh") && strings.Join(req.Args, " ") == "save 41":
			payload, _ := io.ReadAll(req.Stdin)
			savedStates = append(savedStates, string(payload))
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
	if !containsAny(savedStates, `"integrate_failures":"criteria drift, tests failed"`) {
		t.Fatalf("expected integrate failures in saved state, got %v", savedStates)
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

func containsAny(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
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
