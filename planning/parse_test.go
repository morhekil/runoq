package planning

import (
	"testing"
)

func TestParseVerdictBlock(t *testing.T) {
	t.Parallel()

	text := string(loadFixture(t, "testdata/reviewer-technical-pass.txt"))
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
