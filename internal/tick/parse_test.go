package tick

import (
	"testing"
)

func TestParseVerdictBlock(t *testing.T) {
	t.Parallel()

	text := string(loadFixture(t, "../../test/fixtures/tick/reviewer-technical-pass.txt"))
	score, err := ParseVerdictBlock(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score.Verdict != "PASS" {
		t.Errorf("verdict = %q, want PASS", score.Verdict)
	}
	if score.Score != "31/35" {
		t.Errorf("score = %q, want 31/35", score.Score)
	}
	if score.Checklist != "- [ ] No blocking technical concerns." {
		t.Errorf("checklist = %q", score.Checklist)
	}
}

func TestParseVerdictBlockMissingFields(t *testing.T) {
	t.Parallel()

	_, err := ParseVerdictBlock("some random text")
	if err == nil {
		t.Fatal("expected error for text without VERDICT")
	}
}

func TestExtractMarkedJSONBlock(t *testing.T) {
	t.Parallel()

	text := `Some preamble text.
<!-- runoq:payload:plan-proposal -->
` + "```json\n" + `{"items":[{"title":"Test"}]}` + "\n```\n" + `Trailing text.`

	got, err := ExtractMarkedJSONBlock(text, "runoq:payload:plan-proposal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != `{"items":[{"title":"Test"}]}` {
		t.Errorf("extracted = %q", got)
	}
}

func TestExtractMarkedJSONBlockNotFound(t *testing.T) {
	t.Parallel()

	_, err := ExtractMarkedJSONBlock("no markers here", "runoq:payload:plan-proposal")
	if err == nil {
		t.Fatal("expected error when marker not found")
	}
}

func TestParseHumanCommentSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		issueViewJSON    string
		wantApproved     []int
		wantRejected     []int
	}{
		{
			name: "approve items",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"approve items 1, 3"}]}`,
			wantApproved:  []int{1, 3},
		},
		{
			name: "drop items",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"drop 2"}]}`,
			wantRejected:  []int{2},
		},
		{
			name: "mixed approve and reject",
			issueViewJSON: `{"comments":[
				{"author":{"login":"human"},"body":"approve item 1"},
				{"author":{"login":"human"},"body":"reject 3"}
			]}`,
			wantApproved: []int{1},
			wantRejected: []int{3},
		},
		{
			name:          "no comments",
			issueViewJSON: `{"comments":[]}`,
		},
		{
			name: "bot comments ignored",
			issueViewJSON: `{"comments":[{"author":{"login":"runoq"},"body":"approve items 1, 2"}]}`,
		},
		{
			name: "event comments ignored",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"runoq:event approve items 1"}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sel, err := ParseHumanCommentSelection(tt.issueViewJSON)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !intSliceEqual(sel.Approved, tt.wantApproved) {
				t.Errorf("approved = %v, want %v", sel.Approved, tt.wantApproved)
			}
			if !intSliceEqual(sel.Rejected, tt.wantRejected) {
				t.Errorf("rejected = %v, want %v", sel.Rejected, tt.wantRejected)
			}
		})
	}
}

func intSliceEqual(a, b []int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
