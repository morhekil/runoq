package comments

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/saruman/runoq/agents"
)

// GHClient abstracts GitHub API operations needed by the comment handler.
type GHClient interface {
	IssueView(ctx context.Context, repo string, number int, fields string) (string, error)
	IssueComment(ctx context.Context, repo string, number int, body string) error
	IssueEditBody(ctx context.Context, repo string, number int, body string) error
	IssueAddLabel(ctx context.Context, repo string, number int, label string) error
	AddReaction(ctx context.Context, commentID string, content string) error
}

// HandleCommentsConfig configures a comment handling run.
type HandleCommentsConfig struct {
	Repo              string
	IssueNumber       int
	CommentID         string
	PlanFile          string
	RunoqRoot         string
	PlanApprovedLabel string
	ClaudeBin         string // optional, defaults to "claude"
	GH                GHClient
	Invoker           AgentInvoker
}

// AgentInvoker abstracts agent invocation for testability.
type AgentInvoker interface {
	Invoke(ctx context.Context, opts agents.InvokeOptions) (agents.Response, error)
}

// HandleComments processes one unresponded human comment on a planning issue in
// deterministic order. It handles the selected comment, posts a bot-tagged
// reply marker for that specific comment, and then adds a thumbs_up reaction.
func HandleComments(ctx context.Context, cfg HandleCommentsConfig) error {
	// Fetch issue with comments
	issueView, err := cfg.GH.IssueView(ctx, cfg.Repo, cfg.IssueNumber, "number,title,body,comments")
	if err != nil {
		return fmt.Errorf("fetch issue: %w", err)
	}

	// Find the next unresponded comment.
	pendingComments, err := FindUnrespondedComments(issueView)
	if err != nil {
		return fmt.Errorf("find unresponded: %w", err)
	}
	comment, ok := selectPendingComment(pendingComments, cfg.CommentID)
	if !ok {
		return nil
	}

	if err := cfg.GH.AddReaction(ctx, comment.ID, "EYES"); err != nil {
		return fmt.Errorf("add eyes reaction to %s: %w", comment.ID, err)
	}

	// Build agent payload
	var issueData struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(issueView), &issueData); err != nil {
		return fmt.Errorf("parse issue view: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"repo":        cfg.Repo,
		"issueNumber": cfg.IssueNumber,
		"planPath":    cfg.PlanFile,
		"issueTitle":  issueData.Title,
		"issueBody":   issueData.Body,
		"commentBody": comment.Body,
	})

	// Call agent
	claudeBin := cfg.ClaudeBin
	if claudeBin == "" {
		claudeBin = "claude"
	}
	resp, err := cfg.Invoker.Invoke(ctx, agents.InvokeOptions{
		Backend: agents.Claude,
		Agent:   "plan-comment-responder",
		Bin:     claudeBin,
		RawArgs: []string{"--agent", "plan-comment-responder", "--add-dir", cfg.RunoqRoot, "--", string(payload)},
		WorkDir: cfg.RunoqRoot,
		Payload: string(payload),
	})
	if err != nil {
		return fmt.Errorf("agent failed: %w", err)
	}

	// Parse response
	agentResp, err := ParseAgentResponse(resp.Text)
	if err != nil {
		return fmt.Errorf("parse agent response: %w", err)
	}

	// Dispatch side effects
	switch agentResp.Action {
	case ActionApprove:
		if cfg.PlanApprovedLabel != "" {
			if err := cfg.GH.IssueAddLabel(ctx, cfg.Repo, cfg.IssueNumber, cfg.PlanApprovedLabel); err != nil {
				return fmt.Errorf("add label %q: %w", cfg.PlanApprovedLabel, err)
			}
		}
	case ActionChangeRequest:
		if agentResp.RevisedProposal == nil {
			return fmt.Errorf("change-request action requires revised proposal payload")
		}
		var revised revisedProposal
		if err := json.Unmarshal(*agentResp.RevisedProposal, &revised); err != nil {
			return fmt.Errorf("parse revised proposal: %w", err)
		}
		newBody := replaceProposalInBody(issueData.Body, formatRevisedProposal(revised))
		if err := cfg.GH.IssueEditBody(ctx, cfg.Repo, cfg.IssueNumber, newBody); err != nil {
			return fmt.Errorf("update issue body: %w", err)
		}
	}

	replyBody := agentResp.Reply
	replyMarker := fmt.Sprintf("<!-- runoq:bot:plan-comment-responder comment-id:%s -->", comment.ID)
	if !strings.Contains(replyBody, replyMarker) {
		replyBody = replyMarker + "\n\n" + strings.TrimSpace(strings.TrimPrefix(replyBody, "<!-- runoq:bot:plan-comment-responder -->"))
	}
	if err := cfg.GH.IssueComment(ctx, cfg.Repo, cfg.IssueNumber, replyBody); err != nil {
		return fmt.Errorf("post reply: %w", err)
	}

	if err := cfg.GH.AddReaction(ctx, comment.ID, "THUMBS_UP"); err != nil {
		return fmt.Errorf("add thumbs_up reaction to %s: %w", comment.ID, err)
	}

	return nil
}

type revisedProposal struct {
	Items []struct {
		Title    string   `json:"title"`
		Type     string   `json:"type"`
		Goal     string   `json:"goal,omitzero"`
		Criteria []string `json:"criteria,omitzero"`
		Priority *int     `json:"priority,omitzero"`
	} `json:"items"`
}

const proposalStartMarker = "<!-- runoq:proposal-start -->"

func formatRevisedProposal(p revisedProposal) string {
	var b strings.Builder
	b.WriteString("<!-- runoq:payload:plan-proposal -->\n")
	for i, item := range p.Items {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&b, "### %d. %s\n", i+1, item.Title)
		b.WriteString("**Type:** ")
		b.WriteString(item.Type)
		if item.Priority != nil {
			fmt.Fprintf(&b, " · **Priority:** %d", *item.Priority)
		}
		b.WriteString("\n\n")
		if item.Goal != "" {
			fmt.Fprintf(&b, "> %s\n\n", item.Goal)
		}
		if len(item.Criteria) > 0 {
			b.WriteString("**Acceptance criteria:**\n")
			for _, criterion := range item.Criteria {
				fmt.Fprintf(&b, "- [ ] %s\n", criterion)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func replaceProposalInBody(existingBody, newProposal string) string {
	if idx := strings.Index(existingBody, proposalStartMarker); idx >= 0 {
		return existingBody[:idx] + proposalStartMarker + "\n" + newProposal
	}
	return existingBody + "\n\n" + proposalStartMarker + "\n" + newProposal
}

func selectPendingComment(comments []PendingComment, targetID string) (PendingComment, bool) {
	if targetID == "" {
		if len(comments) == 0 {
			return PendingComment{}, false
		}
		return comments[0], true
	}
	for _, comment := range comments {
		if comment.ID == targetID {
			return comment, true
		}
	}
	return PendingComment{}, false
}
