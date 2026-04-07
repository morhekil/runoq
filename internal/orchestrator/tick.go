package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/saruman/runoq/internal/shell"
	"github.com/saruman/runoq/planning"
)

// TickConfig configures a single tick execution.
type TickConfig struct {
	Repo        string
	PlanFile    string
	RunoqRoot   string
	Env         []string
	ExecCommand shell.CommandExecutor
	Stdout      io.Writer
	Stderr      io.Writer
}

// issue represents a GitHub issue from the issue list.
type issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Body   string   `json:"body"`
	URL    string   `json:"url"`
	Labels []label  `json:"labels"`
}

type label struct {
	Name string `json:"name"`
}

// RunTick executes one step of the planning lifecycle.
// Returns 0 (work done), 2 (waiting), or 1 (error).
func RunTick(ctx context.Context, cfg TickConfig) int {
	t := &tickRunner{cfg: cfg}
	return t.run(ctx)
}

type tickRunner struct {
	cfg    TickConfig
	issues []issue
}

func (t *tickRunner) run(ctx context.Context) int {
	t.step("Starting tick")
	t.detail("repo", t.cfg.Repo)
	t.detail("plan", t.cfg.PlanFile)

	t.step("Fetching issues")
	raw, err := t.ghOutput(ctx, "issue", "list", "--repo", t.cfg.Repo, "--state", "all", "--limit", "200", "--json", "number,title,body,labels,state,url")
	if err != nil {
		return t.fail("fetch issues: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &t.issues); err != nil {
		return t.fail("parse issues: %v", err)
	}
	t.info(fmt.Sprintf("found %d issues", len(t.issues)))

	t.step("Finding current epic")
	epic := t.firstOpenEpic()
	if epic == nil {
		if !t.anyEpicExists() {
			t.info("no epics exist — bootstrapping project")
			return t.handleBootstrap(ctx)
		}
		t.success("All milestones complete")
		fmt.Fprintln(t.cfg.Stdout, "All milestones complete")
		return 2
	}
	t.detail("epic", fmt.Sprintf("#%d %s", epic.Number, epic.Title))

	// TODO: pending review, approved review, planning dispatch, implementation, milestone complete
	t.warn(fmt.Sprintf("tick not fully ported — epic #%d needs attention", epic.Number))
	fmt.Fprintf(t.cfg.Stdout, "tick not fully ported — epic #%d needs attention\n", epic.Number)
	return 2
}

func (t *tickRunner) handleBootstrap(ctx context.Context) int {
	// Call the existing plan-dispatch pipeline via shell scripts
	// This is an interim step — M6 will replace with direct Go calls
	issueQueueScript := filepath.Join(t.cfg.RunoqRoot, "scripts", "gh-issue-queue.sh")
	planDispatchScript := filepath.Join(t.cfg.RunoqRoot, "scripts", "plan-dispatch.sh")

	// Create Project Planning epic
	t.info("creating Project Planning epic")
	epicOutput, err := t.scriptOutput(ctx, issueQueueScript, "create", t.cfg.Repo, "Project Planning", "## Acceptance Criteria\n\n- [ ] Milestones proposed.", "--type", "epic", "--priority", "1", "--estimated-complexity", "low")
	if err != nil {
		return t.fail("create epic: %v", err)
	}
	epicNumber := extractIssueNumber(epicOutput)
	t.detail("epic", "#"+epicNumber)

	// Create planning issue
	t.info("creating planning issue")
	planningOutput, err := t.scriptOutput(ctx, issueQueueScript, "create", t.cfg.Repo, "Break plan into milestones", "## Acceptance Criteria\n\n- [ ] Milestones proposed.", "--type", "planning", "--priority", "1", "--estimated-complexity", "low", "--parent-epic", epicNumber)
	if err != nil {
		return t.fail("create planning issue: %v", err)
	}
	planningNumber := extractIssueNumber(planningOutput)
	t.detail("planning issue", "#"+planningNumber)

	// Dispatch milestone decomposition
	t.step("Running milestone decomposition on #" + planningNumber)
	if err := t.runScript(ctx, planDispatchScript, t.cfg.Repo, planningNumber, "milestone", t.cfg.PlanFile); err != nil {
		return t.fail("plan dispatch: %v", err)
	}

	// Assign after proposal is posted
	t.scriptOutput(ctx, issueQueueScript, "assign", t.cfg.Repo, planningNumber)

	t.success("Proposal posted on #" + planningNumber)
	fmt.Fprintf(t.cfg.Stdout, "Created planning milestone. Proposal posted on #%s\n", planningNumber)
	return 0
}

// --- Issue queries ---

func (t *tickRunner) firstOpenEpic() *issue {
	var best *issue
	bestPriority := 999999
	for i := range t.issues {
		iss := &t.issues[i]
		if iss.State != "OPEN" {
			continue
		}
		if planning.MetadataValue(iss.Body, "type") != "epic" {
			continue
		}
		p := planning.MetadataPriority(iss.Body)
		if p < bestPriority {
			bestPriority = p
			best = iss
		}
	}
	return best
}

func (t *tickRunner) anyEpicExists() bool {
	for _, iss := range t.issues {
		if planning.MetadataValue(iss.Body, "type") == "epic" {
			return true
		}
	}
	return false
}

// --- Shell helpers (interim — replaced by Go package calls in M6) ---

func (t *tickRunner) ghOutput(ctx context.Context, args ...string) (string, error) {
	return shell.CommandOutput(ctx, t.cfg.ExecCommand, shell.CommandRequest{
		Name: "gh",
		Args: args,
		Env:  t.cfg.Env,
	})
}

func (t *tickRunner) scriptOutput(ctx context.Context, script string, args ...string) (string, error) {
	return shell.CommandOutput(ctx, t.cfg.ExecCommand, shell.CommandRequest{
		Name:   script,
		Args:   args,
		Env:    t.cfg.Env,
		Stderr: t.cfg.Stderr,
	})
}

func (t *tickRunner) runScript(ctx context.Context, script string, args ...string) error {
	return t.cfg.ExecCommand(ctx, shell.CommandRequest{
		Name:   script,
		Args:   args,
		Env:    t.cfg.Env,
		Stdout: t.cfg.Stdout,
		Stderr: t.cfg.Stderr,
	})
}

func extractIssueNumber(ghOutput string) string {
	// Extract number from URL like https://github.com/owner/repo/issues/42
	parts := strings.Split(strings.TrimSpace(ghOutput), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}

// --- Output helpers ---

func (t *tickRunner) step(msg string)        { fmt.Fprintf(t.cfg.Stderr, "\033[1;36m▸ %s\033[0m\n", msg) }
func (t *tickRunner) info(msg string)        { fmt.Fprintf(t.cfg.Stderr, "\033[2m  %s\033[0m\n", msg) }
func (t *tickRunner) detail(key, val string) { fmt.Fprintf(t.cfg.Stderr, "\033[2m  %s:\033[0m %s\n", key, val) }
func (t *tickRunner) success(msg string)     { fmt.Fprintf(t.cfg.Stderr, "\033[1;32m✔ %s\033[0m\n", msg) }
func (t *tickRunner) warn(msg string)        { fmt.Fprintf(t.cfg.Stderr, "\033[1;33m⚠ %s\033[0m\n", msg) }

func (t *tickRunner) fail(format string, args ...any) int {
	fmt.Fprintf(t.cfg.Stderr, "\033[1;31mrunoq: %s\033[0m\n", fmt.Sprintf(format, args...))
	return 1
}
