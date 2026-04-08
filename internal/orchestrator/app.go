package orchestrator

import (
	"context"
	"fmt"
	"io"

	"github.com/saruman/runoq/internal/dispatchsafety"
	"github.com/saruman/runoq/internal/issuequeue"
	"github.com/saruman/runoq/internal/issuerunner"
	"github.com/saruman/runoq/internal/shell"
	"github.com/saruman/runoq/internal/verify"
	"github.com/saruman/runoq/internal/worktree"
)

const usageText = `Usage:
  orchestrator.sh run <repo> --issue N [--dry-run]
  orchestrator.sh mention-triage <repo> <pr-number>
`

// OrchestratorConfig holds the config values the orchestrator needs.
// Populated by the CLI from the config file — the orchestrator never reads config files.
type OrchestratorConfig struct {
	MaxRounds        int
	MaxTokenBudget   int
	AutoMergeEnabled bool
	Reviewers        []string
	IdentityHandle   string
	ReadyLabel       string

	// Label config for sub-apps (issuequeue, dispatchsafety).
	// When empty, sub-apps fall back to loading config from RUNOQ_CONFIG.
	InProgressLabel string
	DoneLabel       string
	NeedsReviewLabel string
	BlockedLabel    string

	// Naming config for worktree and branch creation.
	BranchPrefix   string
	WorktreePrefix string
}

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	logWriter   io.Writer
	execCommand shell.CommandExecutor
	cfg         OrchestratorConfig

	worktreeApp       *worktree.App
	issueQueueApp     *issuequeue.App
	verifyApp         *verify.App
	issueRunnerApp    *issuerunner.App
	dispatchSafetyApp *dispatchsafety.App
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

func (a *App) SetConfig(cfg OrchestratorConfig) {
	a.cfg = cfg
}

func (a *App) SetLogWriter(w io.Writer) {
	a.logWriter = w
}

func (a *App) SetCommandExecutor(execFn shell.CommandExecutor) {
	if execFn == nil {
		a.execCommand = shell.RunCommand
		return
	}
	a.execCommand = execFn
}

func (a *App) SetWorktreeApp(app *worktree.App) {
	a.worktreeApp = app
}

func (a *App) SetIssueQueueApp(app *issuequeue.App) {
	a.issueQueueApp = app
}

func (a *App) SetVerifyApp(app *verify.App) {
	a.verifyApp = app
}

func (a *App) SetIssueRunnerApp(app *issuerunner.App) {
	a.issueRunnerApp = app
}

func (a *App) SetDispatchSafetyApp(app *dispatchsafety.App) {
	a.dispatchSafetyApp = app
}

// ensureSubApps lazily initializes sub-apps that haven't been set.
// Sub-apps share the orchestrator's command executor and env.
func (a *App) ensureSubApps() {
	cfg := a.cfg

	if a.worktreeApp == nil {
		wt := worktree.NewDirect(worktree.Naming{
			BranchPrefix:   cfg.BranchPrefix,
			WorktreePrefix: cfg.WorktreePrefix,
		}, a.cwd, a.stderr)
		wt.SetCommandExecutor(a.execCommand)
		a.worktreeApp = wt
	}
	if a.issueQueueApp == nil {
		iq := issuequeue.New(nil, a.env, a.cwd, io.Discard, a.stderr)
		iq.SetCommandExecutor(a.execCommand)
		if cfg.ReadyLabel != "" {
			iq.SetLabels(
				cfg.ReadyLabel,
				orDefault(cfg.InProgressLabel, "runoq:in-progress"),
				orDefault(cfg.DoneLabel, "runoq:done"),
				orDefault(cfg.NeedsReviewLabel, "runoq:needs-review"),
				orDefault(cfg.BlockedLabel, "runoq:blocked"),
			)
		}
		a.issueQueueApp = iq
	}
	if a.verifyApp == nil {
		v := verify.NewDirect(a.env, a.cwd, a.stderr)
		v.SetCommandExecutor(a.execCommand)
		a.verifyApp = v
	}
	if a.issueRunnerApp == nil {
		ir := issuerunner.NewDirect(a.env, a.cwd, a.stderr)
		ir.SetCommandExecutor(a.execCommand)
		a.issueRunnerApp = ir
	}
	if a.dispatchSafetyApp == nil {
		ds := dispatchsafety.NewDirect(a.env, a.cwd, a.stderr)
		ds.SetCommandExecutor(a.execCommand)
		if cfg.ReadyLabel != "" {
			ds.SetConfig(dispatchsafety.DispatchConfig{
				ReadyLabel:      cfg.ReadyLabel,
				InProgressLabel: orDefault(cfg.InProgressLabel, "runoq:in-progress"),
				DoneLabel:       orDefault(cfg.DoneLabel, "runoq:done"),
				NeedsReview:     orDefault(cfg.NeedsReviewLabel, "runoq:needs-review"),
				Blocked:         orDefault(cfg.BlockedLabel, "runoq:blocked"),
				BranchPrefix:    cfg.BranchPrefix,
				WorktreePrefix:  cfg.WorktreePrefix,
			})
		}
		a.dispatchSafetyApp = ds
	}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (a *App) Run(ctx context.Context) int {
	a.ensureSubApps()

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
