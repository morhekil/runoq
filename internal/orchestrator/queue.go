package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/saruman/runoq/internal/shell"
)

type ineligibleIssueError struct {
	IssueNumber int
	Reasons     []string
}

func (e ineligibleIssueError) Error() string {
	details := strings.TrimSpace(strings.Join(e.Reasons, "; "))
	if details == "" {
		return fmt.Sprintf("issue #%d is not eligible", e.IssueNumber)
	}
	return fmt.Sprintf("issue #%d is not eligible: %s", e.IssueNumber, details)
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
				return shell.Fail(a.stderr, "--issue requires a value")
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
			return shell.Fail(a.stderr, "--issue requires a numeric value")
		}
	}

	targetRoot, err := a.targetRoot(ctx, env)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	a.logInfo("Configuring bot identity for target root: %s", targetRoot)
	if err := a.configureGitBotIdentity(ctx, env, targetRoot); err == nil {
		a.logInfo("Bot identity configured successfully")
	} else {
		a.logInfo("Bot identity configuration failed or skipped")
	}

	if err := a.configureGitBotRemote(ctx, env, targetRoot, repo); err == nil {
		a.logInfo("Bot remote configured successfully for repo=%s", repo)
	} else {
		a.logInfo("Bot remote configuration failed or skipped")
	}

	if issueNumber == "" {
		return shell.Fail(a.stderr, "--issue is required. Use 'runoq tick' for queue processing.")
	}

	if a.cfg.ReadyLabel != "" {
		a.logInfo("Running reconciliation")
		if code := a.dispatchSafetyApp.Reconcile(ctx, repo); code != 0 {
			return shell.Failf(a.stderr, "dispatch safety reconcile exited %d", code)
		}
	}

	issue, _ := strconv.Atoi(issueNumber)
	title, err := a.issueTitle(ctx, env, repo, issue)
	if err != nil {
		return shell.Failf(a.stderr, "failed to load issue title: %v", err)
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

// Setup performs one-time authentication and git identity configuration.
// Returns the updated env with GH_TOKEN set. The returned env should be
// used for all subsequent operations. Returns the original env if auth fails.
func (a *App) Setup(ctx context.Context, repo string) []string {
	root := a.runoqRoot()
	if root == "" {
		return a.env
	}

	env := append([]string(nil), a.env...)
	if authEnv := a.prepareAuth(ctx, root, env); authEnv != nil {
		env = authEnv
	}

	targetRoot, err := a.targetRoot(ctx, env)
	if err != nil {
		a.logInfo("Setup: target root resolution failed: %v", err)
		return env
	}

	if err := a.configureGitBotIdentity(ctx, env, targetRoot); err != nil {
		a.logInfo("Setup: bot identity configuration failed or skipped")
	}
	if err := a.configureGitBotRemote(ctx, env, targetRoot, repo); err != nil {
		a.logInfo("Setup: bot remote configuration failed or skipped")
	}

	return env
}

// RunIssue advances a single issue by one phase boundary in the implementation
// state machine (INIT→DEVELOP→VERIFY→REVIEW→DECIDE→FINALIZE).
// The caller provides metadata so no additional API call is needed for issue details.
func (a *App) RunIssue(ctx context.Context, repo string, issueNumber int, dryRun bool, title string, metadata IssueMetadata) (string, error) {
	a.ensureSubApps()
	root := a.runoqRoot()
	if root == "" {
		return "", fmt.Errorf("unable to resolve RUNOQ_ROOT")
	}
	env := append([]string(nil), a.env...)
	return a.runIssueWithEnv(ctx, root, env, repo, issueNumber, dryRun, title, metadata)
}

func (a *App) runSingleIssue(ctx context.Context, root string, env []string, repo string, issueNumber int, dryRun bool, title string) (string, error) {
	metadata, err := a.getIssueMetadata(ctx, root, env, repo, issueNumber)
	if err != nil {
		return "", err
	}
	// CLI path: loop through tick boundaries until a terminal phase.
	stateJSON, err := a.runIssueWithEnv(ctx, root, env, repo, issueNumber, dryRun, title, metadata)
	if err != nil || dryRun {
		return stateJSON, err
	}
	for !isTerminalPhase(stateJSON) {
		if waitingReasonFromState(stateJSON) != "" {
			return stateJSON, nil
		}
		a.logInfo("CLI: phase boundary reached, continuing to next phase")
		stateJSON, err = a.resumeFromState(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		if err != nil {
			return stateJSON, err
		}
	}
	return stateJSON, nil
}

// isTerminalPhase returns true if the state JSON indicates a terminal phase.
func isTerminalPhase(stateJSON string) bool {
	var state struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return false
	}
	return state.Phase == "DONE" || state.Phase == "FINALIZE"
}

func waitingReasonFromState(stateJSON string) string {
	var state struct {
		Waiting       bool   `json:"waiting"`
		WaitingReason string `json:"waiting_reason"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return ""
	}
	if !state.Waiting {
		return ""
	}
	return strings.TrimSpace(state.WaitingReason)
}

func (a *App) runIssueWithEnv(ctx context.Context, root string, env []string, repo string, issueNumber int, dryRun bool, title string, metadata IssueMetadata) (string, error) {
	// Check GitHub for existing work before starting fresh
	if !dryRun {
		derivedState, _, found, deriveErr := a.deriveStateFromGitHub(ctx, env, repo, issueNumber)
		if deriveErr != nil {
			a.logInfo("State derivation failed (starting fresh): %v", deriveErr)
		} else if found && derivedState != "" {
			return a.resumeFromState(ctx, root, env, repo, issueNumber, derivedState, metadata)
		}
	}

	stateJSON, err := a.phaseInit(ctx, root, env, repo, issueNumber, dryRun, title)
	if err != nil {
		return "", err
	}

	if dryRun {
		return stateJSON, nil
	}

	return stateJSON, nil
}

func (a *App) resumeFromState(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	var state struct {
		Phase       string `json:"phase"`
		PRNumber    int    `json:"pr_number"`
		ResumePhase string `json:"resume_phase"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse derived state: %v", err)
	}
	a.logInfo("RESUME: issue #%d from phase %s (PR #%d)", issueNumber, state.Phase, state.PRNumber)

	if state.Phase == "DEVELOP" && waitingReasonFromState(stateJSON) != "" {
		return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	}

	switch state.Phase {
	case "DONE":
		a.logInfo("RESUME: issue #%d already at terminal phase %s", issueNumber, state.Phase)
		return stateJSON, nil
	case "FINALIZE":
		return a.runFromFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "RESPOND":
		switch state.ResumePhase {
		case "DEVELOP":
			return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		case "VERIFY":
			return a.runFromVerify(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		case "REVIEW":
			return a.runFromReview(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		case "DECIDE":
			return a.runFromDecide(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		case "FINALIZE":
			return a.runFromFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		default:
			return "", fmt.Errorf("unsupported RESPOND resume target %q", state.ResumePhase)
		}
	case "INIT":
		return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "CRITERIA":
		// Legacy recovery path for older PR state snapshots.
		return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "DEVELOP":
		return a.runFromVerify(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "VERIFY":
		var verifyState struct {
			VerificationPassed bool `json:"verification_passed"`
		}
		if err := json.Unmarshal([]byte(stateJSON), &verifyState); err != nil {
			return "", fmt.Errorf("failed to parse verify state for resume: %v", err)
		}
		if !verifyState.VerificationPassed {
			return a.runFromDecide(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		}
		return a.runFromReview(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "REVIEW":
		return a.runFromDecide(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "DECIDE":
		var decideState struct {
			Decision        string `json:"decision"`
			NextPhase       string `json:"next_phase"`
			ReviewChecklist string `json:"review_checklist"`
		}
		if err := json.Unmarshal([]byte(stateJSON), &decideState); err != nil {
			return "", fmt.Errorf("failed to parse decide state for resume: %v", err)
		}
		if decideState.NextPhase == "DEVELOP" && decideState.Decision == "iterate" {
			stateJSON, err := updateStateJSON(stateJSON, func(s map[string]any) {
				s["previous_checklist"] = decideState.ReviewChecklist
			})
			if err != nil {
				return "", err
			}
			return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		}
		return a.runFromFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	default:
		return "", fmt.Errorf("unsupported resume phase %q", state.Phase)
	}
}

// maxTransientRetries is the number of consecutive transient failures before
// escalating to needs-review.
const maxTransientRetries = 5

// transientBackoffSchedule maps retry count to backoff duration.
var transientBackoffSchedule = []time.Duration{
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
}

// transientBackoffDuration returns the backoff duration for the given retry count.
func (a *App) transientBackoffDuration(retries int) time.Duration {
	if retries >= len(transientBackoffSchedule) {
		return transientBackoffSchedule[len(transientBackoffSchedule)-1]
	}
	return transientBackoffSchedule[retries]
}

func (a *App) preemptWithRespond(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, nextPhase string) (string, bool, error) {
	var state struct {
		PRNumber int `json:"pr_number"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", false, fmt.Errorf("failed to parse state for respond preemption: %v", err)
	}
	if state.PRNumber == 0 {
		return stateJSON, false, nil
	}

	comments, err := a.findUnprocessedComments(ctx, repo, "pr", state.PRNumber)
	if err != nil {
		return "", false, err
	}
	if len(comments) == 0 {
		return stateJSON, false, nil
	}

	a.logInfo("%s: preempting with RESPOND for PR #%d (%d unprocessed comment(s))", nextPhase, state.PRNumber, len(comments))
	respondState, err := updateStateJSON(stateJSON, func(s map[string]any) {
		s["resume_phase"] = nextPhase
	})
	if err != nil {
		return "", false, err
	}

	respondState, err = a.phaseRespond(ctx, root, env, repo, issueNumber, respondState)
	if err != nil {
		return "", false, err
	}
	return respondState, true, nil
}

func (a *App) runFromDevelop(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	if respondState, interrupted, err := a.preemptWithRespond(ctx, root, env, repo, issueNumber, stateJSON, "DEVELOP"); err != nil {
		return "", err
	} else if interrupted {
		return respondState, nil
	}

	// Check transient backoff gate.
	var backoffState struct {
		TransientRetries    int    `json:"transient_retries"`
		TransientRetryAfter string `json:"transient_retry_after"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &backoffState); err == nil {
		if backoffState.TransientRetryAfter != "" {
			retryAfter, err := time.Parse(time.RFC3339, backoffState.TransientRetryAfter)
			if err == nil && time.Now().Before(retryAfter) {
				a.logInfo("DEVELOP: issue #%d backoff active until %s, skipping", issueNumber, backoffState.TransientRetryAfter)
				stateJSON, err = updateStateJSON(stateJSON, func(state map[string]any) {
					state["waiting"] = true
					state["waiting_reason"] = "transient_backoff"
				})
				if err != nil {
					return "", err
				}
				return stateJSON, nil
			}
		}
	}

	var developResult issueRunnerResult
	var err error
	stateJSON, developResult, err = a.phaseDevelop(ctx, root, env, repo, issueNumber, stateJSON)
	if err != nil {
		return "", err
	}

	// Always create PR so work is visible
	stateJSON, err = a.ensurePRCreated(ctx, root, env, repo, issueNumber, stateJSON, metadata.Title)
	if err != nil {
		return "", err
	}

	if developResult.Status == "transient_error" {
		retries := backoffState.TransientRetries + 1

		// Escalate after max retries.
		if retries >= maxTransientRetries {
			a.logInfo("DEVELOP: issue #%d transient errors exhausted (%d), escalating to needs-review", issueNumber, retries)
			return a.phaseDevelopNeedsReview(ctx, root, env, repo, issueNumber, stateJSON, developResult.Status, developResult.Summary)
		}

		backoff := a.transientBackoffDuration(retries)
		retryAfter := time.Now().Add(backoff).Format(time.RFC3339)
		a.logInfo("DEVELOP: issue #%d transient error (attempt %d/%d), backing off %v", issueNumber, retries, maxTransientRetries, backoff)

		stateJSON, err = updateStateJSON(stateJSON, func(state map[string]any) {
			state["transient_retries"] = retries
			state["transient_retry_after"] = retryAfter
			state["waiting"] = true
			state["waiting_reason"] = "transient_backoff"
		})
		if err != nil {
			return "", err
		}

		// Post diagnostic comment if PR exists.
		var prState struct {
			PRNumber int `json:"pr_number"`
		}
		if json.Unmarshal([]byte(stateJSON), &prState) == nil && prState.PRNumber != 0 {
			commentBody := fmt.Sprintf(
				"Transient codex error (attempt %d/%d): %s\n\nBacking off for %v — next tick will retry automatically.",
				retries, maxTransientRetries, developResult.Summary, backoff)
			if err := a.postAuditCommentWithState(ctx, root, env, repo, prState.PRNumber, "develop-transient", stateJSON, commentBody); err != nil {
				return "", fmt.Errorf("post develop-transient audit comment: %w", err)
			}
		}

		return stateJSON, nil
	}

	// Non-transient result — reset backoff counters.
	if backoffState.TransientRetries > 0 {
		stateJSON, err = updateStateJSON(stateJSON, func(state map[string]any) {
			state["transient_retries"] = 0
			state["transient_retry_after"] = ""
			delete(state, "waiting")
			delete(state, "waiting_reason")
		})
		if err != nil {
			return "", err
		}
	}

	if developResult.Status != "completed" && developResult.Status != "review_ready" {
		return a.phaseDevelopNeedsReview(ctx, root, env, repo, issueNumber, stateJSON, developResult.Status, developResult.Summary)
	}

	// Tick boundary: PR created, next tick runs verify.
	return stateJSON, nil
}

// ensurePRCreated backfills a missing PR for legacy or recovered develop states
// without introducing a separate pseudo-phase in the state machine.
func (a *App) ensurePRCreated(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, title string) (string, error) {
	var state struct {
		Phase    string `json:"phase"`
		PRNumber int    `json:"pr_number"`
		Branch   string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", err
	}
	if state.PRNumber != 0 {
		return stateJSON, nil
	}
	if strings.TrimSpace(state.Branch) == "" {
		return "", errors.New("state is missing branch")
	}

	prResult, err := a.createDraftPR(ctx, repo, state.Branch, issueNumber, title)
	if err != nil {
		return "", fmt.Errorf("create draft PR: %w", err)
	}
	if prResult.Number == 0 {
		return "", errors.New("draft PR creation returned an invalid payload")
	}

	nextState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["pr_number"] = prResult.Number
	})
	if err != nil {
		return "", err
	}

	event := strings.ToLower(strings.TrimSpace(state.Phase))
	if event == "" || event == "respond" || event == "done" || event == "finalize" {
		event = "develop"
	}
	if err := a.postAuditCommentWithState(ctx, root, env, repo, prResult.Number, event, nextState, fmt.Sprintf("PR created. Branch: `%s`", state.Branch)); err != nil {
		return "", fmt.Errorf("post %s audit comment: %w", event, err)
	}

	return nextState, nil
}

func (a *App) runFromVerify(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, _ IssueMetadata) (string, error) {
	if respondState, interrupted, err := a.preemptWithRespond(ctx, root, env, repo, issueNumber, stateJSON, "VERIFY"); err != nil {
		return "", err
	} else if interrupted {
		return respondState, nil
	}
	return a.phaseVerify(ctx, root, env, repo, issueNumber, stateJSON)
}

func (a *App) runFromReview(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	if respondState, interrupted, err := a.preemptWithRespond(ctx, root, env, repo, issueNumber, stateJSON, "REVIEW"); err != nil {
		return "", err
	} else if interrupted {
		return respondState, nil
	}
	// Tick boundary: run review only, return. Next tick will run decide.
	return a.phaseReview(ctx, root, env, repo, issueNumber, stateJSON)
}

func (a *App) runFromDecide(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, _ IssueMetadata) (string, error) {
	if respondState, interrupted, err := a.preemptWithRespond(ctx, root, env, repo, issueNumber, stateJSON, "DECIDE"); err != nil {
		return "", err
	} else if interrupted {
		return respondState, nil
	}

	var err error
	stateJSON, err = a.phaseDecide(ctx, root, env, issueNumber, stateJSON)
	if err != nil {
		return "", err
	}
	var decideState struct {
		PRNumber        int    `json:"pr_number"`
		Decision        string `json:"decision"`
		NextPhase       string `json:"next_phase"`
		ReviewChecklist string `json:"review_checklist"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &decideState); err != nil {
		return "", fmt.Errorf("failed to parse decide state: %v", err)
	}
	if decideState.NextPhase == "DEVELOP" && decideState.Decision == "iterate" {
		stateJSON, err = updateStateJSON(stateJSON, func(s map[string]any) {
			s["previous_checklist"] = decideState.ReviewChecklist
		})
		if err != nil {
			return "", err
		}
	}
	if decideState.PRNumber != 0 {
		body := "Decision recorded."
		switch decideState.Decision {
		case "iterate":
			body = "Decision: iterate. Next round of development will address review feedback."
		case "finalize":
			body = "Decision: finalize. Next tick will perform finalization."
		case "finalize-needs-review":
			body = "Decision: finalize-needs-review. Next tick will hand off for human review."
		}
		if err := a.postAuditCommentWithState(ctx, root, env, repo, decideState.PRNumber, "decide", stateJSON, body); err != nil {
			return "", fmt.Errorf("post decide audit comment: %w", err)
		}
	}
	// Tick boundary: DECIDE never chains into FINALIZE in the same tick.
	return stateJSON, nil
}

func (a *App) runFromFinalize(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	if respondState, interrupted, err := a.preemptWithRespond(ctx, root, env, repo, issueNumber, stateJSON, "FINALIZE"); err != nil {
		return "", err
	} else if interrupted {
		return respondState, nil
	}
	return a.phaseFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
}

func (a *App) getIssueMetadata(ctx context.Context, root string, env []string, repo string, issueNumber int) (IssueMetadata, error) {
	issueOut, err := a.ghOutput(ctx, env, "issue", "view", strconv.Itoa(issueNumber), "--repo", repo, "--json", "number,title,body,labels,url")
	if err != nil {
		return IssueMetadata{}, err
	}

	var issue issueView
	if err := json.Unmarshal([]byte(issueOut), &issue); err != nil {
		return IssueMetadata{}, fmt.Errorf("failed to parse issue metadata: %v", err)
	}

	cfg := a.cfg

	queueOut, err := a.issueQueueApp.ListIssuesDirect(ctx, repo, cfg.ReadyLabel)
	if err != nil {
		queueOut = "[]"
	}

	queueMeta, found := IssueMetadataFromQueue(queueOut, issueNumber)
	if found {
		return queueMeta, nil
	}

	return metadataFromIssueView(issue), nil
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
