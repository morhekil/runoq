package runtimedispatchsafety

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
)

const usageText = `Usage:
  dispatch-safety.sh reconcile <repo>
  dispatch-safety.sh eligibility <repo> <issue-number>
`

var numericBasenamePattern = regexp.MustCompile(`^[0-9]+$`)

type commandRequest struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string
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
	BranchPrefix string `json:"branchPrefix"`
}

type reconcileAction struct {
	Issue    int     `json:"issue"`
	PRNumber *int    `json:"pr_number,omitempty"`
	Action   string  `json:"action"`
	Phase    *string `json:"phase,omitempty"`
	Round    *int    `json:"round,omitempty"`
}

type eligibilityResult struct {
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
		return a.runReconcile(ctx, a.args[1])
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

func (a *App) runReconcile(ctx context.Context, repo string) int {
	activeIssues, err := a.activeStateIssues()
	if err != nil {
		return a.failf("%v", err)
	}

	files, err := a.stateJSONFiles()
	if err != nil {
		return a.failf("%v", err)
	}

	actions := make([]reconcileAction, 0, len(files))
	for _, file := range files {
		base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		if !numericBasenamePattern.MatchString(base) {
			continue
		}

		action, ok, commandErr := a.reconcileStateFile(ctx, repo, file)
		if commandErr != nil {
			if isExitError(commandErr) {
				return 1
			}
			return a.failf("%v", commandErr)
		}
		if ok {
			actions = append(actions, action)
		}
	}

	staleActions, err := a.reconcileStaleLabels(ctx, repo, activeIssues)
	if err != nil {
		if isExitError(err) {
			return 1
		}
		return a.failf("%v", err)
	}
	actions = append(actions, staleActions...)

	return a.writeJSON(actions)
}

func (a *App) runEligibility(ctx context.Context, repo string, issueArg string) int {
	issueOutput, err := a.ghOutput(ctx, "issue", "view", issueArg, "--repo", repo, "--json", "number,title,body,labels,url")
	if err != nil {
		if isExitError(err) {
			return 1
		}
		return a.failf("%v", err)
	}

	var issue struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal([]byte(issueOutput), &issue); err != nil {
		return a.failf("failed to parse issue metadata: %v", err)
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return a.failf("%v", err)
	}

	reasons := make([]string, 0, 4)
	if strings.TrimSpace(issue.Title) == "" || !hasAcceptanceCriteria(issue.Body) {
		reasons = append(reasons, "missing acceptance criteria")
	}

	metadata := parseIssueMetadata(issue.Body)
	for _, dependency := range metadata.DependsOn {
		reason, blocked, err := a.dependencyReason(ctx, repo, dependency, cfg.Labels.Done)
		if err != nil {
			if isExitError(err) {
				return 1
			}
			return a.failf("%v", err)
		}
		if blocked {
			reasons = append(reasons, reason)
		}
	}

	branch := branchName(cfg.BranchPrefix, issue.Number, issue.Title)

	openPRReason, err := a.openPRReason(ctx, repo, branch)
	if err != nil {
		if isExitError(err) {
			return 1
		}
		return a.failf("%v", err)
	}
	if openPRReason != "" {
		reasons = append(reasons, openPRReason)
	}

	hasConflicts, err := a.branchHasConflicts(ctx, branch)
	if err != nil {
		if isExitError(err) {
			return 1
		}
		return a.failf("%v", err)
	}
	if hasConflicts {
		reasons = append(reasons, "branch "+branch+" has unresolved conflicts with origin/main")
	}

	result := eligibilityResult{
		Allowed: len(reasons) == 0,
		Issue:   issue.Number,
		Branch:  branch,
		Reasons: reasons,
	}

	if len(reasons) == 0 {
		return a.writeJSON(result)
	}

	message := "Skipped: " + strings.Join(reasons, "; ") + "."
	if err := a.issueComment(ctx, repo, issue.Number, message); err != nil {
		if isExitError(err) {
			return 1
		}
		return a.failf("%v", err)
	}
	if code := a.writeJSON(result); code != 0 {
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
	if phase == "DONE" || phase == "FAILED" {
		return reconcileAction{}, false, nil
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
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
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

	targetRoot, err := a.targetRoot(ctx)
	if err != nil {
		return false, err
	}

	gitDirInfo, statErr := os.Stat(filepath.Join(targetRoot, ".git"))
	if statErr != nil || !gitDirInfo.IsDir() {
		return false, nil
	}

	remoteOut, err := a.commandOutput(ctx, commandRequest{
		Name: "git",
		Args: []string{"-C", targetRoot, "ls-remote", "--heads", "origin", branch},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return false, nil
	}
	remoteSHA := firstField(remoteOut)
	if remoteSHA == "" {
		return false, nil
	}

	_ = a.execCommand(ctx, commandRequest{
		Name:   "git",
		Args:   []string{"-C", targetRoot, "fetch", "origin", "main", branch},
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})

	mergeBase, err := a.commandOutput(ctx, commandRequest{
		Name: "git",
		Args: []string{"-C", targetRoot, "merge-base", "origin/main", remoteSHA},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil || strings.TrimSpace(mergeBase) == "" {
		return false, nil
	}

	mergeTree, err := a.commandOutput(ctx, commandRequest{
		Name: "git",
		Args: []string{"-C", targetRoot, "merge-tree", strings.TrimSpace(mergeBase), "origin/main", remoteSHA},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return false, err
	}

	return strings.Contains(mergeTree, "<<<<<<<"), nil
}

func (a *App) branchIsPushed(ctx context.Context, branch string) (bool, error) {
	if strings.TrimSpace(branch) == "" {
		return false, nil
	}

	targetRoot, err := a.targetRoot(ctx)
	if err != nil {
		return false, err
	}

	gitDirInfo, statErr := os.Stat(filepath.Join(targetRoot, ".git"))
	if statErr != nil || !gitDirInfo.IsDir() {
		return false, nil
	}

	output, err := a.commandOutput(ctx, commandRequest{
		Name: "git",
		Args: []string{"-C", targetRoot, "ls-remote", "--heads", "origin", branch},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(output) != "", nil
}

func (a *App) issueComment(ctx context.Context, repo string, issueNumber int, body string) error {
	return a.runGh(ctx, io.Discard, "issue", "comment", strconv.Itoa(issueNumber), "--repo", repo, "--body", body)
}

func (a *App) prComment(ctx context.Context, repo string, prNumber int, body string) error {
	return a.runGh(ctx, io.Discard, "pr", "comment", strconv.Itoa(prNumber), "--repo", repo, "--body", body)
}

func (a *App) setIssueStatus(ctx context.Context, repo string, issueNumber int, status string) error {
	root, err := a.runoqRoot()
	if err != nil {
		return err
	}

	return a.execCommand(ctx, commandRequest{
		Name:   filepath.Join(root, "scripts", "gh-issue-queue.sh"),
		Args:   []string{"set-status", repo, strconv.Itoa(issueNumber), status},
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: io.Discard,
		Stderr: a.stderr,
	})
}

func (a *App) runGh(ctx context.Context, stdout io.Writer, args ...string) error {
	return a.runGhWithStderr(ctx, stdout, a.stderr, args...)
}

func (a *App) runGhWithStderr(ctx context.Context, stdout io.Writer, stderr io.Writer, args ...string) error {
	root, err := a.runoqRoot()
	if err != nil {
		return err
	}

	script := `source "$1/scripts/lib/common.sh"; shift; runoq::gh "$@"`
	commandArgs := append([]string{"-lc", script, "bash", root}, args...)
	return a.execCommand(ctx, commandRequest{
		Name:   "bash",
		Args:   commandArgs,
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
	if value, ok := envLookup(a.env, "RUNOQ_CONFIG"); ok && strings.TrimSpace(value) != "" {
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

func (a *App) runoqRoot() (string, error) {
	if value, ok := envLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	if strings.TrimSpace(a.cwd) != "" {
		candidate := filepath.Join(a.cwd, "scripts", "lib", "common.sh")
		if _, err := os.Stat(candidate); err == nil {
			return a.cwd, nil
		}
	}
	return "", errors.New("RUNOQ_ROOT is required")
}

func (a *App) targetRoot(ctx context.Context) (string, error) {
	if value, ok := envLookup(a.env, "TARGET_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}

	output, err := a.commandOutput(ctx, commandRequest{
		Name:   "git",
		Args:   []string{"rev-parse", "--show-toplevel"},
		Dir:    a.cwd,
		Env:    a.env,
		Stderr: io.Discard,
	})
	if err != nil || strings.TrimSpace(output) == "" {
		return "", contractError{message: "Run runoq from inside a git repository."}
	}
	return strings.TrimSpace(output), nil
}

func (a *App) stateDir(ctx context.Context) (string, error) {
	if value, ok := envLookup(a.env, "RUNOQ_STATE_DIR"); ok && strings.TrimSpace(value) != "" {
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

func (a *App) commandOutput(ctx context.Context, req commandRequest) (string, error) {
	var stdout bytes.Buffer
	req.Stdout = &stdout
	if req.Stderr == nil {
		req.Stderr = io.Discard
	}
	if err := a.execCommand(ctx, req); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (a *App) writeJSON(value any) int {
	encoder := json.NewEncoder(a.stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return a.failf("Failed to encode JSON output: %v", err)
	}
	return 0
}

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}

func (a *App) fail(message string) int {
	_, _ = fmt.Fprintf(a.stderr, "runoq: %s\n", message)
	return 1
}

func (a *App) failf(format string, args ...any) int {
	return a.fail(fmt.Sprintf(format, args...))
}

type issueMetadata struct {
	DependsOn []string
}

func parseIssueMetadata(body string) issueMetadata {
	block := extractMetadataBlock(body)
	if block == "" {
		return issueMetadata{DependsOn: []string{}}
	}

	dependsLine := ""
	for line := range strings.SplitSeq(block, "\n") {
		if rest, ok := strings.CutPrefix(line, "depends_on:"); ok {
			dependsLine = strings.TrimSpace(rest)
			break
		}
	}
	if dependsLine == "" || !json.Valid([]byte(dependsLine)) {
		return issueMetadata{DependsOn: []string{}}
	}

	var raw []any
	if err := json.Unmarshal([]byte(dependsLine), &raw); err != nil {
		return issueMetadata{DependsOn: []string{}}
	}

	dependencies := make([]string, 0, len(raw))
	for _, item := range raw {
		value := strings.TrimSpace(rawStringOr(item, ""))
		if value != "" {
			dependencies = append(dependencies, value)
		}
	}
	return issueMetadata{DependsOn: dependencies}
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

func firstField(input string) string {
	for line := range strings.SplitSeq(strings.TrimSpace(input), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
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

func runCommand(ctx context.Context, req commandRequest) error {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	if req.Stdout != nil {
		cmd.Stdout = req.Stdout
	} else {
		cmd.Stdout = io.Discard
	}
	if req.Stderr != nil {
		cmd.Stderr = req.Stderr
	} else {
		cmd.Stderr = io.Discard
	}
	return cmd.Run()
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
