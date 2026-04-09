package worktree

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/saruman/runoq/internal/shell"
)

func TestBranchName(t *testing.T) {
	t.Parallel()

	configFile := writeWorktreeConfig(t)

	code, stdout, stderr := runApp(t, []string{"branch-name", "42", "Implement queue"}, []string{"RUNOQ_CONFIG=" + configFile}, ".")
	if code != 0 {
		t.Fatalf("branch-name code=%d stderr=%q", code, stderr)
	}
	if stdout != "runoq/42-implement-queue\n" {
		t.Fatalf("unexpected branch name: %q", stdout)
	}
}

func TestCreateWorktreeDirectCall(t *testing.T) {
	t.Parallel()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	localDir := filepath.Join(t.TempDir(), "local")
	makeRemoteBackedRepo(t, remoteDir, localDir)

	writeIdentityFile(t, localDir, `{"appId":123}`)

	naming := Naming{
		BranchPrefix:   "runoq/",
		WorktreePrefix: "runoq-wt-",
		AppSlug:        "runoq",
	}
	app := NewDirect(naming, localDir, nil)
	app.SetCommandExecutor(shell.RunCommand)

	result, err := app.CreateWorktree(t.Context(), 42, "Implement queue")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if result.Branch != "runoq/42-implement-queue" {
		t.Fatalf("expected branch runoq/42-implement-queue, got %q", result.Branch)
	}
	if _, err := os.Stat(result.Worktree); err != nil {
		t.Fatalf("worktree dir should exist: %v", err)
	}

	// RemoveWorktree
	if err := app.RemoveWorktree(t.Context(), 42); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(result.Worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, got: %v", err)
	}
}

func TestRehydrateWorktreeFromBranch(t *testing.T) {
	t.Parallel()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	localDir := filepath.Join(t.TempDir(), "local")
	makeRemoteBackedRepo(t, remoteDir, localDir)

	writeIdentityFile(t, localDir, `{"appId":123}`)

	naming := Naming{
		BranchPrefix:   "runoq/",
		WorktreePrefix: "runoq-wt-",
		AppSlug:        "runoq",
	}
	app := NewDirect(naming, localDir, nil)
	app.SetCommandExecutor(shell.RunCommand)

	branch := "runoq/42-implement-queue"
	mustRun(t, localDir, "git", "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(localDir, "feature.txt"), []byte("first version\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	mustRun(t, localDir, "git", "add", "feature.txt")
	mustRun(t, localDir, "git", "commit", "-m", "Add feature")
	mustRun(t, localDir, "git", "push", "-u", "origin", branch)
	mustRun(t, localDir, "git", "checkout", "main")

	first, err := app.RehydrateWorktree(t.Context(), 42, branch)
	if err != nil {
		t.Fatalf("RehydrateWorktree first: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(first.Worktree, "feature.txt"))
	if err != nil {
		t.Fatalf("read rehydrated feature.txt: %v", err)
	}
	if string(content) != "first version\n" {
		t.Fatalf("unexpected first worktree content: %q", string(content))
	}

	updaterDir := filepath.Join(t.TempDir(), "updater")
	mustRun(t, ".", "git", "clone", remoteDir, updaterDir)
	mustRun(t, updaterDir, "git", "config", "user.name", "Test User")
	mustRun(t, updaterDir, "git", "config", "user.email", "test@example.com")
	mustRun(t, updaterDir, "git", "checkout", branch)
	if err := os.WriteFile(filepath.Join(updaterDir, "feature.txt"), []byte("second version\n"), 0o644); err != nil {
		t.Fatalf("rewrite feature.txt: %v", err)
	}
	mustRun(t, updaterDir, "git", "add", "feature.txt")
	mustRun(t, updaterDir, "git", "commit", "-m", "Update feature")
	mustRun(t, updaterDir, "git", "push", "origin", branch)

	second, err := app.RehydrateWorktree(t.Context(), 42, branch)
	if err != nil {
		t.Fatalf("RehydrateWorktree second: %v", err)
	}
	content, err = os.ReadFile(filepath.Join(second.Worktree, "feature.txt"))
	if err != nil {
		t.Fatalf("read refreshed feature.txt: %v", err)
	}
	if string(content) != "second version\n" {
		t.Fatalf("unexpected refreshed worktree content: %q", string(content))
	}
}

func TestCreateInspectAndRemove(t *testing.T) {
	t.Parallel()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	localDir := filepath.Join(t.TempDir(), "local")
	makeRemoteBackedRepo(t, remoteDir, localDir)

	if err := os.WriteFile(filepath.Join(localDir, "local-only.txt"), []byte("local only\n"), 0o644); err != nil {
		t.Fatalf("write local-only file: %v", err)
	}
	mustRun(t, localDir, "git", "add", "local-only.txt")
	mustRun(t, localDir, "git", "commit", "-m", "Local-only commit")

	configFile := writeWorktreeConfig(t)
	writeIdentityFile(t, localDir, `{"appId":123}`)
	env := []string{
		"RUNOQ_CONFIG=" + configFile,
		"TARGET_ROOT=" + localDir,
	}

	createCode, createOut, createErr := runApp(t, []string{"create", "42", "Implement queue"}, env, localDir)
	if createCode != 0 {
		t.Fatalf("create code=%d stderr=%q", createCode, createErr)
	}

	var created struct {
		Branch   string `json:"branch"`
		Worktree string `json:"worktree"`
		BaseRef  string `json:"base_ref"`
	}
	if err := json.Unmarshal([]byte(createOut), &created); err != nil {
		t.Fatalf("unmarshal create output: %v\n%s", err, createOut)
	}

	if created.Branch != "runoq/42-implement-queue" {
		t.Fatalf("unexpected branch: %q", created.Branch)
	}
	if created.BaseRef != "origin/main" {
		t.Fatalf("unexpected base ref: %q", created.BaseRef)
	}
	if _, err := os.Stat(created.Worktree); err != nil {
		t.Fatalf("expected worktree to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(created.Worktree, "local-only.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected local-only file to be absent in worktree, stat err=%v", err)
	}

	if got := strings.TrimSpace(mustRun(t, created.Worktree, "git", "config", "user.name")); got != "runoq[bot]" {
		t.Fatalf("unexpected git user.name: %q", got)
	}
	if got := strings.TrimSpace(mustRun(t, created.Worktree, "git", "config", "user.email")); got != "123+runoq[bot]@users.noreply.github.com" {
		t.Fatalf("unexpected git user.email: %q", got)
	}

	inspectCode, inspectOut, inspectErr := runApp(t, []string{"inspect", "42"}, env, localDir)
	if inspectCode != 0 {
		t.Fatalf("inspect code=%d stderr=%q", inspectCode, inspectErr)
	}

	var inspected struct {
		Worktree string `json:"worktree"`
		Exists   bool   `json:"exists"`
	}
	if err := json.Unmarshal([]byte(inspectOut), &inspected); err != nil {
		t.Fatalf("unmarshal inspect output: %v\n%s", err, inspectOut)
	}
	if inspected.Worktree != created.Worktree || !inspected.Exists {
		t.Fatalf("unexpected inspect output: %+v", inspected)
	}

	removeCode, removeOut, removeErr := runApp(t, []string{"remove", "42"}, env, localDir)
	if removeCode != 0 {
		t.Fatalf("remove code=%d stderr=%q", removeCode, removeErr)
	}

	var removed struct {
		Removed  bool   `json:"removed"`
		Worktree string `json:"worktree"`
	}
	if err := json.Unmarshal([]byte(removeOut), &removed); err != nil {
		t.Fatalf("unmarshal remove output: %v\n%s", err, removeOut)
	}
	if !removed.Removed || removed.Worktree != created.Worktree {
		t.Fatalf("unexpected remove output: %+v", removed)
	}
	if _, err := os.Stat(created.Worktree); !os.IsNotExist(err) {
		t.Fatalf("expected worktree to be removed, stat err=%v", err)
	}
}

func TestCreateFailsWhenPathExists(t *testing.T) {
	t.Parallel()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	localDir := filepath.Join(t.TempDir(), "local")
	makeRemoteBackedRepo(t, remoteDir, localDir)

	configFile := writeWorktreeConfig(t)
	env := []string{
		"RUNOQ_CONFIG=" + configFile,
		"TARGET_ROOT=" + localDir,
	}

	worktreeDir, err := worktreePath("runoq-wt-", localDir, "42")
	if err != nil {
		t.Fatalf("resolve worktree path: %v", err)
	}
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("mkdir existing worktree path: %v", err)
	}

	code, _, stderr := runApp(t, []string{"create", "42", "Implement queue"}, env, localDir)
	if code == 0 {
		t.Fatalf("expected create to fail")
	}
	if !strings.Contains(stderr, "Worktree already exists: "+worktreeDir) {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestCreateRecoversStaleBranchAndWorktree(t *testing.T) {
	t.Parallel()

	remoteDir := filepath.Join(t.TempDir(), "remote.git")
	localDir := filepath.Join(t.TempDir(), "local")
	makeRemoteBackedRepo(t, remoteDir, localDir)

	configFile := writeWorktreeConfig(t)
	env := []string{
		"RUNOQ_CONFIG=" + configFile,
		"TARGET_ROOT=" + localDir,
	}

	// First create succeeds
	code, stdout, stderr := runApp(t, []string{"create", "42", "Implement queue"}, env, localDir)
	if code != 0 {
		t.Fatalf("first create failed: code=%d stderr=%q", code, stderr)
	}
	var result struct {
		Worktree string `json:"worktree"`
		Branch   string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse first create: %v", err)
	}

	// Simulate kill: remove worktree directory but leave git metadata and branch
	if err := os.RemoveAll(result.Worktree); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}

	// Second create should recover: prune stale metadata, delete stale branch, create fresh
	code, stdout, stderr = runApp(t, []string{"create", "42", "Implement queue"}, env, localDir)
	if code != 0 {
		t.Fatalf("second create (recovery) failed: code=%d stderr=%q", code, stderr)
	}
	var result2 struct {
		Worktree string `json:"worktree"`
		Branch   string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(stdout), &result2); err != nil {
		t.Fatalf("parse second create: %v", err)
	}
	if result2.Branch != result.Branch {
		t.Fatalf("expected same branch %q, got %q", result.Branch, result2.Branch)
	}
	if _, err := os.Stat(result2.Worktree); err != nil {
		t.Fatalf("worktree dir should exist after recovery: %v", err)
	}
}

func runApp(t *testing.T, args []string, env []string, cwd string) (int, string, string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(args, env, cwd, &stdout, &stderr)
	code := app.Run(t.Context())
	return code, stdout.String(), stderr.String()
}

func makeRemoteBackedRepo(t *testing.T, remoteDir string, localDir string) {
	t.Helper()

	seedDir := filepath.Join(t.TempDir(), "seed")
	mustRun(t, ".", "git", "init", "-b", "main", seedDir)
	mustRun(t, seedDir, "git", "config", "user.name", "Test User")
	mustRun(t, seedDir, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustRun(t, seedDir, "git", "add", "README.md")
	mustRun(t, seedDir, "git", "commit", "-m", "Initial commit")

	mustRun(t, ".", "git", "clone", "--bare", seedDir, remoteDir)
	mustRun(t, ".", "git", "clone", remoteDir, localDir)
	mustRun(t, localDir, "git", "config", "user.name", "Test User")
	mustRun(t, localDir, "git", "config", "user.email", "test@example.com")
}

func writeWorktreeConfig(t *testing.T) string {
	t.Helper()

	configFile := filepath.Join(t.TempDir(), "runoq.json")
	config := `{
  "labels": {
    "ready": "runoq:ready",
    "inProgress": "runoq:in-progress",
    "done": "runoq:done",
    "needsReview": "runoq:needs-human-review",
    "blocked": "runoq:blocked",
    "maintenanceReview": "runoq:maintenance-review"
  },
  "identity": {
    "appSlug": "runoq",
    "handle": "runoq"
  },
  "branchPrefix": "runoq/",
  "worktreePrefix": "runoq-wt-"
}`
	if err := os.WriteFile(configFile, []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configFile
}

func writeIdentityFile(t *testing.T, targetRoot string, payload string) {
	t.Helper()

	identityDir := filepath.Join(targetRoot, ".runoq")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatalf("mkdir identity dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(identityDir, "identity.json"), []byte(payload), 0o644); err != nil {
		t.Fatalf("write identity file: %v", err)
	}
}

func mustRun(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
	return string(out)
}
