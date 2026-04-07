package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/orchestrator"
	"github.com/saruman/runoq/internal/report"
	"github.com/saruman/runoq/internal/shell"
)

const usageText = `Usage:
  runoq init [--plan <path>]
  runoq plan [file] [--auto-confirm] [--dry-run]
  runoq tick
  runoq loop [--backoff N]
  runoq run [--issue N] [--dry-run]
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
	"tick": `Usage: runoq tick

Run one iteration of the planning lifecycle.

Exit codes:
  0    Work done, more available
  2    Nothing to do, waiting for human input
  1    Error
`,
	"loop": `Usage: runoq loop [--backoff N] [--max-wait-cycles N]

Run tick in a loop until interrupted or complete.

Calls runoq tick repeatedly. On exit 0 (work done), loops immediately.
On exit 2 (waiting), sleeps for the backoff duration before retrying.
On exit 1 (error), stops. On exit 3 (all milestones complete), stops.

Options:
  --backoff N           Seconds to wait when tick has no work (default: 30)
  --max-wait-cycles N   Stop after N consecutive waiting ticks (default: unlimited)
`,
	"run": `Usage: runoq run [--issue N] [--dry-run]

Execute the next issue from the queue.

Options:
  --issue N     Run a specific issue number instead of the next in queue
  --dry-run     Show what would be executed without running it
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

type App struct {
	args           []string
	env            []string
	cwd            string
	stdout         io.Writer
	stderr         io.Writer
	executablePath string
	execCommand    shell.CommandExecutor
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
	case "init", "plan", "tick", "loop", "run", "report", "maintenance":
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
		return a.runScript(ctx, targetEnv, runoqRoot, "setup.sh", args)
	case "plan":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		fmt.Fprintln(a.stderr, "runoq plan is deprecated; prefer `runoq tick` for the iterative planning workflow.")
		repo, _ := shell.EnvLookup(targetEnv, "REPO")
		targetRoot, _ := shell.EnvLookup(targetEnv, "TARGET_ROOT")
		planArgs, err := a.resolvePlanArgs(targetRoot, args)
		if err != nil {
			return shell.Fail(a.stderr, err.Error())
		}
		planArgs = append([]string{repo}, planArgs...)
		return a.runScript(ctx, targetEnv, runoqRoot, "plan.sh", planArgs)
	case "tick":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		return a.runTick(ctx, targetEnv, runoqRoot)
	case "loop":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		return a.runLoop(ctx, targetEnv, runoqRoot, args)
	case "run":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		authEnv, code := a.prepareAuth(ctx, targetEnv, runoqRoot)
		if code != 0 {
			return code
		}
		return a.runScript(ctx, authEnv, runoqRoot, "run.sh", args)
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
	if a.cwd != "" && shell.FileExists(filepath.Join(a.cwd, "scripts", "lib", "common.sh")) {
		return a.cwd
	}
	if a.executablePath == "" {
		return ""
	}
	base := filepath.Dir(a.executablePath)
	candidate := filepath.Clean(filepath.Join(base, ".."))
	if shell.FileExists(filepath.Join(candidate, "scripts", "lib", "common.sh")) {
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

	repo, err := a.resolveRepo(ctx, env, targetRoot)
	if err != nil {
		return nil, shell.Fail(a.stderr, err.Error())
	}

	nextEnv := append([]string(nil), env...)
	nextEnv = shell.EnvSet(nextEnv, "TARGET_ROOT", targetRoot)
	nextEnv = shell.EnvSet(nextEnv, "REPO", repo)
	nextEnv = prependPath(nextEnv, filepath.Join(runoqRoot, "scripts"))
	return nextEnv, 0
}

func (a *App) prepareAuth(ctx context.Context, env []string, runoqRoot string) ([]string, int) {
	token, err := shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
		Name: "bash",
		Args: []string{
			"-lc",
			`eval "$("$1" export-token)"; printf '%s' "$GH_TOKEN"`,
			"bash",
			filepath.Join(runoqRoot, "scripts", "gh-auth.sh"),
		},
		Dir: a.cwd,
		Env: env,
	})
	if err != nil {
		return nil, shell.ExitCodeFromError(err)
	}
	if token == "" {
		return nil, shell.Fail(a.stderr, "Failed to export GH_TOKEN.")
	}

	nextEnv := append([]string(nil), env...)
	nextEnv = shell.EnvSet(nextEnv, "GH_TOKEN", token)
	return nextEnv, 0
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

func (a *App) resolvePlanArgs(targetRoot string, args []string) ([]string, error) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return append([]string(nil), args...), nil
	}

	planFile, err := readProjectPlanFile(targetRoot)
	if err != nil {
		return nil, err
	}

	resolved := []string{planFile}
	resolved = append(resolved, args...)
	return resolved, nil
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

func (a *App) runScript(ctx context.Context, env []string, runoqRoot string, script string, args []string) int {
	req := shell.CommandRequest{
		Name:   filepath.Join(runoqRoot, "scripts", script),
		Args:   append([]string(nil), args...),
		Dir:    a.cwd,
		Env:    env,
		Stdout: a.stdout,
		Stderr: a.stderr,
	}
	if err := a.execCommand(ctx, req); err != nil {
		return shell.ExitCodeFromError(err)
	}
	return 0
}

func (a *App) runMaintenance(ctx context.Context, env []string, runoqRoot string) int {
	req := shell.CommandRequest{
		Name: "bash",
		Args: []string{
			"-lc",
			`source "$1/scripts/lib/common.sh"; claude_bin="${RUNOQ_CLAUDE_BIN:-claude}"; runoq::captured_exec claude "$TARGET_ROOT" "$claude_bin" --agent maintenance-reviewer --add-dir "$1"`,
			"bash",
			runoqRoot,
		},
		Dir:    a.cwd,
		Env:    env,
		Stdout: a.stdout,
		Stderr: a.stderr,
	}
	if err := a.execCommand(ctx, req); err != nil {
		return shell.ExitCodeFromError(err)
	}
	return 0
}

func (a *App) runTick(ctx context.Context, env []string, runoqRoot string) int {
	repo, _ := shell.EnvLookup(env, "REPO")
	targetRoot, _ := shell.EnvLookup(env, "TARGET_ROOT")

	// Read plan file from project config
	planFile, err := readProjectPlanFile(targetRoot)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	// Read labels from runoq config
	configPath, _ := shell.EnvLookup(env, "RUNOQ_CONFIG")
	planApprovedLabel := readConfigLabel(configPath, "planApproved")
	readyLabel := readConfigLabel(configPath, "ready")
	inProgressLabel := readConfigLabel(configPath, "inProgress")

	return orchestrator.RunTick(ctx, orchestrator.TickConfig{
		Repo:              repo,
		PlanFile:          planFile,
		RunoqRoot:         runoqRoot,
		PlanApprovedLabel: planApprovedLabel,
		ReadyLabel:        readyLabel,
		InProgressLabel:   inProgressLabel,
		Env:               env,
		ExecCommand:       a.execCommand,
		Stdout:            a.stdout,
		Stderr:            a.stderr,
	})
}

func readConfigLabel(configPath string, key string) string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	var cfg struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.Labels[key]
}

func (a *App) runLoop(ctx context.Context, env []string, runoqRoot string, args []string) int {
	backoff := 30
	maxWaitCycles := 0 // 0 = unlimited
	for i := 0; i < len(args); i++ {
		switch args[i] {
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
		}
	}

	loopCtx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	waitCycles := 0
	for {
		code := a.runTick(loopCtx, env, runoqRoot)

		select {
		case <-loopCtx.Done():
			return 0
		default:
		}

		switch code {
		case 0:
			// Work done, reset wait counter, loop immediately
			waitCycles = 0
		case 2:
			waitCycles++
			if maxWaitCycles > 0 && waitCycles >= maxWaitCycles {
				fmt.Fprintf(a.stderr, "stopping after %d consecutive wait cycles\n", waitCycles)
				return 0
			}
			fmt.Fprintf(a.stderr, "waiting %ds before next tick\n", backoff)
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
