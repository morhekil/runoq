package orchestrator

import (
	"context"
	"fmt"
	"io"

	"github.com/saruman/runoq/internal/shell"
)

const usageText = `Usage:
  orchestrator.sh run <repo> --issue N [--dry-run]
  orchestrator.sh mention-triage <repo> <pr-number>
`

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	execCommand shell.CommandExecutor
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:        append([]string(nil), args...),
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      stdout,
		stderr:      stderr,
		execCommand: shell.RunCommand,
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
	if len(a.args) == 0 {
		a.printUsage(a.stderr)
		return 1
	}

	root := a.runoqRoot()
	if root == "" {
		return shell.Fail(a.stderr, "Unable to resolve RUNOQ_ROOT for runtime orchestrator.")
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

func (a *App) printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

func (a *App) logInfo(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stderr, "[orchestrator] "+format+"\n", args...)
}

func (a *App) logError(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stderr, "[orchestrator] ERROR: "+format+"\n", args...)
}
