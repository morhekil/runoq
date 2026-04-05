package main

import (
	"context"
	"os"

	"github.com/saruman/runoq/internal/cli"
	"github.com/saruman/runoq/internal/dispatchsafety"
	"github.com/saruman/runoq/internal/issuequeue"
	"github.com/saruman/runoq/internal/issuerunner"
	"github.com/saruman/runoq/internal/orchestrator"
	"github.com/saruman/runoq/internal/state"
	"github.com/saruman/runoq/internal/verify"
	"github.com/saruman/runoq/internal/worktree"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if runoqCWD := os.Getenv("RUNOQ_CWD"); runoqCWD != "" {
		cwd = runoqCWD
	}

	executablePath, err := os.Executable()
	if err != nil {
		executablePath = ""
	}

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "__state" {
		stateApp := state.New(
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
		verifyApp := verify.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(verifyApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__worktree" {
		worktreeApp := worktree.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(worktreeApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__issue_queue" {
		queueApp := issuequeue.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(queueApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__dispatch_safety" {
		dispatchSafetyApp := dispatchsafety.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(dispatchSafetyApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__issue_runner" {
		issueRunnerApp := issuerunner.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(issueRunnerApp.Run(context.Background()))
	}
	if len(args) > 0 && args[0] == "__orchestrator" {
		orchestratorApp := orchestrator.New(
			args[1:],
			os.Environ(),
			cwd,
			os.Stdout,
			os.Stderr,
		)
		os.Exit(orchestratorApp.Run(context.Background()))
	}

	cliApp := cli.New(
		args,
		os.Environ(),
		cwd,
		os.Stdout,
		os.Stderr,
		executablePath,
	)
	os.Exit(cliApp.Run(context.Background()))
}
