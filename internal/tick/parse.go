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
