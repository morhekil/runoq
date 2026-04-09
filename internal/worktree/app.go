package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
)

const usageText = `Usage:
  worktree.sh create <issue-number> <title>
  worktree.sh remove <issue-number>
  worktree.sh inspect <issue-number>
  worktree.sh branch-name <issue-number> <title>
`

// Naming holds the config values the worktree package needs.
type Naming struct {
	BranchPrefix   string
	WorktreePrefix string
	AppSlug        string
}

// CreateResult holds the result of creating a worktree.
type CreateResult struct {
	Branch   string
	Worktree string
	BaseRef  string
}

type App struct {
	args        []string
	env         []string
	cwd         string
	naming      Naming
	stdout      io.Writer
	stderr      io.Writer
	execCommand shell.CommandExecutor
	repo        gitops.Repo
}

type config struct {
	BranchPrefix   string `json:"branchPrefix"`
	WorktreePrefix string `json:"worktreePrefix"`
	Identity       struct {
		AppSlug string `json:"appSlug"`
	} `json:"identity"`
}

var (
	nonAlnumPattern = regexp.MustCompile(`[^a-z0-9]+`)
	multiDash       = regexp.MustCompile(`-+`)
)

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
	subcommand := ""
	if len(a.args) > 0 {
		subcommand = a.args[0]
	}

	switch subcommand {
	case "create":
		if len(a.args) != 3 {
			a.printUsage()
			return 1
		}
		return a.runCreate(ctx, a.args[1], a.args[2])
	case "remove":
		if len(a.args) != 2 {
			a.printUsage()
			return 1
		}
		return a.runRemove(ctx, a.args[1])
	case "inspect":
		if len(a.args) != 2 {
			a.printUsage()
			return 1
		}
		return a.runInspect(ctx, a.args[1])
	case "branch-name":
		if len(a.args) != 3 {
			a.printUsage()
			return 1
		}
		return a.runBranchName(a.args[1], a.args[2])
	default:
		a.printUsage()
		return 1
	}
}

func (a *App) runCreate(ctx context.Context, issue string, title string) int {
	cfg, err := a.loadConfig()
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read config: %v", err)
	}

	targetRoot, code := a.targetRoot(ctx)
	if code != 0 {
		return code
	}
	a.repo = gitops.OpenCLI(ctx, targetRoot, a.execCommand)

	branch := branchName(cfg.BranchPrefix, issue, title)
	path, err := worktreePath(cfg.WorktreePrefix, targetRoot, issue)
	if err != nil {
		return shell.Failf(a.stderr, "Failed to resolve worktree path: %v", err)
	}
	baseRef := defaultBaseRef(a.env)
	if baseRef == "" {
		// Detect remote default branch
		remoteBranch, err := a.repo.DefaultBranch("origin")
		if err != nil {
			return shell.Failf(a.stderr, "Failed to detect default branch: %v", err)
		}
		baseRef = "origin/" + remoteBranch

		// Fetch the detected branch
		if err := a.repo.Fetch("origin", remoteBranch); err != nil {
			return shell.Failf(a.stderr, "Failed to fetch origin %s: %v", remoteBranch, err)
		}
	}

	a.log("worktree", fmt.Sprintf("create: source_ref=%s target_path=%s branch=%s", baseRef, path, branch))

	if _, err := os.Lstat(path); err == nil {
		return shell.Failf(a.stderr, "Worktree already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return shell.Failf(a.stderr, "Failed to inspect worktree path %s: %v", path, err)
	}

	// Prune stale worktree metadata (directory removed but git still tracks it)
	_ = a.repo.WorktreePrune()

	// Delete stale local branch from a previous killed run
	_ = a.repo.DeleteBranch(branch)

	if err := a.repo.WorktreeAdd(path, branch, baseRef); err != nil {
		return shell.Failf(a.stderr, "Failed to create worktree: %v", err)
	}

	_ = a.configureGitBotIdentity(ctx, path, cfg.Identity.AppSlug, targetRoot)

	return shell.WriteJSON(a.stdout, a.stderr, struct {
		Branch   string `json:"branch"`
		Worktree string `json:"worktree"`
		BaseRef  string `json:"base_ref"`
	}{
		Branch:   branch,
		Worktree: path,
		BaseRef:  baseRef,
	})
}

func (a *App) runRemove(ctx context.Context, issue string) int {
	cfg, err := a.loadConfig()
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read config: %v", err)
	}

	targetRoot, code := a.targetRoot(ctx)
	if code != 0 {
		return code
	}
	a.repo = gitops.OpenCLI(ctx, targetRoot, a.execCommand)

	path, err := worktreePath(cfg.WorktreePrefix, targetRoot, issue)
	if err != nil {
		return shell.Failf(a.stderr, "Failed to resolve worktree path: %v", err)
	}
	a.log("worktree", fmt.Sprintf("remove: path=%s", path))

	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return shell.WriteJSON(a.stdout, a.stderr, struct {
			Removed  bool   `json:"removed"`
			Worktree string `json:"worktree"`
		}{
			Removed:  false,
			Worktree: path,
		})
	} else if err != nil {
		return shell.Failf(a.stderr, "Failed to inspect worktree path %s: %v", path, err)
	}

	if err := a.repo.WorktreeRemove(path); err != nil {
		return shell.Failf(a.stderr, "Failed to remove worktree: %v", err)
	}

	return shell.WriteJSON(a.stdout, a.stderr, struct {
		Removed  bool   `json:"removed"`
		Worktree string `json:"worktree"`
	}{
		Removed:  true,
		Worktree: path,
	})
}

func (a *App) runInspect(ctx context.Context, issue string) int {
	cfg, err := a.loadConfig()
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read config: %v", err)
	}

	targetRoot, code := a.targetRoot(ctx)
	if code != 0 {
		return code
	}

	path, err := worktreePath(cfg.WorktreePrefix, targetRoot, issue)
	if err != nil {
		return shell.Failf(a.stderr, "Failed to resolve worktree path: %v", err)
	}

	_, statErr := os.Lstat(path)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return shell.Failf(a.stderr, "Failed to inspect worktree path %s: %v", path, statErr)
	}

	return shell.WriteJSON(a.stdout, a.stderr, struct {
		Worktree string `json:"worktree"`
		Exists   bool   `json:"exists"`
	}{
		Worktree: path,
		Exists:   exists,
	})
}

func (a *App) runBranchName(issue string, title string) int {
	cfg, err := a.loadConfig()
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read config: %v", err)
	}

	_, err = fmt.Fprintf(a.stdout, "%s\n", branchName(cfg.BranchPrefix, issue, title))
	if err != nil {
		return shell.Failf(a.stderr, "Failed to write branch name: %v", err)
	}
	return 0
}

func (a *App) targetRoot(_ context.Context) (string, int) {
	if targetRoot, ok := shell.EnvLookup(a.env, "TARGET_ROOT"); ok && targetRoot != "" {
		return targetRoot, 0
	}

	root, err := gitops.FindRoot(a.cwd)
	if err != nil {
		return "", shell.Fail(a.stderr, "Run runoq from inside a git repository.")
	}
	return root, 0
}

func (a *App) loadConfig() (config, error) {
	path := configPath(a.env, a.cwd)
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}

	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (a *App) configureGitBotIdentity(ctx context.Context, dir string, slug string, targetRoot string) error {
	if slug == "" {
		return nil
	}

	wtRepo := gitops.OpenCLI(ctx, dir, a.execCommand)

	if err := wtRepo.SetConfig("user.name", slug+"[bot]"); err != nil {
		return err
	}

	appID := a.appID(targetRoot)
	if appID == "" {
		return nil
	}

	return wtRepo.SetConfig("user.email", fmt.Sprintf("%s+%s[bot]@users.noreply.github.com", appID, slug))
}

func (a *App) appID(targetRoot string) string {
	if appID, ok := shell.EnvLookup(a.env, "RUNOQ_APP_ID"); ok && appID != "" {
		return appID
	}

	data, err := os.ReadFile(filepath.Join(targetRoot, ".runoq", "identity.json"))
	if err != nil {
		return ""
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return ""
	}

	value, ok := payload["appId"]
	if !ok || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case json.Number:
		return typed.String()
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func (a *App) log(prefix string, message string) {
	if value, ok := shell.EnvLookup(a.env, "RUNOQ_LOG"); !ok || value == "" {
		return
	}
	_, _ = fmt.Fprintf(a.stderr, "[%s] %s\n", prefix, message)
}

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}

func branchName(prefix string, issue string, title string) string {
	return prefix + issue + "-" + branchSlug(title)
}

func branchSlug(raw string) string {
	slug := strings.ToLower(raw)
	slug = nonAlnumPattern.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	slug = multiDash.ReplaceAllString(slug, "-")
	if slug == "" {
		return "issue"
	}
	return slug
}

func worktreePath(prefix string, targetRoot string, issue string) (string, error) {
	parent, err := filepath.Abs(filepath.Join(targetRoot, ".."))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, prefix+issue), nil
}

func defaultBaseRef(env []string) string {
	if baseRef, ok := shell.EnvLookup(env, "RUNOQ_BASE_REF"); ok && baseRef != "" {
		return baseRef
	}
	return "" // auto-detect from remote
}

func configPath(env []string, cwd string) string {
	if path, ok := shell.EnvLookup(env, "RUNOQ_CONFIG"); ok && path != "" {
		return path
	}
	if root, ok := shell.EnvLookup(env, "RUNOQ_ROOT"); ok && root != "" {
		return filepath.Join(root, "config", "runoq.json")
	}
	return filepath.Join(cwd, "config", "runoq.json")
}

// NewDirect creates an App for direct Go calls (not subprocess).
// Config values are injected — no env vars or config file reads.
func NewDirect(naming Naming, targetRoot string, logWriter io.Writer) *App {
	stderr := io.Writer(io.Discard)
	if logWriter != nil {
		stderr = logWriter
	}
	return &App{
		naming:      naming,
		cwd:         targetRoot,
		env:         []string{"TARGET_ROOT=" + targetRoot},
		stdout:      io.Discard,
		stderr:      stderr,
		execCommand: shell.RunCommand,
	}
}

// CreateWorktree creates a worktree for the given issue. Returns typed result, not JSON.
func (a *App) CreateWorktree(ctx context.Context, issueNumber int, title string) (CreateResult, error) {
	targetRoot := a.cwd
	if v, ok := shell.EnvLookup(a.env, "TARGET_ROOT"); ok && v != "" {
		targetRoot = v
	}
	a.repo = gitops.OpenCLI(ctx, targetRoot, a.execCommand)

	cfg := a.naming
	if cfg.BranchPrefix == "" {
		// Fallback for script entry path
		loaded, err := a.loadConfig()
		if err != nil {
			return CreateResult{}, err
		}
		cfg = Naming{
			BranchPrefix:   loaded.BranchPrefix,
			WorktreePrefix: loaded.WorktreePrefix,
			AppSlug:        loaded.Identity.AppSlug,
		}
	}

	issue := strconv.Itoa(issueNumber)
	branch := branchName(cfg.BranchPrefix, issue, title)
	path, err := worktreePath(cfg.WorktreePrefix, targetRoot, issue)
	if err != nil {
		return CreateResult{}, fmt.Errorf("resolve worktree path: %w", err)
	}

	baseRef := defaultBaseRef(a.env)
	if baseRef == "" {
		remoteBranch, err := a.repo.DefaultBranch("origin")
		if err != nil {
			return CreateResult{}, fmt.Errorf("detect default branch: %w", err)
		}
		baseRef = "origin/" + remoteBranch
		if err := a.repo.Fetch("origin", remoteBranch); err != nil {
			return CreateResult{}, fmt.Errorf("fetch origin %s: %w", remoteBranch, err)
		}
	}

	if _, err := os.Lstat(path); err == nil {
		return CreateResult{}, fmt.Errorf("worktree already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return CreateResult{}, fmt.Errorf("inspect worktree path %s: %w", path, err)
	}

	_ = a.repo.WorktreePrune()
	_ = a.repo.DeleteBranch(branch)

	if err := a.repo.WorktreeAdd(path, branch, baseRef); err != nil {
		return CreateResult{}, fmt.Errorf("create worktree: %w", err)
	}

	_ = a.configureGitBotIdentity(ctx, path, cfg.AppSlug, targetRoot)

	return CreateResult{Branch: branch, Worktree: path, BaseRef: baseRef}, nil
}

// RemoveWorktree removes the worktree for the given issue.
func (a *App) RemoveWorktree(ctx context.Context, issueNumber int) error {
	targetRoot := a.cwd
	if v, ok := shell.EnvLookup(a.env, "TARGET_ROOT"); ok && v != "" {
		targetRoot = v
	}
	a.repo = gitops.OpenCLI(ctx, targetRoot, a.execCommand)

	cfg := a.naming
	if cfg.WorktreePrefix == "" {
		loaded, err := a.loadConfig()
		if err != nil {
			return err
		}
		cfg.WorktreePrefix = loaded.WorktreePrefix
	}

	issue := strconv.Itoa(issueNumber)
	path, err := worktreePath(cfg.WorktreePrefix, targetRoot, issue)
	if err != nil {
		return fmt.Errorf("resolve worktree path: %w", err)
	}

	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil // already gone
	} else if err != nil {
		return fmt.Errorf("inspect worktree path %s: %w", path, err)
	}

	return a.repo.WorktreeRemove(path)
}

// RehydrateWorktree recreates the issue worktree from an existing pushed branch.
// Any prior disposable worktree at the issue path is removed first.
func (a *App) RehydrateWorktree(ctx context.Context, issueNumber int, branch string) (CreateResult, error) {
	targetRoot := a.cwd
	if v, ok := shell.EnvLookup(a.env, "TARGET_ROOT"); ok && v != "" {
		targetRoot = v
	}
	a.repo = gitops.OpenCLI(ctx, targetRoot, a.execCommand)

	cfg := a.naming
	if cfg.WorktreePrefix == "" {
		loaded, err := a.loadConfig()
		if err != nil {
			return CreateResult{}, err
		}
		cfg.WorktreePrefix = loaded.WorktreePrefix
		if cfg.AppSlug == "" {
			cfg.AppSlug = loaded.Identity.AppSlug
		}
	}

	issue := strconv.Itoa(issueNumber)
	path, err := worktreePath(cfg.WorktreePrefix, targetRoot, issue)
	if err != nil {
		return CreateResult{}, fmt.Errorf("resolve worktree path: %w", err)
	}

	if err := a.repo.Fetch("origin", branch); err != nil {
		return CreateResult{}, fmt.Errorf("fetch origin %s: %w", branch, err)
	}

	if _, err := os.Lstat(path); err == nil {
		if err := a.repo.WorktreeRemove(path); err != nil {
			return CreateResult{}, fmt.Errorf("remove stale worktree: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return CreateResult{}, fmt.Errorf("inspect worktree path %s: %w", path, err)
	}

	_ = a.repo.WorktreePrune()
	_ = a.repo.DeleteBranch(branch)

	baseRef := "origin/" + branch
	if err := a.repo.WorktreeAdd(path, branch, baseRef); err != nil {
		return CreateResult{}, fmt.Errorf("create worktree from %s: %w", baseRef, err)
	}

	_ = a.configureGitBotIdentity(ctx, path, cfg.AppSlug, targetRoot)

	return CreateResult{Branch: branch, Worktree: path, BaseRef: baseRef}, nil
}
