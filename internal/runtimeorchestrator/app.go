package runtimeorchestrator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const usageText = `Usage:
  orchestrator.sh run <repo> [--issue N] [--dry-run]
  orchestrator.sh mention-triage <repo> <pr-number>
`

type commandRequest struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type commandExecutor func(context.Context, commandRequest) error

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	execCommand commandExecutor
}

type issueMetadata struct {
	Number              int
	Title               string
	Body                string
	URL                 string
	EstimatedComplexity string
	ComplexityRationale *string
	Type                string
}

type issueView struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

type queueConfig struct {
	Labels struct {
		Ready string `json:"ready"`
	} `json:"labels"`
	Identity struct {
		Handle string `json:"handle"`
	} `json:"identity"`
	AutoMerge struct {
		Enabled       bool   `json:"enabled"`
		MaxComplexity string `json:"maxComplexity"`
	} `json:"autoMerge"`
	Reviewers      []string `json:"reviewers"`
	MaxRounds      int      `json:"maxRounds"`
	MaxTokenBudget int      `json:"maxTokenBudget"`
}

type eligibilityResult struct {
	Allowed bool     `json:"allowed"`
	Issue   int      `json:"issue"`
	Branch  string   `json:"branch"`
	Reasons []string `json:"reasons"`
}

type worktreeCreateResult struct {
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
}

type prCreateResult struct {
	Number any `json:"number"`
}

type queueSelectionResult struct {
	Issue   *queueSelectionIssue  `json:"issue"`
	Skipped []queueSelectionIssue `json:"skipped"`
}

type queueSelectionIssue struct {
	Number         int      `json:"number"`
	Title          string   `json:"title"`
	BlockedReasons []string `json:"blocked_reasons"`
}

type queueListedIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Type   string `json:"type"`
}

type epicStatusResult struct {
	AllDone bool  `json:"all_done"`
	Pending []int `json:"pending"`
}

type verifyIntegrateResult struct {
	OK       bool     `json:"ok"`
	Failures []string `json:"failures"`
}

type issueRunnerResult struct {
	Status               string   `json:"status"`
	LogDir               string   `json:"logDir"`
	BaselineHash         string   `json:"baselineHash"`
	HeadHash             string   `json:"headHash"`
	CommitRange          string   `json:"commitRange"`
	ReviewLogPath        string   `json:"reviewLogPath"`
	SpecRequirements     string   `json:"specRequirements"`
	ChangedFiles         []string `json:"changedFiles"`
	RelatedFiles         []string `json:"relatedFiles"`
	CumulativeTokens     int      `json:"cumulativeTokens"`
	VerificationPassed   bool     `json:"verificationPassed"`
	VerificationFailures []string `json:"verificationFailures"`
	Caveats              []string `json:"caveats"`
	Summary              string   `json:"summary"`
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:        append([]string(nil), args...),
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      stdout,
		stderr:      stderr,
		execCommand: runCommand,
	}
}

func (a *App) SetCommandExecutor(execFn commandExecutor) {
	if execFn == nil {
		a.execCommand = runCommand
		return
	}
	a.execCommand = execFn
}

func (a *App) Run(ctx context.Context) int {
	if len(a.args) == 0 {
		a.printUsage(a.stderr)
		return 1
	}

	root := a.runoqRoot()
	if root == "" {
		return a.fail("Unable to resolve RUNOQ_ROOT for runtime orchestrator.")
	}

	env := append([]string(nil), a.env...)
	if authEnv := a.prepareAuth(ctx, root, env); authEnv != nil {
		env = authEnv
	}

	switch a.args[0] {
	case "run":
		return a.runCommandEntry(ctx, root, env, a.args[1:])
	case "mention-triage":
		return a.mentionTriageEntry(ctx, root, env, a.args[1:])
	case "-h", "--help", "help":
		a.printUsage(a.stdout)
		return 0
	default:
		a.printUsage(a.stderr)
		return 1
	}
}

func (a *App) mentionTriageEntry(ctx context.Context, root string, env []string, args []string) int {
	if len(args) != 2 {
		a.printUsage(a.stderr)
		return 1
	}

	repo := args[0]
	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return a.fail(err.Error())
	}

	var stdout bytes.Buffer
	if err := a.runScript(ctx, root, env, "gh-pr-lifecycle.sh", []string{"poll-mentions", repo, cfg.Identity.Handle}, nil, &stdout, a.stderr); err != nil {
		return commandExitCode(err)
	}

	var mentions []json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &mentions); err != nil {
		return a.failf("poll-mentions returned invalid JSON: %v", err)
	}
	if len(mentions) == 0 {
		return 0
	}

	return a.fail("mention-triage with mentions not implemented")
}

func (a *App) runCommandEntry(ctx context.Context, root string, env []string, args []string) int {
	if len(args) == 0 {
		a.printUsage(a.stderr)
		return 1
	}

	repo := args[0]
	rest := args[1:]

	issueNumber := ""
	dryRun := false
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--issue":
			if i+1 >= len(rest) {
				return a.fail("--issue requires a value")
			}
			issueNumber = rest[i+1]
			i++
		case "--dry-run":
			dryRun = true
		default:
			a.printUsage(a.stderr)
			return 1
		}
	}

	if issueNumber != "" {
		if _, err := strconv.Atoi(issueNumber); err != nil {
			return a.fail("--issue requires a numeric value")
		}
	}

	targetRoot, err := a.targetRoot(ctx, env)
	if err != nil {
		return a.fail(err.Error())
	}

	a.logInfo("Configuring bot identity for target root: %s", targetRoot)
	if err := a.configureGitBotIdentity(ctx, root, env, targetRoot); err == nil {
		a.logInfo("Bot identity configured successfully")
	} else {
		a.logInfo("Bot identity configuration failed or skipped")
	}

	if err := a.configureGitBotRemote(ctx, root, env, targetRoot, repo); err == nil {
		a.logInfo("Bot remote configured successfully for repo=%s", repo)
	} else {
		a.logInfo("Bot remote configuration failed or skipped")
	}

	a.logInfo("Running reconciliation")
	reconcileEnv := append([]string(nil), env...)
	reconcileEnv = envSet(reconcileEnv, "RUNOQ_NO_AUTO_TOKEN", "1")
	_ = a.runScript(ctx, root, reconcileEnv, "dispatch-safety.sh", []string{"reconcile", repo}, nil, io.Discard, io.Discard)

	if issueNumber == "" {
		if dryRun {
			return a.runQueueDryRun(ctx, root, env, repo)
		}
		return a.runQueue(ctx, root, env, repo)
	}

	issue, _ := strconv.Atoi(issueNumber)
	title, err := a.issueTitle(ctx, env, repo, issue)
	if err != nil {
		return a.failf("failed to load issue title: %v", err)
	}

	stateJSON, err := a.runSingleIssue(ctx, root, env, repo, issue, dryRun, title)
	if err != nil {
		a.logError("Issue #%d failed: %v", issue, err)
		return 1
	}

	if dryRun {
		_, _ = fmt.Fprintln(a.stdout, stateJSON)
		return 0
	}

	_, _ = fmt.Fprintln(a.stdout, stateJSON)
	return 0
}

func (a *App) runQueue(ctx context.Context, root string, env []string, repo string) int {
	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return a.fail(err.Error())
	}

	queueEnv := append([]string(nil), env...)
	queueEnv = envSet(queueEnv, "RUNOQ_LOG", "1")
	queueEnv = envSet(queueEnv, "RUNOQ_NO_AUTO_TOKEN", "1")

	for {
		queueOut, queueStderr, err := a.scriptOutputWithStderr(ctx, root, queueEnv, "gh-issue-queue.sh", []string{"next", repo, cfg.Labels.Ready}, nil)
		if strings.TrimSpace(queueStderr) != "" {
			_, _ = fmt.Fprintln(a.stderr, queueStderr)
		}
		if err != nil {
			return commandExitCode(err)
		}

		var selection queueSelectionResult
		if err := json.Unmarshal([]byte(queueOut), &selection); err != nil {
			return a.failf("gh-issue-queue.sh next returned invalid JSON: %v", err)
		}

		totalSkipped := len(selection.Skipped)
		skippedSummary := formatSkippedSummary(selection.Skipped)
		if selection.Issue == nil {
			a.logInfo("Queue result: 0 actionable issues, %d skipped", totalSkipped)
			if totalSkipped > 0 {
				a.logInfo("Skipped details: %s", skippedSummary)
			}
			break
		}

		a.logInfo("Queue result: 1 actionable issue found, %d skipped", totalSkipped)
		if totalSkipped > 0 {
			a.logInfo("Skipped details: %s", skippedSummary)
		}

		title := strings.TrimSpace(selection.Issue.Title)
		if title == "" {
			title = "untitled"
		}
		a.logInfo("Processing issue #%d: %s", selection.Issue.Number, title)

		stateJSON, err := a.runSingleIssue(ctx, root, queueEnv, repo, selection.Issue.Number, false, title)
		if err != nil {
			a.logError("Issue #%d failed: %v", selection.Issue.Number, err)
			return 1
		}

		phase := "unknown"
		var state map[string]any
		if err := json.Unmarshal([]byte(stateJSON), &state); err == nil {
			if value, ok := state["phase"].(string); ok && strings.TrimSpace(value) != "" {
				phase = value
			}
		}
		a.logInfo("Issue #%d succeeded — terminal phase: %s", selection.Issue.Number, phase)
	}

	if err := a.runEpicSweep(ctx, root, queueEnv, repo, cfg.Labels.Ready); err != nil {
		return a.fail(err.Error())
	}
	return 0
}

func (a *App) runQueueDryRun(ctx context.Context, root string, env []string, repo string) int {
	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return a.fail(err.Error())
	}

	queueEnv := append([]string(nil), env...)
	queueEnv = envSet(queueEnv, "RUNOQ_LOG", "1")
	queueEnv = envSet(queueEnv, "RUNOQ_NO_AUTO_TOKEN", "1")
	queueOut, queueStderr, err := a.scriptOutputWithStderr(ctx, root, queueEnv, "gh-issue-queue.sh", []string{"next", repo, cfg.Labels.Ready}, nil)
	if strings.TrimSpace(queueStderr) != "" {
		_, _ = fmt.Fprintln(a.stderr, queueStderr)
	}
	if err != nil {
		return commandExitCode(err)
	}

	var selection queueSelectionResult
	if err := json.Unmarshal([]byte(queueOut), &selection); err != nil {
		return a.failf("gh-issue-queue.sh next returned invalid JSON: %v", err)
	}

	totalSkipped := len(selection.Skipped)
	skippedSummary := formatSkippedSummary(selection.Skipped)
	if selection.Issue == nil {
		a.logInfo("Queue result: 0 actionable issues, %d skipped", totalSkipped)
		if totalSkipped > 0 {
			a.logInfo("Skipped details: %s", skippedSummary)
		}
		return 0
	}

	a.logInfo("Queue result: 1 actionable issue found, %d skipped", totalSkipped)
	if totalSkipped > 0 {
		a.logInfo("Skipped details: %s", skippedSummary)
	}

	title := strings.TrimSpace(selection.Issue.Title)
	if title == "" {
		title = "untitled"
	}
	a.logInfo("Processing issue #%d: %s", selection.Issue.Number, title)

	stateJSON, err := a.runSingleIssue(ctx, root, queueEnv, repo, selection.Issue.Number, true, title)
	if err != nil {
		a.logError("Issue #%d failed", selection.Issue.Number)
		return 1
	}

	a.logInfo("Issue #%d succeeded — terminal phase: ", selection.Issue.Number)
	_, _ = fmt.Fprintln(a.stdout, stateJSON)
	return 0
}

func (a *App) runSingleIssue(ctx context.Context, root string, env []string, repo string, issueNumber int, dryRun bool, title string) (string, error) {
	metadata, err := a.getIssueMetadata(ctx, root, env, repo, issueNumber)
	if err != nil {
		return "", err
	}

	stateJSON, err := a.phaseInit(ctx, root, env, repo, issueNumber, dryRun, title)
	if err != nil {
		return "", err
	}

	if dryRun {
		return stateJSON, nil
	}

	stateJSON, err = a.phaseCriteria(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	if err != nil {
		return "", err
	}
	if metadata.EstimatedComplexity != "low" {
		return a.phaseCriteriaNeedsReviewHandoff(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	}

	for {
		var developResult issueRunnerResult
		stateJSON, developResult, err = a.phaseDevelop(ctx, root, env, repo, issueNumber, stateJSON)
		if err != nil {
			return "", err
		}

		if developResult.Status != "review_ready" {
			return a.phaseDevelopNeedsReview(ctx, root, env, repo, issueNumber, stateJSON)
		}

		stateJSON, err = a.phaseReview(ctx, root, env, repo, issueNumber, stateJSON)
		if err != nil {
			return "", err
		}

		stateJSON, err = a.phaseDecide(ctx, root, env, issueNumber, stateJSON)
		if err != nil {
			return "", err
		}

		var decideState struct {
			Decision        string `json:"decision"`
			NextPhase       string `json:"next_phase"`
			ReviewChecklist string `json:"review_checklist"`
		}
		if err := json.Unmarshal([]byte(stateJSON), &decideState); err != nil {
			return "", fmt.Errorf("failed to parse decide state: %v", err)
		}

		if decideState.NextPhase == "DEVELOP" && decideState.Decision == "iterate" {
			stateJSON, err = updateStateJSON(stateJSON, func(state map[string]any) {
				state["previous_checklist"] = decideState.ReviewChecklist
			})
			if err != nil {
				return "", err
			}
			if err := a.saveState(ctx, root, env, issueNumber, stateJSON); err != nil {
				return "", err
			}
			continue
		}

		return a.phaseFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	}
}

func (a *App) runEpicSweep(ctx context.Context, root string, env []string, repo string, readyLabel string) error {
	issuesOut, err := a.scriptOutput(ctx, root, env, "gh-issue-queue.sh", []string{"list", repo, readyLabel}, nil)
	if err != nil {
		return err
	}

	var issues []queueListedIssue
	if strings.TrimSpace(issuesOut) != "" {
		if err := json.Unmarshal([]byte(issuesOut), &issues); err != nil {
			return fmt.Errorf("gh-issue-queue.sh list returned invalid JSON: %v", err)
		}
	}

	epics := make([]queueListedIssue, 0, len(issues))
	for _, issue := range issues {
		if strings.TrimSpace(issue.Type) == "epic" {
			epics = append(epics, issue)
		}
	}
	if len(epics) == 0 {
		return nil
	}

	a.logInfo("Epic sweep: found %d epic(s) to evaluate", len(epics))
	for _, epic := range epics {
		epicStatusOut, err := a.scriptOutput(ctx, root, env, "gh-issue-queue.sh", []string{"epic-status", repo, strconv.Itoa(epic.Number)}, nil)
		if err != nil {
			a.logError("Epic sweep: failed epic-status for epic #%d", epic.Number)
			continue
		}

		var epicStatus epicStatusResult
		if err := json.Unmarshal([]byte(epicStatusOut), &epicStatus); err != nil {
			a.logError("Epic sweep: invalid epic-status payload for epic #%d", epic.Number)
			continue
		}

		epicTitle := strings.TrimSpace(epic.Title)
		if epicTitle == "" {
			epicTitle = "untitled"
		}
		if !epicStatus.AllDone {
			pending := make([]string, 0, len(epicStatus.Pending))
			for _, number := range epicStatus.Pending {
				pending = append(pending, "#"+strconv.Itoa(number))
			}
			a.logInfo("Epic sweep: epic #%d (%s) — children pending: %s", epic.Number, epicTitle, strings.Join(pending, ", "))
			continue
		}

		a.logInfo("Epic sweep: all children done for epic #%d (%s) — running integration", epic.Number, epicTitle)

		epicState, err := a.scriptOutput(ctx, root, env, "state.sh", []string{"load", strconv.Itoa(epic.Number)}, nil)
		if err != nil || strings.TrimSpace(epicState) == "" {
			epicState, err = marshalJSON(map[string]any{
				"issue_number": epic.Number,
				"phase":        "DECIDE",
				"next_phase":   "INTEGRATE",
			})
			if err != nil {
				return err
			}
		}

		epicState, err = a.phaseIntegrate(ctx, root, env, repo, epic.Number, epicState, epicTitle)
		if err != nil {
			a.logError("Epic sweep: epic #%d integration failed", epic.Number)
			continue
		}

		phase := "unknown"
		var state map[string]any
		if err := json.Unmarshal([]byte(epicState), &state); err == nil {
			if value, ok := state["phase"].(string); ok && strings.TrimSpace(value) != "" {
				phase = value
			}
		}
		a.logInfo("Epic sweep: epic #%d integration complete — phase: %s", epic.Number, phase)
		_, _ = fmt.Fprintln(a.stdout, epicState)
	}
	return nil
}

func (a *App) phaseIntegrate(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, title string) (string, error) {
	a.logInfo("INTEGRATE: checking epic #%d", issueNumber)

	epicStatusOut, err := a.scriptOutput(ctx, root, env, "gh-issue-queue.sh", []string{"epic-status", repo, strconv.Itoa(issueNumber)}, nil)
	if err != nil {
		return "", err
	}

	var epicStatus epicStatusResult
	if err := json.Unmarshal([]byte(epicStatusOut), &epicStatus); err != nil {
		return "", fmt.Errorf("failed to parse epic-status payload: %v", err)
	}
	if !epicStatus.AllDone {
		a.logInfo("INTEGRATE: not all children done for epic #%d", issueNumber)
		nextState, err := updateStateJSON(stateJSON, func(state map[string]any) {
			state["phase"] = "DECIDE"
			state["decision"] = "integrate-pending"
			state["next_phase"] = "INTEGRATE"
		})
		if err != nil {
			return "", err
		}
		if err := a.saveState(ctx, root, env, issueNumber, nextState); err != nil {
			return "", err
		}
		return nextState, nil
	}

	var state struct {
		Worktree       string `json:"worktree"`
		CriteriaCommit string `json:"criteria_commit"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for integrate: %v", err)
	}

	resolvedTitle, titleErr := a.issueTitle(ctx, env, repo, issueNumber)
	if titleErr == nil && strings.TrimSpace(resolvedTitle) != "" {
		title = resolvedTitle
	}

	integrateTitle := strings.TrimSpace(title)
	if integrateTitle == "" {
		integrateTitle = "untitled"
	}
	worktreeOut, _, worktreeErr := a.scriptOutputWithStderr(ctx, root, env, "worktree.sh", []string{"create", strconv.Itoa(issueNumber), integrateTitle + "-integrate"}, nil)
	integrateWorktree := ""
	if worktreeErr == nil {
		var worktreeInfo worktreeCreateResult
		if err := json.Unmarshal([]byte(worktreeOut), &worktreeInfo); err == nil {
			integrateWorktree = strings.TrimSpace(worktreeInfo.Worktree)
		}
	}
	if integrateWorktree == "" {
		integrateWorktree = strings.TrimSpace(state.Worktree)
	}

	if strings.TrimSpace(state.CriteriaCommit) != "" {
		verifyOut, verifyErr := a.scriptOutput(ctx, root, env, "verify.sh", []string{"integrate", integrateWorktree, strings.TrimSpace(state.CriteriaCommit)}, nil)
		verifyResult := verifyIntegrateResult{OK: false}
		if verifyErr == nil && strings.TrimSpace(verifyOut) != "" {
			_ = json.Unmarshal([]byte(verifyOut), &verifyResult)
		}

		if verifyResult.OK {
			_ = a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "done"}, nil, io.Discard, io.Discard)
			integrateState, err := updateStateJSON(stateJSON, func(state map[string]any) {
				state["phase"] = "INTEGRATE"
			})
			if err != nil {
				return "", err
			}
			if err := a.saveState(ctx, root, env, issueNumber, integrateState); err != nil {
				return "", err
			}
			doneState, err := updateStateJSON(integrateState, func(state map[string]any) {
				state["phase"] = "DONE"
			})
			if err != nil {
				return "", err
			}
			if err := a.saveState(ctx, root, env, issueNumber, doneState); err != nil {
				return "", err
			}
			return doneState, nil
		}

		failures := strings.Join(verifyResult.Failures, ", ")
		a.logError("INTEGRATE: verification failed for epic #%d: %s", issueNumber, failures)
		_ = a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "needs-review"}, nil, io.Discard, io.Discard)
		integrateState, err := updateStateJSON(stateJSON, func(state map[string]any) {
			state["phase"] = "INTEGRATE"
			state["integrate_failures"] = failures
		})
		if err != nil {
			return "", err
		}
		if err := a.saveState(ctx, root, env, issueNumber, integrateState); err != nil {
			return "", err
		}
		failedState, err := updateStateJSON(integrateState, func(state map[string]any) {
			state["phase"] = "FAILED"
		})
		if err != nil {
			return "", err
		}
		if err := a.saveState(ctx, root, env, issueNumber, failedState); err != nil {
			return "", err
		}
		return failedState, nil
	}

	_ = a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "done"}, nil, io.Discard, io.Discard)
	integrateState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "INTEGRATE"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, integrateState); err != nil {
		return "", err
	}
	doneState, err := updateStateJSON(integrateState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, doneState); err != nil {
		return "", err
	}
	return doneState, nil
}

func (a *App) getIssueMetadata(ctx context.Context, root string, env []string, repo string, issueNumber int) (issueMetadata, error) {
	issueOut, err := a.ghOutput(ctx, env, "issue", "view", strconv.Itoa(issueNumber), "--repo", repo, "--json", "number,title,body,labels,url")
	if err != nil {
		return issueMetadata{}, err
	}

	var issue issueView
	if err := json.Unmarshal([]byte(issueOut), &issue); err != nil {
		return issueMetadata{}, fmt.Errorf("failed to parse issue metadata: %v", err)
	}

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return issueMetadata{}, err
	}

	queueOut, err := a.scriptOutput(ctx, root, env, "gh-issue-queue.sh", []string{"list", repo, cfg.Labels.Ready}, nil)
	if err != nil {
		queueOut = "[]"
	}

	queueMeta, found := issueMetadataFromQueue(queueOut, issueNumber)
	if found {
		return queueMeta, nil
	}

	return metadataFromIssueView(issue), nil
}

func (a *App) phaseInit(ctx context.Context, root string, env []string, repo string, issueNumber int, dryRun bool, title string) (string, error) {
	a.logInfo("INIT: issue #%d", issueNumber)

	dispatchEnv := append([]string(nil), env...)
	dispatchEnv = envSet(dispatchEnv, "RUNOQ_NO_AUTO_TOKEN", "1")
	eligibilityOut, eligibilityErr := a.scriptOutput(ctx, root, dispatchEnv, "dispatch-safety.sh", []string{"eligibility", repo, strconv.Itoa(issueNumber)}, nil)
	if eligibilityErr != nil {
		return "", eligibilityErr
	}

	var eligibility eligibilityResult
	if err := json.Unmarshal([]byte(eligibilityOut), &eligibility); err != nil {
		return "", fmt.Errorf("failed to parse eligibility result: %v", err)
	}

	branch := eligibility.Branch
	if dryRun {
		stateJSON, err := marshalJSON(map[string]any{
			"phase":   "INIT",
			"dry_run": true,
			"issue":   issueNumber,
			"branch":  branch,
		})
		if err != nil {
			return "", err
		}
		a.logInfo("DRY-RUN: would create worktree, branch %s, draft PR for issue #%d", branch, issueNumber)
		return stateJSON, nil
	}

	if err := a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "in-progress"}, nil, io.Discard, io.Discard); err != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "failed to set issue status to in-progress", branch, "", nil)
	}

	worktreeOut, worktreeStderr, worktreeErr := a.scriptOutputWithStderr(ctx, root, env, "worktree.sh", []string{"create", strconv.Itoa(issueNumber), title}, nil)
	if worktreeErr != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, fmt.Sprintf("worktree creation failed: %s", stderrOrUnknown(worktreeStderr)), branch, "", nil)
	}

	var worktreeInfo worktreeCreateResult
	if err := json.Unmarshal([]byte(worktreeOut), &worktreeInfo); err != nil || strings.TrimSpace(worktreeInfo.Branch) == "" || strings.TrimSpace(worktreeInfo.Worktree) == "" {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "worktree creation returned an invalid payload", branch, "", nil)
	}

	worktree := worktreeInfo.Worktree
	branch = worktreeInfo.Branch
	a.logInfo("INIT: worktree=%s branch=%s", worktree, branch)

	if err := a.configureGitBotRemote(ctx, root, env, worktree, repo); err == nil {
		a.logInfo("INIT: bot remote configured for worktree")
	} else {
		a.logInfo("INIT: bot remote configuration failed or skipped for worktree")
	}

	if err := a.runProgram(ctx, env, "git", []string{"-C", worktree, "commit", "--allow-empty", "-m", fmt.Sprintf("runoq: begin work on #%d", issueNumber)}, nil, io.Discard, io.Discard); err != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "failed to create the initial worktree commit", branch, worktree, nil)
	}

	if err := a.runProgram(ctx, env, "git", []string{"-C", worktree, "push", "-u", "origin", branch}, nil, io.Discard, io.Discard); err != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "failed to push the initial worktree branch", branch, worktree, nil)
	}

	prOut, prStderr, prErr := a.scriptOutputWithStderr(ctx, root, env, "gh-pr-lifecycle.sh", []string{"create", repo, branch, strconv.Itoa(issueNumber), title}, nil)
	if prErr != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, fmt.Sprintf("draft PR creation failed: %s", stderrOrUnknown(prStderr)), branch, worktree, nil)
	}

	prNumber, ok := parsePRNumber(prOut)
	if !ok {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "draft PR creation returned an invalid payload", branch, worktree, nil)
	}

	a.logInfo("INIT: created draft PR #%d for branch=%s", prNumber, branch)

	stateJSON, err := marshalJSON(map[string]any{
		"issue":                issueNumber,
		"phase":                "INIT",
		"branch":               branch,
		"worktree":             worktree,
		"pr_number":            prNumber,
		"round":                0,
		"cumulative_tokens":    0,
		"consecutive_failures": 0,
	})
	if err != nil {
		return "", err
	}

	if err := a.saveState(ctx, root, env, issueNumber, stateJSON); err != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "failed to persist INIT state", branch, worktree, &prNumber)
	}

	_ = a.postAuditComment(ctx, root, env, repo, prNumber, "init", fmt.Sprintf("Orchestrator initialized. Branch: `%s`", branch))
	return stateJSON, nil
}

func (a *App) phaseCriteria(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata issueMetadata) (string, error) {
	complexity := strings.TrimSpace(metadata.EstimatedComplexity)
	if complexity == "" {
		complexity = "medium"
	}
	a.logInfo("CRITERIA: issue #%d complexity=%s type=%s", issueNumber, complexity, defaultString(metadata.Type, "task"))
	nextState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "CRITERIA"
		state["complexity"] = complexity
		state["issue_type"] = defaultString(metadata.Type, "task")
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, nextState); err != nil {
		return "", err
	}
	return nextState, nil
}

func (a *App) phaseCriteriaNeedsReviewHandoff(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata issueMetadata) (string, error) {
	complexity := strings.TrimSpace(metadata.EstimatedComplexity)
	if complexity == "" {
		complexity = "medium"
	}
	issueType := defaultString(metadata.Type, "task")
	reason := fmt.Sprintf("criteria for complexity=%s type=%s requires human review in the current runtime slice", complexity, issueType)
	a.logInfo("CRITERIA: issue #%d %s", issueNumber, reason)

	var state struct {
		PRNumber int `json:"pr_number"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for criteria handoff: %v", err)
	}
	if state.PRNumber == 0 {
		return "", errors.New("CRITERIA state is missing pr_number")
	}

	reviewState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "REVIEW"
		state["verdict"] = "FAIL"
		state["criteria_handoff"] = reason
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, reviewState); err != nil {
		return "", err
	}

	decideState, err := updateStateJSON(reviewState, func(state map[string]any) {
		state["phase"] = "DECIDE"
		state["decision"] = "finalize-needs-review"
		state["next_phase"] = "FINALIZE"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, decideState); err != nil {
		return "", err
	}

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return "", err
	}
	finalizeArgs := []string{"finalize", repo, strconv.Itoa(state.PRNumber), "needs-review"}
	if reviewer := firstReviewer(cfg.Reviewers); reviewer != "" {
		finalizeArgs = append(finalizeArgs, "--reviewer", reviewer)
	}
	if err := a.runScript(ctx, root, env, "gh-pr-lifecycle.sh", finalizeArgs, nil, io.Discard, io.Discard); err != nil {
		return "", err
	}
	if err := a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "needs-review"}, nil, io.Discard, io.Discard); err != nil {
		return "", err
	}

	finalizeState, err := updateStateJSON(decideState, func(state map[string]any) {
		state["phase"] = "FINALIZE"
		state["finalize_verdict"] = "needs-review"
		state["issue_status"] = "needs-review"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, finalizeState); err != nil {
		return "", err
	}

	doneState, err := updateStateJSON(finalizeState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, doneState); err != nil {
		return "", err
	}
	return doneState, nil
}

func (a *App) phaseDevelop(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string) (string, issueRunnerResult, error) {
	a.logInfo("DEVELOP: issue #%d", issueNumber)

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return "", issueRunnerResult{}, err
	}

	var state struct {
		Worktree          string `json:"worktree"`
		Branch            string `json:"branch"`
		PRNumber          int    `json:"pr_number"`
		Round             int    `json:"round"`
		CumulativeTokens  int    `json:"cumulative_tokens"`
		LogDir            string `json:"log_dir"`
		CriteriaCommit    string `json:"criteria_commit"`
		PreviousChecklist string `json:"previous_checklist"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", issueRunnerResult{}, fmt.Errorf("failed to parse state for develop: %v", err)
	}
	if strings.TrimSpace(state.Worktree) == "" || strings.TrimSpace(state.Branch) == "" || state.PRNumber == 0 {
		return "", issueRunnerResult{}, errors.New("INIT state is missing worktree, branch, or PR metadata")
	}

	bodyOut, err := a.ghOutput(ctx, env, "issue", "view", strconv.Itoa(issueNumber), "--repo", repo, "--json", "body")
	if err != nil {
		return "", issueRunnerResult{}, fmt.Errorf("failed to load issue body: %v", err)
	}
	var issueBody struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(bodyOut), &issueBody); err != nil {
		return "", issueRunnerResult{}, fmt.Errorf("failed to parse issue body: %v", err)
	}

	specFile, err := os.CreateTemp("", "runoq-spec.*")
	if err != nil {
		return "", issueRunnerResult{}, err
	}
	defer func() {
		_ = os.Remove(specFile.Name())
	}()
	if _, err := io.WriteString(specFile, issueBody.Body); err != nil {
		_ = specFile.Close()
		return "", issueRunnerResult{}, err
	}
	if err := specFile.Close(); err != nil {
		return "", issueRunnerResult{}, err
	}

	round := state.Round + 1
	payload := map[string]any{
		"issueNumber":    issueNumber,
		"prNumber":       state.PRNumber,
		"worktree":       state.Worktree,
		"branch":         state.Branch,
		"specPath":       specFile.Name(),
		"repo":           repo,
		"maxRounds":      cfg.MaxRounds,
		"maxTokenBudget": cfg.MaxTokenBudget,
		"round":          round,
		"guidelines":     "",
	}
	if strings.TrimSpace(state.LogDir) != "" {
		payload["logDir"] = state.LogDir
	}
	if state.CumulativeTokens > 0 {
		payload["cumulativeTokens"] = state.CumulativeTokens
	}
	if strings.TrimSpace(state.PreviousChecklist) != "" {
		payload["previousChecklist"] = state.PreviousChecklist
	}
	if strings.TrimSpace(state.CriteriaCommit) != "" {
		payload["criteria_commit"] = state.CriteriaCommit
	}

	payloadJSON, err := marshalJSON(payload)
	if err != nil {
		return "", issueRunnerResult{}, err
	}

	payloadFile, err := os.CreateTemp("", "runoq-runner-payload.*")
	if err != nil {
		return "", issueRunnerResult{}, err
	}
	defer func() {
		_ = os.Remove(payloadFile.Name())
	}()
	if _, err := io.WriteString(payloadFile, payloadJSON); err != nil {
		_ = payloadFile.Close()
		return "", issueRunnerResult{}, err
	}
	if err := payloadFile.Close(); err != nil {
		return "", issueRunnerResult{}, err
	}

	runnerEnv := envSet(env, "RUNOQ_ISSUE_RUNNER_IMPLEMENTATION", "shell")
	runnerOut, runnerStderr, err := a.scriptOutputWithStderr(ctx, root, runnerEnv, "issue-runner.sh", []string{"run", payloadFile.Name()}, nil)
	if strings.TrimSpace(runnerStderr) != "" {
		a.logInfo("DEVELOP: issue-runner stderr: %s", runnerStderr)
	}
	if err != nil {
		return "", issueRunnerResult{}, err
	}

	var result issueRunnerResult
	if err := json.Unmarshal([]byte(runnerOut), &result); err != nil {
		return "", issueRunnerResult{}, fmt.Errorf("issue-runner returned invalid JSON: %v", err)
	}

	nextState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "DEVELOP"
		state["round"] = round
		state["status"] = result.Status
		state["log_dir"] = result.LogDir
		state["baseline_hash"] = result.BaselineHash
		state["head_hash"] = result.HeadHash
		state["commit_range"] = result.CommitRange
		state["review_log_path"] = result.ReviewLogPath
		state["spec_requirements"] = result.SpecRequirements
		state["changed_files"] = result.ChangedFiles
		state["related_files"] = result.RelatedFiles
		state["cumulative_tokens"] = result.CumulativeTokens
		state["verification_passed"] = result.VerificationPassed
		state["verification_failures"] = result.VerificationFailures
		state["caveats"] = result.Caveats
		state["summary"] = result.Summary
	})
	if err != nil {
		return "", issueRunnerResult{}, err
	}
	if err := a.saveState(ctx, root, env, issueNumber, nextState); err != nil {
		return "", issueRunnerResult{}, err
	}
	return nextState, result, nil
}

func (a *App) phaseReview(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string) (string, error) {

	var state struct {
		Worktree          string   `json:"worktree"`
		Branch            string   `json:"branch"`
		PRNumber          int      `json:"pr_number"`
		Round             int      `json:"round"`
		BaselineHash      string   `json:"baseline_hash"`
		HeadHash          string   `json:"head_hash"`
		ReviewLogPath     string   `json:"review_log_path"`
		SpecRequirements  string   `json:"spec_requirements"`
		ChangedFiles      []string `json:"changed_files"`
		RelatedFiles      []string `json:"related_files"`
		PreviousChecklist string   `json:"previous_checklist"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for review: %v", err)
	}
	if strings.TrimSpace(state.Worktree) == "" || strings.TrimSpace(state.Branch) == "" || state.PRNumber == 0 {
		return "", errors.New("DEVELOP state is missing worktree, branch, or PR metadata")
	}

	round := max(state.Round, 1)
	a.logInfo("REVIEW: spawning diff-reviewer for issue #%d round %d", issueNumber, round)

	changedFilesJSON := "[]"
	if len(state.ChangedFiles) > 0 {
		if payload, err := marshalJSON(state.ChangedFiles); err == nil {
			changedFilesJSON = payload
		}
	}
	relatedFilesJSON := "[]"
	if len(state.RelatedFiles) > 0 {
		if payload, err := marshalJSON(state.RelatedFiles); err == nil {
			relatedFilesJSON = payload
		}
	}

	reviewPayload, err := marshalJSON(map[string]any{
		"issueNumber":       issueNumber,
		"round":             round,
		"worktree":          state.Worktree,
		"baselineHash":      state.BaselineHash,
		"headHash":          state.HeadHash,
		"reviewLogPath":     state.ReviewLogPath,
		"specRequirements":  state.SpecRequirements,
		"guidelines":        "",
		"changedFiles":      changedFilesJSON,
		"relatedFiles":      relatedFilesJSON,
		"previousChecklist": state.PreviousChecklist,
	})
	if err != nil {
		return "", err
	}

	reviewOutputFile, err := os.CreateTemp("", "runoq-review-out.*")
	if err != nil {
		return "", err
	}
	reviewOutputPath := reviewOutputFile.Name()
	defer func() {
		_ = os.Remove(reviewOutputPath)
	}()
	if err := reviewOutputFile.Close(); err != nil {
		return "", err
	}

	if err := a.runProgram(ctx, env, "bash", []string{
		"-lc",
		`source "$1"; runoq::claude_stream "$2" --permission-mode bypassPermissions --agent diff-reviewer --add-dir "$3" -- "$4"`,
		"bash",
		filepath.Join(root, "scripts", "lib", "common.sh"),
		reviewOutputPath,
		root,
		reviewPayload,
	}, nil, io.Discard, io.Discard); err != nil {
		return "", err
	}

	reviewLogAbs := strings.TrimSpace(state.ReviewLogPath)
	if reviewLogAbs != "" && !filepath.IsAbs(reviewLogAbs) {
		reviewLogAbs = filepath.Join(state.Worktree, reviewLogAbs)
	}
	reviewLogExists := fileExists(reviewLogAbs)
	a.logInfo("REVIEW: review_log_path=%s review_log_abs=%s exists=%s", state.ReviewLogPath, reviewLogAbs, yesNo(reviewLogExists))

	verdictResult := reviewVerdictResult{}
	if reviewLogExists {
		parsed, err := parseReviewVerdict(reviewLogAbs)
		if err != nil {
			return "", err
		}
		verdictResult = parsed
	} else {
		a.logInfo("REVIEW: review log not found, parsing from claude output file")
		parsed, err := parseReviewVerdict(reviewOutputPath)
		if err != nil {
			verdictResult = reviewVerdictResult{Verdict: "FAIL", Score: "0", Checklist: "", ReviewType: ""}
		} else {
			verdictResult = parsed
		}
	}

	verdict := strings.TrimSpace(verdictResult.Verdict)
	if verdict == "" || verdict == "null" {
		verdict = "FAIL"
		a.logError("Could not parse review verdict; treating as FAIL")
	}
	score := strings.TrimSpace(verdictResult.Score)
	if score == "" || score == "null" {
		score = "0"
	}
	reviewChecklist := verdictResult.Checklist
	a.logInfo("REVIEW: verdict=%s score=%s", verdict, score)

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return "", err
	}
	reviewBody := fmt.Sprintf("## Diff Review - round %d / %d\n\n> Posted by `orchestrator` via `diff-reviewer` agent\n\n| Field | Value |\n|-------|-------|\n| **Verdict** | %s |\n| **Score** | %s |\n| **Commit range** | `%s..%s` |\n| **Changed files** | %s |\n", round, cfg.MaxRounds, verdict, score, truncateHash(state.BaselineHash), truncateHash(state.HeadHash), changedFilesJSON)
	if strings.TrimSpace(reviewChecklist) != "" {
		reviewBody += "\n### Checklist\n" + reviewChecklist + "\n"
	}
	_ = a.postAuditComment(ctx, root, env, repo, state.PRNumber, "review", reviewBody)

	reviewState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "REVIEW"
		state["verdict"] = verdict
		state["score"] = score
		state["review_checklist"] = reviewChecklist
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, reviewState); err != nil {
		return "", err
	}

	return reviewState, nil
}

func (a *App) phaseDecide(ctx context.Context, root string, env []string, issueNumber int, stateJSON string) (string, error) {
	var state struct {
		Verdict string `json:"verdict"`
		Round   int    `json:"round"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for decide: %v", err)
	}

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return "", err
	}

	verdict := strings.TrimSpace(state.Verdict)
	if verdict == "" {
		verdict = "FAIL"
	}

	a.logInfo("DECIDE: verdict=%s round=%d/%d", verdict, max(state.Round, 1), cfg.MaxRounds)

	decision := "finalize-needs-review"
	nextPhase := "FINALIZE"
	if verdict == "PASS" {
		decision = "finalize"
	} else if verdict == "ITERATE" && state.Round < cfg.MaxRounds {
		decision = "iterate"
		nextPhase = "DEVELOP"
	}

	decideState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "DECIDE"
		state["decision"] = decision
		state["next_phase"] = nextPhase
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, decideState); err != nil {
		return "", err
	}
	return decideState, nil
}

func (a *App) phaseFinalize(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata issueMetadata) (string, error) {
	var state struct {
		PRNumber int      `json:"pr_number"`
		Worktree string   `json:"worktree"`
		Verdict  string   `json:"verdict"`
		Decision string   `json:"decision"`
		Score    string   `json:"score"`
		Round    int      `json:"round"`
		Caveats  []string `json:"caveats"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for finalize: %v", err)
	}
	if state.PRNumber == 0 {
		return "", errors.New("DECIDE state is missing pr_number")
	}

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return "", err
	}

	complexity := strings.TrimSpace(metadata.EstimatedComplexity)
	if complexity == "" {
		complexity = "medium"
	}

	a.logInfo("FINALIZE: issue #%d decision=%s complexity=%s", issueNumber, defaultString(state.Decision, "finalize-needs-review"), complexity)

	finalizeVerdict, issueStatus, finalizeReason, complexityOK := finalizeDecision(state, cfg, complexity)
	a.logInfo(
		"FINALIZE: decision table: auto_merge_enabled=%t max_complexity=%s complexity=%s complexity_ok=%s finalize_verdict=%s finalize_reason=%s issue_status=%s",
		cfg.AutoMerge.Enabled,
		autoMergeMaxComplexity(cfg),
		complexity,
		complexityDecisionValue(complexityOK),
		finalizeVerdict,
		defaultString(finalizeReason, "none"),
		issueStatus,
	)

	finalizeArgs := []string{"finalize", repo, strconv.Itoa(state.PRNumber), finalizeVerdict}
	if finalizeVerdict == "needs-review" {
		if reviewer := firstReviewer(cfg.Reviewers); reviewer != "" {
			finalizeArgs = append(finalizeArgs, "--reviewer", reviewer)
		}
	}

	a.logInfo("FINALIZE: calling pr-lifecycle finalize verdict=%s pr=#%d", finalizeVerdict, state.PRNumber)
	if err := a.runScript(ctx, root, env, "gh-pr-lifecycle.sh", finalizeArgs, nil, io.Discard, io.Discard); err != nil {
		a.logInfo("FINALIZE: pr-lifecycle finalize failed")
	}

	a.logInfo("FINALIZE: setting issue #%d status to %s", issueNumber, issueStatus)
	if err := a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), issueStatus}, nil, io.Discard, io.Discard); err == nil {
		a.logInfo("FINALIZE: set-status succeeded for issue #%d", issueNumber)
	} else {
		a.logInfo("FINALIZE: set-status failed for issue #%d", issueNumber)
	}

	if finalizeVerdict == "auto-merge" {
		a.logInfo("FINALIZE: removing worktree for issue #%d (auto-merged)", issueNumber)
		if err := a.runScript(ctx, root, env, "worktree.sh", []string{"remove", strconv.Itoa(issueNumber)}, nil, io.Discard, io.Discard); err == nil {
			a.logInfo("FINALIZE: worktree removed successfully")
		} else {
			a.logInfo("FINALIZE: worktree removal failed")
		}
	}

	finalizeBody := fmt.Sprintf(
		"## Finalize - issue #%d\n\n| Field | Value |\n|-------|-------|\n| **Decision** | `%s` |\n| **Issue status** | `%s` |\n| **Review verdict** | %s |\n| **Review score** | %s |\n| **Complexity** | %s |\n| **Auto-merge enabled** | %t |\n| **Auto-merge max complexity** | %s |\n| **Round** | %d / %d |\n",
		issueNumber,
		finalizeVerdict,
		issueStatus,
		defaultString(strings.TrimSpace(state.Verdict), "FAIL"),
		defaultString(strings.TrimSpace(state.Score), "0"),
		complexity,
		cfg.AutoMerge.Enabled,
		autoMergeMaxComplexity(cfg),
		max(state.Round, 1),
		cfg.MaxRounds,
	)
	if strings.TrimSpace(finalizeReason) != "" {
		finalizeBody += "\n**Reason**: " + finalizeReason + "\n"
	}
	if len(state.Caveats) > 0 {
		finalizeBody += "\n**Caveats**: " + strings.Join(state.Caveats, ", ") + "\n"
	}
	_ = a.postAuditComment(ctx, root, env, repo, state.PRNumber, "finalize", finalizeBody)

	finalizeState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "FINALIZE"
		state["finalize_verdict"] = finalizeVerdict
		state["issue_status"] = issueStatus
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, finalizeState); err != nil {
		return "", err
	}

	doneState, err := updateStateJSON(finalizeState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, doneState); err != nil {
		return "", err
	}
	return doneState, nil
}

func (a *App) phaseDevelopNeedsReview(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string) (string, error) {
	a.logInfo("DEVELOP: issue #%d requires deterministic needs-review handoff", issueNumber)

	var state struct {
		PRNumber int `json:"pr_number"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for needs-review handoff: %v", err)
	}
	if state.PRNumber == 0 {
		return "", errors.New("DEVELOP state is missing pr_number")
	}

	reviewState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "REVIEW"
		state["verdict"] = "FAIL"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, reviewState); err != nil {
		return "", err
	}

	decideState, err := updateStateJSON(reviewState, func(state map[string]any) {
		state["phase"] = "DECIDE"
		state["decision"] = "finalize-needs-review"
		state["next_phase"] = "FINALIZE"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, decideState); err != nil {
		return "", err
	}

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return "", err
	}
	finalizeArgs := []string{"finalize", repo, strconv.Itoa(state.PRNumber), "needs-review"}
	if reviewer := firstReviewer(cfg.Reviewers); reviewer != "" {
		finalizeArgs = append(finalizeArgs, "--reviewer", reviewer)
	}
	if err := a.runScript(ctx, root, env, "gh-pr-lifecycle.sh", finalizeArgs, nil, io.Discard, io.Discard); err != nil {
		return "", err
	}
	if err := a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "needs-review"}, nil, io.Discard, io.Discard); err != nil {
		return "", err
	}

	finalizeState, err := updateStateJSON(decideState, func(state map[string]any) {
		state["phase"] = "FINALIZE"
		state["finalize_verdict"] = "needs-review"
		state["issue_status"] = "needs-review"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, finalizeState); err != nil {
		return "", err
	}

	doneState, err := updateStateJSON(finalizeState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	if err := a.saveState(ctx, root, env, issueNumber, doneState); err != nil {
		return "", err
	}
	return doneState, nil
}

func (a *App) handleInitFailure(ctx context.Context, root string, env []string, repo string, issueNumber int, reason string, branch string, worktree string, prNumber *int) error {
	a.logError("INIT: %s", reason)

	failureState, err := marshalJSON(initFailureState(reason, branch, worktree, prNumber))
	if err == nil {
		_ = a.saveState(ctx, root, env, issueNumber, failureState)
	}

	if prNumber == nil {
		_ = a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "ready"}, nil, io.Discard, io.Discard)
		if strings.TrimSpace(worktree) != "" {
			_ = a.runScript(ctx, root, env, "worktree.sh", []string{"remove", strconv.Itoa(issueNumber)}, nil, io.Discard, io.Discard)
		}
	}

	return errors.New(reason)
}

func initFailureState(reason string, branch string, worktree string, prNumber *int) map[string]any {
	state := map[string]any{
		"phase":          "FAILED",
		"failure_stage":  "INIT",
		"failure_scope":  "internal",
		"failure_reason": reason,
	}
	if strings.TrimSpace(branch) != "" {
		state["branch"] = branch
	}
	if strings.TrimSpace(worktree) != "" {
		state["worktree"] = worktree
	}
	if prNumber != nil {
		state["pr_number"] = *prNumber
	}
	return state
}

func (a *App) saveState(ctx context.Context, root string, env []string, issueNumber int, stateJSON string) error {
	return a.runScript(ctx, root, env, "state.sh", []string{"save", strconv.Itoa(issueNumber)}, strings.NewReader(stateJSON), io.Discard, io.Discard)
}

func (a *App) issueTitle(ctx context.Context, env []string, repo string, issueNumber int) (string, error) {
	out, err := a.ghOutput(ctx, env, "issue", "view", strconv.Itoa(issueNumber), "--repo", repo, "--json", "title")
	if err != nil {
		return "", err
	}
	var payload struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Title) == "" {
		return "untitled", nil
	}
	return payload.Title, nil
}

func (a *App) loadConfig(root string, env []string) (queueConfig, error) {
	configPath, ok := envLookup(env, "RUNOQ_CONFIG")
	if !ok || strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(root, "config", "runoq.json")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return queueConfig{}, fmt.Errorf("failed to read config: %v", err)
	}
	var cfg queueConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return queueConfig{}, fmt.Errorf("failed to parse config: %v", err)
	}
	return cfg, nil
}

func metadataFromIssueView(issue issueView) issueMetadata {
	meta := parseMetadataBlock(issue.Body)
	complexity := meta.EstimatedComplexity
	if complexity == "" {
		complexity = "medium"
	}
	issueType := meta.Type
	if issueType == "" {
		issueType = "task"
	}

	return issueMetadata{
		Number:              issue.Number,
		Title:               issue.Title,
		Body:                issue.Body,
		URL:                 issue.URL,
		EstimatedComplexity: complexity,
		ComplexityRationale: nullableString(meta.ComplexityRationale),
		Type:                issueType,
	}
}

func formatSkippedSummary(skipped []queueSelectionIssue) string {
	if len(skipped) == 0 {
		return ""
	}

	parts := make([]string, 0, len(skipped))
	for _, issue := range skipped {
		number := "?"
		if issue.Number > 0 {
			number = strconv.Itoa(issue.Number)
		}
		reasons := issue.BlockedReasons
		if len(reasons) == 0 {
			reasons = []string{"unknown"}
		}
		parts = append(parts, "#"+number+" — "+strings.Join(reasons, ", "))
	}
	return strings.Join(parts, "; ")
}

func issueMetadataFromQueue(raw string, issueNumber int) (issueMetadata, bool) {
	var queueEntries []struct {
		Number              int     `json:"number"`
		Title               string  `json:"title"`
		Body                string  `json:"body"`
		URL                 string  `json:"url"`
		EstimatedComplexity string  `json:"estimated_complexity"`
		ComplexityRationale *string `json:"complexity_rationale"`
		Type                string  `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &queueEntries); err != nil {
		return issueMetadata{}, false
	}
	for _, entry := range queueEntries {
		if entry.Number != issueNumber {
			continue
		}
		complexity := entry.EstimatedComplexity
		if complexity == "" {
			complexity = "medium"
		}
		issueType := entry.Type
		if issueType == "" {
			issueType = "task"
		}
		return issueMetadata{
			Number:              entry.Number,
			Title:               entry.Title,
			Body:                entry.Body,
			URL:                 entry.URL,
			EstimatedComplexity: complexity,
			ComplexityRationale: entry.ComplexityRationale,
			Type:                issueType,
		}, true
	}
	return issueMetadata{}, false
}

type metadataBlock struct {
	EstimatedComplexity string
	ComplexityRationale string
	Type                string
}

func parseMetadataBlock(body string) metadataBlock {
	block := extractMetaBlock(body)
	if block == "" {
		return metadataBlock{}
	}

	meta := metadataBlock{}
	for line := range strings.SplitSeq(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "estimated_complexity":
			meta.EstimatedComplexity = value
		case "complexity_rationale":
			meta.ComplexityRationale = value
		case "type":
			meta.Type = value
		}
	}
	return meta
}

func extractMetaBlock(body string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "<!-- runoq:meta") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}

	var block strings.Builder
	for _, line := range lines[start:] {
		if strings.Contains(line, "-->") {
			break
		}
		if block.Len() > 0 {
			block.WriteByte('\n')
		}
		block.WriteString(line)
	}
	return block.String()
}

func parsePRNumber(raw string) (int, bool) {
	var payload prCreateResult
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0, false
	}
	switch value := payload.Number.(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	default:
		return 0, false
	}
}

type reviewVerdictResult struct {
	ReviewType string
	Verdict    string
	Score      string
	Checklist  string
}

func parseReviewVerdict(path string) (reviewVerdictResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return reviewVerdictResult{}, err
	}
	defer func() {
		_ = file.Close()
	}()

	result := reviewVerdictResult{}
	var checklistLines []string
	inChecklist := false

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if inChecklist {
			checklistLines = append(checklistLines, line)
			continue
		}

		switch {
		case strings.HasPrefix(line, "REVIEW-TYPE:"):
			result.ReviewType = strings.TrimSpace(strings.TrimPrefix(line, "REVIEW-TYPE:"))
		case strings.HasPrefix(line, "VERDICT:"):
			result.Verdict = strings.TrimSpace(strings.TrimPrefix(line, "VERDICT:"))
		case strings.HasPrefix(line, "SCORE:"):
			result.Score = strings.TrimSpace(strings.TrimPrefix(line, "SCORE:"))
		case line == "CHECKLIST:":
			inChecklist = true
		}
	}
	if err := scanner.Err(); err != nil {
		return reviewVerdictResult{}, err
	}

	result.Checklist = strings.Join(checklistLines, "\n")
	return result, nil
}

func (a *App) prepareAuth(ctx context.Context, root string, env []string) []string {
	authEnv := envSet(env, "RUNOQ_FORCE_REFRESH_TOKEN", "1")
	var stdout bytes.Buffer
	err := a.execCommand(ctx, commandRequest{
		Name: "bash",
		Args: []string{
			"-lc",
			`if eval "$("$1" export-token)" 2>/dev/null; then printf 'ok\n%s' "${GH_TOKEN:-}"; else printf 'fail\n%s' "${GH_TOKEN:-}"; fi`,
			"bash",
			filepath.Join(root, "scripts", "gh-auth.sh"),
		},
		Dir:    a.cwd,
		Env:    authEnv,
		Stdout: &stdout,
		Stderr: a.stderr,
	})
	if err != nil {
		a.logInfo("Token mint failed or skipped (will use ambient credentials)")
		return authEnv
	}

	out := strings.TrimSpace(stdout.String())
	status, token, _ := strings.Cut(out, "\n")
	if status == "ok" {
		a.logInfo("Token mint succeeded")
	} else {
		a.logInfo("Token mint failed or skipped (will use ambient credentials)")
	}
	if strings.TrimSpace(token) != "" {
		authEnv = envSet(authEnv, "GH_TOKEN", strings.TrimSpace(token))
	}
	return authEnv
}

func (a *App) targetRoot(ctx context.Context, env []string) (string, error) {
	if value, ok := envLookup(env, "TARGET_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	out, err := a.commandOutput(ctx, commandRequest{
		Name: "git",
		Args: []string{"rev-parse", "--show-toplevel"},
		Dir:  a.cwd,
		Env:  env,
	})
	if err != nil {
		return "", errors.New("run runoq from inside a git repository")
	}
	return out, nil
}

func (a *App) configureGitBotIdentity(ctx context.Context, root string, env []string, dir string) error {
	return a.runProgram(ctx, env, "bash", []string{
		"-lc",
		`source "$1"; runoq::configure_git_bot_identity "$2"`,
		"bash",
		filepath.Join(root, "scripts", "lib", "common.sh"),
		dir,
	}, nil, io.Discard, io.Discard)
}

func (a *App) configureGitBotRemote(ctx context.Context, root string, env []string, dir string, repo string) error {
	return a.runProgram(ctx, env, "bash", []string{
		"-lc",
		`source "$1"; runoq::configure_git_bot_remote "$2" "$3"`,
		"bash",
		filepath.Join(root, "scripts", "lib", "common.sh"),
		dir,
		repo,
	}, nil, io.Discard, io.Discard)
}

func (a *App) postAuditComment(ctx context.Context, root string, env []string, repo string, prNumber int, event string, body string) error {
	commentFile, err := os.CreateTemp("", "runoq-audit.*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(commentFile.Name())
	}()

	if _, err := fmt.Fprintf(commentFile, "<!-- runoq:event:%s -->\n> Posted by `orchestrator` — %s phase\n\n%s\n", event, event, body); err != nil {
		_ = commentFile.Close()
		return err
	}
	if err := commentFile.Close(); err != nil {
		return err
	}

	return a.runScript(ctx, root, env, "gh-pr-lifecycle.sh", []string{"comment", repo, strconv.Itoa(prNumber), commentFile.Name()}, nil, io.Discard, io.Discard)
}

func (a *App) ghOutput(ctx context.Context, env []string, args ...string) (string, error) {
	return a.commandOutput(ctx, commandRequest{
		Name: envOrDefault(env, "GH_BIN", "gh"),
		Args: args,
		Dir:  a.cwd,
		Env:  env,
	})
}

func (a *App) scriptOutput(ctx context.Context, root string, env []string, script string, args []string, stdin io.Reader) (string, error) {
	var stdout bytes.Buffer
	err := a.runScript(ctx, root, env, script, args, stdin, &stdout, io.Discard)
	return strings.TrimSpace(stdout.String()), err
}

func (a *App) scriptOutputWithStderr(ctx context.Context, root string, env []string, script string, args []string, stdin io.Reader) (string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := a.runScript(ctx, root, env, script, args, stdin, &stdout, &stderr)
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func (a *App) runScript(ctx context.Context, root string, env []string, script string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	return a.runProgram(ctx, env, filepath.Join(root, "scripts", script), args, stdin, stdout, stderr)
}

func (a *App) runProgram(ctx context.Context, env []string, name string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	return a.execCommand(ctx, commandRequest{
		Name:   name,
		Args:   append([]string(nil), args...),
		Dir:    a.cwd,
		Env:    append([]string(nil), env...),
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
}

func (a *App) commandOutput(ctx context.Context, req commandRequest) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	req.Stdout = &stdout
	req.Stderr = &stderr
	err := a.execCommand(ctx, req)
	return strings.TrimSpace(stdout.String()), err
}

func (a *App) runoqRoot() string {
	if root, ok := envLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(root) != "" {
		return root
	}
	if a.cwd != "" && fileExists(filepath.Join(a.cwd, "scripts", "lib", "common.sh")) {
		return a.cwd
	}
	return ""
}

func (a *App) printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

func (a *App) fail(message string) int {
	_, _ = fmt.Fprintf(a.stderr, "runoq: %s\n", message)
	return 1
}

func (a *App) failf(format string, args ...any) int {
	return a.fail(fmt.Sprintf(format, args...))
}

func (a *App) logInfo(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stderr, "[orchestrator] "+format+"\n", args...)
}

func (a *App) logError(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stderr, "[orchestrator] ERROR: "+format+"\n", args...)
}

func marshalJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func nullableString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return &trimmed
}

func stderrOrUnknown(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return "unknown error"
	}
	return stderr
}

func finalizeDecision(state struct {
	PRNumber int      `json:"pr_number"`
	Worktree string   `json:"worktree"`
	Verdict  string   `json:"verdict"`
	Decision string   `json:"decision"`
	Score    string   `json:"score"`
	Round    int      `json:"round"`
	Caveats  []string `json:"caveats"`
}, cfg queueConfig, complexity string) (finalizeVerdict string, issueStatus string, finalizeReason string, complexityOK bool) {
	if state.Verdict != "PASS" {
		return "needs-review", "needs-review", fmt.Sprintf("Review verdict was %s (not PASS).", defaultString(state.Verdict, "FAIL")), false
	}
	if len(state.Caveats) > 0 {
		return "needs-review", "needs-review", "Caveats present: " + strings.Join(state.Caveats, ", "), false
	}
	if !cfg.AutoMerge.Enabled {
		return "needs-review", "needs-review", "Auto-merge is disabled in config.", false
	}

	complexityOK = autoMergeComplexityAllowed(complexity, autoMergeMaxComplexity(cfg))
	if complexityOK {
		return "auto-merge", "done", "", true
	}
	return "needs-review", "needs-review", fmt.Sprintf("Complexity %q exceeds auto-merge threshold %q.", complexity, autoMergeMaxComplexity(cfg)), false
}

func autoMergeComplexityAllowed(complexity string, maxComplexity string) bool {
	switch maxComplexity {
	case "high":
		return true
	case "medium":
		return complexity == "low" || complexity == "medium"
	default:
		return complexity == "low"
	}
}

func autoMergeMaxComplexity(cfg queueConfig) string {
	value := strings.TrimSpace(cfg.AutoMerge.MaxComplexity)
	if value == "" {
		return "low"
	}
	return value
}

func complexityDecisionValue(ok bool) string {
	if ok {
		return "true"
	}
	return "false"
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func truncateHash(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	if len(trimmed) <= 7 {
		return trimmed
	}
	return trimmed[:7]
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}

	type exitCoder interface {
		ExitCode() int
	}

	var exitErr exitCoder
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func firstReviewer(reviewers []string) string {
	for _, reviewer := range reviewers {
		trimmed := strings.TrimSpace(reviewer)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func updateStateJSON(stateJSON string, update func(map[string]any)) (string, error) {
	var state map[string]any
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", err
	}
	update(state)
	return marshalJSON(state)
}

func envLookup(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix), true
		}
	}
	return "", false
}

func envOrDefault(env []string, key string, fallback string) string {
	if value, ok := envLookup(env, key); ok && value != "" {
		return value
	}
	return fallback
}

func envSet(env []string, key string, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				next = append(next, prefix+value)
				replaced = true
			}
			continue
		}
		next = append(next, entry)
	}
	if !replaced {
		next = append(next, prefix+value)
	}
	return next
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func runCommand(ctx context.Context, req commandRequest) error {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	return cmd.Run()
}
