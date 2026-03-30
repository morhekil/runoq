package runtimecli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const usageText = `Usage:
  runoq init
  runoq plan <file>
  runoq run [--issue N] [--dry-run]
  runoq report <summary|issue|cost> [...]
  runoq maintenance
`

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
	args           []string
	env            []string
	cwd            string
	stdout         io.Writer
	stderr         io.Writer
	executablePath string
	execCommand    commandExecutor
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer, executablePath string) *App {
	return &App{
		args:           append([]string(nil), args...),
		env:            append([]string(nil), env...),
		cwd:            cwd,
		stdout:         stdout,
		stderr:         stderr,
		executablePath: executablePath,
		execCommand:    runCommand,
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
	runoqRoot, env, ok := a.resolveRuntimeEnv()
	if !ok {
		return a.fail("Unable to resolve RUNOQ_ROOT for runtime mode.")
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
		if len(args) == 0 {
			return a.fail("Usage: runoq plan <file> [--auto-confirm] [--dry-run]")
		}
		targetEnv, code := a.prepareTargetContext(ctx, runoqRoot, env)
		if code != 0 {
			return code
		}
		repo, _ := envLookup(targetEnv, "REPO")
		planArgs := append([]string{repo}, args...)
		return a.runScript(ctx, targetEnv, runoqRoot, "plan.sh", planArgs)
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
		return a.runScript(ctx, targetEnv, runoqRoot, "report.sh", args)
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
	runoqRoot, ok := envLookup(env, "RUNOQ_ROOT")
	if !ok || strings.TrimSpace(runoqRoot) == "" {
		runoqRoot = a.fallbackRoot()
	}
	if strings.TrimSpace(runoqRoot) == "" {
		return "", nil, false
	}

	env = envSet(env, "RUNOQ_ROOT", runoqRoot)
	configValue, hasConfig := envLookup(env, "RUNOQ_CONFIG")
	if !hasConfig || configValue == "" {
		env = envSet(env, "RUNOQ_CONFIG", filepath.Join(runoqRoot, "config", "runoq.json"))
	}
	return runoqRoot, env, true
}

func (a *App) fallbackRoot() string {
	if a.cwd != "" && fileExists(filepath.Join(a.cwd, "scripts", "lib", "common.sh")) {
		return a.cwd
	}
	if a.executablePath == "" {
		return ""
	}
	base := filepath.Dir(a.executablePath)
	candidate := filepath.Clean(filepath.Join(base, ".."))
	if fileExists(filepath.Join(candidate, "scripts", "lib", "common.sh")) {
		return candidate
	}
	return ""
}

func (a *App) prepareTargetContext(ctx context.Context, runoqRoot string, env []string) ([]string, int) {
	targetRoot, ok := envLookup(env, "TARGET_ROOT")
	if !ok || strings.TrimSpace(targetRoot) == "" {
		var err error
		targetRoot, err = a.commandOutput(ctx, commandRequest{
			Name: "git",
			Args: []string{"rev-parse", "--show-toplevel"},
			Dir:  a.cwd,
			Env:  env,
		})
		if err != nil {
			return nil, a.fail("Run runoq from inside a git repository.")
		}
	}

	repo, err := a.resolveRepo(ctx, env, targetRoot)
	if err != nil {
		return nil, a.fail(err.Error())
	}

	nextEnv := append([]string(nil), env...)
	nextEnv = envSet(nextEnv, "TARGET_ROOT", targetRoot)
	nextEnv = envSet(nextEnv, "REPO", repo)
	nextEnv = prependPath(nextEnv, filepath.Join(runoqRoot, "scripts"))
	return nextEnv, 0
}

func (a *App) prepareAuth(ctx context.Context, env []string, runoqRoot string) ([]string, int) {
	token, err := a.commandOutput(ctx, commandRequest{
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
		return nil, exitCodeFromError(err)
	}
	if token == "" {
		return nil, a.fail("Failed to export GH_TOKEN.")
	}

	nextEnv := append([]string(nil), env...)
	nextEnv = envSet(nextEnv, "GH_TOKEN", token)
	return nextEnv, 0
}

func (a *App) resolveRepo(ctx context.Context, env []string, targetRoot string) (string, error) {
	if value, ok := envLookup(env, "RUNOQ_REPO"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}

	originURL, err := a.commandOutput(ctx, commandRequest{
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

func (a *App) runScript(ctx context.Context, env []string, runoqRoot string, script string, args []string) int {
	req := commandRequest{
		Name:   filepath.Join(runoqRoot, "scripts", script),
		Args:   append([]string(nil), args...),
		Dir:    a.cwd,
		Env:    env,
		Stdout: a.stdout,
		Stderr: a.stderr,
	}
	if err := a.execCommand(ctx, req); err != nil {
		return exitCodeFromError(err)
	}
	return 0
}

func (a *App) runMaintenance(ctx context.Context, env []string, runoqRoot string) int {
	req := commandRequest{
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
		return exitCodeFromError(err)
	}
	return 0
}

func (a *App) commandOutput(ctx context.Context, req commandRequest) (string, error) {
	var stdout bytes.Buffer
	req.Stdout = &stdout
	req.Stderr = a.stderr
	if err := a.execCommand(ctx, req); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (a *App) fail(message string) int {
	_, _ = fmt.Fprintf(a.stderr, "runoq: %s\n", message)
	return 1
}

func (a *App) printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
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

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}

	var exitCoder interface{ ExitCode() int }
	if errors.As(err, &exitCoder) {
		code := exitCoder.ExitCode()
		if code > 0 {
			return code
		}
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
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

func envSet(env []string, key string, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	filtered = append(filtered, key+"="+value)
	return filtered
}

func prependPath(env []string, head string) []string {
	current, _ := envLookup(env, "PATH")
	if current == "" {
		return envSet(env, "PATH", head)
	}
	return envSet(env, "PATH", head+string(os.PathListSeparator)+current)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
