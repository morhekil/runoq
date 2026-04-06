package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/saruman/runoq/internal/common"
	"github.com/saruman/runoq/internal/report"
)

const usageText = `Usage:
  runoq init
  runoq plan [file]
  runoq tick
  runoq run [--issue N] [--dry-run]
  runoq report <summary|issue|cost> [...]
  runoq maintenance
`

type App struct {
	args           []string
	env            []string
	cwd            string
	stdout         io.Writer
	stderr         io.Writer
	executablePath string
	execCommand    common.CommandExecutor
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer, executablePath string) *App {
	return &App{
		args:           append([]string(nil), args...),
		env:            append([]string(nil), env...),
		cwd:            cwd,
		stdout:         stdout,
		stderr:         stderr,
		executablePath: executablePath,
		execCommand:    common.RunCommand,
	}
}

func (a *App) SetCommandExecutor(execFn common.CommandExecutor) {
	if execFn == nil {
		a.execCommand = common.RunCommand
		return
	}
	a.execCommand = execFn
}

func (a *App) Run(ctx context.Context) int {
	runoqRoot, env, ok := a.resolveRuntimeEnv()
	if !ok {
		return common.Fail(a.stderr, "Unable to resolve RUNOQ_ROOT for runtime mode.")
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
		repo, _ := common.EnvLookup(targetEnv, "REPO")
		targetRoot, _ := common.EnvLookup(targetEnv, "TARGET_ROOT")
		planArgs, err := a.resolvePlanArgs(targetRoot, args)
		if err != nil {
			return common.Fail(a.stderr, err.Error())
		}
		planArgs = append([]string{repo}, planArgs...)
		return a.runScript(ctx, targetEnv, runoqRoot, "plan.sh", planArgs)
	case "tick":
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		return a.runScript(ctx, targetEnv, runoqRoot, "tick.sh", args)
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
	runoqRoot, ok := common.EnvLookup(env, "RUNOQ_ROOT")
	if !ok || strings.TrimSpace(runoqRoot) == "" {
		runoqRoot = a.fallbackRoot()
	}
	if strings.TrimSpace(runoqRoot) == "" {
		return "", nil, false
	}

	env = common.EnvSet(env, "RUNOQ_ROOT", runoqRoot)
	configValue, hasConfig := common.EnvLookup(env, "RUNOQ_CONFIG")
	if !hasConfig || configValue == "" {
		env = common.EnvSet(env, "RUNOQ_CONFIG", filepath.Join(runoqRoot, "config", "runoq.json"))
	}
	return runoqRoot, env, true
}

func (a *App) fallbackRoot() string {
	if a.cwd != "" && common.FileExists(filepath.Join(a.cwd, "scripts", "lib", "common.sh")) {
		return a.cwd
	}
	if a.executablePath == "" {
		return ""
	}
	base := filepath.Dir(a.executablePath)
	candidate := filepath.Clean(filepath.Join(base, ".."))
	if common.FileExists(filepath.Join(candidate, "scripts", "lib", "common.sh")) {
		return candidate
	}
	return ""
}

func (a *App) prepareTargetContext(ctx context.Context, runoqRoot string, env []string) ([]string, int) {
	targetRoot, ok := common.EnvLookup(env, "TARGET_ROOT")
	if !ok || strings.TrimSpace(targetRoot) == "" {
		var err error
		targetRoot, err = common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
			Name: "git",
			Args: []string{"rev-parse", "--show-toplevel"},
			Dir:  a.cwd,
			Env:  env,
		})
		if err != nil {
			return nil, common.Fail(a.stderr, "Run runoq from inside a git repository.")
		}
	}

	repo, err := a.resolveRepo(ctx, env, targetRoot)
	if err != nil {
		return nil, common.Fail(a.stderr, err.Error())
	}

	nextEnv := append([]string(nil), env...)
	nextEnv = common.EnvSet(nextEnv, "TARGET_ROOT", targetRoot)
	nextEnv = common.EnvSet(nextEnv, "REPO", repo)
	nextEnv = prependPath(nextEnv, filepath.Join(runoqRoot, "scripts"))
	return nextEnv, 0
}

func (a *App) prepareAuth(ctx context.Context, env []string, runoqRoot string) ([]string, int) {
	token, err := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
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
		return nil, common.ExitCodeFromError(err)
	}
	if token == "" {
		return nil, common.Fail(a.stderr, "Failed to export GH_TOKEN.")
	}

	nextEnv := append([]string(nil), env...)
	nextEnv = common.EnvSet(nextEnv, "GH_TOKEN", token)
	return nextEnv, 0
}

func (a *App) resolveRepo(ctx context.Context, env []string, targetRoot string) (string, error) {
	if value, ok := common.EnvLookup(env, "RUNOQ_REPO"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}

	originURL, err := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
		Name: "git",
		Args: []string{"-C", targetRoot, "remote", "get-url", "origin"},
		Dir:  a.cwd,
		Env:  env,
	})
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
		return "", errors.New("Plan file not configured: target repository root is unknown")
	}

	configPath := filepath.Join(targetRoot, "runoq.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("Plan file not configured: missing %s. Run `runoq init --plan <path>` or pass a plan file explicitly.", configPath)
		}
		return "", fmt.Errorf("Plan file not configured: failed to read %s: %w", configPath, err)
	}

	var cfg struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("Plan file not configured: invalid JSON in %s: %w", configPath, err)
	}
	if strings.TrimSpace(cfg.Plan) == "" {
		return "", fmt.Errorf("Plan file not configured: %s is missing a non-empty `plan` value.", configPath)
	}
	return cfg.Plan, nil
}

func (a *App) runScript(ctx context.Context, env []string, runoqRoot string, script string, args []string) int {
	req := common.CommandRequest{
		Name:   filepath.Join(runoqRoot, "scripts", script),
		Args:   append([]string(nil), args...),
		Dir:    a.cwd,
		Env:    env,
		Stdout: a.stdout,
		Stderr: a.stderr,
	}
	if err := a.execCommand(ctx, req); err != nil {
		return common.ExitCodeFromError(err)
	}
	return 0
}

func (a *App) runMaintenance(ctx context.Context, env []string, runoqRoot string) int {
	req := common.CommandRequest{
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
		return common.ExitCodeFromError(err)
	}
	return 0
}

func (a *App) printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

func prependPath(env []string, head string) []string {
	current, _ := common.EnvLookup(env, "PATH")
	if current == "" {
		return common.EnvSet(env, "PATH", head)
	}
	return common.EnvSet(env, "PATH", head+string(os.PathListSeparator)+current)
}
