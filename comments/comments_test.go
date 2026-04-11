package comments

import (
	"testing"
)

func TestParseHumanCommentSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		issueViewJSON string
		wantApproved []int
		wantRejected []int
	}{
		{
			name:          "approve items",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"approve items 1, 3"}]}`,
			wantApproved:  []int{1, 3},
		},
		{
			name:          "drop items",
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
			name:          "bot comments ignored",
			issueViewJSON: `{"comments":[{"author":{"login":"runoq"},"body":"approve items 1, 2"}]}`,
		},
		{
			name:          "bot marker comments ignored",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"<!-- runoq:bot:orchestrator -->\napprove items 1"}]}`,
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
			name:          "human comment with human thumbs up is not skipped",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"done","id":"IC1","reactionGroups":[{"content":"THUMBS_UP","users":{"nodes":[{"login":"human2"}]}}]}]}`,
			wantIDs:       []string{"IC1"},
		},
		{
			name:          "human comment with bot thumbs up is skipped",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"done","id":"IC1","reactionGroups":[{"content":"THUMBS_UP","users":{"nodes":[{"login":"runoq"}]}}]}]}`,
		},
		{
			name:          "human comment with eyes only is not skipped",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"q?","id":"IC1","reactionGroups":[{"content":"EYES","users":{"totalCount":1}}]}]}`,
			wantIDs:       []string{"IC1"},
		},
		{
			name:          "bot-tagged comment skipped",
			issueViewJSON: `{"comments":[{"author":{"login":"human"},"body":"<!-- runoq:bot:orchestrator -->\ndone","id":"IC1","reactionGroups":[]}]}`,
		},
		{
			name: "multiple: one responded one not",
			issueViewJSON: `{"comments":[
				{"author":{"login":"human"},"body":"first","id":"IC1","reactionGroups":[{"content":"THUMBS_UP","users":{"nodes":[{"login":"runoq"}]}}]},
				{"author":{"login":"runoq"},"body":"<!-- runoq:bot:plan-comment-responder -->\nreply","id":"IC2","reactionGroups":[]},
				{"author":{"login":"human"},"body":"second","id":"IC3","reactionGroups":[]}
			]}`,
			wantIDs: []string{"IC3"},
		},
		{
			name: "bot reply marker for original comment skips it",
			issueViewJSON: `{"comments":[
				{"author":{"login":"human"},"body":"Please revise","id":"IC1","reactionGroups":[]},
				{"author":{"login":"runoq"},"body":"<!-- runoq:bot:plan-comment-responder comment-id:IC1 -->\nDone.","id":"IC2","reactionGroups":[]}
			]}`,
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
	input := `{"action":"approve","reply":"Ship it."}`
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
	input := `{"action":"change-request","reply":"Dropped item 3.","revised_proposal":{"items":[{"title":"A","type":"task"}]}}`
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
}

func TestParseAgentResponseChangeRequestMissingProposal(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentResponse(`{"action":"change-request","reply":"Dropped it."}`)
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

func TestParseAgentResponseInvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := ParseAgentResponse(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
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

func stringSliceEqual(a, b []string) bool {
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
