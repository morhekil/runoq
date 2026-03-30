package runtimeorchestrator

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

	if issueNumber == "" {
		return a.fail("queue mode is not implemented in the runtime orchestrator yet")
	}

	issue, err := strconv.Atoi(issueNumber)
	if err != nil {
		return a.fail("--issue requires a numeric value")
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
	_ = a.runScript(ctx, root, env, "dispatch-safety.sh", []string{"reconcile", repo}, nil, io.Discard, io.Discard)

	title, err := a.issueTitle(ctx, env, repo, issue)
	if err != nil {
		return a.failf("failed to load issue title: %v", err)
	}

	stateJSON, initDone, err := a.runSingleIssue(ctx, root, env, repo, issue, dryRun, title)
	if err != nil {
		a.logError("Issue #%d failed", issue)
		return 1
	}

	if dryRun {
		_, _ = fmt.Fprintln(a.stdout, stateJSON)
		return 0
	}

	if initDone {
		a.logError("CRITERIA phase is not implemented in the runtime orchestrator yet")
		return 1
	}

	return 0
}

func (a *App) runSingleIssue(ctx context.Context, root string, env []string, repo string, issueNumber int, dryRun bool, title string) (string, bool, error) {
	if _, err := a.getIssueMetadata(ctx, root, env, repo, issueNumber); err != nil {
		return "", false, err
	}

	stateJSON, err := a.phaseInit(ctx, root, env, repo, issueNumber, dryRun, title)
	if err != nil {
		return "", false, err
	}

	if dryRun {
		return stateJSON, false, nil
	}
	return stateJSON, true, nil
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

	eligibilityOut, eligibilityErr := a.scriptOutput(ctx, root, env, "dispatch-safety.sh", []string{"eligibility", repo, strconv.Itoa(issueNumber)}, nil)
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

func (a *App) prepareAuth(ctx context.Context, root string, env []string) []string {
	authEnv := envSet(env, "RUNOQ_FORCE_REFRESH_TOKEN", "1")
	out, err := a.commandOutput(ctx, commandRequest{
		Name: "bash",
		Args: []string{
			"-lc",
			`if eval "$("$1" export-token)" 2>/dev/null; then printf 'ok\n%s' "${GH_TOKEN:-}"; else printf 'fail\n%s' "${GH_TOKEN:-}"; fi`,
			"bash",
			filepath.Join(root, "scripts", "gh-auth.sh"),
		},
		Dir: a.cwd,
		Env: authEnv,
	})
	if err != nil {
		a.logInfo("Token mint failed or skipped (will use ambient credentials)")
		return authEnv
	}

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
