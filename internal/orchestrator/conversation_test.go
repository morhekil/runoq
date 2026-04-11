package orchestrator

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

func TestFindUnprocessedCommentsFiltersProcessed(t *testing.T) {
	t.Parallel()

	issueCommentsJSON := `[
		{"id": 100, "body": "<!-- runoq:bot:orchestrator:init -->\nOrchestrator initialized.", "user": {"login": "runoq[bot]"}, "created_at": "2026-01-01T00:00:00Z", "reactions": {"+1": 0}},
		{"id": 150, "body": "<!-- runoq:bot:orchestrator:respond source:issue-comment:200 -->\nAcknowledged.", "user": {"login": "runoq[bot]"}, "created_at": "2026-01-01T01:30:00Z", "reactions": {"+1": 0}},
		{"id": 200, "body": "Looks good!", "user": {"login": "human1"}, "created_at": "2026-01-01T01:00:00Z", "reactions": {"+1": 1}},
		{"id": 300, "body": "Please add error handling", "user": {"login": "human2"}, "created_at": "2026-01-01T02:00:00Z", "reactions": {"+1": 0}}
	]`

	app := New(nil, []string{}, "/tmp", io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issues/42/comments"):
			_, _ = io.WriteString(req.Stdout, issueCommentsJSON)
		case strings.Contains(args, "pulls/42/comments"), strings.Contains(args, "pulls/42/reviews"):
			_, _ = io.WriteString(req.Stdout, "[]")
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
		}
		return nil
	})
	app.SetConfig(OrchestratorConfig{IdentityHandle: "runoq"})

	comments, err := app.findUnprocessedComments(ctx(t), "owner/repo", "pr", 42)
	if err != nil {
		t.Fatalf("findUnprocessedComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 unprocessed comment, got %d", len(comments))
	}
	if comments[0].ID != 300 {
		t.Fatalf("expected comment ID 300, got %d", comments[0].ID)
	}
	if comments[0].Author != "human2" {
		t.Fatalf("expected author human2, got %q", comments[0].Author)
	}
	if comments[0].CommenterIdentity != "human:human2" {
		t.Fatalf("expected identity human:human2, got %q", comments[0].CommenterIdentity)
	}
	if comments[0].SourceType != "issue-comment" {
		t.Fatalf("expected issue-comment source, got %q", comments[0].SourceType)
	}
}

func TestFindUnprocessedCommentsDetectsAgentIdentity(t *testing.T) {
	t.Parallel()

	commentsJSON := `[
		{"id": 100, "body": "<!-- runoq:agent:diff-reviewer -->\n## Scoresheet\n...", "user": {"login": "runoq[bot]"}, "created_at": "2026-01-01T00:00:00Z", "reactions": {"+1": 0}}
	]`

	app := New(nil, []string{}, "/tmp", io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issues/42/comments"):
			_, _ = io.WriteString(req.Stdout, commentsJSON)
		case strings.Contains(args, "pulls/42/comments"), strings.Contains(args, "pulls/42/reviews"):
			_, _ = io.WriteString(req.Stdout, "[]")
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
		}
		return nil
	})
	app.SetConfig(OrchestratorConfig{IdentityHandle: "runoq"})

	comments, err := app.findUnprocessedComments(ctx(t), "owner/repo", "pr", 42)
	if err != nil {
		t.Fatalf("findUnprocessedComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].CommenterIdentity != "agent:diff-reviewer" {
		t.Fatalf("expected identity agent:diff-reviewer, got %q", comments[0].CommenterIdentity)
	}
}

func TestFindUnprocessedCommentsEmptyList(t *testing.T) {
	t.Parallel()

	app := New(nil, []string{}, "/tmp", io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		_, _ = io.WriteString(req.Stdout, "[]")
		return nil
	})
	app.SetConfig(OrchestratorConfig{IdentityHandle: "runoq"})

	comments, err := app.findUnprocessedComments(ctx(t), "owner/repo", "issue", 10)
	if err != nil {
		t.Fatalf("findUnprocessedComments: %v", err)
	}
	if len(comments) != 0 {
		t.Fatalf("expected 0 comments, got %d", len(comments))
	}
}

func TestFindUnprocessedCommentsIncludesPRReviewCommentsAndReviews(t *testing.T) {
	t.Parallel()

	app := New(nil, []string{}, "/tmp", io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issues/42/comments"):
			_, _ = io.WriteString(req.Stdout, `[]`)
		case strings.Contains(args, "pulls/42/comments"):
			_, _ = io.WriteString(req.Stdout, `[
				{"id": 410, "body": "Inline review feedback", "user": {"login": "reviewer1"}, "created_at": "2026-01-01T01:00:00Z"}
			]`)
		case strings.Contains(args, "pulls/42/reviews"):
			_, _ = io.WriteString(req.Stdout, `[
				{"id": 510, "body": "Overall review summary", "user": {"login": "reviewer2"}, "submitted_at": "2026-01-01T02:00:00Z", "state": "COMMENTED"}
			]`)
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
		}
		return nil
	})
	app.SetConfig(OrchestratorConfig{IdentityHandle: "runoq"})

	comments, err := app.findUnprocessedComments(ctx(t), "owner/repo", "pr", 42)
	if err != nil {
		t.Fatalf("findUnprocessedComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(comments))
	}
	if comments[0].ID != 410 || comments[0].SourceType != "review-comment" {
		t.Fatalf("expected inline review comment first, got %+v", comments[0])
	}
	if comments[1].ID != 510 || comments[1].SourceType != "review" {
		t.Fatalf("expected review summary second, got %+v", comments[1])
	}
}

func TestFindUnprocessedCommentsDoesNotTreatHumanPlusOneAsProcessed(t *testing.T) {
	t.Parallel()

	app := New(nil, []string{}, "/tmp", io.Discard, io.Discard)
	app.SetCommandExecutor(func(_ context.Context, req shell.CommandRequest) error {
		args := strings.Join(req.Args, " ")
		switch {
		case strings.Contains(args, "issues/42/comments"):
			_, _ = io.WriteString(req.Stdout, `[
				{"id": 200, "body": "Looks good!", "user": {"login": "human1"}, "created_at": "2026-01-01T01:00:00Z", "reactions": {"+1": 1}}
			]`)
		case strings.Contains(args, "pulls/42/comments"), strings.Contains(args, "pulls/42/reviews"):
			_, _ = io.WriteString(req.Stdout, "[]")
		default:
			t.Fatalf("unexpected command: %s %s", req.Name, args)
		}
		return nil
	})
	app.SetConfig(OrchestratorConfig{IdentityHandle: "runoq"})

	comments, err := app.findUnprocessedComments(ctx(t), "owner/repo", "pr", 42)
	if err != nil {
		t.Fatalf("findUnprocessedComments: %v", err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
}

func ctx(t *testing.T) context.Context {
	return t.Context()
}
