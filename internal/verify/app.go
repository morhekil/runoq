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

	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
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
	execCommand shell.CommandExecutor
	openRepo    func(ctx context.Context, root string) gitops.Repo
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

// NewDirect creates an App for direct Go calls (not subprocess).
func NewDirect(env []string, cwd string, logWriter io.Writer) *App {
	stderr := io.Writer(io.Discard)
	if logWriter != nil {
		stderr = logWriter
	}
	a := &App{
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      io.Discard,
		stderr:      stderr,
		execCommand: shell.RunCommand,
	}
	a.openRepo = func(ctx context.Context, root string) gitops.Repo {
		return gitops.OpenCLI(ctx, root, a.execCommand)
	}
	return a
}

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer) *App {
	a := &App{
		args:        append([]string(nil), args...),
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      stdout,
		stderr:      stderr,
		execCommand: shell.RunCommand,
	}
	a.openRepo = func(ctx context.Context, root string) gitops.Repo {
		return gitops.OpenCLI(ctx, root, a.execCommand)
	}
	return a
}

func (a *App) SetCommandExecutor(execFn shell.CommandExecutor) {
	if execFn == nil {
		a.execCommand = shell.RunCommand
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
		return shell.Failf(a.stderr, "Failed to compute ground truth: %v", err)
	}

	payload, err := a.readPayload(payloadFile)
	if err != nil {
		return shell.Failf(a.stderr, "%v", err)
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

	repo := a.openRepo(ctx, worktree)

	localSHA, err := repo.ResolveHEAD()
	if err != nil {
		return shell.Failf(a.stderr, "Failed to resolve local HEAD: %v", err)
	}

	remoteSHA, remoteExists, _ := repo.RemoteRefExists("origin", branch)
	if !remoteExists || remoteSHA != localSHA {
		failures = append(failures, "branch tip is not pushed to origin")
	}

	testCommand, buildCommand, err := a.verificationCommands()
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
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
	return shell.WriteJSON(a.stdout, a.stderr, res)
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
		return shell.Fail(a.stderr, err.Error())
	}
	if _, err := a.runCheckCommand(ctx, worktree, testCommand); err != nil {
		failures = append(failures, "test command failed")
	}

	return shell.WriteJSON(a.stdout, a.stderr, integrateResult{
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
	repo := a.openRepo(ctx, worktree)

	commits, err := repo.CommitLog(baseSHA, "HEAD")
	if err != nil {
		return groundTruth{}, err
	}
	changes, err := repo.DiffNameStatus(baseSHA, "HEAD")
	if err != nil {
		return groundTruth{}, err
	}

	shas := make([]string, 0, len(commits))
	for _, c := range commits {
		shas = append(shas, c.SHA)
	}

	gt := groundTruth{
		CommitsPushed: shas,
		FilesChanged:  []string{},
		FilesAdded:    []string{},
		FilesDeleted:  []string{},
	}
	if len(gt.CommitsPushed) == 0 {
		gt.CommitsPushed = []string{}
	}

	for _, fc := range changes {
		switch fc.Status {
		case "A":
			gt.FilesAdded = append(gt.FilesAdded, fc.Path)
		case "D":
			gt.FilesDeleted = append(gt.FilesDeleted, fc.Path)
		default:
			gt.FilesChanged = append(gt.FilesChanged, fc.Path)
		}
	}

	return gt, nil
}

func (a *App) verificationCommands() (string, string, error) {
	configPath := ""
	if value, ok := shell.EnvLookup(a.env, "RUNOQ_CONFIG"); ok && strings.TrimSpace(value) != "" {
		configPath = value
	} else if root, ok := shell.EnvLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(root) != "" {
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
	err := a.execCommand(ctx, shell.CommandRequest{
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
	repo := a.openRepo(ctx, worktree)
	exists, _ := repo.CommitExists(sha)
	return exists
}

func (a *App) criteriaFiles(ctx context.Context, worktree string, criteriaCommit string) []string {
	repo := a.openRepo(ctx, worktree)
	files, err := repo.DiffTreeFiles(criteriaCommit)
	if err != nil {
		return []string{}
	}
	if files == nil {
		return []string{}
	}
	return files
}

func (a *App) isCriteriaTampered(ctx context.Context, worktree string, criteriaCommit string, cfile string) bool {
	repo := a.openRepo(ctx, worktree)
	changed, _ := repo.FileChanged(criteriaCommit, "HEAD", cfile)
	return changed
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

// IntegrateResult holds the result of an integration verification.
type IntegrateResult struct {
	OK       bool
	Failures []string
}

// IntegrateVerify runs the integrate verification directly (no subprocess).
func (a *App) IntegrateVerify(ctx context.Context, worktreePath string, criteriaCommit string) (IntegrateResult, error) {
	failures := make([]string, 0, 4)
	criteriaFiles := a.criteriaFiles(ctx, worktreePath, criteriaCommit)
	if len(criteriaFiles) == 0 {
		failures = append(failures, "no criteria files found in criteria commit")
	} else {
		for _, cfile := range criteriaFiles {
			if cfile == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(worktreePath, cfile)); err != nil {
				failures = append(failures, "criteria file missing: "+cfile)
				continue
			}
			if a.isCriteriaTampered(ctx, worktreePath, criteriaCommit, cfile) {
				failures = append(failures, "criteria tampered: "+cfile)
			}
		}
	}

	testCommand, _, err := a.verificationCommands()
	if err != nil {
		return IntegrateResult{OK: false, Failures: []string{err.Error()}}, err
	}
	if _, err := a.runCheckCommand(ctx, worktreePath, testCommand); err != nil {
		failures = append(failures, "test command failed")
	}

	return IntegrateResult{
		OK:       len(failures) == 0,
		Failures: failures,
	}, nil
}

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}
