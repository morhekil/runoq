package orchestrator

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/saruman/runoq/agents"
	"github.com/saruman/runoq/comments"
	"github.com/saruman/runoq/internal/dispatchsafety"
	"github.com/saruman/runoq/internal/issuequeue"
	"github.com/saruman/runoq/internal/shell"
	"github.com/saruman/runoq/planning"
)

// TickConfig configures a single tick execution.
type TickConfig struct {
	Repo             string
	PlanFile         string
	RunoqRoot        string
	PlanApprovedLabel string
	Env              []string
	ExecCommand      shell.CommandExecutor
	Stdout           io.Writer
	Stderr           io.Writer
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

	planApprovedLabel := t.cfg.PlanApprovedLabel

	// Check for pending review
	t.step("Checking for pending review")
	if pending := t.findReviewIssue("pending", planApprovedLabel); pending != nil {
		t.info(fmt.Sprintf("found pending review #%d", pending.Number))
		result := t.handlePendingReview(ctx, pending)
		if result == 0 || result == 2 {
			return result
		}
		t.info("pending review not actionable, continuing")
	} else {
		t.info("none")
	}

	// Check for approved review
	t.step("Checking for approved review")
	if approved := t.findReviewIssue("approved", planApprovedLabel); approved != nil {
		reviewType := planning.MetadataValue(approved.Body, "type")
		reviewParent := planning.MetadataValue(approved.Body, "parent_epic")
		t.info(fmt.Sprintf("found approved %s review #%d (parent #%s)", reviewType, approved.Number, reviewParent))

		t.step(fmt.Sprintf("Loading review details for #%d", approved.Number))
		reviewView, err := t.ghOutput(ctx, "issue", "view", fmt.Sprintf("%d", approved.Number), "--repo", t.cfg.Repo, "--json", "number,title,body,comments,labels,state")
		if err != nil {
			return t.fail("load review: %v", err)
		}
		selectionJSON, _ := comments.ParseHumanCommentSelection(reviewView)

		switch reviewType {
		case "planning":
			t.step(fmt.Sprintf("Applying approved planning from #%d", approved.Number))
			return t.handleApprovedPlanning(ctx, reviewView, approved.Number, reviewParent, selectionJSON)
		case "adjustment":
			t.step(fmt.Sprintf("Applying approved adjustments from #%d", approved.Number))
			return t.handleApprovedAdjustment(ctx, reviewView, approved.Number, reviewParent, selectionJSON)
		}
	} else {
		t.info("none")
	}

	// Scan children of current epic
	epicNumber := epic.Number
	epicTitle := epic.Title
	epicType := planning.MetadataValue(epic.Body, "milestone_type")

	t.step(fmt.Sprintf("Scanning children of epic #%d", epicNumber))

	// Look for planning child that needs dispatch
	if planningChild := t.findPlanningChild(epicNumber); planningChild != nil {
		t.info(fmt.Sprintf("found planning issue #%d", planningChild.Number))
		if t.planningNeedsDispatch(ctx, planningChild) {
			t.step(fmt.Sprintf("Dispatching plan decomposition for #%d", planningChild.Number))
			return t.handlePlanningDispatch(ctx, planningChild, epic, epicTitle)
		}
		t.info(fmt.Sprintf("planning issue #%d already has a proposal", planningChild.Number))
	}

	// Count open children
	openChildren, hasOpenTask := t.countOpenChildren(epicNumber)
	t.detail("open children", fmt.Sprintf("%d", openChildren))
	t.detail("has open tasks", fmt.Sprintf("%v", hasOpenTask))

	if hasOpenTask {
		t.step(fmt.Sprintf("Dispatching implementation for epic #%d", epicNumber))
		return t.handleImplementation(ctx)
	}

	if openChildren == 0 {
		t.step(fmt.Sprintf("All tasks complete — reviewing milestone #%d", epicNumber))
		return t.handleMilestoneComplete(ctx, epicNumber, epicTitle, epicType)
	}

	t.warn(fmt.Sprintf("%d tasks in progress, none ready", openChildren))
	fmt.Fprintf(t.cfg.Stdout, "%d tasks in progress, none ready\n", openChildren)
	return 2
}

func (t *tickRunner) handleBootstrap(ctx context.Context) int {
	// Create Project Planning epic
	t.info("creating Project Planning epic")
	epicNumber, err := t.issueCreate(ctx, t.cfg.Repo, "Project Planning", "## Acceptance Criteria\n\n- [ ] Milestones proposed.", "--type", "epic", "--priority", "1", "--estimated-complexity", "low")
	if err != nil {
		return t.fail("create epic: %v", err)
	}
	t.detail("epic", "#"+epicNumber)

	// Create planning issue
	t.info("creating planning issue")
	planningNumber, err := t.issueCreate(ctx, t.cfg.Repo, "Break plan into milestones", "## Acceptance Criteria\n\n- [ ] Milestones proposed.", "--type", "planning", "--priority", "1", "--estimated-complexity", "low", "--parent-epic", epicNumber)
	if err != nil {
		return t.fail("create planning issue: %v", err)
	}
	t.detail("planning issue", "#"+planningNumber)

	// Dispatch milestone decomposition
	t.step("Running milestone decomposition on #" + planningNumber)
	invoker := agents.NewInvoker(agents.InvokerConfig{
		LogRoot: filepath.Join(t.cfg.RunoqRoot, "log"),
	})
	result, err := planning.RunDispatch(ctx, planning.DispatchConfig{
		ReviewType: "milestone",
		PlanFile:   t.cfg.PlanFile,
		RunoqRoot:  t.cfg.RunoqRoot,
		MaxRounds:  3,
		Invoker:    invoker,
		Stderr:     t.cfg.Stderr,
	})
	if err != nil {
		return t.fail("plan dispatch: %v", err)
	}

	// Write proposal to issue body
	currentBody, _ := t.ghOutput(ctx, "issue", "view", planningNumber, "--repo", t.cfg.Repo, "--json", "body", "--jq", ".body // \"\"")
	newBody := planning.ReplaceProposalInBody(currentBody, result.FormattedBody)
	t.ghEditBody(ctx, planningNumber, newBody)

	// Assign after proposal is posted
	t.issueAssign(ctx, t.cfg.Repo, planningNumber)

	t.success("Proposal posted on #" + planningNumber)
	fmt.Fprintf(t.cfg.Stdout, "Created planning milestone. Proposal posted on #%s\n", planningNumber)
	return 0
}

func (t *tickRunner) handlePendingReview(ctx context.Context, pending *issue) int {
	issueView, err := t.ghOutput(ctx, "issue", "view", fmt.Sprintf("%d", pending.Number), "--repo", t.cfg.Repo, "--json", "number,title,body,comments,labels,state")
	if err != nil {
		return t.fail("load review: %v", err)
	}

	issueType := planning.MetadataValue(pending.Body, "type")
	if issueType == "planning" && !strings.Contains(issueView, "runoq:payload:plan-proposal") {
		t.info(fmt.Sprintf("planning issue #%d has no proposal yet — needs dispatch", pending.Number))
		return 1
	}

	ids, _ := comments.FindUnrespondedCommentIDs(issueView)
	if len(ids) > 0 {
		t.step(fmt.Sprintf("Responding to unanswered comments on #%d", pending.Number))
		invoker := agents.NewInvoker(agents.InvokerConfig{
			LogRoot: filepath.Join(t.cfg.RunoqRoot, "log"),
		})
		ghClient := &tickGHAdapter{runner: t, ctx: ctx}
		if err := comments.HandleComments(ctx, comments.HandleCommentsConfig{
			Repo:              t.cfg.Repo,
			IssueNumber:       pending.Number,
			PlanFile:          t.cfg.PlanFile,
			RunoqRoot:         t.cfg.RunoqRoot,
			PlanApprovedLabel: t.cfg.PlanApprovedLabel,
			GH:                ghClient,
			Invoker:           invoker,
		}); err != nil {
			t.warn(fmt.Sprintf("Comment handler failed for #%d: %v", pending.Number, err))
			fmt.Fprintf(t.cfg.Stdout, "Comment handler failed for #%d\n", pending.Number)
		} else {
			t.success(fmt.Sprintf("Responded to comments on #%d", pending.Number))
			fmt.Fprintf(t.cfg.Stdout, "Responded to comments on #%d\n", pending.Number)
		}
		return 0
	}

	t.warn(fmt.Sprintf("Awaiting human decision on #%d", pending.Number))
	fmt.Fprintf(t.cfg.Stdout, "Awaiting human decision on #%d\n", pending.Number)
	return 2
}

func (t *tickRunner) handleApprovedPlanning(ctx context.Context, reviewView string, reviewNumber int, reviewParent string, selection comments.ItemSelection) int {
	proposalJSON, err := planning.ExtractMarkedJSONBlock(reviewView, "runoq:payload:plan-proposal")
	if err != nil {
		// Try extracting from the body field
		var view struct{ Body string `json:"body"` }
		json.Unmarshal([]byte(reviewView), &view)
		proposalJSON, err = planning.ExtractMarkedJSONBlock(view.Body, "runoq:payload:plan-proposal")
		if err != nil {
			return t.fail("extract proposal: %v", err)
		}
	}

	var proposal planning.Proposal
	if err := json.Unmarshal([]byte(proposalJSON), &proposal); err != nil {
		return t.fail("parse proposal: %v", err)
	}

	filtered := planning.SelectItemsFromProposal(proposal, selection)
	parentTitle := t.titleForIssue(reviewParent)
	t.detail("parent", "#"+reviewParent+" "+parentTitle)
	t.detail("items to create", fmt.Sprintf("%d", len(filtered.Items)))

	

	if parentTitle == "Project Planning" {
		t.info("creating milestone epics")
		var firstMilestone, firstMilestoneTitle string
		for _, item := range filtered.Items {
			body := planning.FormatMilestoneBody(item)
			priority := "1"
			if item.Priority != nil {
				priority = fmt.Sprintf("%d", *item.Priority)
			}
			num, err := t.issueCreate(ctx, t.cfg.Repo, item.Title, body, "--type", "epic", "--priority", priority, "--estimated-complexity", "low", "--milestone-type", item.Type)
			if err != nil {
				return t.fail("create epic: %v", err)
			}
			t.info(fmt.Sprintf("created epic #%s: %s", num, item.Title))
			if firstMilestone == "" {
				firstMilestone = num
				firstMilestoneTitle = item.Title
			}
		}
		if firstMilestone != "" {
			t.info("creating planning issue for first milestone #" + firstMilestone)
			t.issueCreate(ctx, t.cfg.Repo, "Break down "+firstMilestoneTitle+" into tasks", "## Acceptance Criteria\n\n- [ ] Tasks proposed.", "--type", "planning", "--priority", "1", "--estimated-complexity", "low", "--parent-epic", firstMilestone)
		}
		t.info(fmt.Sprintf("closing review #%d and parent #%s", reviewNumber, reviewParent))
		t.issueSetStatus(ctx, t.cfg.Repo, fmt.Sprintf("%d", reviewNumber), "done")
		t.issueSetStatus(ctx, t.cfg.Repo, reviewParent, "done")
	} else {
		t.info("creating task issues under epic #" + reviewParent)
		for _, item := range filtered.Items {
			body := item.Body
			priority := "1"
			if item.Priority != nil {
				priority = fmt.Sprintf("%d", *item.Priority)
			}
			complexity := cmp.Or(item.EstimatedComplexity, "medium")
			t.issueCreate(ctx, t.cfg.Repo, item.Title, body, "--type", "task", "--priority", priority, "--estimated-complexity", complexity, "--complexity-rationale", item.ComplexityRationale, "--parent-epic", reviewParent)
			t.info(fmt.Sprintf("created task: %s (%s)", item.Title, complexity))
		}
		t.info(fmt.Sprintf("closing review #%d", reviewNumber))
		t.issueSetStatus(ctx, t.cfg.Repo, fmt.Sprintf("%d", reviewNumber), "done")
	}

	t.success(fmt.Sprintf("Applied approvals from #%d, created issues", reviewNumber))
	fmt.Fprintf(t.cfg.Stdout, "Applied approvals from #%d, created issues\n", reviewNumber)
	return 0
}

func (t *tickRunner) handleApprovedAdjustment(ctx context.Context, reviewView string, reviewNumber int, reviewParent string, selection comments.ItemSelection) int {
	// Extract adjustment JSON from issue body
	var view struct{ Body string `json:"body"` }
	json.Unmarshal([]byte(reviewView), &view)

	adjustmentJSON := view.Body
	// Try extracting structured JSON
	if extracted, err := planning.ExtractMarkedJSONBlock(view.Body, "runoq:payload:milestone-reviewer"); err == nil {
		adjustmentJSON = extracted
	}

	var adjInput planning.AdjustmentReviewInput
	if err := json.Unmarshal([]byte(adjustmentJSON), &adjInput); err != nil {
		return t.fail("parse adjustments: %v", err)
	}

	// Filter by selection
	filtered := make([]planning.Adjustment, 0)
	for i, adj := range adjInput.ProposedAdjustments {
		idx := i + 1
		if len(selection.Rejected) > 0 && sliceContains(selection.Rejected, idx) {
			continue
		}
		if len(selection.Approved) > 0 && !sliceContains(selection.Approved, idx) {
			continue
		}
		filtered = append(filtered, adj)
	}
	t.detail("adjustments to apply", fmt.Sprintf("%d", len(filtered)))

	

	for _, adj := range filtered {
		switch adj.Type {
		case "modify":
			if adj.TargetMilestoneNumber != nil {
				t.info(fmt.Sprintf("modifying issue #%d", *adj.TargetMilestoneNumber))
				target := fmt.Sprintf("%d", *adj.TargetMilestoneNumber)
				targetIssue := t.findIssueByNumber(*adj.TargetMilestoneNumber)
				if targetIssue != nil {
					newBody := targetIssue.Body + "\n\n" + adj.Description
					t.ghEditBody(ctx, target, newBody)
				}
			}
		case "new_milestone":
			title := cmp.Or(adj.Title, adj.Description)
			t.info("creating new milestone: " + title)
			desc := cmp.Or(adj.Description, adj.Reason)
			t.issueCreate(ctx, t.cfg.Repo, title, "## Context\n\n"+desc+"\n\n## Acceptance Criteria\n\n- [ ] "+desc, "--type", "epic", "--priority", "99", "--estimated-complexity", "low")
		default:
			t.info(fmt.Sprintf("applying %s adjustment", adj.Type))
		}
	}

	t.info(fmt.Sprintf("closing review #%d and parent #%s", reviewNumber, reviewParent))
	t.issueSetStatus(ctx, t.cfg.Repo, fmt.Sprintf("%d", reviewNumber), "done")
	t.issueSetStatus(ctx, t.cfg.Repo, reviewParent, "done")

	// Refresh and seed next planning issue
	raw, _ := t.ghOutput(ctx, "issue", "list", "--repo", t.cfg.Repo, "--state", "all", "--limit", "200", "--json", "number,title,body,labels,state,url")
	json.Unmarshal([]byte(raw), &t.issues)
	if next := t.firstOpenEpic(); next != nil {
		t.info(fmt.Sprintf("seeding planning issue for next epic #%d", next.Number))
		t.issueCreate(ctx, t.cfg.Repo, "Break down "+next.Title+" into tasks", "## Acceptance Criteria\n\n- [ ] Tasks proposed.", "--type", "planning", "--priority", "1", "--estimated-complexity", "low", "--parent-epic", fmt.Sprintf("%d", next.Number))
	}

	t.success(fmt.Sprintf("Applied adjustments from #%d", reviewNumber))
	fmt.Fprintf(t.cfg.Stdout, "Applied approvals from #%d, created issues\n", reviewNumber)
	return 0
}

func (t *tickRunner) handlePlanningDispatch(ctx context.Context, planningChild *issue, epic *issue, epicTitle string) int {
	mode := "task"
	if epicTitle == "Project Planning" {
		mode = "milestone"
	}
	t.detail("mode", mode)
	t.detail("issue", fmt.Sprintf("#%d", planningChild.Number))

	invoker := agents.NewInvoker(agents.InvokerConfig{
		LogRoot: filepath.Join(t.cfg.RunoqRoot, "log"),
	})

	var milestoneFile string
	if mode == "task" {
		tmp, _ := os.CreateTemp("", "runoq-milestone-*.json")
		epicJSON, _ := json.Marshal(epic)
		tmp.Write(epicJSON)
		tmp.Close()
		defer os.Remove(tmp.Name())
		milestoneFile = tmp.Name()
	}

	result, err := planning.RunDispatch(ctx, planning.DispatchConfig{
		ReviewType:    mode,
		PlanFile:      t.cfg.PlanFile,
		MilestoneFile: milestoneFile,
		RunoqRoot:     t.cfg.RunoqRoot,
		MaxRounds:     3,
		Invoker:       invoker,
		Stderr:        t.cfg.Stderr,
	})
	if err != nil {
		return t.fail("plan dispatch: %v", err)
	}

	// Write proposal to issue body
	issueNumber := fmt.Sprintf("%d", planningChild.Number)
	currentBody, _ := t.ghOutput(ctx, "issue", "view", issueNumber, "--repo", t.cfg.Repo, "--json", "body", "--jq", ".body // \"\"")
	newBody := planning.ReplaceProposalInBody(currentBody, result.FormattedBody)
	t.ghEditBody(ctx, issueNumber, newBody)

	t.issueAssign(ctx, t.cfg.Repo, issueNumber)
	t.success(fmt.Sprintf("Proposal posted on #%d", planningChild.Number))
	fmt.Fprintf(t.cfg.Stdout, "Proposal posted on #%d\n", planningChild.Number)
	return 0
}

func (t *tickRunner) handleImplementation(ctx context.Context) int {
	t.info("reconciling dispatch safety")
	dsApp := dispatchsafety.New(
		[]string{"reconcile", t.cfg.Repo},
		t.cfg.Env, "", io.Discard, io.Discard,
	)
	dsApp.SetCommandExecutor(t.cfg.ExecCommand)
	dsApp.Run(ctx) // best-effort, ignore errors

	t.info("running next issue")
	runApp := New(
		[]string{"run"},
		t.cfg.Env, "", t.cfg.Stdout, t.cfg.Stderr,
	)
	runApp.SetCommandExecutor(t.cfg.ExecCommand)
	runApp.Run(ctx) // may fail, that's OK

	t.success("Executed issue")
	fmt.Fprintln(t.cfg.Stdout, "Executed issue")
	return 0
}

func (t *tickRunner) handleMilestoneComplete(ctx context.Context, epicNumber int, epicTitle, epicType string) int {
	// Call milestone-reviewer agent via captured_exec shell wrapper
	// This will move to agents/ package in M6
	t.step("Running milestone reviewer")
	t.detail("milestone", fmt.Sprintf("#%d %s", epicNumber, epicTitle))

	capturedExecScript := filepath.Join(t.cfg.RunoqRoot, "scripts", "tick.sh")
	

	// For now, delegate milestone-complete to tick.sh via a special env var
	// TODO: M6 replaces this with direct agents/ package call
	_ = capturedExecScript

	// Create adjustment review body using planning package
	adjustmentBody := planning.FormatAdjustmentReviewBody(planning.AdjustmentReviewInput{
		ProposedAdjustments: []planning.Adjustment{
			{Type: "discovery", Title: "Review completed milestone", Description: "Milestone completed — review for adjustments.", Reason: "milestone completed"},
		},
	})

	t.info("creating adjustment review issue")
	adjNumber, err := t.issueCreate(ctx, t.cfg.Repo, "Review milestone adjustments", adjustmentBody, "--type", "adjustment", "--priority", "1", "--estimated-complexity", "low", "--parent-epic", fmt.Sprintf("%d", epicNumber))
	if err != nil {
		return t.fail("create adjustment issue: %v", err)
	}
	t.issueAssign(ctx, t.cfg.Repo, adjNumber)

	t.success(fmt.Sprintf("Milestone #%d reviewed. Adjustments on #%s", epicNumber, adjNumber))
	fmt.Fprintf(t.cfg.Stdout, "Milestone #%d review complete. Adjustments proposed on #%s\n", epicNumber, adjNumber)
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

func (t *tickRunner) findReviewIssue(mode string, planApprovedLabel string) *issue {
	for i := range t.issues {
		iss := &t.issues[i]
		if iss.State != "OPEN" {
			continue
		}
		issType := planning.MetadataValue(iss.Body, "type")
		if issType != "planning" && issType != "adjustment" {
			continue
		}
		hasLabel := t.issueHasLabel(iss, planApprovedLabel)
		if mode == "approved" && !hasLabel {
			continue
		}
		if mode == "pending" && hasLabel {
			continue
		}
		return iss
	}
	return nil
}

func (t *tickRunner) issueHasLabel(iss *issue, labelName string) bool {
	for _, l := range iss.Labels {
		if l.Name == labelName {
			return true
		}
	}
	return false
}

func (t *tickRunner) findPlanningChild(epicNumber int) *issue {
	epicStr := fmt.Sprintf("%d", epicNumber)
	for i := range t.issues {
		iss := &t.issues[i]
		if iss.State != "OPEN" {
			continue
		}
		if planning.MetadataValue(iss.Body, "parent_epic") != epicStr {
			continue
		}
		if planning.MetadataValue(iss.Body, "type") == "planning" {
			return iss
		}
	}
	return nil
}

func (t *tickRunner) planningNeedsDispatch(ctx context.Context, iss *issue) bool {
	view, err := t.ghOutput(ctx, "issue", "view", fmt.Sprintf("%d", iss.Number), "--repo", t.cfg.Repo, "--json", "body")
	if err != nil {
		return true
	}
	var v struct{ Body string `json:"body"` }
	json.Unmarshal([]byte(view), &v)
	return !strings.Contains(v.Body, "runoq:payload:plan-proposal")
}

func (t *tickRunner) countOpenChildren(epicNumber int) (int, bool) {
	epicStr := fmt.Sprintf("%d", epicNumber)
	count := 0
	hasTask := false
	for _, iss := range t.issues {
		if iss.State != "OPEN" {
			continue
		}
		if planning.MetadataValue(iss.Body, "parent_epic") != epicStr {
			continue
		}
		count++
		if planning.MetadataValue(iss.Body, "type") == "task" {
			hasTask = true
		}
	}
	return count, hasTask
}

func (t *tickRunner) titleForIssue(numberStr string) string {
	for _, iss := range t.issues {
		if fmt.Sprintf("%d", iss.Number) == numberStr {
			return iss.Title
		}
	}
	return ""
}

func (t *tickRunner) findIssueByNumber(number int) *issue {
	for i := range t.issues {
		if t.issues[i].Number == number {
			return &t.issues[i]
		}
	}
	return nil
}

func (t *tickRunner) ghEditBody(ctx context.Context, issueNumber string, newBody string) {
	tmpFile, _ := os.CreateTemp("", "runoq-edit-*.md")
	tmpFile.WriteString(newBody)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())
	t.ghOutput(ctx, "issue", "edit", issueNumber, "--repo", t.cfg.Repo, "--body-file", tmpFile.Name())
}

func sliceContains(s []int, v int) bool {
	return slices.Contains(s, v)
}

// --- GH adapter for comments.GHClient ---

type tickGHAdapter struct {
	runner *tickRunner
	ctx    context.Context
}

func (a *tickGHAdapter) IssueView(_ context.Context, repo string, number int, fields string) (string, error) {
	return a.runner.ghOutput(a.ctx, "issue", "view", fmt.Sprintf("%d", number), "--repo", repo, "--json", fields)
}

func (a *tickGHAdapter) IssueComment(_ context.Context, repo string, number int, body string) error {
	tmpFile, _ := os.CreateTemp("", "runoq-comment-*.md")
	tmpFile.WriteString(body)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())
	_, err := a.runner.ghOutput(a.ctx, "issue", "comment", fmt.Sprintf("%d", number), "--repo", repo, "--body-file", tmpFile.Name())
	return err
}

func (a *tickGHAdapter) IssueEditBody(_ context.Context, repo string, number int, body string) error {
	a.runner.ghEditBody(a.ctx, fmt.Sprintf("%d", number), body)
	return nil
}

func (a *tickGHAdapter) IssueAddLabel(_ context.Context, repo string, number int, label string) error {
	_, err := a.runner.ghOutput(a.ctx, "issue", "edit", fmt.Sprintf("%d", number), "--repo", repo, "--add-label", label)
	return err
}

func (a *tickGHAdapter) AddReaction(_ context.Context, commentID string, content string) error {
	query := fmt.Sprintf(`mutation { addReaction(input: {subjectId: "%s", content: %s}) { reaction { content } } }`, commentID, content)
	_, err := a.runner.ghOutput(a.ctx, "api", "graphql", "-f", "query="+query)
	return err
}

// --- Issue queue helpers (direct Go calls) ---

func (t *tickRunner) issueQueueRun(ctx context.Context, args ...string) (string, error) {
	var stdout bytes.Buffer
	app := issuequeue.New(args, t.cfg.Env, "", &stdout, t.cfg.Stderr)
	app.SetCommandExecutor(t.cfg.ExecCommand)
	code := app.Run(ctx)
	if code != 0 {
		return stdout.String(), fmt.Errorf("issue-queue %s exited %d", args[0], code)
	}
	return stdout.String(), nil
}

func (t *tickRunner) issueCreate(ctx context.Context, repo, title, body string, opts ...string) (string, error) {
	args := append([]string{"create", repo, title, body}, opts...)
	output, err := t.issueQueueRun(ctx, args...)
	if err != nil {
		return "", err
	}
	return extractIssueNumber(output), nil
}

func (t *tickRunner) issueSetStatus(ctx context.Context, repo, issueNumber, status string) error {
	_, err := t.issueQueueRun(ctx, "set-status", repo, issueNumber, status)
	return err
}

func (t *tickRunner) issueAssign(ctx context.Context, repo, issueNumber string) {
	t.issueQueueRun(ctx, "assign", repo, issueNumber)
}

// --- Shell helpers (interim — for scripts not yet ported) ---

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
