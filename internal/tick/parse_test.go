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

func TestParseAgentResponseQuestion(t *testing.T) {
	t.Parallel()
	input := `{"action":"question","reply":"The ordering reduces dependency risk."}`
	resp, err := ParseAgentResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Action != ActionQuestion {
		t.Errorf("action = %q, want question", resp.Action)
	}
	if resp.Reply != "The ordering reduces dependency risk." {
		t.Errorf("reply = %q", resp.Reply)
	}
	if resp.RevisedProposal != nil {
		t.Error("revised_proposal should be nil for question")
	}
}

func TestParseAgentResponseApprove(t *testing.T) {
	t.Parallel()
	input := `{"action":"approve","reply":"Acknowledged, proceeding with the approved set."}`
	resp, err := ParseAgentResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Action != ActionApprove {
		t.Errorf("action = %q, want approve", resp.Action)
	}
}

func TestParseAgentResponseChangeRequest(t *testing.T) {
	t.Parallel()
	input := `{"action":"change-request","reply":"Dropped item 3 as requested.","revised_proposal":{"items":[{"title":"A","type":"task"}]}}`
	resp, err := ParseAgentResponse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Action != ActionChangeRequest {
		t.Errorf("action = %q, want change-request", resp.Action)
	}
	if resp.RevisedProposal == nil {
		t.Fatal("revised_proposal must be non-nil for change-request")
	}
	if len(resp.RevisedProposal.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(resp.RevisedProposal.Items))
	}
}

func TestParseAgentResponseChangeRequestMissingProposal(t *testing.T) {
	t.Parallel()
	input := `{"action":"change-request","reply":"Dropped it."}`
	_, err := ParseAgentResponse(input)
	if err == nil {
		t.Fatal("expected error for change-request without revised_proposal")
	}
}

func TestParseAgentResponseMissingAction(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentResponse(`{"reply":"text"}`)
	if err == nil {
		t.Fatal("expected error for missing action")
	}
}

func TestParseAgentResponseMissingReply(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentResponse(`{"action":"question"}`)
	if err == nil {
		t.Fatal("expected error for missing reply")
	}
}

func TestParseAgentResponseUnknownAction(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentResponse(`{"action":"bogus","reply":"text"}`)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestParseAgentResponseFencedJSON(t *testing.T) {
	t.Parallel()
	input := "```json\n{\"action\":\"approve\",\"reply\":\"Ship it.\"}\n```"
	resp, err := ParseAgentResponse(input)
	if err != nil {
		t.Fatalf("should handle fenced JSON: %v", err)
	}
	if resp.Action != ActionApprove {
		t.Errorf("action = %q", resp.Action)
	}
}

func TestParseAgentResponseFencedJSONWithPreamble(t *testing.T) {
	t.Parallel()
	input := "Here's the response:\n\n```json\n{\"action\":\"question\",\"reply\":\"Because reasons.\"}\n```\n\nHope that helps."
	resp, err := ParseAgentResponse(input)
	if err != nil {
		t.Fatalf("should handle fenced JSON with preamble: %v", err)
	}
	if resp.Action != ActionQuestion {
		t.Errorf("action = %q", resp.Action)
	}
}

func TestParseAgentResponseInvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentResponse(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFindUnrespondedCommentIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		issueViewJSON string
		wantIDs       []string
	}{
		{
			name:          "no comments",
			issueViewJSON: `{"comments":[]}`,
		},
		{
			name:          "bot comment only",
			issueViewJSON: `{"comments":[{"author":{"login":"runoq"},"body":"reply","id":"IC1"}]}`,
		},
		{
			name:          "human comment without +1",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"question?","id":"IC1","reactionGroups":[]}]}`,
			wantIDs:       []string{"IC1"},
		},
		{
			name:          "human comment with +1 is skipped",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"done","id":"IC1","reactionGroups":[{"content":"THUMBS_UP","users":{"totalCount":1}}]}]}`,
		},
		{
			name:          "human comment with eyes only is not skipped",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"q?","id":"IC1","reactionGroups":[{"content":"EYES","users":{"totalCount":1}}]}]}`,
			wantIDs:       []string{"IC1"},
		},
		{
			name:          "event-tagged comment skipped",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"runoq:event done","id":"IC1","reactionGroups":[]}]}`,
		},
		{
			name: "multiple: one responded one not",
			issueViewJSON: `{"comments":[
				{"author":{"login":"human"},"body":"first","id":"IC1","reactionGroups":[{"content":"THUMBS_UP","users":{"totalCount":1}}]},
				{"author":{"login":"runoq"},"body":"<!-- runoq:event -->\nreply","id":"IC2","reactionGroups":[]},
				{"author":{"login":"human"},"body":"second","id":"IC3","reactionGroups":[]}
			]}`,
			wantIDs: []string{"IC3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ids, err := FindUnrespondedCommentIDs(tt.issueViewJSON)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !stringSliceEqual(ids, tt.wantIDs) {
				t.Errorf("got %v, want %v", ids, tt.wantIDs)
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
