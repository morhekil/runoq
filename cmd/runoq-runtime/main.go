package main

import (
	"context"
	"os"

	"github.com/saruman/runoq/internal/runtimecli"
	"github.com/saruman/runoq/internal/runtimedispatchsafety"
	"github.com/saruman/runoq/internal/runtimestate"
	"github.com/saruman/runoq/internal/runtimeverify"
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
