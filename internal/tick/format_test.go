package tick

import (
	"encoding/json"
	"os"
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

	data := loadFixture(t, "../../test/fixtures/tick/milestone-decomposer-output.json")
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
		Technical: ReviewScore{Verdict: "PASS", Score: "32/35"},
		Product:   ReviewScore{Verdict: "PASS", Score: "27/30"},
	}

	got := FormatProposalCommentBody(input)

	// Review scores table
	if !containsString(got, "| Technical | 32/35 | PASS |") {
		t.Error("missing technical score row")
	}
	if !containsString(got, "| Product | 27/30 | PASS |") {
		t.Error("missing product score row")
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
