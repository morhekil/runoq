package dispatchsafety

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReconcileResumesRecoverableOrphanedRun(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeDispatchConfig(t, configPath)

	remoteDir, localDir := newRemoteBackedRepo(t)
	_ = remoteDir
	stateDir := filepath.Join(localDir, ".runoq", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}

	runCmd(t, localDir, "git", "checkout", "-b", "runoq/42-implement-queue")
	if err := os.WriteFile(filepath.Join(localDir, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatalf("write work file: %v", err)
	}
	runCmd(t, localDir, "git", "add", "work.txt")
	runCmd(t, localDir, "git", "commit", "-m", "Work in progress")
	runCmd(t, localDir, "git", "push", "-u", "origin", "runoq/42-implement-queue")

	writeIssueStateFile(t, filepath.Join(stateDir, "42.json"), 42, "REVIEW", 2, "runoq/42-implement-queue", "87")

	scenarioPath := filepath.Join(t.TempDir(), "scenario.json")
	writeFakeGHScenario(t, scenarioPath, `[
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "number"],
    "stdout": "{\"number\":87}"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: REVIEW round 2. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["pr", "comment", "87", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: REVIEW round 2. Resuming."],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  }
]`)

	logPath := filepath.Join(t.TempDir(), "fake-gh.log")
	env := dispatchTestEnv(repoRoot, configPath, localDir, stateDir, scenarioPath, logPath)

	code, stdout, stderr := runApp(t, []string{"reconcile", "owner/repo"}, env, localDir)
	if code != 0 {
		t.Fatalf("reconcile code=%d stderr=%q", code, stderr)
	}

	var actions []reconcileAction
	if err := json.Unmarshal([]byte(stdout), &actions); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "resume" || actions[0].Issue != 42 {
		t.Fatalf("unexpected actions: %+v", actions)
	}

	logOutput, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake gh log: %v", err)
	}
	logText := string(logOutput)
	if !strings.Contains(logText, "pr view 87 --repo owner/repo --json number") {
		t.Fatalf("missing PR view call in log: %q", logText)
	}
	if !strings.Contains(logText, "issue comment 42 --repo owner/repo --body Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: REVIEW round 2. Resuming.") {
		t.Fatalf("missing issue comment call in log: %q", logText)
	}
}

func TestReconcileMarksUnrecoverableRunForHumanReview(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeDispatchConfig(t, configPath)

	_, localDir := newRemoteBackedRepo(t)
	stateDir := filepath.Join(localDir, ".runoq", "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	writeIssueStateFile(t, filepath.Join(stateDir, "42.json"), 42, "DEVELOP", 1, "runoq/42-implement-queue", "87")

	scenarioPath := filepath.Join(t.TempDir(), "scenario.json")
	writeFakeGHScenario(t, scenarioPath, `[
  {
    "contains": ["pr", "view", "87", "--repo", "owner/repo", "--json", "number"],
    "exit_code": 1,
    "stderr": "not found"
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue", "--json", "number"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "labels"],
    "stdout": "{\"labels\":[{\"name\":\"runoq:in-progress\"}]}"
  },
  {
    "contains": ["issue", "edit", "42", "--repo", "owner/repo", "--remove-label", "runoq:in-progress", "--add-label", "runoq:needs-human-review"],
    "stdout": ""
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Detected interrupted run from 2026-03-17T00:00:00Z. Previous phase: DEVELOP round 1. Marking for human review."],
    "stdout": ""
  },
  {
    "contains": ["issue", "list", "--repo", "owner/repo", "--label", "runoq:in-progress"],
    "stdout": "[]"
  }
]`)

	logPath := filepath.Join(t.TempDir(), "fake-gh.log")
	env := dispatchTestEnv(repoRoot, configPath, localDir, stateDir, scenarioPath, logPath)

	code, stdout, stderr := runApp(t, []string{"reconcile", "owner/repo"}, env, localDir)
	if code != 0 {
		t.Fatalf("reconcile code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"action": "needs-review"`) {
		t.Fatalf("unexpected reconcile output: %q", stdout)
	}

	logOutput, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake gh log: %v", err)
	}
	if !strings.Contains(string(logOutput), "issue edit 42 --repo owner/repo --remove-label runoq:in-progress --add-label runoq:needs-human-review") {
		t.Fatalf("missing set-status edit call in log: %q", string(logOutput))
	}
}

func TestEligibilityRejectsMissingAcceptanceCriteria(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeDispatchConfig(t, configPath)

	_, localDir := newRemoteBackedRepo(t)
	stateDir := filepath.Join(localDir, ".runoq", "state")
	scenarioPath := filepath.Join(t.TempDir(), "scenario.json")
	writeFakeGHScenario(t, scenarioPath, `[
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": "{\"number\":42,\"title\":\"Implement queue\",\"body\":\"No acceptance criteria here.\",\"labels\":[{\"name\":\"runoq:ready\"}],\"url\":\"https://example.test/issues/42\"}"
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue", "--json", "number,url"],
    "stdout": "[]"
  },
  {
    "contains": ["issue", "comment", "42", "--repo", "owner/repo", "--body", "Skipped: missing acceptance criteria."],
    "stdout": ""
  }
]`)

	logPath := filepath.Join(t.TempDir(), "fake-gh.log")
	env := dispatchTestEnv(repoRoot, configPath, localDir, stateDir, scenarioPath, logPath)

	code, stdout, stderr := runApp(t, []string{"eligibility", "owner/repo", "42"}, env, localDir)
	if code == 0 {
		t.Fatalf("expected non-zero eligibility exit code")
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, `"allowed": false`) || !strings.Contains(stdout, `"missing acceptance criteria"`) {
		t.Fatalf("unexpected eligibility output: %q", stdout)
	}
}

func TestEligibilityAllowsIssueWhenNoBlockingReasonsExist(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeDispatchConfig(t, configPath)

	_, localDir := newRemoteBackedRepo(t)
	stateDir := filepath.Join(localDir, ".runoq", "state")
	scenarioPath := filepath.Join(t.TempDir(), "scenario.json")
	body := issueBodyWithMeta("[]")
	issueJSON, err := json.Marshal(map[string]any{
		"number": 42,
		"title":  "Implement queue",
		"body":   body,
		"labels": []map[string]string{{"name": "runoq:ready"}},
		"url":    "https://example.test/issues/42",
	})
	if err != nil {
		t.Fatalf("marshal issue json: %v", err)
	}
	writeFakeGHScenario(t, scenarioPath, fmt.Sprintf(`[
  {
    "contains": ["issue", "view", "42", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": %q
  },
  {
    "contains": ["pr", "list", "--repo", "owner/repo", "--state", "open", "--head", "runoq/42-implement-queue", "--json", "number,url"],
    "stdout": "[]"
  }
]`, string(issueJSON)))

	logPath := filepath.Join(t.TempDir(), "fake-gh.log")
	env := dispatchTestEnv(repoRoot, configPath, localDir, stateDir, scenarioPath, logPath)

	code, stdout, stderr := runApp(t, []string{"eligibility", "owner/repo", "42"}, env, localDir)
	if code != 0 {
		t.Fatalf("eligibility code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"allowed": true`) || !strings.Contains(stdout, `"branch": "runoq/42-implement-queue"`) {
		t.Fatalf("unexpected eligibility output: %q", stdout)
	}

	logOutput, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake gh log: %v", err)
	}
	if strings.Contains(string(logOutput), "issue comment 42") {
		t.Fatalf("unexpected skip comment log: %q", string(logOutput))
	}
}

func TestPlanningEligibilityAllowsMissingAcceptanceCriteria(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeDispatchConfig(t, configPath)

	_, localDir := newRemoteBackedRepo(t)
	stateDir := filepath.Join(localDir, ".runoq", "state")
	scenarioPath := filepath.Join(t.TempDir(), "scenario.json")
	body := strings.Join([]string{
		"<!-- runoq:meta",
		"depends_on: []",
		"priority: 1",
		"estimated_complexity: low",
		"type: planning",
		"-->",
		"",
		"Plan body.",
	}, "\n")
	issueJSON, err := json.Marshal(map[string]any{
		"number": 99,
		"title":  "Plan milestone 1",
		"body":   body,
		"labels": []map[string]string{{"name": "runoq:ready"}},
		"url":    "https://example.test/issues/99",
	})
	if err != nil {
		t.Fatalf("marshal issue json: %v", err)
	}
	writeFakeGHScenario(t, scenarioPath, fmt.Sprintf(`[
  {
    "contains": ["issue", "view", "99", "--repo", "owner/repo", "--json", "number,title,body,labels,url"],
    "stdout": %q
  }
]`, string(issueJSON)))

	logPath := filepath.Join(t.TempDir(), "fake-gh.log")
	env := dispatchTestEnv(repoRoot, configPath, localDir, stateDir, scenarioPath, logPath)

	code, stdout, stderr := runApp(t, []string{"eligibility", "owner/repo", "99"}, env, localDir)
	if code != 0 {
		t.Fatalf("eligibility code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"allowed": true`) {
		t.Fatalf("unexpected eligibility output: %q", stdout)
	}

	logOutput, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake gh log: %v", err)
	}
	if strings.Contains(string(logOutput), "issue comment 99") {
		t.Fatalf("unexpected skip comment log: %q", string(logOutput))
	}
}

func runApp(t *testing.T, args []string, env []string, cwd string) (int, string, string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(args, env, cwd, &stdout, &stderr)
	code := app.Run(context.Background())
	return code, stdout.String(), stderr.String()
}

func newRemoteBackedRepo(t *testing.T) (string, string) {
	t.Helper()

	base := t.TempDir()
	seedDir := filepath.Join(base, "seed")
	remoteDir := filepath.Join(base, "remote.git")
	localDir := filepath.Join(base, "local")

	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatalf("mkdir seed: %v", err)
	}
	runCmd(t, seedDir, "git", "init", "-b", "main")
	runCmd(t, seedDir, "git", "config", "user.name", "Test User")
	runCmd(t, seedDir, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed readme: %v", err)
	}
	runCmd(t, seedDir, "git", "add", "README.md")
	runCmd(t, seedDir, "git", "commit", "-m", "Initial commit")

	runCmd(t, base, "git", "clone", "--bare", seedDir, remoteDir)
	runCmd(t, base, "git", "clone", remoteDir, localDir)
	runCmd(t, localDir, "git", "config", "user.name", "Test User")
	runCmd(t, localDir, "git", "config", "user.email", "test@example.com")

	return remoteDir, localDir
}

func writeDispatchConfig(t *testing.T, path string) {
	t.Helper()

	const raw = `{
  "labels": {
    "ready": "runoq:ready",
    "inProgress": "runoq:in-progress",
    "done": "runoq:done",
    "needsReview": "runoq:needs-human-review",
    "blocked": "runoq:blocked"
  },
  "branchPrefix": "runoq/"
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeIssueStateFile(t *testing.T, path string, issue int, phase string, round int, branch string, prNumber string) {
	t.Helper()

	state := `{
  "issue": ` + strconv.Itoa(issue) + `,
  "phase": "` + phase + `",
  "round": ` + strconv.Itoa(round) + `,
  "branch": "` + branch + `",
  "pr_number": ` + prNumber + `,
  "updated_at": "2026-03-17T00:00:00Z"
}`
	if err := os.WriteFile(path, []byte(state), 0o644); err != nil {
		t.Fatalf("write state file: %v", err)
	}
}

func issueBodyWithMeta(dependsOn string) string {
	return strings.Join([]string{
		"<!-- runoq:meta",
		"depends_on: " + dependsOn,
		"priority: 2",
		"estimated_complexity: low",
		"-->",
		"",
		"## Acceptance Criteria",
		"",
		"- [ ] Works.",
	}, "\n")
}

func writeFakeGHScenario(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fake-gh scenario: %v", err)
	}
}

func dispatchTestEnv(repoRoot string, configPath string, targetRoot string, stateDir string, scenarioPath string, logPath string) []string {
	env := append([]string(nil), os.Environ()...)
	env = envSet(env, "RUNOQ_ROOT", repoRoot)
	env = envSet(env, "RUNOQ_CONFIG", configPath)
	env = envSet(env, "TARGET_ROOT", targetRoot)
	env = envSet(env, "RUNOQ_STATE_DIR", stateDir)
	env = envSet(env, "GH_BIN", filepath.Join(repoRoot, "test", "helpers", "gh"))
	env = envSet(env, "GH_TOKEN", "existing-token")
	env = envSet(env, "FAKE_GH_SCENARIO", scenarioPath)
	env = envSet(env, "FAKE_GH_STATE", filepath.Join(filepath.Dir(logPath), "fake-gh.state"))
	env = envSet(env, "FAKE_GH_LOG", logPath)
	env = envSet(env, "FAKE_GH_CAPTURE_DIR", filepath.Join(filepath.Dir(logPath), "capture"))
	return env
}

func envSet(env []string, key string, value string) []string {
	prefix := key + "="
	for i, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

func runCmd(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(output))
	}
	return string(output)
}
