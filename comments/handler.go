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

// HandleComments processes unresponded human comments on a planning issue.
// For each batch of unresponded comments: adds eyes reaction, calls the
// comment-responder agent, posts the reply, dispatches side effects
// (change-request → update body, approve → add label), and adds thumbs_up.
func HandleComments(ctx context.Context, cfg HandleCommentsConfig) error {
	// Fetch issue with comments
	issueView, err := cfg.GH.IssueView(ctx, cfg.Repo, cfg.IssueNumber, "number,title,body,comments")
	if err != nil {
		return fmt.Errorf("fetch issue: %w", err)
	}

	// Find unresponded comment IDs
	ids, err := FindUnrespondedCommentIDs(issueView)
	if err != nil {
		return fmt.Errorf("find unresponded: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}

	// Gather comment bodies
	commentBody := extractCommentBodies(issueView, ids)

	// Mark as picked up (eyes)
	for _, id := range ids {
		cfg.GH.AddReaction(ctx, id, "EYES")
	}

	// Build agent payload
	var issueData struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	json.Unmarshal([]byte(issueView), &issueData)

	payload, _ := json.Marshal(map[string]any{
		"repo":        cfg.Repo,
		"issueNumber": cfg.IssueNumber,
		"planPath":    cfg.PlanFile,
		"issueTitle":  issueData.Title,
		"issueBody":   issueData.Body,
		"commentBody": commentBody,
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

	// Post reply comment
	replyBody := agentResp.Reply
	if !strings.Contains(replyBody, "runoq:bot") {
		replyBody = "<!-- runoq:bot -->\n\n" + replyBody
	}
	if err := cfg.GH.IssueComment(ctx, cfg.Repo, cfg.IssueNumber, replyBody); err != nil {
		return fmt.Errorf("post reply: %w", err)
	}

	// Dispatch side effects
	switch agentResp.Action {
	case ActionApprove:
		if cfg.PlanApprovedLabel != "" {
			cfg.GH.IssueAddLabel(ctx, cfg.Repo, cfg.IssueNumber, cfg.PlanApprovedLabel)
		}
	case ActionChangeRequest:
		// TODO: update issue body with revised proposal
		// This requires planning.FormatProposalCommentBody + ReplaceProposalInBody
		// which creates a circular import (comments → planning). For now, the
		// change-request side effect is handled by the caller.
	}

	// Mark as responded (thumbs_up)
	for _, id := range ids {
		cfg.GH.AddReaction(ctx, id, "THUMBS_UP")
	}

	return nil
}

func extractCommentBodies(issueViewJSON string, ids []string) string {
	var view struct {
		Comments []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	json.Unmarshal([]byte(issueViewJSON), &view)

	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	var bodies []string
	for _, c := range view.Comments {
		if idSet[c.ID] {
			bodies = append(bodies, c.Body)
		}
	}
	return strings.Join(bodies, "\n\n---\n\n")
}
