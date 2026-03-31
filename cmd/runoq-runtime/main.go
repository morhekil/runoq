package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/saruman/runoq/internal/runtimecli"
	"github.com/saruman/runoq/internal/runtimedispatchsafety"
	"github.com/saruman/runoq/internal/runtimeissuequeue"
	"github.com/saruman/runoq/internal/runtimeorchestrator"
	"github.com/saruman/runoq/internal/runtimestate"
	"github.com/saruman/runoq/internal/runtimeverify"
	"github.com/saruman/runoq/internal/runtimeworktree"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	executablePath, err := os.Executable()
	if err != nil {
		executablePath = ""
	}

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "__state" {
		stateApp := runtimestate.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdin,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(stateApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__verify" {
		verifyApp := runtimeverify.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(verifyApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__worktree" {
		worktreeApp := runtimeworktree.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(worktreeApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__issue_queue" {
		queueApp := runtimeissuequeue.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(queueApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__dispatch_safety" {
		dispatchSafetyApp := runtimedispatchsafety.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(dispatchSafetyApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__orchestrator" {
		orchestratorApp := runtimeorchestrator.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(orchestratorApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__issue_runner" {
		os.Exit(runIssueRunnerCompat(
			context.Background(),
			args[1:],
			os.Environ(),
			cwd,
			os.Stdin,
			os.Stdout,
			os.Stderr,
		))
	}

	cliApp := runtimecli.New(
		args,
		os.Environ(),
		cwd,
		os.Stdout,
		os.Stderr,
		executablePath,
	)
	os.Exit(cliApp.Run(context.Background()))
}

func runIssueRunnerCompat(ctx context.Context, args []string, env []string, cwd string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	runoqRoot, ok := envLookup(env, "RUNOQ_ROOT")
	if !ok || strings.TrimSpace(runoqRoot) == "" {
		_, _ = fmt.Fprintln(stderr, "runoq: RUNOQ_ROOT is required for __issue_runner")
		return 1
	}

	scriptPath := filepath.Join(runoqRoot, "scripts", "issue-runner.sh")
	cmd := exec.CommandContext(ctx, "bash", append([]string{scriptPath}, args...)...)
	cmd.Dir = cwd
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = setEnv(env, "RUNOQ_ISSUE_RUNNER_IMPLEMENTATION", "shell")

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		_, _ = fmt.Fprintf(stderr, "runoq: issue-runner runtime shim failed: %v\n", err)
		return 1
	}
	return 0
}

func envLookup(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
}

func setEnv(env []string, key string, value string) []string {
	prefix := key + "="
	updated := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			updated = append(updated, prefix+value)
			replaced = true
			continue
		}
		updated = append(updated, entry)
	}
	if !replaced {
		updated = append(updated, prefix+value)
	}
	return updated
}
