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
)

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
		if strings.Contains(c.Body, "runoq:event") {
			continue
		}
		body := c.Body
		if m := approvePattern.FindStringSubmatch(body); m != nil {
			sel.Approved = append(sel.Approved, extractNumbers(m[1])...)
		}
		if m := rejectPattern.FindStringSubmatch(body); m != nil {
			sel.Rejected = append(sel.Rejected, extractNumbers(m[1])...)
		}
		if m := rejectAltPattern.FindStringSubmatch(body); m != nil {
			sel.Rejected = append(sel.Rejected, extractNumbers(m[1])...)
		}
	}
	return sel, nil
}

// FindUnrespondedCommentIDs returns the node IDs of human comments that have
// not been marked as responded to (no THUMBS_UP reaction). Skips bot comments
// and event-tagged comments.
func FindUnrespondedCommentIDs(issueViewJSON string) ([]string, error) {
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
					TotalCount int `json:"totalCount"`
				} `json:"users"`
			} `json:"reactionGroups"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(issueViewJSON), &view); err != nil {
		return nil, fmt.Errorf("parse issue view: %w", err)
	}

	var ids []string
	for _, c := range view.Comments {
		if c.Author.Login == "runoq" {
			continue
		}
		if strings.Contains(c.Body, "runoq:event") {
			continue
		}
		hasThumbsUp := false
		for _, rg := range c.ReactionGroups {
			if rg.Content == "THUMBS_UP" && rg.Users.TotalCount > 0 {
				hasThumbsUp = true
				break
			}
		}
		if hasThumbsUp {
			continue
		}
		ids = append(ids, c.ID)
	}
	return ids, nil
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
