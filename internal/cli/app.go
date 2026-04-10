package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/saruman/runoq/internal/claude"
	"github.com/saruman/runoq/internal/config"
	"github.com/saruman/runoq/internal/gh"
	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/orchestrator"
	"github.com/saruman/runoq/internal/report"
	"github.com/saruman/runoq/internal/runlog"
	"github.com/saruman/runoq/internal/setup"
	"github.com/saruman/runoq/internal/shell"
)

const usageText = `Usage:
  runoq init [--plan <path>]
  runoq plan [file] [--auto-confirm] [--dry-run]
  runoq tick [--issue N]
  runoq loop [--backoff N] [--max-wait-cycles N] [--issue N]
  runoq report <summary|issue|cost> [...]
  runoq maintenance
`

var subcommandHelp = map[string]string{
	"init": `Usage: runoq init [--plan <path>]

Bootstrap a target repository for runoq.

Steps performed:
  1. Create .runoq/identity.json with GitHub App credentials
  2. Ensure managed labels exist on the repository
  3. Create a minimal package.json if none exists
  4. Symlink Claude agents and skills into the target repo
  5. Add .runoq/ to .gitignore
  6. Optionally write the plan path to runoq.json (--plan)
  7. Symlink the runoq binary into PATH

Options:
  --plan <path>   Set the plan file path in runoq.json and stage it

Environment:
  RUNOQ_APP_KEY          Path to GitHub App private key (default: ~/.runoq/app-key.pem)
  RUNOQ_APP_ID           GitHub App ID (required for private apps)
  RUNOQ_SYMLINK_DIR      Directory for the runoq symlink (default: /usr/local/bin)
`,
	"plan": `Usage: runoq plan [file] [--auto-confirm] [--dry-run]

Decompose a plan document into GitHub issues. (Deprecated: prefer runoq tick)

Arguments:
  file              Path to the plan markdown file (reads from runoq.json if omitted)

Options:
  --auto-confirm    Skip confirmation prompt
  --dry-run         Show decomposition without creating issues
`,
	"tick": `Usage: runoq tick [--issue N]

Run one iteration of the planning lifecycle.

Options:
  --issue N     Run a single implementation task instead of queue selection

Exit codes:
  0    Work done, more available
  2    Nothing to do, waiting for human input
  1    Error
`,
	"loop": `Usage: runoq loop [--backoff N] [--max-wait-cycles N] [--issue N]

Run tick in a loop until interrupted or complete.

Calls runoq tick repeatedly. On exit 0 (work done), loops immediately.
On exit 2 (waiting), sleeps for the backoff duration before retrying.
On exit 1 (error), stops. On exit 3 (all milestones complete), stops.

Options:
  --backoff N           Seconds to wait when tick has no work (default: 30)
  --max-wait-cycles N   Stop after N consecutive waiting ticks (default: unlimited)
  --issue N             Run a single implementation task instead of queue selection
`,
	"report": `Usage: runoq report <summary|issue|cost> [...]

Generate reports from runoq state.

Subcommands:
  summary       Aggregate status across all tracked issues
  issue <N>     Show details for a specific issue
  cost          Summarise token usage
`,
	"maintenance": `Usage: runoq maintenance

Run a maintenance review of the target repository.
`,
}

// configLabels holds the label names loaded from runoq.json.
type configLabels struct {
	Ready             string `json:"ready"`
	InProgress        string `json:"inProgress"`
	Done              string `json:"done"`
	NeedsReview       string `json:"needsReview"`
	Blocked           string `json:"blocked"`
	PlanApproved      string `json:"planApproved"`
	MaintenanceReview string `json:"maintenanceReview"`
}

type App struct {
	args           []string
	env            []string
	cwd            string
	stdout         io.Writer
	stderr         io.Writer
	executablePath string
	execCommand    shell.CommandExecutor
	logCloser      io.Closer // set when log writer is created
	labels         configLabels
	branchPrefix   string
	worktreePrefix string
	configRaw      map[string]json.RawMessage
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer, executablePath string) *App {
	return &App{
		args:           append([]string(nil), args...),
		env:            append([]string(nil), env...),
		cwd:            cwd,
		stdout:         stdout,
		stderr:         stderr,
		executablePath: executablePath,
		execCommand:    shell.RunCommand,
	}
}

func (a *App) SetCommandExecutor(execFn shell.CommandExecutor) {
	if execFn == nil {
		a.execCommand = shell.RunCommand
		return
	}
	a.execCommand = execFn
}

func (a *App) Run(ctx context.Context) int {
	defer func() {
		if a.logCloser != nil {
			_ = a.logCloser.Close()
		}
	}()

	runoqRoot, env, ok := a.resolveRuntimeEnv()
	if !ok {
		return shell.Fail(a.stderr, "Unable to resolve RUNOQ_ROOT for runtime mode.")
	}

	subcommand := ""
	if len(a.args) > 0 {
		subcommand = a.args[0]
	}
	args := a.args
	if len(args) > 0 {
		args = args[1:]
	}

	switch subcommand {
	case "init", "plan", "tick", "loop", "report", "maintenance":
		if hasHelpFlag(args) {
			_, _ = io.WriteString(a.stdout, subcommandHelp[subcommand])
			return 0
		}
	}

	switch subcommand {
	case "init":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		return a.runSetup(ctx, targetEnv, runoqRoot, args)
	case "plan":
		if _, err := fmt.Fprintln(a.stderr, "runoq plan is removed; use `runoq tick` for the iterative planning workflow."); err != nil {
			return 1
		}
		return 1
	case "tick":
		if _, err := parseTickArgs(args); err != nil {
			return shell.Fail(a.stderr, err.Error())
		}
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		return a.runTick(ctx, targetEnv, runoqRoot, args)
	case "loop":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		return a.runLoop(ctx, targetEnv, runoqRoot, args)
	case "report":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		reportApp := report.New(args, targetEnv, a.cwd, a.stdout, a.stderr)
		return reportApp.Run()
	case "maintenance":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		authEnv, code := a.prepareAuth(ctx, targetEnv, runoqRoot)
		if code != 0 {
			return code
		}
		return a.runMaintenance(ctx, authEnv, runoqRoot)
	case "", "-h", "--help", "help":
		a.printUsage(a.stdout)
		return 0
	default:
		a.printUsage(a.stderr)
		return 1
	}
}

func (a *App) resolveRuntimeEnv() (string, []string, bool) {
	env := append([]string(nil), a.env...)
	runoqRoot, ok := shell.EnvLookup(env, "RUNOQ_ROOT")
	if !ok || strings.TrimSpace(runoqRoot) == "" {
		runoqRoot = a.fallbackRoot()
	}
	if strings.TrimSpace(runoqRoot) == "" {
		return "", nil, false
	}

	env = shell.EnvSet(env, "RUNOQ_ROOT", runoqRoot)
	configValue, hasConfig := shell.EnvLookup(env, "RUNOQ_CONFIG")
	if !hasConfig || configValue == "" {
		env = shell.EnvSet(env, "RUNOQ_CONFIG", filepath.Join(runoqRoot, "config", "runoq.json"))
	}
	return runoqRoot, env, true
}

func (a *App) fallbackRoot() string {
	if a.cwd != "" && shell.FileExists(filepath.Join(a.cwd, "config", "runoq.json")) {
		return a.cwd
	}
	if a.executablePath == "" {
		return ""
	}
	base := filepath.Dir(a.executablePath)
	candidate := filepath.Clean(filepath.Join(base, ".."))
	if shell.FileExists(filepath.Join(candidate, "config", "runoq.json")) {
		return candidate
	}
	return ""
}

func (a *App) prepareTargetContext(ctx context.Context, runoqRoot string, env []string) ([]string, int) {
	targetRoot, ok := shell.EnvLookup(env, "TARGET_ROOT")
	if !ok || strings.TrimSpace(targetRoot) == "" {
		var err error
		targetRoot, err = gitops.FindRoot(a.cwd)
		if err != nil {
			return nil, shell.Fail(a.stderr, "Run runoq from inside a git repository.")
		}
	}

	// Create persistent log file and clean up old logs
	logDir := filepath.Join(targetRoot, "log")
	_ = runlog.Cleanup(logDir, 20)
	logWriter, logErr := runlog.NewWriter(a.stderr, logDir)
	if logErr == nil {
		a.stderr = logWriter
		a.logCloser = logWriter
		_, _ = fmt.Fprintf(a.stderr, "  log: %s\n", logWriter.Path())
	}

	repo, err := a.resolveRepo(ctx, env, targetRoot)
	if err != nil {
		return nil, shell.Fail(a.stderr, err.Error())
	}

	nextEnv := append([]string(nil), env...)
	nextEnv = shell.EnvSet(nextEnv, "TARGET_ROOT", targetRoot)
	nextEnv = shell.EnvSet(nextEnv, "REPO", repo)
	nextEnv = prependPath(nextEnv, filepath.Join(runoqRoot, "scripts"))

	// Load runoq.json once for the lifetime of this command.
	configPath, _ := shell.EnvLookup(nextEnv, "RUNOQ_CONFIG")
	configPath = config.ResolvePath(configPath, runoqRoot)
	raw, loadErr := config.LoadFile(configPath)
	if loadErr == nil {
		a.configRaw = raw
		if labelsJSON, ok := raw["labels"]; ok {
			_ = json.Unmarshal(labelsJSON, &a.labels)
		}
		if v, ok := raw["branchPrefix"]; ok {
			_ = json.Unmarshal(v, &a.branchPrefix)
		}
		if v, ok := raw["worktreePrefix"]; ok {
			_ = json.Unmarshal(v, &a.worktreePrefix)
		}
	}

	return nextEnv, 0
}

func (a *App) prepareAuth(ctx context.Context, env []string, runoqRoot string) ([]string, int) {
	authEnv := append([]string(nil), env...)
	if forceRefresh, ok := shell.EnvLookup(authEnv, "RUNOQ_FORCE_REFRESH_TOKEN"); ok && strings.TrimSpace(forceRefresh) != "" {
		authEnv = withoutEnvKeys(authEnv, "GH_TOKEN", "GITHUB_TOKEN")
	}

	targetRoot, _ := shell.EnvLookup(authEnv, "TARGET_ROOT")
	homeDir, _ := shell.EnvLookup(authEnv, "HOME")
	clientCWD := targetRoot
	if strings.TrimSpace(clientCWD) == "" {
		clientCWD = a.cwd
	}
	authClient := gh.NewClient(a.execCommand, http.DefaultClient, authEnv, clientCWD, homeDir)
	if err := authClient.EnsureToken(ctx); err != nil {
		return nil, shell.Fail(a.stderr, err.Error())
	}

	nextEnv := append([]string(nil), env...)
	if token, ok := shell.EnvLookup(authClient.Env(), "GH_TOKEN"); ok && token != "" {
		nextEnv = shell.EnvSet(nextEnv, "GH_TOKEN", token)
		return nextEnv, 0
	}
	if token, ok := shell.EnvLookup(nextEnv, "GH_TOKEN"); ok && strings.TrimSpace(token) != "" {
		return nextEnv, 0
	}
	if token, ok := shell.EnvLookup(nextEnv, "GITHUB_TOKEN"); ok && strings.TrimSpace(token) != "" {
		nextEnv = shell.EnvSet(nextEnv, "GH_TOKEN", token)
		return nextEnv, 0
	}

	_ = runoqRoot
	return nil, shell.Fail(a.stderr, "Failed to export GH_TOKEN.")
}

func withoutEnvKeys(env []string, keys ...string) []string {
	if len(keys) == 0 {
		return append([]string(nil), env...)
	}

	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[key] = struct{}{}
	}

	filtered := make([]string, 0, len(env))
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			filtered = append(filtered, item)
			continue
		}
		if _, found := blocked[name]; found {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (a *App) resolveRepo(ctx context.Context, env []string, targetRoot string) (string, error) {
	if value, ok := shell.EnvLookup(env, "RUNOQ_REPO"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}

	gitRepo := gitops.OpenCLI(ctx, targetRoot, a.execCommand)
	originURL, err := gitRepo.RemoteURL("origin")
	if err != nil {
		return "", errors.New("no 'origin' remote found: runoq requires a GitHub-hosted repo")
	}

	repo, err := parseRepoFromRemote(originURL)
	if err != nil {
		return "", err
	}
	return repo, nil
}

func parseRepoFromRemote(remote string) (string, error) {
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		repo := strings.TrimPrefix(remote, "git@github.com:")
		repo = strings.TrimSuffix(repo, ".git")
		if repo == "" {
			return "", fmt.Errorf("origin remote is not a GitHub URL: %s", remote)
		}
		return repo, nil
	case strings.HasPrefix(remote, "ssh://git@github.com/"):
		repo := strings.TrimPrefix(remote, "ssh://git@github.com/")
		repo = strings.TrimSuffix(repo, ".git")
		if repo == "" {
			return "", fmt.Errorf("origin remote is not a GitHub URL: %s", remote)
		}
		return repo, nil
	case strings.HasPrefix(remote, "https://"):
		trimmed := strings.TrimPrefix(remote, "https://")
		if at := strings.Index(trimmed, "@"); at >= 0 {
			trimmed = trimmed[at+1:]
		}
		if !strings.HasPrefix(trimmed, "github.com/") {
			return "", fmt.Errorf("origin remote is not a GitHub URL: %s", remote)
		}
		repo := strings.TrimPrefix(trimmed, "github.com/")
		repo = strings.TrimSuffix(repo, ".git")
		if repo == "" {
			return "", fmt.Errorf("origin remote is not a GitHub URL: %s", remote)
		}
		return repo, nil
	default:
		return "", fmt.Errorf("origin remote is not a GitHub URL: %s", remote)
	}
}

func readProjectPlanFile(targetRoot string) (string, error) {
	if strings.TrimSpace(targetRoot) == "" {
		return "", errors.New("plan file not configured: target repository root is unknown")
	}

	configPath := filepath.Join(targetRoot, "runoq.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("plan file not configured: missing %s, run `runoq init --plan <path>` or pass a plan file explicitly", configPath)
		}
		return "", fmt.Errorf("plan file not configured: failed to read %s: %w", configPath, err)
	}

	var cfg struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("plan file not configured: invalid JSON in %s: %w", configPath, err)
	}
	if strings.TrimSpace(cfg.Plan) == "" {
		return "", fmt.Errorf("plan file not configured: %s is missing a non-empty `plan` value", configPath)
	}
	return cfg.Plan, nil
}

func (a *App) runSetup(ctx context.Context, env []string, runoqRoot string, args []string) int {
	var planPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			if i+1 >= len(args) {
				return shell.Fail(a.stderr, "Missing value for --plan.")
			}
			planPath = args[i+1]
			i++
		default:
			return shell.Failf(a.stderr, "Unknown option: %s", args[i])
		}
	}

	targetRoot, _ := shell.EnvLookup(env, "TARGET_ROOT")
	repo, _ := shell.EnvLookup(env, "REPO")
	configPath, _ := shell.EnvLookup(env, "RUNOQ_CONFIG")
	homeDir, _ := shell.EnvLookup(env, "HOME")
	appKeyPath, _ := shell.EnvLookup(env, "RUNOQ_APP_KEY")
	symlinkDir, _ := shell.EnvLookup(env, "RUNOQ_SYMLINK_DIR")

	var appID int64
	if v, ok := shell.EnvLookup(env, "RUNOQ_APP_ID"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			appID = n
		}
	}

	// Read appSlug from config.
	var appSlug string
	if a.configRaw != nil {
		if v, ok := a.configRaw["identity"]; ok {
			var id struct {
				AppSlug string `json:"appSlug"`
			}
			if json.Unmarshal(v, &id) == nil {
				appSlug = id.AppSlug
			}
		}
	}

	cfg := setup.Config{
		TargetRoot: targetRoot,
		RunoqRoot:  runoqRoot,
		Repo:       repo,
		PlanPath:   planPath,
		AppSlug:    appSlug,
		AppKeyPath: appKeyPath,
		AppID:      appID,
		SymlinkDir: symlinkDir,
		HomeDir:    homeDir,
		ConfigPath: configPath,
		Env:        env,
	}

	if err := setup.Run(ctx, cfg, http.DefaultClient, a.execCommand, a.stderr); err != nil {
		return shell.Fail(a.stderr, err.Error())
	}
	return 0
}

func (a *App) runMaintenance(ctx context.Context, env []string, runoqRoot string) int {
	targetRoot, _ := shell.EnvLookup(env, "TARGET_ROOT")
	if err := claude.CapturedExec(ctx, a.execCommand, claude.CaptureConfig{
		WorkDir: targetRoot,
		Args:    []string{"--agent", "maintenance-reviewer", "--add-dir", runoqRoot},
		Env:     env,
		Stdout:  a.stdout,
		Stderr:  a.stderr,
	}); err != nil {
		return shell.ExitCodeFromError(err)
	}
	return 0
}

func (a *App) orchestratorConfig() orchestrator.OrchestratorConfig {
	cfg := orchestrator.OrchestratorConfig{
		ReadyLabel:       a.labels.Ready,
		InProgressLabel:  a.labels.InProgress,
		DoneLabel:        a.labels.Done,
		NeedsReviewLabel: a.labels.NeedsReview,
		BlockedLabel:     a.labels.Blocked,
		BranchPrefix:     a.branchPrefix,
		WorktreePrefix:   a.worktreePrefix,
		AutoMergeEnabled: true,
		Reviewers:        []string{},
		IdentityHandle:   "runoq",
		MaxRounds:        5,
		MaxTokenBudget:   500000,
	}
	if a.configRaw != nil {
		if v, ok := a.configRaw["maxRounds"]; ok {
			_ = json.Unmarshal(v, &cfg.MaxRounds)
		}
		if v, ok := a.configRaw["maxTokenBudget"]; ok {
			_ = json.Unmarshal(v, &cfg.MaxTokenBudget)
		}
		if v, ok := a.configRaw["reviewers"]; ok {
			_ = json.Unmarshal(v, &cfg.Reviewers)
		}
		if v, ok := a.configRaw["autoMerge"]; ok {
			var am struct {
				Enabled bool `json:"enabled"`
			}
			if json.Unmarshal(v, &am) == nil {
				cfg.AutoMergeEnabled = am.Enabled
			}
		}
		if v, ok := a.configRaw["identity"]; ok {
			var id struct {
				Handle string `json:"handle"`
			}
			if json.Unmarshal(v, &id) == nil && id.Handle != "" {
				cfg.IdentityHandle = id.Handle
			}
		}
	}
	return cfg
}

func (a *App) planningMaxRounds() int {
	maxRounds := 3
	if a.configRaw == nil {
		return maxRounds
	}

	if v, ok := a.configRaw["planning"]; ok {
		var planning struct {
			MaxDecompositionRounds int `json:"maxDecompositionRounds"`
		}
		if json.Unmarshal(v, &planning) == nil && planning.MaxDecompositionRounds > 0 {
			maxRounds = planning.MaxDecompositionRounds
		}
	}
	return maxRounds
}

func (a *App) runTick(ctx context.Context, env []string, runoqRoot string, args []string) int {
	repo, _ := shell.EnvLookup(env, "REPO")
	targetRoot, _ := shell.EnvLookup(env, "TARGET_ROOT")
	targetIssue, err := parseTickArgs(args)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	// Read plan file from project config
	planFile := ""
	if targetIssue == 0 {
		planFile, err = readProjectPlanFile(targetRoot)
		if err != nil {
			return shell.Fail(a.stderr, err.Error())
		}
	}

	dryImpl, _ := shell.EnvLookup(env, "RUNOQ_DRY_RUN_IMPL")
	orchCfg := a.orchestratorConfig()
	return orchestrator.RunTick(ctx, orchestrator.TickConfig{
		Repo:                 repo,
		PlanFile:             planFile,
		RunoqRoot:            runoqRoot,
		TargetIssue:          targetIssue,
		PlanApprovedLabel:    a.labels.PlanApproved,
		MaxRounds:            orchCfg.MaxRounds,
		MaxTokenBudget:       orchCfg.MaxTokenBudget,
		AutoMergeEnabled:     orchCfg.AutoMergeEnabled,
		AutoMergeConfigured:  true,
		Reviewers:            orchCfg.Reviewers,
		IdentityHandle:       orchCfg.IdentityHandle,
		ReadyLabel:           a.labels.Ready,
		InProgressLabel:      a.labels.InProgress,
		DoneLabel:            a.labels.Done,
		NeedsReviewLabel:     a.labels.NeedsReview,
		BlockedLabel:         a.labels.Blocked,
		BranchPrefix:         a.branchPrefix,
		WorktreePrefix:       a.worktreePrefix,
		MaxPlanningRounds:    a.planningMaxRounds(),
		DryRunImplementation: dryImpl == "1",
		Env:                  env,
		ExecCommand:          a.execCommand,
		Stdout:               a.stdout,
		Stderr:               a.stderr,
	})
}

func (a *App) runTickWithCapture(ctx context.Context, env []string, runoqRoot string, lastCompleted int, targetIssue int) (int, int) {
	repo, _ := shell.EnvLookup(env, "REPO")
	targetRoot, _ := shell.EnvLookup(env, "TARGET_ROOT")

	planFile := ""
	var err error
	if targetIssue == 0 {
		planFile, err = readProjectPlanFile(targetRoot)
		if err != nil {
			return shell.Fail(a.stderr, err.Error()), 0
		}
	}

	var buf bytes.Buffer
	teeStdout := io.MultiWriter(a.stdout, &buf)
	orchCfg := a.orchestratorConfig()

	code := orchestrator.RunTick(ctx, orchestrator.TickConfig{
		Repo:                repo,
		PlanFile:            planFile,
		RunoqRoot:           runoqRoot,
		TargetIssue:         targetIssue,
		PlanApprovedLabel:   a.labels.PlanApproved,
		MaxRounds:           orchCfg.MaxRounds,
		MaxTokenBudget:      orchCfg.MaxTokenBudget,
		AutoMergeEnabled:    orchCfg.AutoMergeEnabled,
		AutoMergeConfigured: true,
		Reviewers:           orchCfg.Reviewers,
		IdentityHandle:      orchCfg.IdentityHandle,
		ReadyLabel:          a.labels.Ready,
		InProgressLabel:     a.labels.InProgress,
		DoneLabel:           a.labels.Done,
		NeedsReviewLabel:    a.labels.NeedsReview,
		BlockedLabel:        a.labels.Blocked,
		BranchPrefix:        a.branchPrefix,
		WorktreePrefix:      a.worktreePrefix,
		MaxPlanningRounds:   a.planningMaxRounds(),
		LastCompletedIssue:  lastCompleted,
		Env:                 env,
		ExecCommand:         a.execCommand,
		Stdout:              teeStdout,
		Stderr:              a.stderr,
	})

	completed := parseCompletedIssue(buf.String())
	return code, completed
}

// parseCompletedIssue extracts the issue number from "Issue #N — phase: DONE" output.
func parseCompletedIssue(output string) int {
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, "phase: DONE") {
			// "Issue #42 — phase: DONE"
			if start := strings.Index(line, "#"); start >= 0 {
				rest := line[start+1:]
				end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
				if end < 0 {
					end = len(rest)
				}
				if end > 0 {
					n, _ := strconv.Atoi(rest[:end])
					return n
				}
			}
		}
	}
	return 0
}

func (a *App) runLoop(ctx context.Context, env []string, runoqRoot string, args []string) int {
	backoff := 30
	maxWaitCycles := 0 // 0 = unlimited
	targetIssue := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--issue":
			if i+1 >= len(args) {
				return shell.Failf(a.stderr, "Missing --issue value")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v < 1 {
				return shell.Failf(a.stderr, "Invalid --issue value: %s", args[i+1])
			}
			targetIssue = v
			i++
		case "--backoff":
			if i+1 >= len(args) {
				return shell.Failf(a.stderr, "Missing --backoff value")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v < 1 {
				return shell.Failf(a.stderr, "Invalid --backoff value: %s", args[i+1])
			}
			backoff = v
			i++
		case "--max-wait-cycles":
			if i+1 >= len(args) {
				return shell.Failf(a.stderr, "Missing --max-wait-cycles value")
			}
			v, err := strconv.Atoi(args[i+1])
			if err != nil || v < 1 {
				return shell.Failf(a.stderr, "Invalid --max-wait-cycles value: %s", args[i+1])
			}
			maxWaitCycles = v
			i++
		default:
			return shell.Failf(a.stderr, "Unknown option: %s", args[i])
		}
	}

	loopCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	waitCycles := 0
	lastCompleted := 0
	for {
		code, completed := a.runTickWithCapture(loopCtx, env, runoqRoot, lastCompleted, targetIssue)

		select {
		case <-loopCtx.Done():
			return 0
		default:
		}

		switch code {
		case 0:
			// Work done, reset wait counter, loop immediately
			waitCycles = 0
			if completed > 0 {
				lastCompleted = completed
				if targetIssue > 0 && completed == targetIssue {
					return 0
				}
			}
		case 2:
			waitCycles++
			if maxWaitCycles > 0 && waitCycles >= maxWaitCycles {
				_, _ = fmt.Fprintf(a.stderr, "stopping after %d consecutive wait cycles\n", waitCycles)
				return 0
			}
			_, _ = fmt.Fprintf(a.stderr, "waiting %ds before next tick\n", backoff)
			select {
			case <-time.After(time.Duration(backoff) * time.Second):
			case <-loopCtx.Done():
				return 0
			}
		case 3:
			// All milestones complete
			return 0
		default:
			return shell.Failf(a.stderr, "tick exited with status %d", code)
		}
	}
}

func parseTickArgs(args []string) (int, error) {
	if len(args) == 0 {
		return 0, nil
	}
	if args[0] != "--issue" {
		return 0, fmt.Errorf("unknown option: %s", args[0])
	}
	if len(args) == 1 {
		return 0, fmt.Errorf("missing --issue value")
	}
	if len(args) != 2 {
		return 0, fmt.Errorf("unknown option: %s", args[2])
	}
	v, err := strconv.Atoi(args[1])
	if err != nil || v < 1 {
		return 0, fmt.Errorf("invalid --issue value: %s", args[1])
	}
	return v, nil
}

func (a *App) printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

func prependPath(env []string, head string) []string {
	current, _ := shell.EnvLookup(env, "PATH")
	if current == "" {
		return shell.EnvSet(env, "PATH", head)
	}
	return shell.EnvSet(env, "PATH", head+string(os.PathListSeparator)+current)
}
