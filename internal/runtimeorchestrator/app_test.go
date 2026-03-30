package runtimeorchestrator

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	app.SetCommandExecutor(func(_ context.Context, req commandRequest) error {
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

func TestRunStopsAfterInitSuccessWithNotImplemented(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeRuntimeConfig(t, root)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var savedState string
	var calls []string

	app := New([]string{"run", "owner/repo", "--issue", "42"}, []string{
		"RUNOQ_ROOT=" + root,
		"RUNOQ_CONFIG=" + filepath.Join(root, "config", "runoq.json"),
		"TARGET_ROOT=" + root,
	}, root, &stdout, &stderr)
	app.SetCommandExecutor(func(_ context.Context, req commandRequest) error {
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
			savedState = string(payload)
			return nil
		case strings.HasSuffix(req.Name, "/gh-pr-lifecycle.sh") && strings.HasPrefix(strings.Join(req.Args, " "), "comment owner/repo 87 "):
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
		t.Fatalf("expected no stdout on partial runtime stop, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "CRITERIA phase is not implemented in the runtime orchestrator yet") {
		t.Fatalf("expected not-implemented error, got %q", stderr.String())
	}
	if !strings.Contains(savedState, `"phase":"INIT"`) {
		t.Fatalf("expected INIT state save, got %s", savedState)
	}
	if !containsCall(calls, "gh-pr-lifecycle.sh comment owner/repo 87") {
		t.Fatalf("expected audit comment call, got %v", calls)
	}
}

func writeRuntimeConfig(t *testing.T, root string) {
	t.Helper()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "runoq.json"), []byte(`{"labels":{"ready":"runoq:ready"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func commandLine(req commandRequest) string {
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
