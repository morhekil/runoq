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
	"strconv"
	"strings"
	"time"

	"github.com/saruman/runoq/agents"
	"github.com/saruman/runoq/comments"
	"github.com/saruman/runoq/internal/dispatchsafety"
	"github.com/saruman/runoq/internal/issuequeue"
	"github.com/saruman/runoq/internal/shell"
	"github.com/saruman/runoq/planning"
)

// TickConfig configures a single tick execution.
type TickConfig struct {
	Repo              string
	PlanFile          string
	RunoqRoot         string
	PlanApprovedLabel string
	ReadyLabel        string
	InProgressLabel    string
	DoneLabel          string
	NeedsReviewLabel   string
	BlockedLabel       string
	BranchPrefix       string
	WorktreePrefix     string
	LastCompletedIssue int
	DryRunImplementation bool // when true, implementation dispatch is dry-run only (no worktree/codex)
	Env                []string
	ExecCommand       shell.CommandExecutor
	Stdout            io.Writer
	Stderr            io.Writer
}

// issue represents a GitHub issue from the issue list.
type issue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	Body      string   `json:"body"`
	URL       string   `json:"url"`
	Labels    []label  `json:"labels"`
	BlockedBy   []int  `json:"-"` // populated from GitHub's blockedBy API
	IssueType   string `json:"-"` // populated from GitHub's issueType API
	ParentEpic  int    `json:"-"` // populated from sub-issues API
}

type label struct {
	Name string `json:"name"`
}

// RunTick executes one step of the planning lifecycle.
// Returns 0 (work done), 1 (error), 2 (waiting), or 3 (complete).
func RunTick(ctx context.Context, cfg TickConfig) int {
	t := &tickRunner{cfg: cfg}
	return t.run(ctx)
}

type tickRunner struct {
	cfg        TickConfig
	issues     []issue
	lastStepAt time.Time
}

func (t *tickRunner) run(ctx context.Context) int {
	t.step("Starting tick")
	t.detail("repo", t.cfg.Repo)
	t.detail("plan", t.cfg.PlanFile)

	// One-time setup: auth, identity, reconcile
	t.step("Setting up")
	setupApp := New(nil, t.cfg.Env, "", io.Discard, t.cfg.Stderr)
	setupApp.SetCommandExecutor(t.cfg.ExecCommand)
	t.cfg.Env = setupApp.Setup(ctx, t.cfg.Repo)

	dsApp := dispatchsafety.New(nil, t.cfg.Env, "", io.Discard, io.Discard)
	dsApp.SetCommandExecutor(t.cfg.ExecCommand)
	if t.cfg.ReadyLabel != "" {
		dsApp.SetConfig(dispatchsafety.DispatchConfig{
			ReadyLabel:      t.cfg.ReadyLabel,
			InProgressLabel: t.cfg.InProgressLabel,
			DoneLabel:       t.cfg.DoneLabel,
			NeedsReview:     t.cfg.NeedsReviewLabel,
			Blocked:         t.cfg.BlockedLabel,
			BranchPrefix:    t.cfg.BranchPrefix,
			WorktreePrefix:  t.cfg.WorktreePrefix,
		})
	}
	dsApp.Reconcile(ctx, t.cfg.Repo)

	t.step("Fetching issues")
	raw, err := t.ghOutput(ctx, "issue", "list", "--repo", t.cfg.Repo, "--state", "all", "--limit", "200", "--json", "number,title,body,labels,state,url")
	if err != nil {
		return t.fail("fetch issues: %v", err)
	}
	if err := json.Unmarshal([]byte(raw), &t.issues); err != nil {
		return t.fail("parse issues: %v", err)
	}
	t.info(fmt.Sprintf("found %d issues", len(t.issues)))

	// Populate dependency info from GitHub's native APIs
	if err := t.fetchDependencies(ctx); err != nil {
		return t.fail("%v", err)
	}

	t.step("Finding current epic")
	epic := t.firstOpenEpic()
	if epic == nil {
		if !t.anyEpicExists() {
			t.info("no epics exist — bootstrapping project")
			return t.handleBootstrap(ctx)
		}
		t.success("All milestones complete")
		fmt.Fprintln(t.cfg.Stdout, "All milestones complete")
		return 3
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
		reviewType := issueTypeOf(*approved)
		reviewParent := strconv.Itoa(issueParentEpic(approved))
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

	// Check for active conversations on in-progress coding PRs
	t.step("Checking for active conversations")
	if result := t.handleActiveConversations(ctx); result >= 0 {
		return result
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
		return t.handleImplementation(ctx, epicNumber)
	}

	if openChildren == 0 {
		t.step(fmt.Sprintf("All tasks complete — reviewing milestone #%d", epicNumber))
		return t.handleMilestoneComplete(ctx, epicNumber, epicTitle, epicType)
	}

	t.warn(fmt.Sprintf("%d tasks in progress, none ready", openChildren))
	fmt.Fprintf(t.cfg.Stdout, "%d tasks in progress, none ready\n", openChildren)
	return 2
}

// handleActiveConversations scans in-progress tasks for linked PRs with unprocessed comments.
// Returns a valid exit code (0, 1, 2) if a conversation was found and handled, or -1 if none found.
func (t *tickRunner) handleActiveConversations(ctx context.Context) int {
	if t.cfg.InProgressLabel == "" {
		t.info("no in-progress label configured, skipping conversation check")
		return -1
	}

	for i := range t.issues {
		iss := &t.issues[i]
		if iss.State != "OPEN" {
			continue
		}
		if issueTypeOf(*iss) != "task" {
			continue
		}
		if !t.issueHasLabel(iss, t.cfg.InProgressLabel) {
			continue
		}

		// Find linked PR for this in-progress task
		prListOut, err := t.ghOutput(ctx, "pr", "list", "--repo", t.cfg.Repo, "--search", fmt.Sprintf("closes #%d", iss.Number), "--json", "number")
		if err != nil {
			continue
		}
		var prs []struct {
			Number int `json:"number"`
		}
		if err := json.Unmarshal([]byte(prListOut), &prs); err != nil || len(prs) == 0 {
			continue
		}
		prNumber := prs[0].Number

		// Check for unprocessed comments via the orchestrator App
		orchApp := New(nil, t.cfg.Env, "", io.Discard, t.cfg.Stderr)
		orchApp.SetCommandExecutor(t.cfg.ExecCommand)
		orchApp.SetConfig(OrchestratorConfig{IdentityHandle: t.identityHandle()})

		comments, err := orchApp.findUnprocessedComments(ctx, t.cfg.Repo, "pr", prNumber)
		if err != nil || len(comments) == 0 {
			continue
		}

		t.info(fmt.Sprintf("found %d unprocessed comment(s) on PR #%d for task #%d", len(comments), prNumber, iss.Number))
		t.step(fmt.Sprintf("Responding to comments on PR #%d", prNumber))

		// Build state from the PR's audit comments and run phaseRespond
		respondApp := New(nil, t.cfg.Env, "", t.cfg.Stdout, t.cfg.Stderr)
		respondApp.SetCommandExecutor(t.cfg.ExecCommand)
		respondApp.SetConfig(OrchestratorConfig{IdentityHandle: t.identityHandle()})

		stateJSON, _ := marshalJSON(map[string]any{
			"issue":     iss.Number,
			"pr_number": prNumber,
			"phase":     "REVIEW",
		})

		root := t.cfg.RunoqRoot
		_, err = respondApp.phaseRespond(ctx, root, t.cfg.Env, t.cfg.Repo, iss.Number, stateJSON)
		if err != nil {
			t.warn(fmt.Sprintf("RESPOND failed for PR #%d: %v", prNumber, err))
			return 1
		}

		t.success(fmt.Sprintf("Responded to comments on PR #%d for task #%d", prNumber, iss.Number))
		fmt.Fprintf(t.cfg.Stdout, "Responded to comments on PR #%d\n", prNumber)
		return 0
	}

	t.info("no active conversations")
	return -1
}

func (t *tickRunner) identityHandle() string {
	// Try to extract from env or config
	for _, e := range t.cfg.Env {
		if strings.HasPrefix(e, "RUNOQ_IDENTITY=") {
			return strings.TrimPrefix(e, "RUNOQ_IDENTITY=")
		}
	}
	return "runoq"
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
		ClaudeBin:  envOrDefault(t.cfg.Env, "RUNOQ_CLAUDE_BIN", "claude"),
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

	issueType := issueTypeOf(*pending)
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
			ClaudeBin:         envOrDefault(t.cfg.Env, "RUNOQ_CLAUDE_BIN", "claude"),
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
		keyToNumber := make(map[string]string)
		keyToNodeID := make(map[string]string)
		for _, item := range filtered.Items {
			body := item.Body
			complexity := cmp.Or(item.EstimatedComplexity, "medium")
			createOpts := []string{"--type", "task", "--priority", "1", "--estimated-complexity", complexity, "--complexity-rationale", item.ComplexityRationale, "--parent-epic", reviewParent}

			issueNum, err := t.issueCreate(ctx, t.cfg.Repo, item.Title, body, createOpts...)
			if err != nil {
				t.info(fmt.Sprintf("failed to create task: %s: %v", item.Title, err))
				continue
			}
			if item.Key != "" {
				keyToNumber[item.Key] = issueNum
				// Get the node ID for GraphQL dependency linking
				nodeID := t.issueNodeID(ctx, issueNum)
				if nodeID != "" {
					keyToNodeID[item.Key] = nodeID
				}
			}

			// Set native GitHub dependencies via addBlockedBy
			for _, depKey := range item.DependsOnKeys {
				depNodeID, ok := keyToNodeID[depKey]
				if !ok {
					continue
				}
				taskNodeID := t.issueNodeID(ctx, issueNum)
				if taskNodeID == "" {
					continue
				}
				t.addBlockedBy(ctx, taskNodeID, depNodeID)
			}

			t.info(fmt.Sprintf("created task #%s: %s (%s)", issueNum, item.Title, complexity))
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
	// Try extracting from marked block first
	if extracted, err := planning.ExtractMarkedJSONBlock(view.Body, "runoq:payload:milestone-reviewer"); err == nil {
		adjustmentJSON = extracted
	} else {
		// Fall back to extracting JSON from code fence (used by FormatAdjustmentReviewBody)
		adjustmentJSON = extractJSONFromCodeFence(view.Body)
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
		ClaudeBin:     envOrDefault(t.cfg.Env, "RUNOQ_CLAUDE_BIN", "claude"),
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

func (t *tickRunner) handleImplementation(ctx context.Context, epicNumber int) int {
	// Prefer resuming an in-progress task over selecting a new one
	if inProgress := t.findInProgressTask(epicNumber); inProgress != nil {
		t.detail("resuming", fmt.Sprintf("#%d %s", inProgress.Number, inProgress.Title))
		return t.dispatchTask(ctx, inProgress)
	}

	graph := BuildDepGraph(t.issues, epicNumber, t.cfg.ReadyLabel)

	task := graph.NextAfter(t.cfg.LastCompletedIssue)
	if task == nil {
		if graph.HasCycle() {
			members := graph.CycleMembers()
			t.warn(fmt.Sprintf("dependency cycle detected: %v", members))
		}
		// Report blocked reasons for each task
		for num := range graph.nodes {
			if reason := graph.BlockedReason(num); reason != "" {
				t.info(fmt.Sprintf("#%d: %s", num, reason))
			}
		}
		t.warn("all tasks blocked")
		fmt.Fprintln(t.cfg.Stdout, "All tasks blocked")
		return 2
	}

	t.detail("selected", fmt.Sprintf("#%d %s", task.Number, task.Title))
	result := t.dispatchTask(ctx, task)

	// Update DAG visualization on the epic
	t.postDAGComment(ctx, epicNumber, graph)

	return result
}

func (t *tickRunner) dispatchTask(ctx context.Context, task *issue) int {
	metadata := IssueMetadata{
		Number:              task.Number,
		Title:               task.Title,
		Body:                task.Body,
		URL:                 task.URL,
		EstimatedComplexity: planning.MetadataValue(task.Body, "estimated_complexity"),
		Type:                issueTypeOf(*task),
	}
	if rationale := planning.MetadataValue(task.Body, "complexity_rationale"); rationale != "" {
		metadata.ComplexityRationale = &rationale
	}

	runApp := New(nil, t.cfg.Env, "", t.cfg.Stdout, t.cfg.Stderr)
	runApp.SetCommandExecutor(t.cfg.ExecCommand)
	runApp.SetConfig(OrchestratorConfig{
		BranchPrefix:   t.cfg.BranchPrefix,
		WorktreePrefix: t.cfg.WorktreePrefix,
	})
	stateJSON, err := runApp.RunIssue(ctx, t.cfg.Repo, task.Number, t.cfg.DryRunImplementation, task.Title, metadata)
	if err != nil {
		return t.fail("issue #%d: %v", task.Number, err)
	}

	phase := "unknown"
	var state map[string]any
	if err := json.Unmarshal([]byte(stateJSON), &state); err == nil {
		if value, ok := state["phase"].(string); ok && strings.TrimSpace(value) != "" {
			phase = value
		}
	}

	// If the issue reached a terminal phase, ensure GitHub issue is closed
	if phase == "DONE" || phase == "FINALIZE" {
		t.issueSetStatus(ctx, t.cfg.Repo, fmt.Sprintf("%d", task.Number), "done")
	}

	t.success(fmt.Sprintf("Issue #%d — phase: %s", task.Number, phase))
	fmt.Fprintf(t.cfg.Stdout, "Issue #%d — phase: %s\n", task.Number, phase)
	return 0
}

func (t *tickRunner) handleMilestoneComplete(ctx context.Context, epicNumber int, epicTitle, _ string) int {
	t.step("Running milestone reviewer")
	t.detail("milestone", fmt.Sprintf("#%d %s", epicNumber, epicTitle))

	claudeBin := envOrDefault(t.cfg.Env, "RUNOQ_CLAUDE_BIN", "claude")
	invoker := agents.NewInvoker(agents.InvokerConfig{
		LogRoot: filepath.Join(t.cfg.RunoqRoot, "log"),
	})

	payload, _ := json.Marshal(map[string]any{
		"milestoneNumber": epicNumber,
		"milestoneTitle":  epicTitle,
		"repo":            t.cfg.Repo,
	})
	resp, err := invoker.Invoke(ctx, agents.InvokeOptions{
		Backend: agents.Claude,
		Agent:   "milestone-reviewer",
		Bin:     claudeBin,
		RawArgs: []string{"--agent", "milestone-reviewer", "--add-dir", t.cfg.RunoqRoot, "--", string(payload)},
		WorkDir: t.cfg.RunoqRoot,
		Payload: string(payload),
	})
	if err != nil {
		return t.fail("milestone reviewer failed: %v", err)
	}

	// Parse adjustment output
	var adjInput planning.AdjustmentReviewInput
	adjText := resp.Text
	if extracted, extractErr := planning.ExtractMarkedJSONBlock(adjText, "runoq:payload:milestone-reviewer"); extractErr == nil {
		adjText = extracted
	}
	if err := json.Unmarshal([]byte(adjText), &adjInput); err != nil {
		return t.fail("parse milestone review output: %v", err)
	}

	adjustmentBody := planning.FormatAdjustmentReviewBody(adjInput)

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
		if issueTypeOf(*iss) != "epic" {
			continue
		}
		p := planning.MetadataPriority(iss.Body)
		if p < bestPriority || (p == bestPriority && (best == nil || iss.Number < best.Number)) {
			bestPriority = p
			best = iss
		}
	}
	return best
}

func (t *tickRunner) anyEpicExists() bool {
	for _, iss := range t.issues {
		if issueTypeOf(iss) == "epic" {
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
		issType := issueTypeOf(*iss)
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
	for i := range t.issues {
		iss := &t.issues[i]
		if iss.State != "OPEN" {
			continue
		}
		if issueParentEpic(iss) != epicNumber {
			continue
		}
		if issueTypeOf(*iss) == "planning" {
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

// findInProgressTask finds an open task child of the given epic that has the in-progress label.
// Returns nil if no in-progress task is found.
func (t *tickRunner) findInProgressTask(epicNumber int) *issue {
	if t.cfg.InProgressLabel == "" {
		return nil
	}
	for i := range t.issues {
		iss := &t.issues[i]
		if iss.State != "OPEN" {
			continue
		}
		if issueParentEpic(iss) != epicNumber {
			continue
		}
		if issueTypeOf(*iss) != "task" {
			continue
		}
		if t.issueHasLabel(iss, t.cfg.InProgressLabel) {
			return iss
		}
	}
	return nil
}

// selectNextTask finds the highest-priority unblocked task child of the given epic.
// It uses the already-fetched issue list — no API calls needed.
func (t *tickRunner) countOpenChildren(epicNumber int) (int, bool) {
	count := 0
	hasTask := false
	for i := range t.issues {
		iss := &t.issues[i]
		if iss.State != "OPEN" {
			continue
		}
		if issueParentEpic(iss) != epicNumber {
			continue
		}
		count++
		if issueTypeOf(*iss) == "task" {
			hasTask = true
		}
	}
	return count, hasTask
}

func (t *tickRunner) issueNodeID(ctx context.Context, issueNum string) string {
	raw, err := t.ghOutput(ctx, "api", fmt.Sprintf("repos/%s/issues/%s", t.cfg.Repo, issueNum), "--jq", ".node_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(raw)
}

// extractJSONFromCodeFence pulls JSON out of a markdown ```json code fence.
func extractJSONFromCodeFence(body string) string {
	start := strings.Index(body, "```json\n")
	if start < 0 {
		return body
	}
	content := body[start+8:]
	end := strings.Index(content, "\n```")
	if end < 0 {
		return body
	}
	return strings.TrimSpace(content[:end])
}

func (t *tickRunner) addBlockedBy(ctx context.Context, issueNodeID, blockingNodeID string) {
	query := fmt.Sprintf(`mutation { addBlockedBy(input: { issueId: %q, blockingIssueId: %q }) { blockedIssue { number } } }`, issueNodeID, blockingNodeID)
	_, _ = t.ghOutput(ctx, "api", "graphql", "-f", "query="+query)
}

func (t *tickRunner) fetchParentEpics(ctx context.Context) {
	// For each epic, query its sub-issues and build child→parent map
	for i := range t.issues {
		iss := &t.issues[i]
		if issueTypeOf(*iss) != "epic" {
			continue
		}
		raw, err := t.ghOutput(ctx, "api", fmt.Sprintf("repos/%s/issues/%d/sub_issues", t.cfg.Repo, iss.Number), "--paginate", "--jq", ".[].number")
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
			if num, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				for j := range t.issues {
					if t.issues[j].Number == num {
						t.issues[j].ParentEpic = iss.Number
					}
				}
			}
		}
	}
}

func (t *tickRunner) fetchDependencies(ctx context.Context) error {
	owner, repo, ok := strings.Cut(t.cfg.Repo, "/")
	if !ok {
		return fmt.Errorf("dependency fetch failed: invalid repo format %q", t.cfg.Repo)
	}
	query := fmt.Sprintf(`query { repository(owner: %q, name: %q) { issues(first: 200, states: [OPEN, CLOSED]) { nodes { number blockedBy(first: 20) { nodes { number } } issueType { name } } } } }`, owner, repo)

	var raw string
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		var err error
		raw, err = t.ghOutput(ctx, "api", "graphql", "-f", "query="+query)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if attempt < 3 {
			time.Sleep(3 * time.Second)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("dependency fetch failed after 3 attempts: %w", lastErr)
	}

	fetchBlockedBy(t.issues, raw)
	fetchIssueTypes(t.issues, raw)

	// Populate parent-epic from sub-issues relationships
	t.fetchParentEpics(ctx)
	return nil
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

const dagMarker = "<!-- runoq:dag -->"

func (t *tickRunner) postDAGComment(ctx context.Context, epicNumber int, graph *DepGraph) {
	mermaid := graph.RenderMermaid(t.cfg.InProgressLabel, t.cfg.DoneLabel)
	body := dagMarker + "\n\n" + mermaid + "\n"

	epicStr := fmt.Sprintf("%d", epicNumber)

	// Check if a DAG comment already exists
	commentsJSON, err := t.ghOutput(ctx, "issue", "view", epicStr, "--repo", t.cfg.Repo, "--json", "comments", "--jq", ".comments")
	if err == nil {
		var comments []struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		}
		if json.Unmarshal([]byte(commentsJSON), &comments) == nil {
			for _, c := range comments {
				if strings.Contains(c.Body, dagMarker) {
					// Update existing comment
					t.ghOutput(ctx, "api", "--method", "PATCH",
						fmt.Sprintf("repos/%s/issues/comments/%s", t.cfg.Repo, c.ID),
						"-f", "body="+body)
					return
				}
			}
		}
	}

	// Post new comment
	tmpFile, _ := os.CreateTemp("", "runoq-dag-*.md")
	tmpFile.WriteString(body)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())
	t.ghOutput(ctx, "issue", "comment", epicStr, "--repo", t.cfg.Repo, "--body-file", tmpFile.Name())
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

func (t *tickRunner) newIssueQueueApp() (*issuequeue.App, *bytes.Buffer) {
	var stdout bytes.Buffer
	app := issuequeue.New(nil, t.cfg.Env, "", &stdout, t.cfg.Stderr)
	app.SetCommandExecutor(t.cfg.ExecCommand)
	if t.cfg.ReadyLabel != "" {
		app.SetLabels(t.cfg.ReadyLabel, t.cfg.InProgressLabel, t.cfg.DoneLabel, t.cfg.NeedsReviewLabel, t.cfg.BlockedLabel)
	}
	return app, &stdout
}

func (t *tickRunner) issueCreate(ctx context.Context, repo, title, body string, opts ...string) (string, error) {
	app, stdout := t.newIssueQueueApp()
	code := app.Create(ctx, repo, title, body, opts)
	if code != 0 {
		return "", fmt.Errorf("issue-queue create exited %d", code)
	}
	return extractIssueNumber(stdout.String()), nil
}

func (t *tickRunner) issueSetStatus(ctx context.Context, repo, issueNumber, status string) error {
	app, _ := t.newIssueQueueApp()
	code := app.SetStatus(ctx, repo, issueNumber, status)
	if code != 0 {
		return fmt.Errorf("issue-queue set-status exited %d", code)
	}
	return nil
}

func (t *tickRunner) issueAssign(ctx context.Context, repo, issueNumber string) {
	app, _ := t.newIssueQueueApp()
	app.Assign(ctx, repo, issueNumber)
}

// --- GH helpers ---

func (t *tickRunner) ghOutput(ctx context.Context, args ...string) (string, error) {
	return shell.CommandOutput(ctx, t.cfg.ExecCommand, shell.CommandRequest{
		Name: "gh",
		Args: args,
		Env:  t.cfg.Env,
	})
}

// resolveDependsOn maps dependency keys to issue numbers, returning a
// comma-separated string suitable for --depends-on. Returns "" if no
// dependencies resolve.
func resolveDependsOn(keys []string, keyToNumber map[string]string) string {
	var nums []string
	for _, key := range keys {
		if num, ok := keyToNumber[key]; ok {
			nums = append(nums, num)
		}
	}
	return strings.Join(nums, ",")
}

func extractIssueNumber(ghOutput string) string {
	trimmed := strings.TrimSpace(ghOutput)
	// Try JSON format first: {"title":"...","url":"https://.../.../issues/42"}
	if strings.HasPrefix(trimmed, "{") {
		var result struct {
			URL string `json:"url"`
		}
		if json.Unmarshal([]byte(trimmed), &result) == nil && result.URL != "" {
			trimmed = result.URL
		}
	}
	// Extract number from URL like https://github.com/owner/repo/issues/42
	parts := strings.Split(trimmed, "/")
	if len(parts) > 0 {
		return strings.TrimSpace(parts[len(parts)-1])
	}
	return ""
}

// --- Output helpers ---

func (t *tickRunner) elapsed() string {
	if t.lastStepAt.IsZero() {
		return ""
	}
	return fmt.Sprintf(" (%.1fs)", time.Since(t.lastStepAt).Seconds())
}

func (t *tickRunner) step(msg string) {
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(t.cfg.Stderr, "\033[1;36m▸ [%s] %s\033[0m\n", ts, msg)
	t.lastStepAt = time.Now()
}

func (t *tickRunner) info(msg string)        { fmt.Fprintf(t.cfg.Stderr, "\033[2m  %s\033[0m\n", msg) }
func (t *tickRunner) detail(key, val string) { fmt.Fprintf(t.cfg.Stderr, "\033[2m  %s:\033[0m %s\n", key, val) }

func (t *tickRunner) success(msg string) {
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(t.cfg.Stderr, "\033[1;32m✔ [%s] %s%s\033[0m\n", ts, msg, t.elapsed())
}

func (t *tickRunner) warn(msg string) {
	fmt.Fprintf(t.cfg.Stderr, "\033[1;33m⚠ %s\033[0m\n", msg)
}

func (t *tickRunner) fail(format string, args ...any) int {
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(t.cfg.Stderr, "\033[1;31m✘ [%s] runoq: %s%s\033[0m\n", ts, fmt.Sprintf(format, args...), t.elapsed())
	return 1
}
