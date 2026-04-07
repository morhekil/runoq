package planning

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/agents"
)

// fakeInvoker returns a preset response for each agent call.
type fakeInvoker struct {
	responses map[string]string // agent name → response text
	calls     []string
}

func (f *fakeInvoker) Invoke(_ context.Context, opts agents.InvokeOptions) (agents.Response, error) {
	f.calls = append(f.calls, opts.Agent)
	text := f.responses[opts.Agent]
	return agents.Response{Text: text, CaptureDir: ""}, nil
}

func TestRunDispatchSingleRoundPass(t *testing.T) {
	t.Parallel()

	planFile := filepath.Join(t.TempDir(), "plan.md")
	os.WriteFile(planFile, []byte("# Plan"), 0o644)

	proposalJSON := `{"items":[{"title":"M1","type":"implementation","goal":"Ship it","criteria":["Works"],"priority":1}]}`
	techVerdict := "VERDICT: PASS\nSCORE: 32/35\nCHECKLIST:\n- [ ] None."
	productVerdict := "VERDICT: PASS\nSCORE: 27/30\nCHECKLIST:\n- [ ] None."

	invoker := &fakeInvoker{
		responses: map[string]string{
			"milestone-decomposer":    proposalJSON,
			"plan-reviewer-technical": techVerdict,
			"plan-reviewer-product":   productVerdict,
		},
	}

	var stderr bytes.Buffer
	result, err := RunDispatch(t.Context(), DispatchConfig{
		ReviewType: "milestone",
		PlanFile:   planFile,
		RunoqRoot:  t.TempDir(),
		MaxRounds:  3,
		Invoker:    invoker,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Warning != "" {
		t.Errorf("expected no warning, got %q", result.Warning)
	}
	if len(result.Proposal.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(result.Proposal.Items))
	}
	if result.Technical.Verdict != "PASS" {
		t.Errorf("technical verdict = %q", result.Technical.Verdict)
	}

	// Should have called: decomposer, tech reviewer, product reviewer
	if len(invoker.calls) != 3 {
		t.Errorf("expected 3 agent calls, got %d: %v", len(invoker.calls), invoker.calls)
	}
}

func TestRunDispatchIteratesOnReviewFailure(t *testing.T) {
	t.Parallel()

	planFile := filepath.Join(t.TempDir(), "plan.md")
	os.WriteFile(planFile, []byte("# Plan"), 0o644)

	callCount := 0
	invoker := &fakeInvoker{
		responses: map[string]string{
			"milestone-decomposer":    `{"items":[{"title":"M1","type":"implementation"}]}`,
			"plan-reviewer-technical": "VERDICT: ITERATE\nSCORE: 20/35\nCHECKLIST:\n- [ ] tighten scope",
			"plan-reviewer-product":   "VERDICT: PASS\nSCORE: 28/30\nCHECKLIST:\n- [ ] None.",
		},
	}

	var stderr bytes.Buffer
	result, err := RunDispatch(t.Context(), DispatchConfig{
		ReviewType: "milestone",
		PlanFile:   planFile,
		RunoqRoot:  t.TempDir(),
		MaxRounds:  2,
		Invoker:    invoker,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_ = callCount
	// With ITERATE on round 1 and max 2 rounds: decomposer×2 + reviewer×4 = 6 calls
	if len(invoker.calls) != 6 {
		t.Errorf("expected 6 agent calls (2 rounds), got %d: %v", len(invoker.calls), invoker.calls)
	}
	if result.Warning != "max review rounds reached" {
		t.Errorf("expected max rounds warning, got %q", result.Warning)
	}
}

func TestRunDispatchBodyContainsProposal(t *testing.T) {
	t.Parallel()

	planFile := filepath.Join(t.TempDir(), "plan.md")
	os.WriteFile(planFile, []byte("# Plan"), 0o644)

	invoker := &fakeInvoker{
		responses: map[string]string{
			"milestone-decomposer":    `{"items":[{"title":"M1","type":"implementation","goal":"Ship","criteria":["A"]}]}`,
			"plan-reviewer-technical": "VERDICT: PASS\nSCORE: 32/35\nCHECKLIST:\n- [ ] None.",
			"plan-reviewer-product":   "VERDICT: PASS\nSCORE: 28/30\nCHECKLIST:\n- [ ] None.",
		},
	}

	var stderr bytes.Buffer
	result, err := RunDispatch(t.Context(), DispatchConfig{
		ReviewType: "milestone",
		PlanFile:   planFile,
		RunoqRoot:  t.TempDir(),
		MaxRounds:  3,
		Invoker:    invoker,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := result.FormattedBody
	if !strings.Contains(body, "### 1. M1") {
		t.Error("body missing proposal heading")
	}
	if !strings.Contains(body, "runoq:payload:plan-proposal") {
		t.Error("body missing payload marker")
	}
	if !strings.Contains(body, "32/35") {
		t.Error("body missing technical score")
	}
}
