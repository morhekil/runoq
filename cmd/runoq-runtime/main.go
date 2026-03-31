package main

import (
	"context"
	"os"

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
