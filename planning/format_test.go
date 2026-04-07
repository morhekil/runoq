package planning

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("load fixture %s: %v", path, err)
	}
	return data
}

func TestFormatPlanProposal(t *testing.T) {
	t.Parallel()

	data := loadFixture(t, "../test/fixtures/tick/milestone-decomposer-output.json")
	var p Proposal
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	got := FormatPlanProposal(p)

	// Must start with the marker
	if got[:len("<!-- runoq:payload:plan-proposal -->")] != "<!-- runoq:payload:plan-proposal -->" {
		t.Fatalf("missing payload marker at start")
	}

	// Spot-check structure: numbered headings
	for _, want := range []string{
		"### 1. Core formatter",
		"### 2. Caching strategy",
		"### 3. CLI wrapper",
	} {
		if !containsString(got, want) {
			t.Errorf("missing heading %q", want)
		}
	}

	// Type + priority metadata
	if !containsString(got, "**Type:** implementation · **Priority:** 1") {
		t.Error("missing type/priority for item 1")
	}

	// Blockquote goal
	if !containsString(got, "> Ship a reusable progress formatter") {
		t.Error("missing blockquote goal")
	}

	// Criteria checkboxes
	if !containsString(got, "- [ ] overflow values are clamped to 100 percent") {
		t.Error("missing criteria checkbox")
	}

	// Scope bullets
	if !containsString(got, "- src/progress.js") {
		t.Error("missing scope bullet")
	}

	// Section separators
	if !containsString(got, "\n---\n") {
		t.Error("missing section separator")
	}
}

func TestFormatPlanProposalNoPriority(t *testing.T) {
	t.Parallel()

	p := Proposal{
		Items: []ProposalItem{
			{Title: "No priority item", Type: "discovery", Goal: "Test"},
		},
	}

	got := FormatPlanProposal(p)
	if containsString(got, "Priority") {
		t.Error("should not include Priority when not set")
	}
	if !containsString(got, "**Type:** discovery") {
		t.Error("missing type without priority")
	}
}

func TestFormatPlanProposalEmptySections(t *testing.T) {
	t.Parallel()

	p := Proposal{
		Items: []ProposalItem{
			{Title: "Bare item", Type: "implementation"},
		},
	}

	got := FormatPlanProposal(p)
	if containsString(got, "Acceptance criteria") {
		t.Error("should not include criteria section when empty")
	}
	if containsString(got, "Scope") {
		t.Error("should not include scope section when empty")
	}
	if containsString(got, "\n> ") {
		t.Error("should not include blockquote when goal is empty")
	}
}

func TestFormatProposalCommentBody(t *testing.T) {
	t.Parallel()

	input := ProposalCommentInput{
		Proposal: Proposal{
			Items: []ProposalItem{
				{Title: "Core formatter", Type: "implementation", Goal: "Ship it", Criteria: []string{"Works"}, Priority: new(1)},
			},
			Warnings: []string{"- M2 depends on external API", "M3 scope may grow"},
		},
		Technical: ReviewScore{Verdict: "PASS", Score: "32/35", Checklist: "- [ ] tighten scope on item 2"},
		Product:   ReviewScore{Verdict: "PASS", Score: "27/30", Checklist: "- [ ] clarify acceptance criteria"},
	}

	got := FormatProposalCommentBody(input)

	// Review scores table
	if !containsString(got, "| Technical | 32/35 | PASS |") {
		t.Error("missing technical score row")
	}
	if !containsString(got, "| Product | 27/30 | PASS |") {
		t.Error("missing product score row")
	}

	// Reviewer checklists should be shown when non-empty
	if !containsString(got, "tighten scope on item 2") {
		t.Error("missing technical reviewer checklist")
	}
	if !containsString(got, "clarify acceptance criteria") {
		t.Error("missing product reviewer checklist")
	}

	// Warnings rendered with dash-prefixed text (the printf bug scenario)
	if !containsString(got, "- - M2 depends on external API") {
		t.Error("warning starting with dash not rendered correctly")
	}
	if !containsString(got, "- M3 scope may grow") {
		t.Error("normal warning not rendered")
	}

	// Proposal content included
	if !containsString(got, "### 1. Core formatter") {
		t.Error("missing proposal content")
	}

	// JSON in collapsed details
	if !containsString(got, "<details>") {
		t.Error("missing details tag")
	}
	if !containsString(got, "```json") {
		t.Error("missing json code fence")
	}
}

func TestFormatProposalCommentBodyWithWarning(t *testing.T) {
	t.Parallel()

	input := ProposalCommentInput{
		Proposal:  Proposal{Items: []ProposalItem{{Title: "X", Type: "implementation"}}},
		Technical: ReviewScore{Verdict: "PASS", Score: "30/35"},
		Product:   ReviewScore{Verdict: "PASS", Score: "25/30"},
		Warning:   "max review rounds reached",
	}

	got := FormatProposalCommentBody(input)
	if !containsString(got, "> **Warning:** max review rounds reached") {
		t.Error("missing warning blockquote")
	}
}

func TestFormatProposalCommentBodyTaskMode(t *testing.T) {
	t.Parallel()

	input := ProposalCommentInput{
		Proposal: Proposal{
			Items: []ProposalItem{
				{
					Title:               "Implement formatter",
					Type:                "task",
					Body:                "## Context\n\nAdd the base `formatProgress(complete, total)` function.\n\n## Acceptance Criteria\n\n- [ ] formatProgress returns a human-readable string",
					EstimatedComplexity: "medium",
					ComplexityRationale: "touches two modules",
					Priority:            new(1),
				},
			},
		},
		Technical: ReviewScore{Verdict: "PASS", Score: "30/35"},
		Product:   ReviewScore{Verdict: "PASS", Score: "25/30"},
		ReviewType: "task",
	}

	got := FormatProposalCommentBody(input)

	if !containsString(got, "## Proposed tasks") {
		t.Error("should say 'Proposed tasks' not 'Proposed milestones' for task review type")
	}
	if containsString(got, "Proposed milestones") {
		t.Error("should not say 'Proposed milestones' for task review type")
	}
	if !containsString(got, "**Complexity:** medium") {
		t.Error("missing complexity")
	}
	if !containsString(got, "touches two modules") {
		t.Error("missing complexity rationale")
	}
	// Body content should be rendered (not just in the JSON details block)
	bodyIdx := strings.Index(got, "formatProgress(complete, total)")
	detailsIdx := strings.Index(got, "<details>")
	if bodyIdx < 0 {
		t.Error("missing task body content")
	} else if detailsIdx >= 0 && bodyIdx > detailsIdx {
		t.Error("task body content only appears inside details block, should be rendered in main content")
	}
}

func TestFormatProposalCommentBodyNoWarnings(t *testing.T) {
	t.Parallel()

	input := ProposalCommentInput{
		Proposal:  Proposal{Items: []ProposalItem{{Title: "X", Type: "implementation"}}},
		Technical: ReviewScore{Verdict: "PASS", Score: "30/35"},
		Product:   ReviewScore{Verdict: "PASS", Score: "25/30"},
	}

	got := FormatProposalCommentBody(input)
	if containsString(got, "Warnings from decomposer") {
		t.Error("should not include warnings section when no warnings")
	}
}

func TestFormatMilestoneBody(t *testing.T) {
	t.Parallel()

	item := ProposalItem{
		Goal:     "Ship a reusable formatter",
		Scope:    []string{"src/progress.js", "test/progress.test.js"},
		Criteria: []string{"formats input", "clamps overflow"},
	}

	got := FormatMilestoneBody(item)

	if !containsString(got, "## Context") {
		t.Error("missing context section")
	}
	if !containsString(got, "Goal: Ship a reusable formatter") {
		t.Error("missing goal")
	}
	if !containsString(got, "Scope: src/progress.js, test/progress.test.js") {
		t.Error("missing scope")
	}
	if !containsString(got, "- [ ] formats input") {
		t.Error("missing criteria checkbox")
	}
}

func TestFormatAdjustmentReviewBody(t *testing.T) {
	t.Parallel()

	data := loadFixture(t, "../test/fixtures/tick/milestone-reviewer-adjustment.json")
	var input AdjustmentReviewInput
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := FormatAdjustmentReviewBody(input)

	if !containsString(got, "## Acceptance Criteria") {
		t.Error("missing acceptance criteria section")
	}
	// Each adjustment should have type, title, description, and reason
	if !containsString(got, "### 1. Add validation scope") {
		t.Error("missing adjustment 1 heading")
	}
	if !containsString(got, "**Type:** modify") {
		t.Error("missing adjustment 1 type")
	}
	if !containsString(got, "Expand the caching discovery milestone") {
		t.Error("missing adjustment 1 description")
	}
	if !containsString(got, "Formatter implementation exposed input-shape risks") {
		t.Error("missing adjustment 1 reason")
	}
	if !containsString(got, "### 2. Debt cleanup from formatter shortcuts") {
		t.Error("missing adjustment 2 heading")
	}
	if !containsString(got, "<details>") {
		t.Error("missing collapsed JSON")
	}
}

func TestMergeChecklists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		left, right string
		want        string
	}{
		{"both empty", "", "", ""},
		{"left only", "- [ ] A\n- [ ] B", "", "- [ ] A\n- [ ] B"},
		{"right only", "", "- [ ] C", "- [ ] C"},
		{"both", "- [ ] A", "- [ ] B\n- [ ] C", "- [ ] A\n- [ ] B\n- [ ] C"},
		{"strips blank lines", "- [ ] A\n\n- [ ] B", "- [ ] C", "- [ ] A\n- [ ] B\n- [ ] C"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MergeChecklists(tt.left, tt.right)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReplaceProposalInBody(t *testing.T) {
	t.Parallel()

	existingBody := "<!-- runoq:meta\ntype: planning\n-->\n\n## Acceptance Criteria\n- [ ] Done.\n\n<!-- runoq:proposal-start -->\n## Review scores\nold content"
	newProposal := "## Review scores\nnew content"

	got := ReplaceProposalInBody(existingBody, newProposal)

	// Metadata preserved
	if !containsString(got, "<!-- runoq:meta") {
		t.Error("metadata block lost")
	}
	if !containsString(got, "## Acceptance Criteria") {
		t.Error("acceptance criteria lost")
	}
	// Old proposal replaced
	if containsString(got, "old content") {
		t.Error("old proposal content not replaced")
	}
	// New proposal present
	if !containsString(got, "new content") {
		t.Error("new proposal content missing")
	}
	// Marker preserved
	if !containsString(got, "<!-- runoq:proposal-start -->") {
		t.Error("proposal-start marker missing")
	}
}

func TestReplaceProposalInBodyNoExistingMarker(t *testing.T) {
	t.Parallel()

	existingBody := "<!-- runoq:meta\ntype: planning\n-->\n\n## Acceptance Criteria\n- [ ] Done."
	newProposal := "## Review scores\nnew content"

	got := ReplaceProposalInBody(existingBody, newProposal)

	if !containsString(got, "## Acceptance Criteria") {
		t.Error("acceptance criteria lost")
	}
	if !containsString(got, "<!-- runoq:proposal-start -->") {
		t.Error("proposal-start marker missing")
	}
	if !containsString(got, "new content") {
		t.Error("new proposal content missing")
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
