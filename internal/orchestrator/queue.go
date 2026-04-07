package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/saruman/runoq/internal/shell"
)

func (a *App) mentionTriageEntry(ctx context.Context, root string, env []string, args []string) int {
	if len(args) != 2 {
		a.printUsage(a.stderr)
		return 1
	}

	repo := args[0]
	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	var stdout bytes.Buffer
	if err := a.runScript(ctx, root, env, "gh-pr-lifecycle.sh", []string{"poll-mentions", repo, cfg.Identity.Handle}, nil, &stdout, a.stderr); err != nil {
		return commandExitCode(err)
	}

	var mentions []json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &mentions); err != nil {
		return shell.Failf(a.stderr, "poll-mentions returned invalid JSON: %v", err)
	}
	if len(mentions) == 0 {
		return 0
	}

	return shell.Fail(a.stderr, "mention-triage with mentions not implemented")
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
	reconcileEnv = shell.EnvSet(reconcileEnv, "RUNOQ_NO_AUTO_TOKEN", "1")
	_ = a.runScript(ctx, root, reconcileEnv, "dispatch-safety.sh", []string{"reconcile", repo}, nil, io.Discard, io.Discard)

	if issueNumber == "" {
		return shell.Fail(a.stderr, "--issue is required. Use 'runoq tick' for queue processing.")
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

	if err := a.configureGitBotIdentity(ctx, root, env, targetRoot); err != nil {
		a.logInfo("Setup: bot identity configuration failed or skipped")
	}
	if err := a.configureGitBotRemote(ctx, root, env, targetRoot, repo); err != nil {
		a.logInfo("Setup: bot remote configuration failed or skipped")
	}

	return env
}

// RunIssue runs a single issue through the phase machine (INIT→CRITERIA→DEVELOP→REVIEW→DECIDE→FINALIZE).
// The caller provides metadata so no additional API call is needed for issue details.
func (a *App) RunIssue(ctx context.Context, repo string, issueNumber int, dryRun bool, title string, metadata IssueMetadata) (string, error) {
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
	return a.runIssueWithEnv(ctx, root, env, repo, issueNumber, dryRun, title, metadata)
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

	return a.runFromCriteria(ctx, root, env, repo, issueNumber, stateJSON, metadata)
}

func (a *App) resumeFromState(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	var state struct {
		Phase    string `json:"phase"`
		PRNumber int    `json:"pr_number"`
	}
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", fmt.Errorf("failed to parse derived state: %v", err)
	}
	a.logInfo("RESUME: issue #%d from phase %s (PR #%d)", issueNumber, state.Phase, state.PRNumber)

	switch state.Phase {
	case "DONE", "FINALIZE":
		a.logInfo("RESUME: issue #%d already at terminal phase %s", issueNumber, state.Phase)
		return stateJSON, nil
	case "INIT":
		return a.runFromCriteria(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "CRITERIA":
		return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	case "DEVELOP":
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
		return a.phaseFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	default:
		a.logInfo("RESUME: unknown phase %q, starting from criteria", state.Phase)
		return a.runFromCriteria(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	}
}

func (a *App) runFromCriteria(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	var err error
	stateJSON, err = a.phaseCriteria(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	if err != nil {
		return "", err
	}
	if metadata.EstimatedComplexity != "low" {
		return a.phaseCriteriaNeedsReviewHandoff(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	}
	return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
}

func (a *App) runFromDevelop(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	for {
		var developResult issueRunnerResult
		var err error
		stateJSON, developResult, err = a.phaseDevelop(ctx, root, env, repo, issueNumber, stateJSON)
		if err != nil {
			return "", err
		}

		if developResult.Status != "review_ready" {
			return a.phaseDevelopNeedsReview(ctx, root, env, repo, issueNumber, stateJSON)
		}

		stateJSON, err = a.runFromReviewLoop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
		if err != nil {
			return "", err
		}

		var decideState struct {
			Phase           string `json:"phase"`
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
			continue
		}

		return a.phaseFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	}
}

func (a *App) runFromReview(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	stateJSON, err := a.runFromReviewLoop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	if err != nil {
		return "", err
	}
	return a.phaseFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
}

func (a *App) runFromDecide(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	var err error
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
		stateJSON, err = updateStateJSON(stateJSON, func(s map[string]any) {
			s["previous_checklist"] = decideState.ReviewChecklist
		})
		if err != nil {
			return "", err
		}
		return a.runFromDevelop(ctx, root, env, repo, issueNumber, stateJSON, metadata)
	}
	return a.phaseFinalize(ctx, root, env, repo, issueNumber, stateJSON, metadata)
}

// runFromReviewLoop runs review + decide phases (single pass, no loop).
func (a *App) runFromReviewLoop(ctx context.Context, root string, env []string, repo string, issueNumber int, stateJSON string, metadata IssueMetadata) (string, error) {
	var err error
	stateJSON, err = a.phaseReview(ctx, root, env, repo, issueNumber, stateJSON)
	if err != nil {
		return "", err
	}
	stateJSON, err = a.phaseDecide(ctx, root, env, issueNumber, stateJSON)
	if err != nil {
		return "", err
	}
	return stateJSON, nil
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

	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return IssueMetadata{}, err
	}

	queueOut, err := a.scriptOutput(ctx, root, env, "gh-issue-queue.sh", []string{"list", repo, cfg.Labels.Ready}, nil)
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

func (a *App) loadConfig(root string, env []string) (queueConfig, error) {
	configPath, ok := shell.EnvLookup(env, "RUNOQ_CONFIG")
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
