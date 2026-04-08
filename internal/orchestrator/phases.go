package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
)

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
			doneState, err := updateStateJSON(integrateState, func(state map[string]any) {
				state["phase"] = "DONE"
			})
			if err != nil {
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
		failedState, err := updateStateJSON(integrateState, func(state map[string]any) {
			state["phase"] = "FAILED"
		})
		if err != nil {
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
	doneState, err := updateStateJSON(integrateState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	return doneState, nil
}

func (a *App) phaseInit(ctx context.Context, root string, env []string, repo string, issueNumber int, dryRun bool, title string) (string, error) {
	a.logInfo("INIT: issue #%d", issueNumber)

	dispatchEnv := append([]string(nil), env...)
	dispatchEnv = shell.EnvSet(dispatchEnv, "RUNOQ_NO_AUTO_TOKEN", "1")
	eligibilityOut, eligibilityStderr, eligibilityErr := a.scriptOutputWithStderr(ctx, root, dispatchEnv, "dispatch-safety.sh", []string{"eligibility", repo, strconv.Itoa(issueNumber)}, nil)
	if eligibilityErr != nil {
		return "", fmt.Errorf("eligibility check failed: %s", stderrOrUnknown(eligibilityStderr))
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

	wtRepo := gitops.OpenCLI(ctx, worktree, a.execCommand)
	if err := wtRepo.CommitEmpty(worktree, fmt.Sprintf("runoq: begin work on #%d", issueNumber)); err != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "failed to create the initial worktree commit", branch, worktree, nil)
	}

	if err := wtRepo.Push(worktree, "origin", branch); err != nil {
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


	_ = a.postAuditCommentWithState(ctx, root, env, repo, prNumber, "init", stateJSON, fmt.Sprintf("Orchestrator initialized. Branch: `%s`", branch))
	return stateJSON, nil
}

func (a *App) phaseCriteria(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
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
	return nextState, nil
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

	specDir := filepath.Join(state.Worktree, ".runoq")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		return "", issueRunnerResult{}, err
	}
	specFile, err := os.CreateTemp(specDir, "spec-*")
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
		"guidelines":     []string{},
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

	var runnerStdout bytes.Buffer
	err = a.runScript(ctx, root, env, "issue-runner.sh", []string{"run", payloadFile.Name()}, nil, &runnerStdout, a.stderr)
	runnerOut := strings.TrimSpace(runnerStdout.String())
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

	developBody := fmt.Sprintf(
		"## Develop - round %d\n\n| Field | Value |\n|-------|-------|\n| **Status** | %s |\n| **Commit range** | `%s` |\n| **Cumulative tokens** | %d |\n| **Verification** | %s |\n",
		round, result.Status, result.CommitRange, result.CumulativeTokens, yesNo(result.VerificationPassed),
	)
	if strings.TrimSpace(result.Summary) != "" {
		developBody += "\n**Summary**: " + result.Summary + "\n"
	}
	_ = a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "develop", nextState, developBody)

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

	changedFiles := state.ChangedFiles
	if changedFiles == nil {
		changedFiles = []string{}
	}
	relatedFiles := state.RelatedFiles
	if relatedFiles == nil {
		relatedFiles = []string{}
	}

	reviewPayload, err := marshalJSON(map[string]any{
		"issueNumber":       issueNumber,
		"round":             round,
		"worktree":          state.Worktree,
		"baselineHash":      state.BaselineHash,
		"headHash":          state.HeadHash,
		"reviewLogPath":     state.ReviewLogPath,
		"specRequirements":  state.SpecRequirements,
		"guidelines":        []string{},
		"changedFiles":      changedFiles,
		"relatedFiles":      relatedFiles,
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

	var reviewStderr bytes.Buffer
	if err := a.runProgram(ctx, env, "bash", []string{
		"-lc",
		`source "$1"; runoq::claude_stream "$2" --permission-mode bypassPermissions --agent diff-reviewer --add-dir "$3" -- "$4"`,
		"bash",
		filepath.Join(root, "scripts", "lib", "common.sh"),
		reviewOutputPath,
		root,
		reviewPayload,
	}, nil, io.Discard, &reviewStderr); err != nil {
		a.logInfo("REVIEW: claude_stream error: %v stderr: %s", err, reviewStderr.String())
		return "", err
	}
	if reviewStderr.Len() > 0 {
		a.logInfo("REVIEW: claude_stream stderr: %s", reviewStderr.String())
	}

	reviewLogAbs := strings.TrimSpace(state.ReviewLogPath)
	if reviewLogAbs != "" && !filepath.IsAbs(reviewLogAbs) {
		reviewLogAbs = filepath.Join(state.Worktree, reviewLogAbs)
	}
	reviewLogExists := shell.FileExists(reviewLogAbs)
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
	changedFilesDisplay := "[]"
	if len(changedFiles) > 0 {
		if b, err := json.Marshal(changedFiles); err == nil {
			changedFilesDisplay = string(b)
		}
	}
	reviewBody := fmt.Sprintf("## Diff Review - round %d / %d\n\n> Posted by `orchestrator` via `diff-reviewer` agent\n\n| Field | Value |\n|-------|-------|\n| **Verdict** | %s |\n| **Score** | %s |\n| **Commit range** | `%s..%s` |\n| **Changed files** | %s |\n", round, cfg.MaxRounds, verdict, score, truncateHash(state.BaselineHash), truncateHash(state.HeadHash), changedFilesDisplay)
	if strings.TrimSpace(reviewChecklist) != "" {
		reviewBody += "\n### Checklist\n" + reviewChecklist + "\n"
	}
	reviewState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "REVIEW"
		state["verdict"] = verdict
		state["score"] = score
		state["review_checklist"] = reviewChecklist
	})
	if err != nil {
		return "", err
	}

	_ = a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "review", reviewState, reviewBody)


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
	return decideState, nil
}

func (a *App) phaseFinalize(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	var state struct {
		PRNumber int      `json:"pr_number"`
		Worktree string   `json:"worktree"`
		Verdict  string   `json:"verdict"`
		Decision string   `json:"decision"`
		Score    string   `json:"score"`
		Round    int      `json:"round"`
		Caveats  []string `json:"caveats"`
		Summary  string   `json:"summary"`
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

	a.logInfo("FINALIZE: issue #%d decision=%s", issueNumber, defaultString(state.Decision, "finalize-needs-review"))

	finalizeVerdict, issueStatus, finalizeReason, _ := finalizeDecision(state, cfg)
	a.logInfo(
		"FINALIZE: decision table: auto_merge_enabled=%t finalize_verdict=%s finalize_reason=%s issue_status=%s",
		cfg.AutoMerge.Enabled,
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
		"## Finalize - issue #%d\n\n| Field | Value |\n|-------|-------|\n| **Decision** | `%s` |\n| **Issue status** | `%s` |\n| **Review verdict** | %s |\n| **Review score** | %s |\n| **Auto-merge enabled** | %t |\n| **Round** | %d / %d |\n",
		issueNumber,
		finalizeVerdict,
		issueStatus,
		defaultString(strings.TrimSpace(state.Verdict), "FAIL"),
		defaultString(strings.TrimSpace(state.Score), "0"),
		cfg.AutoMerge.Enabled,
		max(state.Round, 1),
		cfg.MaxRounds,
	)
	if strings.TrimSpace(finalizeReason) != "" {
		finalizeBody += "\n**Reason**: " + finalizeReason + "\n"
	}
	if len(state.Caveats) > 0 {
		finalizeBody += "\n**Caveats**: " + strings.Join(state.Caveats, ", ") + "\n"
	}
	finalizeState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "FINALIZE"
		state["finalize_verdict"] = finalizeVerdict
		state["issue_status"] = issueStatus
	})
	if err != nil {
		return "", err
	}

	_ = a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "finalize", finalizeState, finalizeBody)

	if err := a.updatePRBody(ctx, env, repo, state.PRNumber, state.Summary, defaultString(strings.TrimSpace(state.Verdict), "FAIL"), defaultString(strings.TrimSpace(state.Score), "0"), max(state.Round, 1), cfg.MaxRounds, state.Caveats); err != nil {
		a.logInfo("FINALIZE: PR body update failed: %v", err)
	}


	doneState, err := updateStateJSON(finalizeState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
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

	decideState, err := updateStateJSON(reviewState, func(state map[string]any) {
		state["phase"] = "DECIDE"
		state["decision"] = "finalize-needs-review"
		state["next_phase"] = "FINALIZE"
	})
	if err != nil {
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

	doneState, err := updateStateJSON(finalizeState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	return doneState, nil
}

func (a *App) handleInitFailure(ctx context.Context, root string, env []string, repo string, issueNumber int, reason string, branch string, worktree string, prNumber *int) error {
	a.logError("INIT: %s", reason)

	if prNumber == nil {
		_ = a.runScript(ctx, root, env, "gh-issue-queue.sh", []string{"set-status", repo, strconv.Itoa(issueNumber), "ready"}, nil, io.Discard, io.Discard)
		if strings.TrimSpace(worktree) != "" {
			_ = a.runScript(ctx, root, env, "worktree.sh", []string{"remove", strconv.Itoa(issueNumber)}, nil, io.Discard, io.Discard)
		}
	}

	return errors.New(reason)
}

