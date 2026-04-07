package planning

import (
	"fmt"
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

// MetadataValue extracts a value from a runoq:meta block in an issue body.
// Returns empty string if the key is not found.
func MetadataValue(body string, key string) string {
	inMeta := false
	prefix := key + ":"
	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "<!-- runoq:meta") {
			inMeta = true
			continue
		}
		if inMeta && strings.Contains(trimmed, "-->") {
			return ""
		}
		if inMeta && strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	return ""
}

// MetadataPriority extracts the priority from a runoq:meta block.
// Returns 999999 if not found or not a valid integer.
func MetadataPriority(body string) int {
	val := MetadataValue(body, "priority")
	if val == "" {
		return 999999
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 999999
	}
	return n
}
