package tick

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ParseVerdictBlock reads VERDICT/SCORE/CHECKLIST structured text from
// reviewer agent output.
func ParseVerdictBlock(text string) (ReviewScore, error) {
	var score ReviewScore
	var checklistLines []string
	inChecklist := false

	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "VERDICT:"); ok {
			score.Verdict = strings.TrimSpace(after)
			continue
		}
		if after, ok := strings.CutPrefix(trimmed, "SCORE:"); ok {
			score.Score = strings.TrimSpace(after)
			continue
		}
		if trimmed == "CHECKLIST:" {
			inChecklist = true
			continue
		}
		if inChecklist && trimmed != "" {
			checklistLines = append(checklistLines, trimmed)
		}
	}

	if score.Verdict == "" {
		return score, fmt.Errorf("missing VERDICT in verdict block")
	}
	if score.Score == "" {
		return score, fmt.Errorf("missing SCORE in verdict block")
	}
	score.Checklist = strings.Join(checklistLines, "\n")
	return score, nil
}

// ExtractMarkedJSONBlock extracts JSON from a marker-delimited fenced code
// block. It looks for a line containing the marker, then captures the content
// of the next fenced code block.
func ExtractMarkedJSONBlock(text, marker string) (string, error) {
	sawMarker := false
	inBlock := false
	var block strings.Builder

	for line := range strings.SplitSeq(text, "\n") {
		if !sawMarker {
			if strings.Contains(line, marker) {
				sawMarker = true
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if !inBlock {
				inBlock = true
				continue
			}
			// End of block
			return strings.TrimRight(block.String(), "\n"), nil
		}
		if inBlock {
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}

	if !sawMarker {
		return "", fmt.Errorf("marker %q not found", marker)
	}
	return "", fmt.Errorf("no fenced code block after marker %q", marker)
}

var (
	approvePattern = regexp.MustCompile(`(?i)approve\s+items?\s*[^0-9]*([0-9][0-9 ,and]*)`)
	rejectPattern  = regexp.MustCompile(`(?i)(?:drop|reject|removed?)\s*[^0-9]*([0-9][0-9 ,and]*)`)
	rejectAltPattern = regexp.MustCompile(`(?i)items?\s*([0-9][0-9 ,and]*)\s+(?:removed|dropped|rejected)`)
	numberPattern  = regexp.MustCompile(`[0-9]+`)
)

// ParseHumanCommentSelection extracts approved/rejected item numbers from
// issue view JSON comments. It skips bot comments and event-tagged comments.
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

// stripCodeFence extracts JSON from markdown code fences if present.
// Returns the original text if no fence is found.
func stripCodeFence(text string) string {
	text = strings.TrimSpace(text)
	// Try direct JSON parse first
	if len(text) > 0 && text[0] == '{' {
		return text
	}
	// Look for ```json ... ``` pattern
	start := strings.Index(text, "```")
	if start < 0 {
		return text
	}
	// Skip the opening fence line
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

// AgentAction classifies the intent of a plan-comment-responder response.
type AgentAction string

const (
	ActionQuestion      AgentAction = "question"
	ActionChangeRequest AgentAction = "change-request"
	ActionApprove       AgentAction = "approve"
)

// AgentResponse is the structured output from the plan-comment-responder agent.
type AgentResponse struct {
	Action          AgentAction `json:"action"`
	Reply           string      `json:"reply"`
	RevisedProposal *Proposal   `json:"revised_proposal,omitzero"`
}

// ParseAgentResponse parses and validates the structured JSON output from the
// plan-comment-responder agent. Returns an error if the JSON is invalid, the
// action is unknown, or required fields are missing. Handles JSON wrapped in
// markdown code fences (agents sometimes add these despite instructions).
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
