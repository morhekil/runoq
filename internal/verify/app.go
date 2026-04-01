package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/saruman/runoq/internal/common"
)

const usageText = `Usage:
  verify.sh round <worktree> <branch> <base-sha> <payload-file>
  verify.sh integrate <worktree> <criteria-commit>
`

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	execCommand common.CommandExecutor
}

type groundTruth struct {
	CommitsPushed []string `json:"commits_pushed"`
	FilesChanged  []string `json:"files_changed"`
	FilesAdded    []string `json:"files_added"`
	FilesDeleted  []string `json:"files_deleted"`
}

type roundResult struct {
	OK            bool        `json:"ok"`
	ReviewAllowed bool        `json:"review_allowed"`
	Failures      []string    `json:"failures"`
	Actual        groundTruth `json:"actual"`
}

type integrateResult struct {
	OK       bool     `json:"ok"`
	Failures []string `json:"failures"`
}

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
	subcommand := ""
	if len(a.args) > 0 {
		subcommand = a.args[0]
	}

	switch subcommand {
	case "round":
		if len(a.args) != 5 {
			a.printUsage()
			return 1
		}
		return a.runRound(ctx, a.args[1], a.args[2], a.args[3], a.args[4])
	case "integrate":
		if len(a.args) != 3 {
			a.printUsage()
			return 1
		}
		return a.runIntegrate(ctx, a.args[1], a.args[2])
	default:
		a.printUsage()
		return 1
	}
}

func (a *App) runRound(ctx context.Context, worktree string, branch string, baseSHA string, payloadFile string) int {
	truth, err := a.groundTruth(ctx, worktree, baseSHA)
	if err != nil {
		return common.Failf(a.stderr, "Failed to compute ground truth: %v", err)
	}

	payload, err := a.readPayload(payloadFile)
	if err != nil {
		return common.Failf(a.stderr, "%v", err)
	}

	failures := make([]string, 0, 8)
	if len(truth.CommitsPushed) == 0 {
		failures = append(failures, "no new commits were created")
	}

	for _, sha := range payloadCommitsForExistenceCheck(payload) {
		if strings.TrimSpace(sha) == "" {
			continue
		}
		if !a.commitExists(ctx, worktree, sha) {
			failures = append(failures, fmt.Sprintf("missing commit %s", sha))
		}
	}

	if !payloadFileListsMatchTruth(payload, truth) {
		failures = append(failures, "file lists do not match ground truth")
	}

	localSHA, err := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
		Name: "git",
		Args: []string{"-C", worktree, "rev-parse", "HEAD"},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return common.Failf(a.stderr, "Failed to resolve local HEAD: %v", err)
	}

	remoteSHA, _ := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
		Name: "git",
		Args: []string{"-C", worktree, "ls-remote", "origin", branch},
		Dir:  a.cwd,
		Env:  a.env,
	})
	remoteSHA = parseRemoteSHA(remoteSHA)
	if remoteSHA == "" || remoteSHA != localSHA {
		failures = append(failures, "branch tip is not pushed to origin")
	}

	testCommand, buildCommand, err := a.verificationCommands()
	if err != nil {
		return common.Fail(a.stderr, err.Error())
	}

	if output, err := a.runCheckCommand(ctx, worktree, testCommand); err != nil {
		failures = append(failures, fmt.Sprintf("test command failed (`%s`). Last 30 lines of output:\n```\n%s\n```", testCommand, lastNLines(output, 30)))
	}
	if output, err := a.runCheckCommand(ctx, worktree, buildCommand); err != nil {
		failures = append(failures, fmt.Sprintf("build command failed (`%s`). Last 30 lines of output:\n```\n%s\n```", buildCommand, lastNLines(output, 30)))
	}

	if !rawBoolIsTrue(payload["tests_passed"]) {
		failures = append(failures, fmt.Sprintf("codex self-reported test failure (test_summary: %s)", rawStringOr(payload["test_summary"], "no details provided")))
	}
	if !rawBoolIsTrue(payload["build_passed"]) {
		failures = append(failures, fmt.Sprintf("codex self-reported build failure (notes: %s)", rawStringOr(payload["notes"], "no details provided")))
	}

	criteriaCommit := rawStringOr(payload["criteria_commit"], "")
	if criteriaCommit != "" {
		criteriaFiles := a.criteriaFiles(ctx, worktree, criteriaCommit)
		tampered := make([]string, 0, len(criteriaFiles))
		for _, cfile := range criteriaFiles {
			if cfile == "" {
				continue
			}
			if a.isCriteriaTampered(ctx, worktree, criteriaCommit, cfile) {
				tampered = append(tampered, cfile)
			}
		}
		if len(tampered) > 0 {
			failures = append(failures, "criteria tampered: "+strings.Join(tampered, ", "))
		}
	}

	res := roundResult{
		OK:            len(failures) == 0,
		ReviewAllowed: len(failures) == 0,
		Failures:      failures,
		Actual:        truth,
	}
	return common.WriteJSON(a.stdout, a.stderr, res)
}

func (a *App) runIntegrate(ctx context.Context, worktree string, criteriaCommit string) int {
	failures := make([]string, 0, 4)
	criteriaFiles := a.criteriaFiles(ctx, worktree, criteriaCommit)
	if len(criteriaFiles) == 0 {
		failures = append(failures, "no criteria files found in criteria commit")
	} else {
		for _, cfile := range criteriaFiles {
			if cfile == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(worktree, cfile)); err != nil {
				failures = append(failures, "criteria file missing: "+cfile)
				continue
			}
			if a.isCriteriaTampered(ctx, worktree, criteriaCommit, cfile) {
				failures = append(failures, "criteria tampered: "+cfile)
			}
		}
	}

	testCommand, _, err := a.verificationCommands()
	if err != nil {
		return common.Fail(a.stderr, err.Error())
	}
	if _, err := a.runCheckCommand(ctx, worktree, testCommand); err != nil {
		failures = append(failures, "test command failed")
	}

	return common.WriteJSON(a.stdout, a.stderr, integrateResult{
		OK:       len(failures) == 0,
		Failures: failures,
	})
}

func (a *App) readPayload(payloadFile string) (map[string]any, error) {
	data, err := os.ReadFile(payloadFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload file: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse payload file: %v", err)
	}
	return payload, nil
}

func (a *App) groundTruth(ctx context.Context, worktree string, baseSHA string) (groundTruth, error) {
	commitsOut, err := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
		Name: "git",
		Args: []string{"-C", worktree, "rev-list", "--reverse", baseSHA + "..HEAD"},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return groundTruth{}, err
	}
	diffOut, err := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
		Name: "git",
		Args: []string{"-C", worktree, "diff", "--name-status", baseSHA + "..HEAD"},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return groundTruth{}, err
	}

	gt := groundTruth{
		CommitsPushed: splitNonEmptyLines(commitsOut),
		FilesChanged:  []string{},
		FilesAdded:    []string{},
		FilesDeleted:  []string{},
	}

	for _, line := range splitNonEmptyLines(diffOut) {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		path := parts[len(parts)-1]
		switch status {
		case "A":
			gt.FilesAdded = append(gt.FilesAdded, path)
		case "D":
			gt.FilesDeleted = append(gt.FilesDeleted, path)
		default:
			gt.FilesChanged = append(gt.FilesChanged, path)
		}
	}

	return gt, nil
}

func (a *App) verificationCommands() (string, string, error) {
	configPath := ""
	if value, ok := common.EnvLookup(a.env, "RUNOQ_CONFIG"); ok && strings.TrimSpace(value) != "" {
		configPath = value
	} else if root, ok := common.EnvLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(root) != "" {
		configPath = filepath.Join(root, "config", "runoq.json")
	} else if strings.TrimSpace(a.cwd) != "" {
		configPath = filepath.Join(a.cwd, "config", "runoq.json")
	}

	if configPath == "" {
		return "", "", errors.New("RUNOQ_CONFIG is required")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read config: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", "", fmt.Errorf("failed to parse config: %v", err)
	}

	verification, _ := parsed["verification"].(map[string]any)
	testCommand := rawStringOr(verification["testCommand"], "")
	buildCommand := rawStringOr(verification["buildCommand"], "")
	if testCommand == "" || testCommand == "null" {
		return "", "", errors.New("verification.testCommand is not configured")
	}
	if buildCommand == "" || buildCommand == "null" {
		return "", "", errors.New("verification.buildCommand is not configured")
	}
	return testCommand, buildCommand, nil
}

func (a *App) runCheckCommand(ctx context.Context, worktree string, command string) (string, error) {
	script := fmt.Sprintf("cd %s && %s", shellQuote(worktree), command)
	var output bytes.Buffer
	err := a.execCommand(ctx, common.CommandRequest{
		Name:   "bash",
		Args:   []string{"-lc", script},
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: &output,
		Stderr: &output,
	})
	return strings.TrimRight(output.String(), "\n"), err
}

func (a *App) commitExists(ctx context.Context, worktree string, sha string) bool {
	err := a.execCommand(ctx, common.CommandRequest{
		Name:   "git",
		Args:   []string{"-C", worktree, "rev-parse", "--verify", sha + "^{commit}"},
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	return err == nil
}

func (a *App) criteriaFiles(ctx context.Context, worktree string, criteriaCommit string) []string {
	out, err := common.CommandOutput(ctx, a.execCommand, common.CommandRequest{
		Name: "git",
		Args: []string{"-C", worktree, "diff-tree", "--no-commit-id", "--name-only", "-r", criteriaCommit},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return []string{}
	}
	return splitNonEmptyLines(out)
}

func (a *App) isCriteriaTampered(ctx context.Context, worktree string, criteriaCommit string, cfile string) bool {
	err := a.execCommand(ctx, common.CommandRequest{
		Name:   "git",
		Args:   []string{"-C", worktree, "diff", "--quiet", criteriaCommit, "HEAD", "--", cfile},
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	return err != nil
}

func payloadCommitsForExistenceCheck(payload map[string]any) []string {
	raw := payload["commits_pushed"]
	items, ok := raw.([]any)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if value, ok := item.(string); ok {
			out = append(out, value)
		}
	}
	return out
}

func payloadFileListsMatchTruth(payload map[string]any, truth groundTruth) bool {
	changed, okChanged := rawStringSlice(payload["files_changed"])
	added, okAdded := rawStringSlice(payload["files_added"])
	deleted, okDeleted := rawStringSlice(payload["files_deleted"])
	if !okChanged || !okAdded || !okDeleted {
		return false
	}
	return slices.Equal(changed, truth.FilesChanged) &&
		slices.Equal(added, truth.FilesAdded) &&
		slices.Equal(deleted, truth.FilesDeleted)
}

func rawStringSlice(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		typed, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, typed)
	}
	return out, true
}

func rawStringOr(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(typed)
	}
}

func rawBoolIsTrue(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func splitNonEmptyLines(input string) []string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return []string{}
	}
	lines := strings.Split(trimmed, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseRemoteSHA(input string) string {
	for _, line := range splitNonEmptyLines(input) {
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			return fields[0]
		}
	}
	return ""
}

func lastNLines(input string, n int) string {
	if n <= 0 {
		return ""
	}
	trimmed := strings.TrimRight(input, "\n")
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func shellQuote(input string) string {
	if input == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(input, "'", `'"'"'`) + "'"
}

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}
