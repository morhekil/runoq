package issuequeue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

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

func TestListParsesMetadataVariants(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"list", "owner/repo", "runoq:ready"})
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		if req.Name != "gh" {
			t.Fatalf("unexpected command: %s %v", req.Name, req.Args)
		}
		_, _ = req.Stdout.Write([]byte(`[
  {
    "number": 42,
    "title": "Implement queue",
    "body": "Body",
    "labels": [{"name":"runoq:ready"},{"name":"runoq:priority"}],
    "url": "https://example.test/issues/42"
  },
  {
    "number": 11,
    "title": "No metadata",
    "body": "plain body",
    "labels": [{"name":"runoq:ready"}],
    "url": "https://example.test/issues/11"
  },
  {
    "number": 12,
    "title": "Has labels",
    "body": "Body",
    "labels": [{"name":"runoq:ready"}],
    "url": "https://example.test/issues/12"
  }
]`))
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}

	var issues []listedIssue
	if err := json.Unmarshal(app.stdout.(*bytes.Buffer).Bytes(), &issues); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, app.stdout.(*bytes.Buffer).String())
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(issues))
	}
	if issues[0].Priority == nil || *issues[0].Priority != 0 {
		t.Fatalf("expected priority 0 (from runoq:priority label), got: %+v", issues[0].Priority)
	}
	if !issues[0].MetadataPresent || !issues[0].MetadataValid {
		t.Fatalf("unexpected metadata flags for issue with labels: %+v", issues[0])
	}
	if issues[0].Type != "task" {
		t.Fatalf("expected type task, got: %s", issues[0].Type)
	}
}

func TestListParsesPlanningAndAdjustmentTypes(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"list", "owner/repo", "runoq:ready"})
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		if req.Name != "gh" {
			t.Fatalf("unexpected command: %s %v", req.Name, req.Args)
		}
		_, _ = req.Stdout.Write([]byte(`[
  {
    "number": 99,
    "title": "Plan milestone 1",
    "body": "Body",
    "labels": [{"name":"runoq:ready"},{"name":"runoq:planning"}],
    "url": "https://example.test/issues/99"
  },
  {
    "number": 100,
    "title": "Adjust milestones",
    "body": "Body",
    "labels": [{"name":"runoq:ready"},{"name":"runoq:adjustment"}],
    "url": "https://example.test/issues/100"
  }
]`))
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}

	var issues []listedIssue
	if err := json.Unmarshal(app.stdout.(*bytes.Buffer).Bytes(), &issues); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, app.stdout.(*bytes.Buffer).String())
	}
	if len(issues) != 2 {
		t.Fatalf("expected 2 issues, got %d", len(issues))
	}
	if issues[0].Type != "planning" || issues[1].Type != "adjustment" {
		t.Fatalf("unexpected types: %+v", issues)
	}
}

func TestNextPicksLowestNumberedIssue(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"next", "owner/repo", "runoq:ready"})
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "issue list --repo owner/repo --label runoq:ready"):
			_, _ = req.Stdout.Write([]byte(`[
  {"number": 22, "title": "Second", "body": "Body", "labels": [{"name":"runoq:ready"}], "url": "https://example.test/issues/22"},
  {"number": 21, "title": "First", "body": "Body", "labels": [{"name":"runoq:ready"}], "url": "https://example.test/issues/21"}
]`))
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}

	var result nextResult
	if err := json.Unmarshal(app.stdout.(*bytes.Buffer).Bytes(), &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if result.Issue == nil || result.Issue.Number != 21 {
		t.Fatalf("expected issue #21 as next, got: %+v", result.Issue)
	}
}

func TestSetStatusRemovesExistingRunoqLabels(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"set-status", "owner/repo", "42", "in-progress"})
	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, command)
		switch {
		case strings.Contains(command, "issue view 42 --repo owner/repo --json labels"):
			_, _ = req.Stdout.Write([]byte(`{"labels":[{"name":"runoq:ready"},{"name":"bug"}]}`))
			return nil
		case strings.Contains(command, "issue edit 42 --repo owner/repo --remove-label runoq:ready --add-label runoq:in-progress"):
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if len(commands) != 2 {
		t.Fatalf("unexpected command count: %v", commands)
	}
	if !strings.Contains(app.stdout.(*bytes.Buffer).String(), `"label": "runoq:in-progress"`) {
		t.Fatalf("unexpected output: %s", app.stdout.(*bytes.Buffer).String())
	}
}

func TestSetStatusPreservesNonStateLabels(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"set-status", "owner/repo", "42", "done"})
	var editCommand string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "issue view 42 --repo owner/repo --json labels"):
			_, _ = req.Stdout.Write([]byte(`{"labels":[{"name":"runoq:ready"},{"name":"runoq:plan-approved"},{"name":"runoq:maintenance-review"}]}`))
			return nil
		case strings.Contains(command, "issue edit"):
			editCommand = command
			return nil
		case strings.Contains(command, "issue close"):
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if strings.Contains(editCommand, "runoq:plan-approved") {
		t.Error("set-status should not remove runoq:plan-approved")
	}
	if strings.Contains(editCommand, "runoq:maintenance-review") {
		t.Error("set-status should not remove runoq:maintenance-review")
	}
	if !strings.Contains(editCommand, "--remove-label runoq:ready") {
		t.Error("set-status should remove state label runoq:ready")
	}
}

func TestSetStatusDoneClosesIssue(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"set-status", "owner/repo", "42", "done"})
	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, command)
		switch {
		case strings.Contains(command, "issue view 42 --repo owner/repo --json labels"):
			_, _ = req.Stdout.Write([]byte(`{"labels":[{"name":"runoq:in-progress"},{"name":"bug"}]}`))
			return nil
		case strings.Contains(command, "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:done"):
			return nil
		case strings.Contains(command, "issue close 42 --repo owner/repo"):
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if len(commands) != 3 {
		t.Fatalf("unexpected command count: %v", commands)
	}
	if !strings.Contains(commands[2], "issue close 42 --repo owner/repo") {
		t.Fatalf("expected close command, got %v", commands)
	}
	if !strings.Contains(app.stdout.(*bytes.Buffer).String(), `"label": "runoq:done"`) {
		t.Fatalf("unexpected output: %s", app.stdout.(*bytes.Buffer).String())
	}
}

func TestSetStatusDoneRetriesCloseOnce(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"set-status", "owner/repo", "42", "done"})
	var commands []string
	closeAttempts := 0
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, command)
		switch {
		case strings.Contains(command, "issue view 42 --repo owner/repo --json labels"):
			_, _ = req.Stdout.Write([]byte(`{"labels":[{"name":"runoq:in-progress"},{"name":"bug"}]}`))
			return nil
		case strings.Contains(command, "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:done"):
			return nil
		case strings.Contains(command, "issue close 42 --repo owner/repo"):
			closeAttempts++
			if closeAttempts == 1 {
				return errors.New("exit status 1")
			}
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if closeAttempts != 2 {
		t.Fatalf("expected 2 close attempts, got %d (%v)", closeAttempts, commands)
	}
}

func TestCreateSetsIssueTypeAndLinksParentEpic(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmpDir, ".runoq"))
	mustWriteFile(t, filepath.Join(tmpDir, ".runoq", "issue-types.json"), []byte(`{"task":"IT_task","epic":"IT_epic"}`))

	app := newTestApp(t, []string{
		"create", "owner/repo", "Implement queue", "## Acceptance Criteria\n\n- [ ] Works.",
		"--depends-on", "12,14",
		"--priority", "1",
		"--estimated-complexity", "low",
		"--complexity-rationale", "touches queue scheduling",
		"--type", "task",
		"--parent-epic", "77",
	})
	app.AppendEnv("TARGET_ROOT=" + tmpDir)

	var bodyText string
	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, command)
		switch {
		case strings.Contains(command, "issue create"):
			for i := range len(req.Args) {
				if req.Args[i] == "--body-file" {
					data, err := os.ReadFile(req.Args[i+1])
					if err != nil {
						t.Fatalf("read body file: %v", err)
					}
					bodyText = string(data)
					break
				}
			}
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/99"))
			return nil
		case strings.Contains(command, "--jq .node_id"):
			_, _ = req.Stdout.Write([]byte("MDU6SXNzdWU5OQ=="))
			return nil
		case strings.Contains(command, "--jq .id"):
			_, _ = req.Stdout.Write([]byte("12345"))
			return nil
		case strings.Contains(command, "api graphql"):
			return nil
		case strings.Contains(command, "sub_issues"):
			return nil
		default:
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	// Body should NOT contain runoq:meta block
	if strings.Contains(bodyText, "runoq:meta") {
		t.Fatalf("body should not contain runoq:meta block:\n%s", bodyText)
	}
	// Body should contain the actual content
	if !strings.Contains(bodyText, "## Acceptance Criteria") {
		t.Fatalf("body should contain acceptance criteria:\n%s", bodyText)
	}
	// Should have GraphQL calls for issueType and blockedBy
	hasIssueTypeMutation := false
	hasBlockedByMutation := false
	hasSubIssuesCall := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "updateIssueIssueType") {
			hasIssueTypeMutation = true
		}
		if strings.Contains(cmd, "addBlockedBy") {
			hasBlockedByMutation = true
		}
		if strings.Contains(cmd, "sub_issues") {
			hasSubIssuesCall = true
		}
	}
	if !hasIssueTypeMutation {
		t.Error("expected updateIssueIssueType mutation")
	}
	if !hasBlockedByMutation {
		t.Error("expected addBlockedBy mutation for dependencies")
	}
	if !hasSubIssuesCall {
		t.Error("expected sub_issues API call for parent epic")
	}
}

func TestCreatePlanningAddsLabel(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmpDir, ".runoq"))
	mustWriteFile(t, filepath.Join(tmpDir, ".runoq", "issue-types.json"), []byte(`{"task":"IT_task","epic":"IT_epic"}`))

	app := newTestApp(t, []string{
		"create", "owner/repo", "Plan milestone 1", "body",
		"--type", "planning",
		"--priority", "1",
		"--estimated-complexity", "low",
	})
	app.AppendEnv("TARGET_ROOT=" + tmpDir)

	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, command)
		if strings.Contains(command, "issue create") {
			if strings.Contains(command, "--assignee") {
				t.Fatal("create must not assign; assignment is a separate step")
			}
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/99"))
			return nil
		}
		if strings.Contains(command, "--jq .node_id") {
			_, _ = req.Stdout.Write([]byte("NODE99"))
			return nil
		}
		if strings.Contains(command, "runoq:planning") {
			_, _ = req.Stdout.Write([]byte(`{"data":{"repository":{"label":{"id":"LA_planning"}}}}`))
			return nil
		}
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	// Should set issueType to Task (planning maps to Task) and add runoq:planning label
	hasTypeMutation := false
	hasLabelMutation := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "updateIssueIssueType") && strings.Contains(cmd, "IT_task") {
			hasTypeMutation = true
		}
		if strings.Contains(cmd, "addLabelsToLabelable") && strings.Contains(cmd, "LA_planning") {
			hasLabelMutation = true
		}
	}
	if !hasTypeMutation {
		t.Error("expected issueType set to Task for planning type")
	}
	if !hasLabelMutation {
		t.Error("expected runoq:planning label added")
	}
}

func TestCreateAdjustmentAddsLabel(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmpDir, ".runoq"))
	mustWriteFile(t, filepath.Join(tmpDir, ".runoq", "issue-types.json"), []byte(`{"task":"IT_task","epic":"IT_epic"}`))

	app := newTestApp(t, []string{
		"create", "owner/repo", "Adjust milestones", "body",
		"--type", "adjustment",
		"--priority", "1",
		"--estimated-complexity", "low",
	})
	app.AppendEnv("TARGET_ROOT=" + tmpDir)

	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, command)
		if strings.Contains(command, "issue create") {
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/100"))
			return nil
		}
		if strings.Contains(command, "--jq .node_id") {
			_, _ = req.Stdout.Write([]byte("NODE100"))
			return nil
		}
		if strings.Contains(command, "runoq:adjustment") {
			_, _ = req.Stdout.Write([]byte(`{"data":{"repository":{"label":{"id":"LA_adj"}}}}`))
			return nil
		}
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	hasAdjLabel := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "addLabelsToLabelable") && strings.Contains(cmd, "LA_adj") {
			hasAdjLabel = true
		}
	}
	if !hasAdjLabel {
		t.Error("expected runoq:adjustment label added")
	}
}

func TestAssignUsesOperatorLoginOverride(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"assign", "owner/repo", "99"})
	app.env = append(app.env, "RUNOQ_OPERATOR_LOGIN=override-user")
	var sawEdit bool
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "api user --jq .login"):
			t.Fatal("should use env override, not gh api user")
			return nil
		case strings.Contains(command, "issue edit 99 --repo owner/repo --add-assignee override-user"):
			sawEdit = true
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if !sawEdit {
		t.Fatal("expected issue edit with override-user assignee")
	}
}

func TestEpicStatusTracksPendingChildren(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"epic-status", "owner/repo", "77"})
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		if !strings.Contains(command, "api repos/owner/repo/issues/77/sub_issues --paginate") {
			t.Fatalf("unexpected command: %s", command)
		}
		_, _ = req.Stdout.Write([]byte(`[
  {"number": 12, "labels": [{"name":"runoq:done"}]},
  {"number": 14, "labels": [{"name":"runoq:ready"}]}
]`))
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}

	var result epicStatusResult
	if err := json.Unmarshal(app.stdout.(*bytes.Buffer).Bytes(), &result); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if result.AllDone || len(result.Pending) != 1 || result.Pending[0] != 14 {
		t.Fatalf("unexpected epic status: %+v", result)
	}
}

func TestAssignFallsBackToGHWhenEnvEmpty(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"assign", "owner/repo", "99"})
	app.env = append(app.env, "RUNOQ_OPERATOR_LOGIN=")
	var sawAPICall, sawEdit bool
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "api user --jq .login"):
			sawAPICall = true
			_, _ = req.Stdout.Write([]byte("gh-user"))
			return nil
		case strings.Contains(command, "issue edit 99 --repo owner/repo --add-assignee gh-user"):
			sawEdit = true
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if !sawAPICall {
		t.Fatal("expected gh api user fallback when RUNOQ_OPERATOR_LOGIN is empty")
	}
	if !sawEdit {
		t.Fatal("expected issue edit with --add-assignee")
	}
}

func TestAssignFailsWhenGHReturnsEmpty(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"assign", "owner/repo", "42"})
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "api user --jq .login"):
			_, _ = req.Stdout.Write([]byte(""))
			return nil
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code == 0 {
		t.Fatal("expected non-zero exit when gh api user returns empty login")
	}
	if !strings.Contains(app.stderr.(*bytes.Buffer).String(), "empty login") {
		t.Fatalf("expected empty login error, got %q", app.stderr.(*bytes.Buffer).String())
	}
}

func TestCreateBodyInterpretsEscapedNewlines(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{
		"create", "owner/repo", "Test issue", `## Acceptance Criteria\n\n- [ ] Milestones proposed.`,
		"--type", "task",
		"--priority", "1",
		"--estimated-complexity", "low",
	})
	var bodyText string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		if strings.Contains(command, "issue create") {
			for i := range len(req.Args) {
				if req.Args[i] == "--body-file" {
					data, err := os.ReadFile(req.Args[i+1])
					if err != nil {
						t.Fatalf("read body file: %v", err)
					}
					bodyText = string(data)
					break
				}
			}
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/99"))
			return nil
		}
		return nil // allow post-create mutations
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if strings.Contains(bodyText, `\n`) {
		t.Fatalf("body contains literal \\n instead of actual newlines:\n%s", bodyText)
	}
	if !strings.Contains(bodyText, "## Acceptance Criteria\n\n- [ ] Milestones proposed.") {
		t.Fatalf("body missing expected content with real newlines:\n%s", bodyText)
	}
}

func TestCreatePlanningDoesNotAssign(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{
		"create", "owner/repo", "Plan milestone 1", "body",
		"--type", "planning",
		"--priority", "1",
		"--estimated-complexity", "low",
	})
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		if strings.Contains(command, "issue create") {
			if strings.Contains(command, "--assignee") {
				t.Fatal("planning issue create must not include --assignee")
			}
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/99"))
			return nil
		}
		return nil // allow post-create mutations
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
}

func TestAssignSetsOperatorOnIssue(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"assign", "owner/repo", "42"})
	app.env = append(app.env, "RUNOQ_OPERATOR_LOGIN=the-human")
	var sawEdit bool
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		if strings.Contains(command, "issue edit 42 --repo owner/repo --add-assignee the-human") {
			sawEdit = true
			return nil
		}
		t.Fatalf("unexpected command: %s", command)
		return nil
	})

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("Run returned %d stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	if !sawEdit {
		t.Fatal("expected issue edit with --add-assignee")
	}
}

func TestRunGHMutationWithRetryExhaustion(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"set-status", "owner/repo", "42", "done"})
	editAttempts := 0
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "issue view 42 --repo owner/repo --json labels"):
			_, _ = req.Stdout.Write([]byte(`{"labels":[{"name":"runoq:in-progress"}]}`))
			return nil
		case strings.Contains(command, "issue edit 42 --repo owner/repo"):
			editAttempts++
			return errors.New("server error")
		default:
			t.Fatalf("unexpected command: %s", command)
			return nil
		}
	})

	code := app.Run(t.Context())
	if code == 0 {
		t.Fatal("expected non-zero exit when all retry attempts fail")
	}
	if editAttempts != 2 {
		t.Fatalf("expected 2 edit attempts, got %d", editAttempts)
	}
}

func TestWithoutEnvKeysEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("no keys to remove", func(t *testing.T) {
		env := []string{"FOO=bar", "BAZ=qux"}
		result := withoutEnvKeys(env)
		if len(result) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(result))
		}
	})

	t.Run("removes all matching keys", func(t *testing.T) {
		env := []string{"GH_TOKEN=abc", "OTHER=val", "GITHUB_TOKEN=def"}
		result := withoutEnvKeys(env, "GH_TOKEN", "GITHUB_TOKEN")
		if len(result) != 1 || result[0] != "OTHER=val" {
			t.Fatalf("unexpected result: %v", result)
		}
	})

	t.Run("entry without equals sign kept", func(t *testing.T) {
		env := []string{"NOEQUALS", "GH_TOKEN=abc"}
		result := withoutEnvKeys(env, "GH_TOKEN")
		if len(result) != 1 || result[0] != "NOEQUALS" {
			t.Fatalf("unexpected result: %v", result)
		}
	})
}

func TestCreateExportedMethodSkipsArgParsing(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, nil)
	var bodyText string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "issue create"):
			for i := range len(req.Args) {
				if req.Args[i] == "--body-file" {
					data, _ := os.ReadFile(req.Args[i+1])
					bodyText = string(data)
					break
				}
			}
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/99"))
			return nil
		case strings.Contains(command, "api") && strings.Contains(command, "jq .id"):
			_, _ = req.Stdout.Write([]byte("12345"))
			return nil
		case strings.Contains(command, "sub_issues"):
			return nil
		default:
			return nil
		}
	})

	code := app.Create(t.Context(), "owner/repo", "Test issue", "## AC\n\n- [ ] Works.",
		[]string{"--type", "task", "--priority", "1", "--parent-epic", "5"})
	if code != 0 {
		t.Fatalf("Create returned %d, stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
	stdout := app.stdout.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "github.com/owner/repo/issues/99") {
		t.Fatalf("expected issue URL in stdout, got %q", stdout)
	}
	if strings.Contains(bodyText, "runoq:meta") {
		t.Fatalf("body should not contain metadata block:\n%s", bodyText)
	}
	if !strings.Contains(bodyText, "## AC") {
		t.Fatalf("body should contain content:\n%s", bodyText)
	}
}

func TestCreateFailsWhenParentEpicLinkFails(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, nil)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "issue create"):
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/99"))
			return nil
		case strings.Contains(command, "api") && strings.Contains(command, "jq .id"):
			_, _ = req.Stdout.Write([]byte("12345"))
			return nil
		case strings.Contains(command, "sub_issues"):
			return errors.New("link failed")
		default:
			return nil
		}
	})

	code := app.Create(t.Context(), "owner/repo", "Test issue", "## AC\n\n- [ ] Works.", []string{"--type", "task", "--parent-epic", "5"})
	if code == 0 {
		t.Fatal("expected create to fail when parent linking fails")
	}
}

func TestCreateFailsWhenDependencyMutationFails(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	mustMkdirAll(t, filepath.Join(tmpDir, ".runoq"))
	mustWriteFile(t, filepath.Join(tmpDir, ".runoq", "issue-types.json"), []byte(`{"task":"IT_task","epic":"IT_epic"}`))

	app := newTestApp(t, nil)
	app.AppendEnv("TARGET_ROOT=" + tmpDir)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "issue create"):
			_, _ = req.Stdout.Write([]byte("https://github.com/owner/repo/issues/99"))
			return nil
		case strings.Contains(command, "--jq .node_id"):
			_, _ = req.Stdout.Write([]byte("NODE99"))
			return nil
		case strings.Contains(command, "api graphql") && strings.Contains(command, "addBlockedBy"):
			return errors.New("dependency mutation failed")
		case strings.Contains(command, "api graphql"):
			_, _ = req.Stdout.Write([]byte(`{"data":{"repository":{"label":{"id":"LA1"}}}}`))
			return nil
		default:
			return nil
		}
	})

	code := app.Create(t.Context(), "owner/repo", "Test issue", "## AC\n\n- [ ] Works.", []string{"--type", "task", "--depends-on", "12"})
	if code == 0 {
		t.Fatal("expected create to fail when dependency mutation fails")
	}
}

func TestSetStatusExportedMethodSkipsArgParsing(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, nil)
	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		cmd := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, cmd)
		if strings.Contains(cmd, "issue view") {
			_, _ = req.Stdout.Write([]byte(`{"labels":[{"name":"runoq:ready"}]}`))
		}
		return nil
	})

	code := app.SetStatus(t.Context(), "owner/repo", "42", "done")
	if code != 0 {
		t.Fatalf("SetStatus returned %d", code)
	}
	if len(commands) == 0 {
		t.Fatal("no commands executed")
	}
	found := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "issue edit 42") && strings.Contains(cmd, "runoq:done") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected issue edit with done label, got: %v", commands)
	}
}

func TestAssignExportedMethodSkipsArgParsing(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, nil)
	app.SetCommandExecutor(func(ctx context.Context, req shell.CommandRequest) error {
		command := req.Name + " " + strings.Join(req.Args, " ")
		if strings.Contains(command, "api user") {
			_, _ = req.Stdout.Write([]byte(`{"login":"bot-user"}`))
		}
		return nil
	})

	code := app.Assign(t.Context(), "owner/repo", "42")
	if code != 0 {
		t.Fatalf("Assign returned %d, stderr=%q", code, app.stderr.(*bytes.Buffer).String())
	}
}

func newTestApp(t *testing.T, args []string) *App {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	configPath := writeQueueConfig(t)
	return New(args, []string{
		"RUNOQ_CONFIG=" + configPath,
		"RUNOQ_NO_AUTO_TOKEN=1",
	}, ".", &stdout, &stderr)
}

func writeQueueConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "runoq.json")
	config := `{
  "labels": {
    "ready": "runoq:ready",
    "inProgress": "runoq:in-progress",
    "done": "runoq:done",
    "needsReview": "runoq:needs-human-review",
    "blocked": "runoq:blocked"
  }
}`
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
