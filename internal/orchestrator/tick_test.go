package orchestrator

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

// stubExecutor returns a CommandExecutor that responds to gh commands with
// predefined outputs based on substring matching.
type ghStub struct {
	rules []ghStubRule
	calls []string
}

type ghStubRule struct {
	contains string
	stdout   string
	err      error
}

func (s *ghStub) exec(_ context.Context, req shell.CommandRequest) error {
	cmd := req.Name + " " + strings.Join(req.Args, " ")
	s.calls = append(s.calls, cmd)
	for _, r := range s.rules {
		if strings.Contains(cmd, r.contains) {
			if req.Stdout != nil && r.stdout != "" {
				req.Stdout.Write([]byte(r.stdout))
			}
			return r.err
		}
	}
	return nil
}

func TestRunTickNoEpicsBootstraps(t *testing.T) {
	t.Parallel()

	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: "[]"},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:        "owner/repo",
		PlanFile:    "docs/plan.md",
		RunoqRoot:   t.TempDir(),
		ExecCommand: stub.exec,
		Stdout:      &stdout,
		Stderr:      &stderr,
	})

	// No epics → should bootstrap (call plan-dispatch or issue create)
	// For now just verify it returns 0 (work done)
	if result != 0 {
		t.Errorf("RunTick = %d, want 0; stderr = %s", result, stderr.String())
	}
}

func TestRunTickAllClosedReturnsWaiting(t *testing.T) {
	t.Parallel()

	stub := &ghStub{
		rules: []ghStubRule{
			{contains: "issue list", stdout: `[{"number":1,"title":"M1","state":"CLOSED","body":"<!-- runoq:meta\ntype: epic\n-->","labels":[],"url":"u"}]`},
		},
	}

	var stdout, stderr bytes.Buffer
	result := RunTick(t.Context(), TickConfig{
		Repo:        "owner/repo",
		PlanFile:    "docs/plan.md",
		RunoqRoot:   t.TempDir(),
		ExecCommand: stub.exec,
		Stdout:      &stdout,
		Stderr:      &stderr,
	})

	if result != 2 {
		t.Errorf("RunTick = %d, want 2 (waiting); stderr = %s", result, stderr.String())
	}
	if !strings.Contains(stdout.String(), "All milestones complete") {
		t.Errorf("stdout = %q", stdout.String())
	}
}
