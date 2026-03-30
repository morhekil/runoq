package main

import (
	"context"
	"os"

	"github.com/saruman/runoq/internal/runtimecli"
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

	app := runtimecli.New(
		os.Args[1:],
		os.Environ(),
		cwd,
		os.Stdout,
		os.Stderr,
		executablePath,
	)
	os.Exit(app.Run(context.Background()))
}
