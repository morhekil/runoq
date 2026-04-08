package dispatchsafety

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
)

const usageText = `Usage:
  dispatch-safety.sh reconcile <repo>
  dispatch-safety.sh eligibility <repo> <issue-number>
`

var numericBasenamePattern = regexp.MustCompile(`^[0-9]+$`)

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	execCommand shell.CommandExecutor
	cfgCache    *config // non-nil when injected by caller
}

type contractError struct {
	message string
}

func (e contractError) Error() string {
	return e.message
}

type config struct {
	Labels struct {
		Ready       string `json:"ready"`
		InProgress  string `json:"inProgress"`
		Done        string `json:"done"`
		NeedsReview string `json:"needsReview"`
		Blocked     string `json:"blocked"`
	} `json:"labels"`
	BranchPrefix   string `json:"branchPrefix"`
	WorktreePrefix string `json:"worktreePrefix"`
}

type reconcileAction struct {
	Issue    int     `json:"issue"`
	PRNumber *int    `json:"pr_number,omitempty"`
	Action   string  `json:"action"`
	Phase    *string `json:"phase,omitempty"`
	Round    *int    `json:"round,omitempty"`
}

// EligibilityResult holds the result of an eligibility check.
type EligibilityResult struct {
	Allowed bool     `json:"allowed"`
	Issue   int      `json:"issue"`
	Branch  string   `json:"branch"`
	Reasons []string `json:"reasons"`
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:        append([]string(nil), args...),
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      stdout,
		stderr:      stderr,
		execCommand: shell.RunCommand,
	}
}

func (a *App) SetCommandExecutor(execFn shell.CommandExecutor) {
	if execFn == nil {
		a.execCommand = shell.RunCommand
		return
	}
	a.execCommand = execFn
}

// DispatchConfig holds label and naming config for injection by callers.
type DispatchConfig struct {
	ReadyLabel      string
	InProgressLabel string
	DoneLabel       string
	NeedsReview     string
	Blocked         string
	BranchPrefix    string
	WorktreePrefix  string
}

// SetConfig injects config so the app skips reading from disk.
// Used by callers that already loaded the config (e.g. the tick runner).
func (a *App) SetConfig(dc DispatchConfig) {
	a.cfgCache = &config{
		Labels: struct {
			Ready       string `json:"ready"`
			InProgress  string `json:"inProgress"`
			Done        string `json:"done"`
			NeedsReview string `json:"needsReview"`
			Blocked     string `json:"blocked"`
		}{
			Ready:       dc.ReadyLabel,
			InProgress:  dc.InProgressLabel,
			Done:        dc.DoneLabel,
			NeedsReview: dc.NeedsReview,
			Blocked:     dc.Blocked,
		},
		BranchPrefix:   dc.BranchPrefix,
		WorktreePrefix: dc.WorktreePrefix,
	}
}

func (a *App) Run(ctx context.Context) int {
	subcommand := ""
	if len(a.args) > 0 {
		subcommand = a.args[0]
	}

	switch subcommand {
	case "reconcile":
		if len(a.args) != 2 {
			a.printUsage()
			return 1
		}
		return a.Reconcile(ctx, a.args[1])
	case "eligibility":
		if len(a.args) != 3 {
			a.printUsage()
			return 1
		}
		return a.runEligibility(ctx, a.args[1], a.args[2])
	default:
		a.printUsage()
		return 1
	}
}

// Reconcile checks for stale in-progress labels by querying GitHub directly.
// Issues with in-progress label but no linked PR are reset to ready.
// Issues with a linked PR are left for the tick to resume.
func (a *App) Reconcile(ctx context.Context, repo string) int {
	cfg, err := a.loadConfig()
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
	}

	output, err := a.ghOutput(
		ctx,
		"issue", "list",
		"--repo", repo,
		"--label", cfg.Labels.InProgress,
		"--state", "open",
		"--limit", "200",
		"--json", "number,title,labels",
	)
	if err != nil {
		if isExitError(err) {
			return 1
		}
		return shell.Failf(a.stderr, "%v", err)
	}

	var issues []map[string]any
	if err := json.Unmarshal([]byte(output), &issues); err != nil {
		return shell.Failf(a.stderr, "failed to parse in-progress issues: %v", err)
	}

	actions := make([]reconcileAction, 0, len(issues))
	for _, issue := range issues {
		issueNumber, ok := intValue(issue["number"])
		if !ok {
			continue
		}

		hasLinkedPR, prErr := a.hasLinkedPR(ctx, repo, issueNumber)
		if prErr != nil && !isExitError(prErr) {
			return shell.Failf(a.stderr, "%v", prErr)
		}
		if hasLinkedPR {
			continue
		}

		if err := a.setIssueStatus(ctx, repo, issueNumber, "ready"); err != nil {
			if isExitError(err) {
				return 1
			}
			return shell.Failf(a.stderr, "%v", err)
		}
		message := "Found stale runoq:in-progress label with no linked PR. Reset to runoq:ready."
		if err := a.issueComment(ctx, repo, issueNumber, message); err != nil {
			if isExitError(err) {
				return 1
			}
			return shell.Failf(a.stderr, "%v", err)
		}

		actions = append(actions, reconcileAction{
			Issue:  issueNumber,
			Action: "reset-ready",
		})
	}

	return shell.WriteJSON(a.stdout, a.stderr, actions)
}

func (a *App) hasLinkedPR(ctx context.Context, repo string, issueNumber int) (bool, error) {
	output, err := a.ghOutput(ctx, "pr", "list", "--repo", repo, "--search", fmt.Sprintf("closes #%d", issueNumber), "--json", "number")
	if err != nil {
		return false, err
	}
	var prs []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal([]byte(output), &prs); err != nil {
		return false, err
	}
	return len(prs) > 0, nil
}

func (a *App) runEligibility(ctx context.Context, repo string, issueArg string) int {
	issueOutput, err := a.ghOutput(ctx, "issue", "view", issueArg, "--repo", repo, "--json", "number,title,body,labels,url")
	if err != nil {
		if isExitError(err) {
			return 1
		}
		return shell.Failf(a.stderr, "%v", err)
	}

	var issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []labelEntry `json:"labels"`
	}
	if err := json.Unmarshal([]byte(issueOutput), &issue); err != nil {
		return shell.Failf(a.stderr, "failed to parse issue metadata: %v", err)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
	}

	planning := hasLabelNamed(issue.Labels, "runoq:planning") || hasLabelNamed(issue.Labels, "runoq:adjustment")
	branch := ""
	if !planning {
		branch = branchName(cfg.BranchPrefix, issue.Number, issue.Title)
	}

	reasons := make([]string, 0, 4)
	if strings.TrimSpace(issue.Title) == "" || (!planning && !hasAcceptanceCriteria(issue.Body)) {
		reasons = append(reasons, "missing acceptance criteria")
	}

	blockedByList := a.fetchBlockedBy(ctx, repo, issue.Number)
	for _, dep := range blockedByList {
		reason, blocked, err := a.dependencyReason(ctx, repo, strconv.Itoa(dep), cfg.Labels.Done)
		if err != nil {
			if isExitError(err) {
				return 1
			}
			return shell.Failf(a.stderr, "%v", err)
		}
		if blocked {
			reasons = append(reasons, reason)
		}
	}

	if !planning {
		openPRReason, err := a.openPRReason(ctx, repo, branch)
		if err != nil {
			if isExitError(err) {
				return 1
			}
			return shell.Failf(a.stderr, "%v", err)
		}
		if openPRReason != "" {
			reasons = append(reasons, openPRReason)
		}

		hasConflicts, err := a.branchHasConflicts(ctx, branch)
		if err != nil {
			if isExitError(err) {
				return 1
			}
			return shell.Failf(a.stderr, "%v", err)
		}
		if hasConflicts {
			reasons = append(reasons, "branch "+branch+" has unresolved conflicts with origin/main")
		}
	}

	result := EligibilityResult{
		Allowed: len(reasons) == 0,
		Issue:   issue.Number,
		Branch:  branch,
		Reasons: reasons,
	}

	if len(reasons) == 0 {
		return shell.WriteJSON(a.stdout, a.stderr, result)
	}

	message := "Skipped: " + strings.Join(reasons, "; ") + "."
	if err := a.issueComment(ctx, repo, issue.Number, message); err != nil {
		if isExitError(err) {
			return 1
		}
		return shell.Failf(a.stderr, "%v", err)
	}
	if code := shell.WriteJSON(a.stdout, a.stderr, result); code != 0 {
		return code
	}
	return 1
}

func (a *App) reconcileStateFile(ctx context.Context, repo string, file string) (reconcileAction, bool, error) {
	state, ok, err := loadJSONMap(file)
	if err != nil {
		return reconcileAction{}, false, err
	}
	if !ok {
		return reconcileAction{}, false, nil
	}

	phase := rawStringOr(state["phase"], "")
	if phase == "DONE" {
		return reconcileAction{}, false, nil
	}
	if phase == "FAILED" {
		issueNumber, _ := intValue(state["issue"])
		a.cleanupIssueArtifacts(ctx, repo, issueNumber)
		_ = os.Remove(file)
		return reconcileAction{
			Issue:  issueNumber,
			Action: "cleanup-failed",
		}, true, nil
	}

	issueNumber, ok := intValue(state["issue"])
	if !ok {
		return reconcileAction{}, false, fmt.Errorf("state file %s is missing a numeric issue", file)
	}
	round, _ := intValue(state["round"])
	branch := rawStringOr(state["branch"], "")
	prNumber := rawStringOr(state["pr_number"], "")
	updatedAt := rawStringOr(state["updated_at"], "unknown")

	resumedPR, hasResumedPR, err := a.resolveOpenPRNumber(ctx, repo, prNumber, branch)
	if err != nil {
		if !isExitError(err) {
			return reconcileAction{}, false, err
		}
	}

	if hasResumedPR {
		pushed, branchErr := a.branchIsPushed(ctx, branch)
		if branchErr != nil {
			return reconcileAction{}, false, branchErr
		}
		if pushed {
			message := fmt.Sprintf(
				"Detected interrupted run from %s. Previous phase: %s round %d. Resuming.",
				updatedAt,
				phase,
				round,
			)
			if err := a.issueComment(ctx, repo, issueNumber, message); err != nil {
				return reconcileAction{}, false, err
			}
			if err := a.prComment(ctx, repo, resumedPR, message); err != nil {
				return reconcileAction{}, false, err
			}
			return reconcileAction{
				Issue:    issueNumber,
				PRNumber: intPtr(resumedPR),
				Action:   "resume",
				Phase:    stringPtr(phase),
				Round:    intPtr(round),
			}, true, nil
		}
	}

	message := fmt.Sprintf(
		"Detected interrupted run from %s. Previous phase: %s round %d. Marking for human review.",
		updatedAt,
		phase,
		round,
	)
	if err := a.setIssueStatus(ctx, repo, issueNumber, "needs-review"); err != nil {
		return reconcileAction{}, false, err
	}
	if err := a.issueComment(ctx, repo, issueNumber, message); err != nil {
		return reconcileAction{}, false, err
	}
	if hasResumedPR {
		if err := a.prComment(ctx, repo, resumedPR, message); err != nil {
			return reconcileAction{}, false, err
		}
	}

	return reconcileAction{
		Issue:  issueNumber,
		Action: "needs-review",
		Phase:  stringPtr(phase),
		Round:  intPtr(round),
	}, true, nil
}

func (a *App) reconcileStaleLabels(ctx context.Context, repo string, activeIssues map[int]struct{}) ([]reconcileAction, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, err
	}

	output, err := a.ghOutput(
		ctx,
		"issue", "list",
		"--repo", repo,
		"--label", cfg.Labels.InProgress,
		"--state", "open",
		"--limit", "200",
		"--json", "number,title,labels",
	)
	if err != nil {
		return nil, err
	}

	var issues []map[string]any
	if err := json.Unmarshal([]byte(output), &issues); err != nil {
		return nil, fmt.Errorf("failed to parse in-progress issues: %v", err)
	}

	actions := make([]reconcileAction, 0, len(issues))
	for _, issue := range issues {
		issueNumber, ok := intValue(issue["number"])
		if !ok {
			continue
		}
		if _, exists := activeIssues[issueNumber]; exists {
			continue
		}

		if err := a.setIssueStatus(ctx, repo, issueNumber, "ready"); err != nil {
			return nil, err
		}
		message := "Found stale runoq:in-progress label with no active run. Reset to runoq:ready."
		if err := a.issueComment(ctx, repo, issueNumber, message); err != nil {
			return nil, err
		}

		actions = append(actions, reconcileAction{
			Issue:  issueNumber,
			Action: "reset-ready",
		})
	}

	return actions, nil
}

func (a *App) activeStateIssues() (map[int]struct{}, error) {
	files, err := a.stateJSONFiles()
	if err != nil {
		return nil, err
	}

	issues := make(map[int]struct{})
	for _, file := range files {
		base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if !numericBasenamePattern.MatchString(base) {
			continue
		}

		state, ok, err := loadJSONMap(file)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		phase := rawStringOr(state["phase"], "")
		if phase == "DONE" || phase == "FAILED" {
			continue
		}
		issueNumber, ok := intValue(state["issue"])
		if !ok {
			return nil, fmt.Errorf("state file %s is missing a numeric issue", file)
		}
		issues[issueNumber] = struct{}{}
	}

	return issues, nil
}

func (a *App) resolveOpenPRNumber(ctx context.Context, repo string, prNumber string, branch string) (int, bool, error) {
	if strings.TrimSpace(prNumber) != "" && prNumber != "null" {
		output, err := a.ghOutputQuiet(ctx, "pr", "view", prNumber, "--repo", repo, "--json", "number")
		if err == nil {
			var pr struct {
				Number int `json:"number"`
			}
			if json.Unmarshal([]byte(output), &pr) == nil && pr.Number != 0 {
				return pr.Number, true, nil
			}
		}
	}

	if strings.TrimSpace(branch) == "" {
		return 0, false, nil
	}

	output, err := a.ghOutputQuiet(ctx, "pr", "list", "--repo", repo, "--state", "open", "--head", branch, "--json", "number")
	if err != nil {
		return 0, false, err
	}
	var prs []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal([]byte(output), &prs); err != nil {
		return 0, false, fmt.Errorf("failed to parse PR list: %v", err)
	}
	if len(prs) == 0 || prs[0].Number == 0 {
		return 0, false, nil
	}
	return prs[0].Number, true, nil
}

func (a *App) dependencyReason(ctx context.Context, repo string, dependency string, doneLabel string) (string, bool, error) {
	output, err := a.ghOutput(ctx, "issue", "view", dependency, "--repo", repo, "--json", "labels")
	if err != nil {
		return "", false, err
	}

	var issue struct {
		Labels []labelEntry `json:"labels"`
	}
	if err := json.Unmarshal([]byte(output), &issue); err != nil {
		return "", false, fmt.Errorf("failed to parse dependency labels: %v", err)
	}

	for _, label := range issue.Labels {
		if label.Name == doneLabel {
			return "", false, nil
		}
	}

	return fmt.Sprintf("dependency #%s is not runoq:done", dependency), true, nil
}

func (a *App) openPRReason(ctx context.Context, repo string, branch string) (string, error) {
	output, err := a.ghOutput(ctx, "pr", "list", "--repo", repo, "--state", "open", "--head", branch, "--json", "number,url")
	if err != nil {
		return "", err
	}

	var prs []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal([]byte(output), &prs); err != nil {
		return "", fmt.Errorf("failed to parse open PR list: %v", err)
	}
	if len(prs) == 0 || prs[0].Number == 0 {
		return "", nil
	}
	return fmt.Sprintf("existing open PR #%d already tracks this issue", prs[0].Number), nil
}

func (a *App) branchHasConflicts(ctx context.Context, branch string) (bool, error) {
	if strings.TrimSpace(branch) == "" {
		return false, nil
	}

	repo, err := a.targetRepo(ctx)
	if err != nil {
		return false, err
	}

	gitDirInfo, statErr := os.Stat(filepath.Join(repo.Root(), ".git"))
	if statErr != nil || !gitDirInfo.IsDir() {
		return false, nil
	}

	remoteSHA, exists, err := repo.RemoteRefExists("origin", branch)
	if err != nil || !exists {
		return false, nil
	}

	_ = repo.Fetch("origin", "main", branch)

	mergeBase, err := repo.MergeBase("origin/main", remoteSHA)
	if err != nil || strings.TrimSpace(mergeBase) == "" {
		return false, nil
	}

	return repo.MergeHasConflicts(strings.TrimSpace(mergeBase), "origin/main", remoteSHA)
}

func (a *App) branchIsPushed(ctx context.Context, branch string) (bool, error) {
	if strings.TrimSpace(branch) == "" {
		return false, nil
	}

	repo, err := a.targetRepo(ctx)
	if err != nil {
		return false, err
	}

	gitDirInfo, statErr := os.Stat(filepath.Join(repo.Root(), ".git"))
	if statErr != nil || !gitDirInfo.IsDir() {
		return false, nil
	}

	_, exists, err := repo.RemoteRefExists("origin", branch)
	if err != nil {
		return false, nil
	}
	return exists, nil
}

func (a *App) issueComment(ctx context.Context, repo string, issueNumber int, body string) error {
	return a.runGh(ctx, io.Discard, "issue", "comment", strconv.Itoa(issueNumber), "--repo", repo, "--body", body)
}

func (a *App) prComment(ctx context.Context, repo string, prNumber int, body string) error {
	return a.runGh(ctx, io.Discard, "pr", "comment", strconv.Itoa(prNumber), "--repo", repo, "--body", body)
}

func (a *App) setIssueStatus(ctx context.Context, repo string, issueNumber int, status string) error {
	cfg, err := a.loadConfig()
	if err != nil {
		return err
	}

	issueStr := strconv.Itoa(issueNumber)

	// Read current labels
	labelsJSON, err := a.ghOutputQuiet(ctx, "issue", "view", issueStr, "--repo", repo, "--json", "labels")
	if err != nil {
		return err
	}
	var labelsData struct {
		Labels []labelEntry `json:"labels"`
	}
	_ = json.Unmarshal([]byte(labelsJSON), &labelsData)

	// Remove existing runoq state labels, add new one
	editArgs := []string{"issue", "edit", issueStr, "--repo", repo}
	for _, l := range labelsData.Labels {
		switch l.Name {
		case cfg.Labels.Ready, cfg.Labels.InProgress, cfg.Labels.Done, cfg.Labels.NeedsReview, cfg.Labels.Blocked:
			editArgs = append(editArgs, "--remove-label", l.Name)
		}
	}

	var newLabel string
	switch status {
	case "ready":
		newLabel = cfg.Labels.Ready
	case "in-progress":
		newLabel = cfg.Labels.InProgress
	case "done":
		newLabel = cfg.Labels.Done
	case "needs-review":
		newLabel = cfg.Labels.NeedsReview
	case "blocked":
		newLabel = cfg.Labels.Blocked
	}
	if newLabel != "" {
		editArgs = append(editArgs, "--add-label", newLabel)
	}

	return a.runGhWithStderr(ctx, io.Discard, a.stderr, editArgs...)
}

func (a *App) runGh(ctx context.Context, stdout io.Writer, args ...string) error {
	return a.runGhWithStderr(ctx, stdout, a.stderr, args...)
}

func (a *App) runGhWithStderr(ctx context.Context, stdout io.Writer, stderr io.Writer, args ...string) error {
	ghBin := "gh"
	if v, ok := shell.EnvLookup(a.env, "GH_BIN"); ok && v != "" {
		ghBin = v
	}
	return a.execCommand(ctx, shell.CommandRequest{
		Name:   ghBin,
		Args:   args,
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: stdout,
		Stderr: stderr,
	})
}

func (a *App) ghOutput(ctx context.Context, args ...string) (string, error) {
	var stdout bytes.Buffer
	if err := a.runGh(ctx, &stdout, args...); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (a *App) ghOutputQuiet(ctx context.Context, args ...string) (string, error) {
	var stdout bytes.Buffer
	if err := a.runGhWithStderr(ctx, &stdout, io.Discard, args...); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (a *App) loadConfig() (config, error) {
	if a.cfgCache != nil {
		return *a.cfgCache, nil
	}
	configPath, err := a.configPath()
	if err != nil {
		return config{}, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config{}, fmt.Errorf("failed to read config: %v", err)
	}

	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("failed to parse config: %v", err)
	}
	return cfg, nil
}

func (a *App) configPath() (string, error) {
	if value, ok := shell.EnvLookup(a.env, "RUNOQ_CONFIG"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	root, err := a.runoqRoot()
	if err == nil {
		return filepath.Join(root, "config", "runoq.json"), nil
	}
	if strings.TrimSpace(a.cwd) != "" {
		return filepath.Join(a.cwd, "config", "runoq.json"), nil
	}
	return "", errors.New("RUNOQ_CONFIG is required")
}

// cleanupIssueArtifacts removes all artifacts for a failed issue so it can
// be re-dispatched. Discovers artifacts by convention (prefix + issue number)
// using filesystem operations, only calling gh for PR closure.
func (a *App) cleanupIssueArtifacts(ctx context.Context, repo string, issueNumber int) {
	targetRoot, _ := shell.EnvLookup(a.env, "TARGET_ROOT")
	if targetRoot == "" {
		return
	}

	cfg, _ := a.loadConfig()

	issueStr := strconv.Itoa(issueNumber)

	// 1. Remove worktree directory (pure filesystem)
	worktreePrefix := cfg.WorktreePrefix
	if worktreePrefix == "" {
		worktreePrefix = "runoq-wt-"
	}
	if parent, err := filepath.Abs(filepath.Join(targetRoot, "..")); err == nil {
		_ = os.RemoveAll(filepath.Join(parent, worktreePrefix+issueStr))
	}

	// 2. Prune stale worktree metadata in .git/worktrees/
	worktreesDir := filepath.Join(targetRoot, ".git", "worktrees")
	if entries, err := os.ReadDir(worktreesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), worktreePrefix+issueStr) {
				_ = os.RemoveAll(filepath.Join(worktreesDir, entry.Name()))
			}
		}
	}

	// 3. Delete local branch refs matching prefix + issue number
	branchPrefix := cfg.BranchPrefix
	if branchPrefix == "" {
		branchPrefix = "runoq/"
	}
	branchRefDir := filepath.Join(targetRoot, ".git", "refs", "heads")
	refPrefix := issueStr + "-"
	for _, segment := range strings.Split(branchPrefix, "/") {
		if segment != "" {
			branchRefDir = filepath.Join(branchRefDir, segment)
		}
	}
	if entries, err := os.ReadDir(branchRefDir); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), refPrefix) {
				_ = os.Remove(filepath.Join(branchRefDir, entry.Name()))
			}
		}
	}

	// 4. Close open PRs for this issue's branches (GitHub API — unavoidable)
	// gh pr list doesn't support prefix matching on --head, so list all open
	// PRs and filter by branch prefix client-side.
	prsOut, _ := a.ghOutputQuiet(ctx, "pr", "list", "--repo", repo, "--state", "open",
		"--json", "number,headRefName", "--limit", "50")
	var prs []struct {
		Number      int    `json:"number"`
		HeadRefName string `json:"headRefName"`
	}
	if json.Unmarshal([]byte(prsOut), &prs) == nil {
		matchPrefix := branchPrefix + issueStr + "-"
		for _, pr := range prs {
			if strings.HasPrefix(pr.HeadRefName, matchPrefix) {
				_ = a.runGh(ctx, io.Discard, "pr", "close", strconv.Itoa(pr.Number), "--repo", repo, "--delete-branch")
			}
		}
	}
}

func (a *App) runoqRoot() (string, error) {
	if value, ok := shell.EnvLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	if strings.TrimSpace(a.cwd) != "" {
		candidate := filepath.Join(a.cwd, "config", "runoq.json")
		if _, err := os.Stat(candidate); err == nil {
			return a.cwd, nil
		}
	}
	return "", errors.New("RUNOQ_ROOT is required")
}

func (a *App) targetRoot(_ context.Context) (string, error) {
	if value, ok := shell.EnvLookup(a.env, "TARGET_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}

	root, err := gitops.FindRoot(a.cwd)
	if err != nil {
		return "", contractError{message: "Run runoq from inside a git repository."}
	}
	return root, nil
}

func (a *App) targetRepo(ctx context.Context) (gitops.Repo, error) {
	root, err := a.targetRoot(ctx)
	if err != nil {
		return nil, err
	}
	return gitops.OpenCLI(ctx, root, a.execCommand), nil
}

func (a *App) stateDir(ctx context.Context) (string, error) {
	if value, ok := shell.EnvLookup(a.env, "RUNOQ_STATE_DIR"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	targetRoot, err := a.targetRoot(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(targetRoot, ".runoq", "state"), nil
}

func (a *App) stateJSONFiles() ([]string, error) {
	stateDir, err := a.stateDir(context.Background())
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(stateDir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list state directory: %v", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		files = append(files, filepath.Join(stateDir, entry.Name()))
	}
	sort.Strings(files)
	return files, nil
}

// NewDirect creates an App for direct Go calls (not subprocess).
func NewDirect(env []string, cwd string, logWriter io.Writer) *App {
	stderr := io.Writer(io.Discard)
	if logWriter != nil {
		stderr = logWriter
	}
	return &App{
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      io.Discard,
		stderr:      stderr,
		execCommand: shell.RunCommand,
	}
}

// CheckEligibility runs the eligibility check directly (no subprocess).
// Returns the result and an error. A non-nil error means the check itself failed.
// If result.Allowed is false, the reasons explain why.
func (a *App) CheckEligibility(ctx context.Context, repo string, issueNumber int) (EligibilityResult, error) {
	issueArg := strconv.Itoa(issueNumber)
	issueOutput, err := a.ghOutput(ctx, "issue", "view", issueArg, "--repo", repo, "--json", "number,title,body,labels,url")
	if err != nil {
		return EligibilityResult{}, fmt.Errorf("eligibility: failed to view issue: %w", err)
	}

	var issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []labelEntry `json:"labels"`
	}
	if err := json.Unmarshal([]byte(issueOutput), &issue); err != nil {
		return EligibilityResult{}, fmt.Errorf("eligibility: failed to parse issue metadata: %w", err)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return EligibilityResult{}, fmt.Errorf("eligibility: %w", err)
	}

	planning := hasLabelNamed(issue.Labels, "runoq:planning") || hasLabelNamed(issue.Labels, "runoq:adjustment")
	branch := ""
	if !planning {
		branch = branchName(cfg.BranchPrefix, issue.Number, issue.Title)
	}

	reasons := make([]string, 0, 4)
	if strings.TrimSpace(issue.Title) == "" || (!planning && !hasAcceptanceCriteria(issue.Body)) {
		reasons = append(reasons, "missing acceptance criteria")
	}

	// Query blockedBy dependencies via GraphQL
	blockedBy := a.fetchBlockedBy(ctx, repo, issueNumber)
	for _, dep := range blockedBy {
		reason, blocked, err := a.dependencyReason(ctx, repo, strconv.Itoa(dep), cfg.Labels.Done)
		if err != nil {
			return EligibilityResult{}, fmt.Errorf("eligibility: dependency check: %w", err)
		}
		if blocked {
			reasons = append(reasons, reason)
		}
	}

	if !planning {
		openPRReason, err := a.openPRReason(ctx, repo, branch)
		if err != nil {
			return EligibilityResult{}, fmt.Errorf("eligibility: open PR check: %w", err)
		}
		if openPRReason != "" {
			reasons = append(reasons, openPRReason)
		}

		hasConflicts, err := a.branchHasConflicts(ctx, branch)
		if err != nil {
			return EligibilityResult{}, fmt.Errorf("eligibility: conflict check: %w", err)
		}
		if hasConflicts {
			reasons = append(reasons, "branch "+branch+" has unresolved conflicts with origin/main")
		}
	}

	result := EligibilityResult{
		Allowed: len(reasons) == 0,
		Issue:   issue.Number,
		Branch:  branch,
		Reasons: reasons,
	}

	if len(reasons) > 0 {
		message := "Skipped: " + strings.Join(reasons, "; ") + "."
		_ = a.issueComment(ctx, repo, issue.Number, message)
	}

	return result, nil
}

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}

type issueMetadata struct {
	DependsOn []string
	Type      string
}

type labelEntry struct {
	Name string `json:"name"`
}

func hasLabelNamed(labels []labelEntry, name string) bool {
	for _, l := range labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

func (a *App) fetchBlockedBy(ctx context.Context, repo string, issueNumber int) []int {
	owner, repoName, ok := strings.Cut(repo, "/")
	if !ok {
		return nil
	}
	query := fmt.Sprintf(`query { repository(owner: %q, name: %q) { issue(number: %d) { blockedBy(first: 20) { nodes { number } } } } }`, owner, repoName, issueNumber)
	raw, err := a.ghOutputQuiet(ctx, "api", "graphql", "-f", "query="+query)
	if err != nil {
		return nil
	}
	var resp struct {
		Data struct {
			Repository struct {
				Issue struct {
					BlockedBy struct {
						Nodes []struct {
							Number int `json:"number"`
						} `json:"nodes"`
					} `json:"blockedBy"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &resp) != nil {
		return nil
	}
	var result []int
	for _, n := range resp.Data.Repository.Issue.BlockedBy.Nodes {
		result = append(result, n.Number)
	}
	return result
}

func parseIssueMetadata(body string) issueMetadata {
	block := extractMetadataBlock(body)
	if block == "" {
		return issueMetadata{DependsOn: []string{}, Type: "task"}
	}

	dependsLine := ""
	issueType := "task"
	for line := range strings.SplitSeq(block, "\n") {
		if rest, ok := strings.CutPrefix(line, "depends_on:"); ok {
			dependsLine = strings.TrimSpace(rest)
			continue
		}
		if rest, ok := strings.CutPrefix(line, "type:"); ok {
			value := strings.TrimSpace(rest)
			if isPlanningType(value) || value == "task" || value == "epic" {
				issueType = value
			}
		}
	}
	if dependsLine == "" || !json.Valid([]byte(dependsLine)) {
		return issueMetadata{DependsOn: []string{}, Type: issueType}
	}

	var raw []any
	if err := json.Unmarshal([]byte(dependsLine), &raw); err != nil {
		return issueMetadata{DependsOn: []string{}, Type: issueType}
	}

	dependencies := make([]string, 0, len(raw))
	for _, item := range raw {
		value := strings.TrimSpace(rawStringOr(item, ""))
		if value != "" {
			dependencies = append(dependencies, value)
		}
	}
	return issueMetadata{DependsOn: dependencies, Type: issueType}
}

func extractMetadataBlock(body string) string {
	lines := strings.Split(body, "\n")
	inBlock := false
	block := make([]string, 0, len(lines))
	for _, line := range lines {
		if !inBlock {
			if strings.Contains(line, "<!-- runoq:meta") {
				inBlock = true
			}
			continue
		}
		if strings.Contains(line, "-->") {
			break
		}
		block = append(block, line)
	}
	return strings.Join(block, "\n")
}

func hasAcceptanceCriteria(body string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "## Acceptance Criteria") {
			return true
		}
	}
	return false
}

func isPlanningType(issueType string) bool {
	return issueType == "planning" || issueType == "adjustment"
}

func branchName(prefix string, issue int, title string) string {
	return prefix + strconv.Itoa(issue) + "-" + branchSlug(title)
}

func branchSlug(input string) string {
	input = strings.ToLower(input)
	var builder strings.Builder
	lastDash := false
	for _, r := range input {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if builder.Len() == 0 || lastDash {
			continue
		}
		builder.WriteByte('-')
		lastDash = true
	}

	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "issue"
	}
	return slug
}

func loadJSONMap(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read %s: %v", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, false, nil
	}
	return value, true, nil
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(parsed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func rawStringOr(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(typed)
	}
}

func stringPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}

func isExitError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
