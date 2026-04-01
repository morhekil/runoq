package verify

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundSuccess(t *testing.T) {
	t.Parallel()

	remoteDir, localDir := newRemoteBackedRepo(t)
	_ = remoteDir

	runCmd(t, localDir, "git", "checkout", "-b", "runoq/42-test")
	baseSHA := strings.TrimSpace(runCmd(t, localDir, "git", "rev-parse", "HEAD"))

	if err := os.MkdirAll(filepath.Join(localDir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "src", "app.ts"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatalf("write app.ts: %v", err)
	}
	runCmd(t, localDir, "git", "add", "src/app.ts")
	runCmd(t, localDir, "git", "commit", "-m", "Add app")
	runCmd(t, localDir, "git", "push", "-u", "origin", "runoq/42-test")
	commitSHA := strings.TrimSpace(runCmd(t, localDir, "git", "rev-parse", "HEAD"))

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeVerifyConfig(t, configPath, "true", "true")

	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	payload := map[string]any{
		"status":         "completed",
		"commits_pushed": []string{commitSHA},
		"commit_range":   commitSHA + ".." + commitSHA,
		"files_changed":  []string{},
		"files_added":    []string{"src/app.ts"},
		"files_deleted":  []string{},
		"tests_run":      true,
		"tests_passed":   true,
		"test_summary":   "ok",
		"build_passed":   true,
		"blockers":       []string{},
		"notes":          "",
	}
	writeJSONFile(t, payloadPath, payload)

	code, stdout, stderr := runApp(
		t,
		[]string{"round", localDir, "runoq/42-test", baseSHA, payloadPath},
		[]string{"RUNOQ_CONFIG=" + configPath},
		localDir,
	)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, stderr)
	}

	var result roundResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("unmarshal round output: %v", err)
	}
	if !result.OK || !result.ReviewAllowed {
		t.Fatalf("expected success result, got %+v", result)
	}
}

func TestRoundAccumulatesFailures(t *testing.T) {
	t.Parallel()

	_, localDir := newRemoteBackedRepo(t)
	runCmd(t, localDir, "git", "checkout", "-b", "runoq/42-test")
	baseSHA := strings.TrimSpace(runCmd(t, localDir, "git", "rev-parse", "HEAD"))

	if err := os.WriteFile(filepath.Join(localDir, "src.ts"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatalf("write src.ts: %v", err)
	}
	runCmd(t, localDir, "git", "add", "src.ts")
	runCmd(t, localDir, "git", "commit", "-m", "Add src")
	commitSHA := strings.TrimSpace(runCmd(t, localDir, "git", "rev-parse", "HEAD"))

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeVerifyConfig(t, configPath, "false", "false")

	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	payload := map[string]any{
		"status":         "completed",
		"commits_pushed": []string{commitSHA},
		"commit_range":   commitSHA + ".." + commitSHA,
		"files_changed":  []string{},
		"files_added":    []string{"src.ts"},
		"files_deleted":  []string{},
		"tests_run":      true,
		"tests_passed":   true,
		"test_summary":   "ok",
		"build_passed":   true,
		"blockers":       []string{},
		"notes":          "",
	}
	writeJSONFile(t, payloadPath, payload)

	code, stdout, stderr := runApp(
		t,
		[]string{"round", localDir, "runoq/42-test", baseSHA, payloadPath},
		[]string{"RUNOQ_CONFIG=" + configPath},
		localDir,
	)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, stderr)
	}

	var result roundResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("unmarshal round output: %v", err)
	}
	if result.OK {
		t.Fatalf("expected failure result, got %+v", result)
	}
	joined := strings.Join(result.Failures, "\n")
	if !strings.Contains(joined, "branch tip is not pushed to origin") {
		t.Fatalf("missing branch push failure: %v", result.Failures)
	}
	if !strings.Contains(joined, "test command failed (`false`)") {
		t.Fatalf("missing test command failure: %v", result.Failures)
	}
	if !strings.Contains(joined, "build command failed (`false`)") {
		t.Fatalf("missing build command failure: %v", result.Failures)
	}
}

func TestRoundFailsWhenVerificationCommandsMissing(t *testing.T) {
	t.Parallel()

	_, localDir := newRemoteBackedRepo(t)
	runCmd(t, localDir, "git", "checkout", "-b", "runoq/42-test")
	baseSHA := strings.TrimSpace(runCmd(t, localDir, "git", "rev-parse", "HEAD"))
	commitSHA := baseSHA

	configPath := filepath.Join(t.TempDir(), "config-bad.json")
	writeVerifyConfig(t, configPath, "", "true")

	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	payload := map[string]any{
		"status":         "completed",
		"commits_pushed": []string{commitSHA},
		"commit_range":   commitSHA + ".." + commitSHA,
		"files_changed":  []string{},
		"files_added":    []string{},
		"files_deleted":  []string{},
		"tests_run":      true,
		"tests_passed":   true,
		"test_summary":   "ok",
		"build_passed":   true,
		"blockers":       []string{},
		"notes":          "",
	}
	writeJSONFile(t, payloadPath, payload)

	code, _, stderr := runApp(
		t,
		[]string{"round", localDir, "runoq/42-test", baseSHA, payloadPath},
		[]string{"RUNOQ_CONFIG=" + configPath},
		localDir,
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when verification commands are missing")
	}
	if !strings.Contains(stderr, "verification.testCommand is not configured") {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestIntegrateTamperedCriteria(t *testing.T) {
	t.Parallel()

	_, localDir := newRemoteBackedRepo(t)
	runCmd(t, localDir, "git", "checkout", "-b", "runoq/epic-test")

	configPath := filepath.Join(t.TempDir(), "config.json")
	writeVerifyConfig(t, configPath, "true", "true")

	if err := os.MkdirAll(filepath.Join(localDir, "test"), 0o755); err != nil {
		t.Fatalf("mkdir test: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "test", "integration.test.js"), []byte("test('integration', () => {})\n"), 0o644); err != nil {
		t.Fatalf("write criteria file: %v", err)
	}
	runCmd(t, localDir, "git", "add", "test/integration.test.js")
	runCmd(t, localDir, "git", "commit", "-m", "bar-setter: epic criteria")
	criteriaCommit := strings.TrimSpace(runCmd(t, localDir, "git", "rev-parse", "HEAD"))

	if err := os.WriteFile(filepath.Join(localDir, "test", "integration.test.js"), []byte("test('integration', () => { /* hacked */ })\n"), 0o644); err != nil {
		t.Fatalf("tamper criteria file: %v", err)
	}
	runCmd(t, localDir, "git", "add", "test/integration.test.js")
	runCmd(t, localDir, "git", "commit", "-m", "Tamper criteria")

	code, stdout, stderr := runApp(
		t,
		[]string{"integrate", localDir, criteriaCommit},
		[]string{"RUNOQ_CONFIG=" + configPath},
		localDir,
	)
	if code != 0 {
		t.Fatalf("expected code 0, got %d stderr=%q", code, stderr)
	}

	var result integrateResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("unmarshal integrate output: %v", err)
	}
	if result.OK {
		t.Fatalf("expected integrate failure, got %+v", result)
	}
	if !strings.Contains(strings.Join(result.Failures, "\n"), "criteria tampered: test/integration.test.js") {
		t.Fatalf("missing criteria tamper failure: %v", result.Failures)
	}
}

func runApp(t *testing.T, args []string, env []string, cwd string) (int, string, string) {
	t.Helper()

	var stdout strings.Builder
	var stderr strings.Builder
	app := New(args, env, cwd, &stdout, &stderr)
	code := app.Run(t.Context())
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

func writeVerifyConfig(t *testing.T, path string, testCommand string, buildCommand string) {
	t.Helper()

	quotedTest, err := json.Marshal(testCommand)
	if err != nil {
		t.Fatalf("marshal test command: %v", err)
	}
	quotedBuild, err := json.Marshal(buildCommand)
	if err != nil {
		t.Fatalf("marshal build command: %v", err)
	}
	content := fmt.Sprintf(`{
  "verification": {
    "testCommand": %s,
    "buildCommand": %s
  }
}
`, quotedTest, quotedBuild)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write json file: %v", err)
	}
}
