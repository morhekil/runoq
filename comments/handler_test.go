package comments

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/saruman/runoq/agents"
)

type fakeGH struct {
	issueView   string
	calls       []string
	reactionErr error
	labelAddErr error
	editedBody  string
}

func (f *fakeGH) IssueView(_ context.Context, repo string, number int, fields string) (string, error) {
	f.calls = append(f.calls, "issue-view")
	return f.issueView, nil
}

func (f *fakeGH) IssueComment(_ context.Context, repo string, number int, body string) error {
	f.calls = append(f.calls, "issue-comment")
	return nil
}

func (f *fakeGH) IssueEditBody(_ context.Context, repo string, number int, body string) error {
	f.calls = append(f.calls, "issue-edit-body")
	f.editedBody = body
	return nil
}

func (f *fakeGH) IssueAddLabel(_ context.Context, repo string, number int, label string) error {
	f.calls = append(f.calls, "issue-add-label:"+label)
	return f.labelAddErr
}

func (f *fakeGH) AddReaction(_ context.Context, commentID string, content string) error {
	f.calls = append(f.calls, "reaction:"+content+":"+commentID)
	return f.reactionErr
}

type fakeInvoker struct {
	responseText string
	lastOpts     agents.InvokeOptions
}

func (f *fakeInvoker) Invoke(_ context.Context, opts agents.InvokeOptions) (agents.Response, error) {
	f.lastOpts = opts
	return agents.Response{Text: f.responseText}, nil
}

func TestHandleCommentsQuestion(t *testing.T) {
	t.Parallel()

	gh := &fakeGH{
		issueView: `{"number":2,"title":"Review","body":"proposal body","comments":[
			{"author":{"login":"human"},"body":"Why this order?","id":"IC1","reactionGroups":[]}
		]}`,
	}
	invoker := &fakeInvoker{
		responseText: `{"action":"question","reply":"Because of dependencies."}`,
	}

	err := HandleComments(t.Context(), HandleCommentsConfig{
		Repo:        "owner/repo",
		IssueNumber: 2,
		PlanFile:    "docs/plan.md",
		RunoqRoot:   t.TempDir(),
		GH:          gh,
		Invoker:     invoker,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have: view, eyes reaction, comment, thumbs_up reaction
	hasComment := false
	hasEyes := false
	hasThumbsUp := false
	for _, c := range gh.calls {
		if c == "issue-comment" {
			hasComment = true
		}
		if c == "reaction:EYES:IC1" {
			hasEyes = true
		}
		if c == "reaction:THUMBS_UP:IC1" {
			hasThumbsUp = true
		}
	}
	if !hasComment {
		t.Error("expected issue comment")
	}
	if !hasEyes {
		t.Error("expected eyes reaction")
	}
	if !hasThumbsUp {
		t.Error("expected thumbs_up reaction")
	}
}

func TestHandleCommentsApprove(t *testing.T) {
	t.Parallel()

	gh := &fakeGH{
		issueView: `{"number":2,"title":"Review","body":"proposal body","comments":[
			{"author":{"login":"human"},"body":"Approved","id":"IC1","reactionGroups":[]}
		]}`,
	}
	invoker := &fakeInvoker{
		responseText: `{"action":"approve","reply":"Proceeding with approval."}`,
	}

	err := HandleComments(t.Context(), HandleCommentsConfig{
		Repo:              "owner/repo",
		IssueNumber:       2,
		PlanFile:          "docs/plan.md",
		RunoqRoot:         t.TempDir(),
		PlanApprovedLabel: "runoq:plan-approved",
		GH:                gh,
		Invoker:           invoker,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hasLabel := false
	for _, c := range gh.calls {
		if c == "issue-add-label:runoq:plan-approved" {
			hasLabel = true
		}
	}
	if !hasLabel {
		t.Error("expected plan-approved label to be added")
	}
}

func TestHandleCommentsChangeRequestUpdatesProposalBody(t *testing.T) {
	t.Parallel()

	gh := &fakeGH{
		issueView: `{"number":2,"title":"Review","body":"## Acceptance Criteria\n- [ ] Done.\n\n<!-- runoq:proposal-start -->\nOld proposal","comments":[
			{"author":{"login":"human"},"body":"Please revise","id":"IC1","reactionGroups":[]}
		]}`,
	}
	invoker := &fakeInvoker{
		responseText: `{"action":"change-request","reply":"Updated proposal.","revised_proposal":{"items":[{"title":"Revised milestone","type":"implementation","goal":"Ship it","criteria":["Works"],"priority":1}]}}`,
	}

	err := HandleComments(t.Context(), HandleCommentsConfig{
		Repo:        "owner/repo",
		IssueNumber: 2,
		PlanFile:    "docs/plan.md",
		RunoqRoot:   t.TempDir(),
		GH:          gh,
		Invoker:     invoker,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gh.editedBody == "" {
		t.Fatal("expected issue body to be updated for change-request")
	}
	if gh.editedBody == gh.issueView {
		t.Fatal("expected edited body to differ from original issue view payload")
	}
	if !containsString(gh.editedBody, "Revised milestone") {
		t.Fatalf("expected revised proposal content in body, got %q", gh.editedBody)
	}
	if containsString(gh.editedBody, "Old proposal") {
		t.Fatalf("expected old proposal content to be replaced, got %q", gh.editedBody)
	}
}

func TestHandleCommentsClaudeBinPassedToInvoker(t *testing.T) {
	t.Parallel()

	gh := &fakeGH{
		issueView: `{"number":2,"title":"Review","body":"proposal body","comments":[
			{"author":{"login":"human"},"body":"Looks good","id":"IC1","reactionGroups":[]}
		]}`,
	}
	invoker := &fakeInvoker{
		responseText: `{"action":"approve","reply":"Done."}`,
	}

	err := HandleComments(t.Context(), HandleCommentsConfig{
		Repo:              "owner/repo",
		IssueNumber:       2,
		PlanFile:          "docs/plan.md",
		RunoqRoot:         t.TempDir(),
		PlanApprovedLabel: "runoq:plan-approved",
		ClaudeBin:         "/custom/fixture-claude",
		GH:                gh,
		Invoker:           invoker,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invoker.lastOpts.Bin != "/custom/fixture-claude" {
		t.Errorf("expected Bin=/custom/fixture-claude, got %q", invoker.lastOpts.Bin)
	}
}

func TestHandleCommentsNoUnrespondedSkips(t *testing.T) {
	t.Parallel()

	gh := &fakeGH{
		issueView: `{"number":2,"title":"Review","body":"body","comments":[
			{"author":{"login":"human"},"body":"Done","id":"IC1","reactionGroups":[{"content":"THUMBS_UP","users":{"totalCount":1}}]}
		]}`,
	}

	err := HandleComments(t.Context(), HandleCommentsConfig{
		Repo:        "owner/repo",
		IssueNumber: 2,
		PlanFile:    "docs/plan.md",
		RunoqRoot:   t.TempDir(),
		GH:          gh,
		Invoker:     &fakeInvoker{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No comments to process — should not call comment or reaction
	for _, c := range gh.calls {
		if c == "issue-comment" {
			t.Error("should not post comment when all are responded")
		}
	}
}

func TestHandleCommentsReturnsReactionError(t *testing.T) {
	t.Parallel()

	gh := &fakeGH{
		issueView: `{"number":2,"title":"Review","body":"proposal body","comments":[
			{"author":{"login":"human"},"body":"Why this order?","id":"IC1","reactionGroups":[]}
		]}`,
		reactionErr: errors.New("boom"),
	}

	err := HandleComments(t.Context(), HandleCommentsConfig{
		Repo:        "owner/repo",
		IssueNumber: 2,
		PlanFile:    "docs/plan.md",
		RunoqRoot:   t.TempDir(),
		GH:          gh,
		Invoker:     &fakeInvoker{},
	})
	if err == nil {
		t.Fatal("expected reaction error")
	}
}

func containsString(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
