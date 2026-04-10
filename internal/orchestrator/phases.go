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

	"github.com/saruman/runoq/internal/claude"
	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
)

func (a *App) phaseInit(ctx context.Context, root string, env []string, repo string, issueNumber int, dryRun bool, title string) (string, error) {
	a.ensureSubApps()
	a.logInfo("INIT: issue #%d", issueNumber)

	eligibility, eligibilityErr := a.dispatchSafetyApp.CheckEligibility(ctx, repo, issueNumber)
	if eligibilityErr != nil {
		return "", fmt.Errorf("eligibility check failed: %v", eligibilityErr)
	}
	if !eligibility.Allowed {
		a.logInfo("INIT: issue #%d is not eligible: %s", issueNumber, strings.Join(eligibility.Reasons, "; "))
		return "", ineligibleIssueError{
			IssueNumber: issueNumber,
			Reasons:     append([]string(nil), eligibility.Reasons...),
		}
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

	if code := a.issueQueueApp.SetStatus(ctx, repo, strconv.Itoa(issueNumber), "in-progress"); code != 0 {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "failed to set issue status to in-progress", branch, "", nil)
	}

	worktreeInfo, worktreeErr := a.worktreeApp.CreateWorktree(ctx, issueNumber, title)
	if worktreeErr != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, fmt.Sprintf("worktree creation failed: %v", worktreeErr), branch, "", nil)
	}
	if strings.TrimSpace(worktreeInfo.Branch) == "" || strings.TrimSpace(worktreeInfo.Worktree) == "" {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "worktree creation returned an invalid payload", branch, "", nil)
	}

	worktree := worktreeInfo.Worktree
	branch = worktreeInfo.Branch
	a.logInfo("INIT: worktree=%s branch=%s", worktree, branch)

	if err := a.configureGitBotRemote(ctx, env, worktree, repo); err == nil {
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

	stateJSON, err := marshalJSON(map[string]any{
		"issue":                issueNumber,
		"phase":                "INIT",
		"branch":               branch,
		"worktree":             worktree,
		"pr_number":            0,
		"round":                0,
		"cumulative_tokens":    0,
		"consecutive_failures": 0,
	})
	if err != nil {
		return "", err
	}

	prResult, prErr := a.createDraftPR(ctx, repo, branch, issueNumber, title)
	if prErr != nil {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, fmt.Sprintf("draft PR creation failed: %v", prErr), branch, worktree, nil)
	}
	if prResult.Number == 0 {
		return "", a.handleInitFailure(ctx, root, env, repo, issueNumber, "draft PR creation returned an invalid payload", branch, worktree, nil)
	}

	stateJSON, err = updateStateJSON(stateJSON, func(state map[string]any) {
		state["pr_number"] = prResult.Number
	})
	if err != nil {
		return "", err
	}

	if err := a.postAuditCommentWithState(ctx, root, env, repo, prResult.Number, "init", stateJSON, fmt.Sprintf("Initialized work on issue #%d. Branch: `%s`", issueNumber, branch)); err != nil {
		return "", fmt.Errorf("post init audit comment: %w", err)
	}

	a.logInfo("INIT: branch=%s worktree=%s pr=#%d", branch, worktree, prResult.Number)
	return stateJSON, nil
}

func (a *App) phaseDevelop(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string) (string, issueRunnerResult, error) {
	a.ensureSubApps()
	a.logInfo("DEVELOP: issue #%d", issueNumber)

	cfg := a.cfg

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
	if strings.TrimSpace(state.Branch) == "" {
		return "", issueRunnerResult{}, errors.New("INIT state is missing branch")
	}
	worktreeResult, err := a.worktreeApp.RehydrateWorktree(ctx, issueNumber, state.Branch)
	if err != nil {
		return "", issueRunnerResult{}, fmt.Errorf("rehydrate worktree: %w", err)
	}
	state.Worktree = worktreeResult.Worktree
	defer a.bestEffortCleanupWorktree(ctx, issueNumber, "DEVELOP")

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

	runnerResult, err := a.issueRunnerApp.RunDevelop(ctx, payloadFile.Name())
	if err != nil {
		return "", issueRunnerResult{}, err
	}

	result := issueRunnerResult{
		Status:               runnerResult.Status,
		LogDir:               runnerResult.LogDir,
		BaselineHash:         runnerResult.BaselineHash,
		HeadHash:             runnerResult.HeadHash,
		CommitRange:          runnerResult.CommitRange,
		ReviewLogPath:        runnerResult.ReviewLogPath,
		SpecRequirements:     runnerResult.SpecRequirements,
		ChangedFiles:         runnerResult.ChangedFiles,
		RelatedFiles:         runnerResult.RelatedFiles,
		CumulativeTokens:     runnerResult.CumulativeTokens,
		VerificationPayload:  runnerResult.VerificationPayload,
		VerificationPassed:   runnerResult.VerificationPassed,
		VerificationFailures: runnerResult.VerificationFailures,
		Caveats:              runnerResult.Caveats,
		Summary:              runnerResult.Summary,
	}

	payloadSchemaValid := false
	if rawValid, ok := result.VerificationPayload["payload_schema_valid"].(bool); ok {
		payloadSchemaValid = rawValid
	}
	payloadSchemaErrors := stringSliceFromAny(result.VerificationPayload["payload_schema_errors"])
	payloadSource := ""
	if rawSource, ok := result.VerificationPayload["payload_source"].(string); ok {
		payloadSource = strings.TrimSpace(rawSource)
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
		state["verification_payload"] = result.VerificationPayload
		state["verification_passed"] = result.VerificationPassed
		state["verification_failures"] = result.VerificationFailures
		state["payload_schema_valid"] = payloadSchemaValid
		state["payload_schema_errors"] = payloadSchemaErrors
		state["payload_source"] = payloadSource
		state["caveats"] = result.Caveats
		state["summary"] = result.Summary
	})
	if err != nil {
		return "", issueRunnerResult{}, err
	}

	developBody := fmt.Sprintf(
		"## Develop - round %d\n\n| Field | Value |\n|-------|-------|\n| **Status** | %s |\n| **Commit range** | `%s` |\n| **Cumulative tokens** | %d |\n| **Payload schema valid** | %s |\n| **Payload source** | %s |\n",
		round, result.Status, result.CommitRange, result.CumulativeTokens, yesNo(payloadSchemaValid), orDefault(payloadSource, "unknown"),
	)
	if strings.TrimSpace(result.Summary) != "" {
		developBody += "\n**Summary**: " + result.Summary + "\n"
	}
	if len(payloadSchemaErrors) > 0 {
		developBody += "\n### Payload Schema Errors\n"
		for _, item := range payloadSchemaErrors {
			developBody += "- " + item + "\n"
		}
	}
	if state.PRNumber != 0 {
		if err := a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "develop", nextState, developBody, "issue-runner"); err != nil {
			return "", issueRunnerResult{}, fmt.Errorf("post develop audit comment: %w", err)
		}
	}

	return nextState, result, nil
}

func (a *App) phaseVerify(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string) (string, error) {
	a.ensureSubApps()
	var state struct {
		PRNumber             int            `json:"pr_number"`
		Round                int            `json:"round"`
		Branch               string         `json:"branch"`
		Worktree             string         `json:"worktree"`
		BaselineHash         string         `json:"baseline_hash"`
		HeadHash             string         `json:"head_hash"`
		VerificationPayload  map[string]any `json:"verification_payload"`
		VerificationPassed   bool           `json:"verification_passed"`
		VerificationFailures []string       `json:"verification_failures"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for verify: %v", err)
	}
	if state.PRNumber == 0 {
		return "", errors.New("DEVELOP state is missing pr_number")
	}
	if strings.TrimSpace(state.Branch) == "" || strings.TrimSpace(state.BaselineHash) == "" {
		return "", errors.New("DEVELOP state is missing branch or baseline_hash")
	}

	a.logInfo("VERIFY: issue #%d round %d", issueNumber, max(state.Round, 1))

	headHash := state.HeadHash
	if len(state.VerificationPayload) > 0 {
		worktreeResult, err := a.worktreeApp.RehydrateWorktree(ctx, issueNumber, state.Branch)
		if err != nil {
			return "", fmt.Errorf("rehydrate worktree: %w", err)
		}
		state.Worktree = worktreeResult.Worktree
		defer a.bestEffortCleanupWorktree(ctx, issueNumber, "VERIFY")

		payloadFile, err := os.CreateTemp("", "runoq-verify-payload.*")
		if err != nil {
			return "", err
		}
		defer func() {
			_ = os.Remove(payloadFile.Name())
		}()
		if err := json.NewEncoder(payloadFile).Encode(state.VerificationPayload); err != nil {
			_ = payloadFile.Close()
			return "", err
		}
		if err := payloadFile.Close(); err != nil {
			return "", err
		}

		verifyResult, err := a.verifyApp.RoundVerify(ctx, state.Worktree, state.Branch, state.BaselineHash, payloadFile.Name())
		if err != nil {
			return "", fmt.Errorf("verify round: %w", err)
		}
		state.VerificationPassed = verifyResult.ReviewAllowed
		state.VerificationFailures = verifyResult.Failures
		changedFiles := changedFilesFromGroundTruth(verifyResult.Actual.FilesChanged, verifyResult.Actual.FilesAdded, verifyResult.Actual.FilesDeleted)
		relatedFiles := a.expandReviewScope(ctx, state.Worktree, changedFiles)
		if resolvedHead, resolveErr := gitops.OpenCLI(ctx, state.Worktree, a.execCommand).ResolveHEAD(); resolveErr == nil {
			headHash = resolvedHead
		}

		stateJSON, err = updateStateJSON(stateJSON, func(s map[string]any) {
			s["changed_files"] = changedFiles
			s["related_files"] = relatedFiles
		})
		if err != nil {
			return "", err
		}
	}

	status := "PASS"
	if !state.VerificationPassed {
		status = "FAIL"
	}

	checklist := ""
	if len(state.VerificationFailures) > 0 {
		items := make([]string, 0, len(state.VerificationFailures))
		for _, failure := range state.VerificationFailures {
			items = append(items, "- "+failure)
		}
		checklist = strings.Join(items, "\n")
	}

	verifyState, err := updateStateJSON(stateJSON, func(s map[string]any) {
		s["phase"] = "VERIFY"
		if strings.TrimSpace(state.Worktree) != "" {
			s["worktree"] = state.Worktree
		}
		s["head_hash"] = headHash
		s["verification_passed"] = state.VerificationPassed
		s["verification_failures"] = state.VerificationFailures
		if !state.VerificationPassed {
			s["verdict"] = "FAIL"
		}
		if strings.TrimSpace(checklist) != "" {
			s["review_checklist"] = checklist
		}
	})
	if err != nil {
		return "", err
	}

	verifyBody := fmt.Sprintf(
		"## Verify - round %d\n\n| Field | Value |\n|-------|-------|\n| **Status** | %s |\n| **Commit range** | `%s..%s` |\n| **Branch** | `%s` |\n",
		max(state.Round, 1), status, truncateHash(state.BaselineHash), truncateHash(headHash), state.Branch,
	)
	if strings.TrimSpace(checklist) != "" {
		verifyBody += "\n### Failures\n" + checklist + "\n"
	}
	if err := a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "verify", verifyState, verifyBody, "verifier"); err != nil {
		return "", fmt.Errorf("post verify audit comment: %w", err)
	}

	return verifyState, nil
}

func (a *App) phaseReview(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string) (string, error) {
	a.ensureSubApps()

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
		ReviewThreadID    string   `json:"review_thread_id"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for review: %v", err)
	}
	if strings.TrimSpace(state.Branch) == "" || state.PRNumber == 0 {
		return "", errors.New("DEVELOP state is missing branch or PR metadata")
	}
	worktreeResult, err := a.worktreeApp.RehydrateWorktree(ctx, issueNumber, state.Branch)
	if err != nil {
		return "", fmt.Errorf("rehydrate worktree: %w", err)
	}
	state.Worktree = worktreeResult.Worktree
	defer a.bestEffortCleanupWorktree(ctx, issueNumber, "REVIEW")

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
	streamResult, err := claude.Stream(ctx, a.execCommand, claude.StreamConfig{
		OutputFile: reviewOutputPath,
		WorkDir:    state.Worktree,
		Args:       []string{"--permission-mode", "bypassPermissions", "--agent", "diff-reviewer", "--add-dir", root, "--", reviewPayload},
		Env:        env,
		Stderr:     &reviewStderr,
	})
	if err != nil {
		a.logInfo("REVIEW: claude_stream error: %v stderr: %s", err, reviewStderr.String())
		return "", err
	}
	if reviewStderr.Len() > 0 {
		a.logInfo("REVIEW: claude_stream stderr: %s", reviewStderr.String())
	}
	if strings.TrimSpace(streamResult.ThreadID) != "" {
		state.ReviewThreadID = streamResult.ThreadID
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

	reviewContractValid := false
	reviewContractErrorsList := reviewContractErrors(verdictResult)
	reviewRepairAttempted := false
	if len(reviewContractErrorsList) > 0 && strings.TrimSpace(state.ReviewThreadID) != "" {
		reviewRepairAttempted = true
		a.logInfo("REVIEW: malformed reviewer output detected (%s); attempting same-thread repair", strings.Join(reviewContractErrorsList, ", "))

		reviewRepairOutputFile, err := os.CreateTemp("", "runoq-review-repair-out.*")
		if err != nil {
			return "", err
		}
		reviewRepairOutputPath := reviewRepairOutputFile.Name()
		defer func() {
			_ = os.Remove(reviewRepairOutputPath)
		}()
		if err := reviewRepairOutputFile.Close(); err != nil {
			return "", err
		}

		var repairStderr bytes.Buffer
		_, err = claude.ResumeStream(ctx, a.execCommand, state.ReviewThreadID, claude.StreamConfig{
			OutputFile: reviewRepairOutputPath,
			WorkDir:    state.Worktree,
			Args:       []string{"--permission-mode", "bypassPermissions", "--add-dir", root, "--", reviewRepairPrompt(reviewContractErrorsList)},
			Env:        env,
			Stderr:     &repairStderr,
		})
		if err != nil {
			a.logInfo("REVIEW: repair resume error: %v stderr: %s", err, repairStderr.String())
			return "", err
		}
		if repairStderr.Len() > 0 {
			a.logInfo("REVIEW: repair resume stderr: %s", repairStderr.String())
		}

		parsed, err := parseReviewVerdict(reviewRepairOutputPath)
		if err != nil {
			return "", err
		}
		verdictResult = parsed
		reviewContractErrorsList = reviewContractErrors(verdictResult)
	}
	reviewContractValid = len(reviewContractErrorsList) == 0

	verdict := strings.TrimSpace(verdictResult.Verdict)
	score := strings.TrimSpace(verdictResult.Score)
	reviewChecklist := verdictResult.Checklist
	if !reviewContractValid {
		verdict = "FAIL"
		score = "0"
		reviewChecklist = invalidReviewChecklist(reviewContractErrorsList)
		repairAttempts := 0
		if reviewRepairAttempted {
			repairAttempts = 1
		}
		a.logError("Reviewer output invalid after %d repair attempt(s): %s", repairAttempts, strings.Join(reviewContractErrorsList, ", "))
	} else {
		if verdict == "" || verdict == "null" {
			verdict = "FAIL"
			a.logError("Could not parse review verdict; treating as FAIL")
		}
		if score == "" || score == "null" {
			score = "0"
		}
	}
	a.logInfo("REVIEW: verdict=%s score=%s", verdict, score)

	cfg := a.cfg
	changedFilesDisplay := "[]"
	if len(changedFiles) > 0 {
		if b, err := json.Marshal(changedFiles); err == nil {
			changedFilesDisplay = string(b)
		}
	}
	reviewBody := fmt.Sprintf("<!-- runoq:agent:diff-reviewer -->\n## Diff Review - round %d / %d\n\n> Posted by `orchestrator` via `diff-reviewer` agent\n\n| Field | Value |\n|-------|-------|\n| **Verdict** | %s |\n| **Score** | %s |\n| **Commit range** | `%s..%s` |\n| **Changed files** | %s |\n", round, cfg.MaxRounds, verdict, score, truncateHash(state.BaselineHash), truncateHash(state.HeadHash), changedFilesDisplay)
	reviewBody += fmt.Sprintf("| **Contract valid** | %s |\n", yesNo(reviewContractValid))
	if strings.TrimSpace(state.ReviewThreadID) != "" {
		reviewBody += fmt.Sprintf("| **Review thread** | `%s` |\n", state.ReviewThreadID)
	}
	if reviewRepairAttempted {
		reviewBody += "| **Repair attempted** | yes |\n"
	}
	if strings.TrimSpace(verdictResult.Scorecard) != "" {
		reviewBody += "\n" + verdictResult.Scorecard + "\n"
	}
	if len(reviewContractErrorsList) > 0 {
		reviewBody += "\n### Contract Errors\n"
		for _, item := range reviewContractErrorsList {
			reviewBody += "- " + item + "\n"
		}
	}
	if strings.TrimSpace(reviewChecklist) != "" {
		reviewBody += "\n### Checklist\n" + reviewChecklist + "\n"
	}
	reviewState, err := updateStateJSON(stateJSON, func(s map[string]any) {
		s["phase"] = "REVIEW"
		s["verdict"] = verdict
		s["score"] = score
		s["review_checklist"] = reviewChecklist
		s["review_thread_id"] = state.ReviewThreadID
		s["review_contract_valid"] = reviewContractValid
		s["review_contract_errors"] = reviewContractErrorsList
	})
	if err != nil {
		return "", err
	}

	if err := a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "review", reviewState, reviewBody); err != nil {
		return "", fmt.Errorf("post review audit comment: %w", err)
	}

	return reviewState, nil
}

func changedFilesFromGroundTruth(changed []string, added []string, deleted []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, paths := range [][]string{changed, added, deleted} {
		for _, path := range paths {
			if strings.TrimSpace(path) == "" || seen[path] {
				continue
			}
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

func (a *App) expandReviewScope(ctx context.Context, worktree string, changedFiles []string) []string {
	if len(changedFiles) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	for _, path := range changedFiles {
		seen[path] = true
	}

	var related []string
	for _, changed := range changedFiles {
		base := filepath.Base(changed)
		ext := filepath.Ext(base)
		nameNoExt := strings.TrimSuffix(base, ext)
		if nameNoExt == "" {
			continue
		}

		out, err := shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
			Name: "rg",
			Args: []string{"-l", "--glob", "*.ts", "--glob", "*.js", "--glob", "*.py", "--glob", "*.go", nameNoExt, worktree},
			Dir:  a.cwd,
			Env:  a.env,
		})
		if err != nil || out == "" {
			continue
		}

		for _, hit := range strings.Split(out, "\n") {
			hit = strings.TrimSpace(hit)
			if hit == "" {
				continue
			}
			rel := strings.TrimPrefix(hit, worktree+"/")
			if strings.HasPrefix(rel, "node_modules/") || strings.HasPrefix(rel, "vendor/") ||
				strings.HasPrefix(rel, "dist/") || strings.HasPrefix(rel, "build/") {
				continue
			}
			if isReviewTestFile(rel) {
				continue
			}
			if !seen[rel] {
				seen[rel] = true
				related = append(related, rel)
			}
		}
	}

	return related
}

func isReviewTestFile(rel string) bool {
	base := filepath.Base(rel)
	for _, pat := range []string{".test.", ".spec.", "_test.", "_spec."} {
		if strings.Contains(base, pat) {
			return true
		}
	}
	for _, prefix := range []string{"test/", "tests/", "__tests__/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func (a *App) bestEffortCleanupWorktree(ctx context.Context, issueNumber int, phase string) {
	if err := a.worktreeApp.RemoveWorktree(ctx, issueNumber); err != nil {
		a.logInfo("%s: best-effort worktree cleanup failed for issue #%d: %v", phase, issueNumber, err)
		return
	}
	a.logInfo("%s: cleaned up disposable worktree for issue #%d", phase, issueNumber)
}

func (a *App) phaseDecide(ctx context.Context, root string, env []string, issueNumber int, stateJSON string) (string, error) {
	var state struct {
		Phase              string `json:"phase"`
		Verdict            string `json:"verdict"`
		Round              int    `json:"round"`
		VerificationPassed bool   `json:"verification_passed"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for decide: %v", err)
	}

	cfg := a.cfg

	verdict := strings.TrimSpace(state.Verdict)
	if verdict == "" {
		verdict = "FAIL"
	}

	a.logInfo("DECIDE: verdict=%s round=%d/%d", verdict, max(state.Round, 1), cfg.MaxRounds)

	decision := "finalize-needs-review"
	nextPhase := "FINALIZE"
	if state.Phase == "VERIFY" && !state.VerificationPassed {
		if state.Round < cfg.MaxRounds {
			decision = "iterate"
			nextPhase = "DEVELOP"
		}
	} else if verdict == "PASS" {
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
	a.ensureSubApps()
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

	cfg := a.cfg

	a.logInfo("FINALIZE: issue #%d decision=%s", issueNumber, defaultString(state.Decision, "finalize-needs-review"))

	finalizeVerdict, issueStatus, finalizeReason := finalizeDecision(state, cfg)
	a.logInfo(
		"FINALIZE: decision table: auto_merge_enabled=%t finalize_verdict=%s finalize_reason=%s issue_status=%s",
		cfg.AutoMergeEnabled,
		finalizeVerdict,
		defaultString(finalizeReason, "none"),
		issueStatus,
	)

	reviewer := ""
	if finalizeVerdict == "needs-review" {
		reviewer = firstReviewer(cfg.Reviewers)
	}

	a.logInfo("FINALIZE: calling pr-lifecycle finalize verdict=%s pr=#%d", finalizeVerdict, state.PRNumber)
	if err := a.finalizePR(ctx, repo, state.PRNumber, finalizeVerdict, reviewer); err != nil {
		a.logInfo("FINALIZE: pr-lifecycle finalize failed")
		return "", fmt.Errorf("pr finalize: %w", err)
	}

	a.logInfo("FINALIZE: setting issue #%d status to %s", issueNumber, issueStatus)
	if code := a.issueQueueApp.SetStatus(ctx, repo, strconv.Itoa(issueNumber), issueStatus); code != 0 {
		a.logInfo("FINALIZE: set-status failed for issue #%d", issueNumber)
		return "", fmt.Errorf("set issue status: issue-queue set-status exited %d", code)
	}
	a.logInfo("FINALIZE: set-status succeeded for issue #%d", issueNumber)

	if finalizeVerdict == "auto-merge" {
		a.logInfo("FINALIZE: removing worktree for issue #%d (auto-merged)", issueNumber)
		if err := a.worktreeApp.RemoveWorktree(ctx, issueNumber); err == nil {
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
		cfg.AutoMergeEnabled,
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

	if err := a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "finalize", finalizeState, finalizeBody); err != nil {
		return "", fmt.Errorf("post finalize audit comment: %w", err)
	}

	if err := a.updatePRBody(ctx, env, repo, state.PRNumber, state.Summary, defaultString(strings.TrimSpace(state.Verdict), "FAIL"), defaultString(strings.TrimSpace(state.Score), "0"), max(state.Round, 1), cfg.MaxRounds, state.Caveats); err != nil {
		a.logInfo("FINALIZE: PR body update failed: %v", err)
		return "", fmt.Errorf("update PR body: %w", err)
	}

	doneState, err := updateStateJSON(finalizeState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	return doneState, nil
}

func (a *App) phaseDevelopNeedsReview(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, developStatus string, developSummary string) (string, error) {
	a.ensureSubApps()
	a.logInfo("DEVELOP: issue #%d requires deterministic needs-review handoff", issueNumber)

	var state struct {
		PRNumber int `json:"pr_number"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for needs-review handoff: %v", err)
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

	cfg := a.cfg
	reviewer := firstReviewer(cfg.Reviewers)
	if state.PRNumber != 0 {
		if err := a.finalizePR(ctx, repo, state.PRNumber, "needs-review", reviewer); err != nil {
			return "", err
		}
	}
	if code := a.issueQueueApp.SetStatus(ctx, repo, strconv.Itoa(issueNumber), "needs-review"); code != 0 {
		return "", fmt.Errorf("failed to set issue #%d status to needs-review", issueNumber)
	}

	finalizeState, err := updateStateJSON(decideState, func(state map[string]any) {
		state["phase"] = "FINALIZE"
		state["finalize_verdict"] = "needs-review"
		state["issue_status"] = "needs-review"
	})
	if err != nil {
		return "", err
	}

	if state.PRNumber != 0 {
		body := fmt.Sprintf(
			"## Needs review — issue #%d\n\n| Field | Value |\n|-------|-------|\n| **Develop status** | `%s` |\n| **Reason** | %s |\n",
			issueNumber, developStatus, developSummary)
		if err := a.postAuditCommentWithState(ctx, root, env, repo, state.PRNumber, "finalize", finalizeState, body); err != nil {
			return "", fmt.Errorf("post finalize audit comment: %w", err)
		}
	}

	doneState, err := updateStateJSON(finalizeState, func(state map[string]any) {
		state["phase"] = "DONE"
	})
	if err != nil {
		return "", err
	}
	return doneState, nil
}

// phaseRespond scans for unprocessed comments on the PR and posts acknowledgment
// replies. Each comment receives a response and a +1 reaction (the "processed" marker).
// This is the entry point for the conversation loop — agents address feedback here.
func (a *App) phaseRespond(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string) (string, error) {
	a.ensureSubApps()

	var state struct {
		PRNumber int `json:"pr_number"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse state for respond: %v", err)
	}
	if state.PRNumber == 0 {
		a.logInfo("RESPOND: no PR for issue #%d, skipping", issueNumber)
		return stateJSON, nil
	}

	a.logInfo("RESPOND: checking for unprocessed comments on PR #%d", state.PRNumber)

	comments, err := a.findUnprocessedComments(ctx, repo, "pr", state.PRNumber)
	if err != nil {
		return "", fmt.Errorf("RESPOND: failed to find comments: %v", err)
	}

	if len(comments) == 0 {
		a.logInfo("RESPOND: no unprocessed comments on PR #%d", state.PRNumber)
		return stateJSON, nil
	}

	a.logInfo("RESPOND: found %d unprocessed comment(s) on PR #%d", len(comments), state.PRNumber)

	handledComments := 0
	var processingErrors []error

	// Post acknowledgment reply for each unprocessed comment and mark processed via +1 reaction
	for _, comment := range comments {
		reply := fmt.Sprintf("Acknowledged feedback from %s. This will be addressed in the next development round.", comment.CommenterIdentity)
		if err := a.commentPR(ctx, repo, state.PRNumber, fmt.Sprintf("<!-- runoq:agent:codex -->\n> Re: comment by @%s\n\n%s", comment.Author, reply)); err != nil {
			a.logInfo("RESPOND: failed to post reply for comment %d: %v", comment.ID, err)
			processingErrors = append(processingErrors, fmt.Errorf("reply to comment %d: %w", comment.ID, err))
			continue
		}

		// Mark as processed with +1 reaction
		reactionEndpoint := fmt.Sprintf("repos/%s/issues/comments/%d/reactions", repo, comment.ID)
		if _, err := a.ghOutput(ctx, env, "api", reactionEndpoint, "-f", "content=+1", "--method", "POST"); err != nil {
			a.logInfo("RESPOND: failed to add +1 reaction to comment %d: %v", comment.ID, err)
			processingErrors = append(processingErrors, fmt.Errorf("mark comment %d processed: %w", comment.ID, err))
			continue
		}

		a.logInfo("RESPOND: replied to comment %d by %s", comment.ID, comment.Author)
		handledComments++
	}

	if len(processingErrors) > 0 {
		return "", fmt.Errorf("RESPOND: failed to process %d comment(s): %w", len(processingErrors), errors.Join(processingErrors...))
	}

	nextState, err := updateStateJSON(stateJSON, func(state map[string]any) {
		state["phase"] = "RESPOND"
		state["responded_comments"] = handledComments
	})
	if err != nil {
		return "", err
	}

	return nextState, nil
}

func (a *App) handleInitFailure(ctx context.Context, root string, env []string, repo string, issueNumber int, reason string, branch string, worktree string, prNumber *int) error {
	a.ensureSubApps()
	a.logError("INIT: %s", reason)

	if prNumber == nil {
		_ = a.issueQueueApp.SetStatus(ctx, repo, strconv.Itoa(issueNumber), "ready")
		if strings.TrimSpace(worktree) != "" {
			_ = a.worktreeApp.RemoveWorktree(ctx, issueNumber)
		}
	}

	return errors.New(reason)
}
