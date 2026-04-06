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
