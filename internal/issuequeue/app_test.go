package issuequeue

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/common"
)

func TestListParsesMetadataVariants(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"list", "owner/repo", "runoq:ready"})
	app.SetCommandExecutor(func(ctx context.Context, req common.CommandRequest) error {
		t.Helper()
		if req.Name != "gh" {
			t.Fatalf("unexpected command: %s %v", req.Name, req.Args)
		}
		_, _ = req.Stdout.Write([]byte(`[
  {
    "number": 42,
    "title": "Implement queue",
    "body": "<!-- runoq:meta\ndepends_on: [12,14]\npriority: 1\nestimated_complexity: low\ncomplexity_rationale: touches queue scheduling\ntype: task\nparent_epic: 7\n-->\n\nBody",
    "labels": [{"name":"runoq:ready"}],
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
    "title": "Malformed metadata",
    "body": "<!-- runoq:meta\ndepends_on: nope\npriority: x\nestimated_complexity:\n-->\n\nBody",
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
	if issues[0].Priority == nil || *issues[0].Priority != 1 {
		t.Fatalf("unexpected priority: %+v", issues[0].Priority)
	}
	if !issues[0].MetadataValid || issues[1].MetadataPresent || issues[2].MetadataValid {
		t.Fatalf("unexpected metadata flags: %+v %+v %+v", issues[0], issues[1], issues[2])
	}
	if issues[0].ParentEpic == nil || *issues[0].ParentEpic != 7 {
		t.Fatalf("unexpected parent epic: %+v", issues[0].ParentEpic)
	}
}

func TestNextSkipsBlockedDependencies(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"next", "owner/repo", "runoq:ready"})
	app.SetCommandExecutor(func(ctx context.Context, req common.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		switch {
		case strings.Contains(command, "issue list --repo owner/repo --label runoq:ready"):
			_, _ = req.Stdout.Write([]byte(`[
  {"number": 21, "title": "Blocked", "body": "<!-- runoq:meta\ndepends_on: [5]\npriority: 1\nestimated_complexity: low\ntype: task\n-->", "labels": [{"name":"runoq:ready"}], "url": "https://example.test/issues/21"},
  {"number": 22, "title": "Ready", "body": "<!-- runoq:meta\ndepends_on: []\npriority: 2\nestimated_complexity: low\ntype: task\n-->", "labels": [{"name":"runoq:ready"}], "url": "https://example.test/issues/22"}
]`))
			return nil
		case strings.Contains(command, "issue view 5 --repo owner/repo --json number,labels"):
			_, _ = req.Stdout.Write([]byte(`{"labels":[{"name":"runoq:in-progress"}]}`))
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
	if result.Issue == nil || result.Issue.Number != 22 {
		t.Fatalf("unexpected selected issue: %+v", result.Issue)
	}
	if len(result.Skipped) != 1 || len(result.Skipped[0].BlockedReasons) != 1 || result.Skipped[0].BlockedReasons[0] != "dependency #5 is not runoq:done" {
		t.Fatalf("unexpected skipped output: %+v", result.Skipped)
	}
}

func TestSetStatusRemovesExistingRunoqLabels(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"set-status", "owner/repo", "42", "in-progress"})
	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req common.CommandRequest) error {
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

func TestCreateWritesMetadataAndLinksParentEpic(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{
		"create", "owner/repo", "Implement queue", "## Acceptance Criteria\n\n- [ ] Works.",
		"--depends-on", "12,14",
		"--priority", "1",
		"--estimated-complexity", "low",
		"--complexity-rationale", "touches queue scheduling",
		"--type", "task",
		"--parent-epic", "77",
	})
	var bodyText string
	var commands []string
	app.SetCommandExecutor(func(ctx context.Context, req common.CommandRequest) error {
		t.Helper()
		command := req.Name + " " + strings.Join(req.Args, " ")
		commands = append(commands, command)
		switch {
		case strings.Contains(command, "issue create --repo owner/repo --title Implement queue --body-file "):
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
		case strings.Contains(command, "api repos/owner/repo/issues/99 --jq .id"):
			_, _ = req.Stdout.Write([]byte("12345"))
			return nil
		case strings.Contains(command, "api repos/owner/repo/issues/77/sub_issues --method POST -F sub_issue_id=12345"):
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
	if !strings.Contains(bodyText, "depends_on: [12,14]") || !strings.Contains(bodyText, "priority: 1") || !strings.Contains(bodyText, "complexity_rationale: touches queue scheduling") {
		t.Fatalf("unexpected body file:\n%s", bodyText)
	}
	if len(commands) != 3 {
		t.Fatalf("unexpected command count: %v", commands)
	}
}

func TestEpicStatusTracksPendingChildren(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, []string{"epic-status", "owner/repo", "77"})
	app.SetCommandExecutor(func(ctx context.Context, req common.CommandRequest) error {
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
