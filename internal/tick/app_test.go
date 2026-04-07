package tick

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/saruman/runoq/comments"
	"github.com/saruman/runoq/planning"
)

func TestFormatProposalSubcommand(t *testing.T) {
	t.Parallel()

	input := `{"items":[{"title":"Core","type":"implementation","goal":"Ship it","criteria":["Works"],"priority":1}]}`
	var stdout, stderr bytes.Buffer
	app := New([]string{"format-proposal"}, strings.NewReader(input), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "### 1. Core") {
		t.Errorf("output missing heading: %s", stdout.String())
	}
}

func TestProposalCommentBodySubcommand(t *testing.T) {
	t.Parallel()

	input := planning.ProposalCommentInput{
		Proposal:  planning.Proposal{Items: []planning.ProposalItem{{Title: "X", Type: "task"}}},
		Technical: planning.ReviewScore{Verdict: "PASS", Score: "30/35"},
		Product:   planning.ReviewScore{Verdict: "PASS", Score: "25/30"},
	}
	data, _ := json.Marshal(input)
	var stdout, stderr bytes.Buffer
	app := New([]string{"proposal-comment-body"}, bytes.NewReader(data), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "## Review scores") {
		t.Errorf("output missing review scores: %s", stdout.String())
	}
}

func TestMilestoneBodySubcommand(t *testing.T) {
	t.Parallel()

	input := `{"goal":"Ship it","scope":["src"],"criteria":["Works"]}`
	var stdout, stderr bytes.Buffer
	app := New([]string{"milestone-body"}, strings.NewReader(input), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "## Context") {
		t.Errorf("output missing context: %s", stdout.String())
	}
}

func TestParseVerdictSubcommand(t *testing.T) {
	t.Parallel()

	input := "VERDICT: PASS\nSCORE: 31/35\nCHECKLIST:\n- [ ] None.\n"
	var stdout, stderr bytes.Buffer
	app := New([]string{"parse-verdict"}, strings.NewReader(input), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var score planning.ReviewScore
	if err := json.Unmarshal(stdout.Bytes(), &score); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if score.Verdict != "PASS" {
		t.Errorf("verdict = %q", score.Verdict)
	}
}

func TestExtractJSONSubcommand(t *testing.T) {
	t.Parallel()

	input := "<!-- runoq:payload:test -->\n```json\n{\"ok\":true}\n```\n"
	var stdout, stderr bytes.Buffer
	app := New([]string{"extract-json", "runoq:payload:test"}, strings.NewReader(input), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != `{"ok":true}` {
		t.Errorf("output = %q", stdout.String())
	}
}

func TestHumanCommentSelectionSubcommand(t *testing.T) {
	t.Parallel()

	input := `{"comments":[{"author":{"login":"human"},"body":"approve items 1, 3"}]}`
	var stdout, stderr bytes.Buffer
	app := New([]string{"human-comment-selection"}, strings.NewReader(input), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var sel comments.ItemSelection
	if err := json.Unmarshal(stdout.Bytes(), &sel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sel.Approved) != 2 || sel.Approved[0] != 1 || sel.Approved[1] != 3 {
		t.Errorf("approved = %v", sel.Approved)
	}
}

func TestSelectItemsSubcommand(t *testing.T) {
	t.Parallel()

	input := `{"items":[{"title":"A"},{"title":"B"},{"title":"C"}]}`
	var stdout, stderr bytes.Buffer
	app := New([]string{"select-items", "--selection", `{"approved":[1,3],"rejected":[]}`}, strings.NewReader(input), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var p planning.Proposal
	if err := json.Unmarshal(stdout.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Items) != 2 || p.Items[0].Title != "A" || p.Items[1].Title != "C" {
		t.Errorf("items = %v", p.Items)
	}
}

func TestMergeChecklistsSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := New([]string{"merge-checklists", "- [ ] A", "- [ ] B"}, strings.NewReader(""), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "- [ ] A\n- [ ] B" {
		t.Errorf("output = %q", stdout.String())
	}
}

func TestParseAgentResponseSubcommand(t *testing.T) {
	t.Parallel()

	input := `{"action":"approve","reply":"Ship it."}`
	var stdout, stderr bytes.Buffer
	app := New([]string{"parse-agent-response"}, strings.NewReader(input), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var resp comments.AgentResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Action != comments.ActionApprove {
		t.Errorf("action = %q", resp.Action)
	}
}

func TestReplaceProposalInBodySubcommand(t *testing.T) {
	t.Parallel()

	existingBody := "metadata\n\n<!-- runoq:proposal-start -->\nold proposal"
	proposalFile := t.TempDir() + "/proposal.md"
	os.WriteFile(proposalFile, []byte("new proposal"), 0o644)

	var stdout, stderr bytes.Buffer
	app := New([]string{"replace-proposal-in-body", proposalFile}, strings.NewReader(existingBody), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "new proposal") {
		t.Errorf("output missing new proposal: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), "old proposal") {
		t.Error("output still contains old proposal")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := New([]string{"bogus"}, strings.NewReader(""), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}

func TestNoSubcommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := New(nil, strings.NewReader(""), &stdout, &stderr)

	code := app.Run(t.Context())
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
}
