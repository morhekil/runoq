package issuerunner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/saruman/runoq/internal/common"
)

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	execCommand common.CommandExecutor
}

const usageText = `Usage:
  issue-runner run <payload-json-file>
`

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:        append([]string(nil), args...),
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      stdout,
		stderr:      stderr,
		execCommand: common.RunCommand,
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
	if len(a.args) < 1 {
		a.printUsage()
		return 1
	}
	switch a.args[0] {
	case "run":
		if len(a.args) < 2 {
			return common.Fail(a.stderr, "usage: issue-runner run <payload-json-file>")
		}
		return a.runIssue(ctx, a.args[1])
	default:
		return common.Failf(a.stderr, "unknown command: %s", a.args[0])
	}
}

func (a *App) runIssue(ctx context.Context, payloadPath string) int {
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		return common.Failf(a.stderr, "failed to read payload: %v", err)
	}

	var input inputPayload
	if err := json.Unmarshal(data, &input); err != nil {
		return common.Failf(a.stderr, "failed to parse payload: %v", err)
	}

	if input.MaxRounds <= 0 {
		input.MaxRounds = 3
	}
	if input.Round <= 0 {
		input.Round = 1
	}
	if input.PreviousChecklist == "" {
		input.PreviousChecklist = "None — first round"
	}

	// Read spec requirements
	specRequirements := ""
	if input.SpecPath != "" {
		if specData, err := os.ReadFile(input.SpecPath); err == nil {
			specRequirements = strings.TrimSpace(string(specData))
		}
	}

	// Initialize log directory
	logDir := input.LogDir
	if logDir == "" {
		logDir = filepath.Join(a.cwd, "log", fmt.Sprintf("issue-%d-%d", input.IssueNumber, os.Getpid()))
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return common.Failf(a.stderr, "failed to create log dir: %v", err)
		}
	}

	// Get baseline hash
	baseline, err := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
		Name: "git",
		Args: []string{"-C", input.Worktree, "log", "-1", "--format=%H"},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return common.Failf(a.stderr, "failed to get baseline: %v", err)
	}

	state := &roundState{
		round:             input.Round,
		logDir:            logDir,
		baseline:          baseline,
		headHash:          baseline,
		cumulativeTokens:  input.CumulativeTokens,
		previousChecklist: input.PreviousChecklist,
	}

	// Run the development loop
	result := a.developmentLoop(ctx, &input, state, specRequirements)

	// Emit output
	return common.WriteJSON(a.stdout, a.stderr, result)
}

func (a *App) developmentLoop(_ context.Context, input *inputPayload, state *roundState, specRequirements string) *outputPayload {
	// TODO: Implement multi-round development loop
	// For now, return a stub that indicates the Go implementation is active
	return &outputPayload{
		Status:           "fail",
		Round:            state.round,
		TotalRounds:      input.MaxRounds,
		LogDir:           state.logDir,
		BaselineHash:     state.baseline,
		HeadHash:         state.headHash,
		CumulativeTokens: state.cumulativeTokens,
		SpecRequirements: specRequirements,
		Summary:          "issue-runner Go implementation: development loop not yet implemented",
	}
}

func (a *App) printUsage() {
	io.WriteString(a.stderr, usageText)
}
