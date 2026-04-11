// Package comments provides comment processing for GitHub issues —
// reaction tracking, unresponded comment detection, human selection
// parsing, and agent response parsing.
package comments

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ItemSelection holds approved/rejected item indices (1-based).
type ItemSelection struct {
	Approved []int `json:"approved"`
	Rejected []int `json:"rejected"`
}

// AgentAction classifies the intent of a comment-responder response.
type AgentAction string

const (
	ActionQuestion      AgentAction = "question"
	ActionChangeRequest AgentAction = "change-request"
	ActionApprove       AgentAction = "approve"
)

// AgentResponse is the structured output from a comment-responder agent.
type AgentResponse struct {
	Action          AgentAction      `json:"action"`
	Reply           string           `json:"reply"`
	RevisedProposal *json.RawMessage `json:"revised_proposal,omitzero"`
}

var (
	approvePattern   = regexp.MustCompile(`(?i)approve\s+items?\s*[^0-9]*([0-9][0-9 ,and]*)`)
	rejectPattern    = regexp.MustCompile(`(?i)(?:drop|reject|removed?)\s*[^0-9]*([0-9][0-9 ,and]*)`)
	rejectAltPattern = regexp.MustCompile(`(?i)items?\s*([0-9][0-9 ,and]*)\s+(?:removed|dropped|rejected)`)
	numberPattern    = regexp.MustCompile(`[0-9]+`)
	replyMarker      = regexp.MustCompile(`<!--\s*runoq:bot:plan-comment-responder(?:\s+comment-id:([^\s]+))?\s*-->`)
)

// PendingComment is a human planning comment that still needs handling.
type PendingComment struct {
	ID   string
	Body string
}

// ParseHumanCommentSelection extracts approved/rejected item numbers from
// issue view JSON comments. Skips bot comments and event-tagged comments.
func ParseHumanCommentSelection(issueViewJSON string) (ItemSelection, error) {
	var view struct {
		Comments []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body string `json:"body"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(issueViewJSON), &view); err != nil {
		return ItemSelection{}, fmt.Errorf("parse issue view: %w", err)
	}

	var sel ItemSelection
	for _, c := range view.Comments {
		if c.Author.Login == "runoq" {
			continue
		}
		if strings.Contains(c.Body, "runoq:bot") {
			continue
		}
		sel = mergeSelections(sel, parseSelectionFromBody(c.Body))
	}
	return sel, nil
}

// FindUnrespondedCommentIDs returns the node IDs of human comments that have
// not been marked as responded to (no THUMBS_UP reaction). Skips bot comments
// and event-tagged comments.
func FindUnrespondedCommentIDs(issueViewJSON string) ([]string, error) {
	comments, err := FindUnrespondedComments(issueViewJSON)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	return ids, nil
}

// FindUnrespondedComments returns human planning comments that do not have a
// bot-authored processed marker yet.
func FindUnrespondedComments(issueViewJSON string) ([]PendingComment, error) {
	var view struct {
		Comments []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body           string `json:"body"`
			ID             string `json:"id"`
			ReactionGroups []struct {
				Content string `json:"content"`
				Users   struct {
					Nodes []struct {
						Login string `json:"login"`
					} `json:"nodes"`
				} `json:"users"`
			} `json:"reactionGroups"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(issueViewJSON), &view); err != nil {
		return nil, fmt.Errorf("parse issue view: %w", err)
	}

	respondedByMarker := make(map[string]struct{}, len(view.Comments))
	for _, c := range view.Comments {
		if !isRunoqBotLogin(c.Author.Login) {
			continue
		}
		match := replyMarker.FindStringSubmatch(c.Body)
		if len(match) < 2 || strings.TrimSpace(match[1]) == "" {
			continue
		}
		respondedByMarker[strings.TrimSpace(match[1])] = struct{}{}
	}

	var pending []PendingComment
	for _, c := range view.Comments {
		if isRunoqBotLogin(c.Author.Login) {
			continue
		}
		if strings.Contains(c.Body, "runoq:bot") {
			continue
		}
		if _, ok := respondedByMarker[c.ID]; ok {
			continue
		}
		if hasBotThumbsUp(c.ReactionGroups) {
			continue
		}
		pending = append(pending, PendingComment{ID: c.ID, Body: c.Body})
	}
	return pending, nil
}

// CommentHasSelection reports whether a comment body contains approve/reject
// selection directives that should influence apply selection.
func CommentHasSelection(body string) bool {
	sel := parseSelectionFromBody(body)
	return len(sel.Approved) > 0 || len(sel.Rejected) > 0
}

// ParseAgentResponse parses and validates the structured JSON output from a
// comment-responder agent. Handles JSON wrapped in markdown code fences.
func ParseAgentResponse(text string) (AgentResponse, error) {
	text = stripCodeFence(text)
	var resp AgentResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return AgentResponse{}, fmt.Errorf("invalid agent response JSON: %w", err)
	}
	switch resp.Action {
	case ActionQuestion, ActionApprove:
		// valid, no extra fields required
	case ActionChangeRequest:
		if resp.RevisedProposal == nil {
			return AgentResponse{}, fmt.Errorf("change-request action requires revised_proposal")
		}
	case "":
		return AgentResponse{}, fmt.Errorf("missing required field: action")
	default:
		return AgentResponse{}, fmt.Errorf("unknown action: %q", resp.Action)
	}
	if resp.Reply == "" {
		return AgentResponse{}, fmt.Errorf("missing required field: reply")
	}
	return resp, nil
}

func stripCodeFence(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 0 && text[0] == '{' {
		return text
	}
	start := strings.Index(text, "```")
	if start < 0 {
		return text
	}
	after := text[start+3:]
	nl := strings.Index(after, "\n")
	if nl < 0 {
		return text
	}
	content := after[nl+1:]
	end := strings.Index(content, "```")
	if end < 0 {
		return text
	}
	return strings.TrimSpace(content[:end])
}

func extractNumbers(s string) []int {
	matches := numberPattern.FindAllString(s, -1)
	nums := make([]int, 0, len(matches))
	for _, m := range matches {
		n, err := strconv.Atoi(m)
		if err == nil {
			nums = append(nums, n)
		}
	}
	return nums
}

func parseSelectionFromBody(body string) ItemSelection {
	var sel ItemSelection
	if m := approvePattern.FindStringSubmatch(body); m != nil {
		sel.Approved = append(sel.Approved, extractNumbers(m[1])...)
	}
	if m := rejectPattern.FindStringSubmatch(body); m != nil {
		sel.Rejected = append(sel.Rejected, extractNumbers(m[1])...)
	}
	if m := rejectAltPattern.FindStringSubmatch(body); m != nil {
		sel.Rejected = append(sel.Rejected, extractNumbers(m[1])...)
	}
	return sel
}

func mergeSelections(left, right ItemSelection) ItemSelection {
	left.Approved = append(left.Approved, right.Approved...)
	left.Rejected = append(left.Rejected, right.Rejected...)
	return left
}

func isRunoqBotLogin(login string) bool {
	login = strings.TrimSpace(login)
	return login == "runoq" || login == "runoq[bot]"
}

func hasBotThumbsUp(groups []struct {
	Content string `json:"content"`
	Users   struct {
		Nodes []struct {
			Login string `json:"login"`
		} `json:"nodes"`
	} `json:"users"`
}) bool {
	for _, group := range groups {
		if group.Content != "THUMBS_UP" {
			continue
		}
		for _, user := range group.Users.Nodes {
			if isRunoqBotLogin(user.Login) {
				return true
			}
		}
	}
	return false
}
