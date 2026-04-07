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
		if dryRun {
			return a.runQueueDryRun(ctx, root, env, repo)
		}
		return a.runQueue(ctx, root, env, repo)
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
		return env
	}

	a.configureGitBotIdentity(ctx, root, env, targetRoot)
	a.configureGitBotRemote(ctx, root, env, targetRoot, repo)

	return env
}

// RunQueue performs auth/identity setup and processes the issue queue.
// Unlike runCommandEntry, it does NOT run dispatch-safety reconciliation
// (the caller is expected to handle that separately).
func (a *App) RunQueue(ctx context.Context, repo string) int {
	root := a.runoqRoot()
	if root == "" {
		return shell.Fail(a.stderr, "Unable to resolve RUNOQ_ROOT for RunQueue.")
	}

	env := append([]string(nil), a.env...)
	if authEnv := a.prepareAuth(ctx, root, env); authEnv != nil {
		env = authEnv
	}

	targetRoot, err := a.targetRoot(ctx, env)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	a.configureGitBotIdentity(ctx, root, env, targetRoot)
	a.configureGitBotRemote(ctx, root, env, targetRoot, repo)

	return a.runQueue(ctx, root, env, repo)
}

func (a *App) runQueue(ctx context.Context, root string, env []string, repo string) int {
	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	queueEnv := append([]string(nil), env...)
	queueEnv = shell.EnvSet(queueEnv, "RUNOQ_LOG", "1")
	queueEnv = shell.EnvSet(queueEnv, "RUNOQ_NO_AUTO_TOKEN", "1")

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
			return shell.Failf(a.stderr, "gh-issue-queue.sh next returned invalid JSON: %v", err)
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
		return shell.Fail(a.stderr, err.Error())
	}
	return 0
}

func (a *App) runQueueDryRun(ctx context.Context, root string, env []string, repo string) int {
	cfg, err := a.loadConfig(root, env)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	queueEnv := append([]string(nil), env...)
	queueEnv = shell.EnvSet(queueEnv, "RUNOQ_LOG", "1")
	queueEnv = shell.EnvSet(queueEnv, "RUNOQ_NO_AUTO_TOKEN", "1")
	queueOut, queueStderr, err := a.scriptOutputWithStderr(ctx, root, queueEnv, "gh-issue-queue.sh", []string{"next", repo, cfg.Labels.Ready}, nil)
	if strings.TrimSpace(queueStderr) != "" {
		_, _ = fmt.Fprintln(a.stderr, queueStderr)
	}
	if err != nil {
		return commandExitCode(err)
	}

	var selection queueSelectionResult
	if err := json.Unmarshal([]byte(queueOut), &selection); err != nil {
		return shell.Failf(a.stderr, "gh-issue-queue.sh next returned invalid JSON: %v", err)
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
