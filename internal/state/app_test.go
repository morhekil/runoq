package state

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveAndLoadState(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	env := []string{"RUNOQ_STATE_DIR=" + stateDir}
	fixedNow := time.Date(2026, 3, 30, 6, 0, 0, 0, time.UTC)

	saveCode, saveOut, saveErr := runApp(t, []string{"save", "42"}, env, ".", `{"phase":"INIT","round":0}`, func(app *App) {
		app.SetNowFunc(func() time.Time { return fixedNow })
	})
	if saveCode != 0 {
		t.Fatalf("save code=%d stderr=%q", saveCode, saveErr)
	}
	if !strings.Contains(saveOut, `"issue": 42`) || !strings.Contains(saveOut, `"phase": "INIT"`) {
		t.Fatalf("unexpected save output: %q", saveOut)
	}
	if !strings.Contains(saveOut, `"started_at": "2026-03-30T06:00:00Z"`) {
		t.Fatalf("missing started_at in save output: %q", saveOut)
	}

	loadCode, loadOut, loadErr := runApp(t, []string{"load", "42"}, env, ".", "", nil)
	if loadCode != 0 {
		t.Fatalf("load code=%d stderr=%q", loadCode, loadErr)
	}
	if !strings.Contains(loadOut, `"updated_at": "2026-03-30T06:00:00Z"`) {
		t.Fatalf("missing updated_at in load output: %q", loadOut)
	}
}

func TestSaveRejectsTerminalTransition(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	env := []string{"RUNOQ_STATE_DIR=" + stateDir}

	_, _, _ = runApp(t, []string{"save", "42"}, env, ".", `{"phase":"INIT","round":0}`, nil)
	_, _, _ = runApp(t, []string{"save", "42"}, env, ".", `{"phase":"FINALIZE","round":1}`, nil)
	_, _, _ = runApp(t, []string{"save", "42"}, env, ".", `{"phase":"DONE","round":1}`, nil)

	code, _, stderr := runApp(t, []string{"save", "42"}, env, ".", `{"phase":"DEVELOP","round":2}`, nil)
	if code == 0 {
		t.Fatalf("expected terminal transition failure")
	}
	if !strings.Contains(stderr, "Invalid transition from terminal phase DONE to DEVELOP") {
		t.Fatalf("unexpected stderr: %q", stderr)
	}
}

func TestSaveAllowsFailedToInitRetry(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	env := []string{"RUNOQ_STATE_DIR=" + stateDir}

	_, _, _ = runApp(t, []string{"save", "42"}, env, ".", `{"phase":"INIT","round":0}`, nil)
	_, _, _ = runApp(t, []string{"save", "42"}, env, ".", `{"phase":"FAILED","round":0}`, nil)

	code, _, stderr := runApp(t, []string{"save", "42"}, env, ".", `{"phase":"INIT","round":0}`, nil)
	if code != 0 {
		t.Fatalf("expected FAILED→INIT to succeed, got code=%d stderr=%q", code, stderr)
	}
}

func TestSaveAllowsVerifyToReviewTransition(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	env := []string{"RUNOQ_STATE_DIR=" + stateDir}

	firstCode, _, firstErr := runApp(t, []string{"save", "42"}, env, ".", `{"phase":"VERIFY","round":1}`, nil)
	if firstCode != 0 {
		t.Fatalf("first save code=%d stderr=%q", firstCode, firstErr)
	}

	secondCode, secondOut, secondErr := runApp(t, []string{"save", "42"}, env, ".", `{"phase":"REVIEW","round":1}`, nil)
	if secondCode != 0 {
		t.Fatalf("verify->review save code=%d stderr=%q", secondCode, secondErr)
	}
	if !strings.Contains(secondOut, `"phase": "REVIEW"`) {
		t.Fatalf("expected review phase in save output: %q", secondOut)
	}
}

func TestSaveRejectsDecideToIntegrateTransition(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	env := []string{"RUNOQ_STATE_DIR=" + stateDir}

	firstCode, _, firstErr := runApp(t, []string{"save", "42"}, env, ".", `{"phase":"DECIDE","round":1}`, nil)
	if firstCode != 0 {
		t.Fatalf("first save code=%d stderr=%q", firstCode, firstErr)
	}

	secondCode, _, secondErr := runApp(t, []string{"save", "42"}, env, ".", `{"phase":"INTEGRATE","round":1}`, nil)
	if secondCode == 0 {
		t.Fatal("expected DECIDE->INTEGRATE transition to be rejected")
	}
	if !strings.Contains(secondErr, "Invalid phase transition: DECIDE -> INTEGRATE") {
		t.Fatalf("unexpected stderr: %q", secondErr)
	}
}

func TestRecordAndHasMention(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	env := []string{"RUNOQ_STATE_DIR=" + stateDir}

	code, stdout, stderr := runApp(t, []string{"has-mention", "101"}, env, ".", "", nil)
	if code == 0 || strings.TrimSpace(stdout) != "false" {
		t.Fatalf("expected missing mention, code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runApp(t, []string{"record-mention", "101"}, env, ".", "", nil)
	if code != 0 || !strings.Contains(stdout, "101") {
		t.Fatalf("record-mention failed code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runApp(t, []string{"has-mention", "101"}, env, ".", "", nil)
	if code != 0 || strings.TrimSpace(stdout) != "true" {
		t.Fatalf("expected existing mention, code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	_, _, _ = runApp(t, []string{"record-mention", "101"}, env, ".", "", nil)
	data, err := os.ReadFile(filepath.Join(stateDir, "processed-mentions.json"))
	if err != nil {
		t.Fatalf("read mentions file: %v", err)
	}
	var ids []int64
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatalf("unmarshal mentions: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected deduped mentions, got %v", ids)
	}
}

func TestExtractPayloadPrefersMarkerBlock(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "payload.txt")
	content := strings.Join([]string{
		"```",
		`{"status":"failed","notes":"ignore"}`,
		"```",
		"<!-- runoq:payload:codex-return -->",
		"```json",
		`{"status":"completed","notes":"use marked payload"}`,
		"```",
		"```",
		`{"status":"failed","notes":"ignore trailing fenced block"}`,
		"```",
	}, "\n")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("write payload file: %v", err)
	}

	code, stdout, stderr := runApp(t, []string{"extract-payload", file}, nil, ".", "", nil)
	if code != 0 {
		t.Fatalf("extract-payload code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"use marked payload"`) || strings.Contains(stdout, "ignore trailing") {
		t.Fatalf("unexpected extracted payload: %q", stdout)
	}
}

func TestValidatePayloadSynthesizesWhenMissingJSON(t *testing.T) {
	t.Parallel()

	repoDir, baseSHA := setupPayloadRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "out.txt"), []byte("no fenced block"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repoDir, "src", "app.ts"), []byte("console.log('updated')\n"), 0o644); err != nil {
		t.Fatalf("write updated source: %v", err)
	}
	mustRun(t, repoDir, "git", "add", "src/app.ts")
	mustRun(t, repoDir, "git", "commit", "-m", "Update app")

	code, stdout, stderr := runApp(
		t,
		[]string{"validate-payload", repoDir, baseSHA, filepath.Join(repoDir, "out.txt")},
		nil,
		repoDir,
		"",
		nil,
	)
	if code != 0 {
		t.Fatalf("validate-payload code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"payload_source": "synthetic"`) {
		t.Fatalf("expected synthetic payload: %q", stdout)
	}
	if !strings.Contains(stdout, `"payload_schema_valid": false`) {
		t.Fatalf("expected payload_schema_valid=false: %q", stdout)
	}
	if !strings.Contains(stdout, `"payload_schema_errors": [`) || !strings.Contains(stdout, "payload_missing_or_malformed") {
		t.Fatalf("expected payload schema errors in output: %q", stdout)
	}
	if !strings.Contains(stdout, "Codex did not return a structured payload") {
		t.Fatalf("missing synthetic blocker message: %q", stdout)
	}
}

func TestValidatePayloadSynthesizesWhenSourceFileMissing(t *testing.T) {
	t.Parallel()

	repoDir, baseSHA := setupPayloadRepo(t)
	missingSource := filepath.Join(repoDir, "does-not-exist.txt")

	if err := os.WriteFile(filepath.Join(repoDir, "src", "app.ts"), []byte("console.log('updated')\n"), 0o644); err != nil {
		t.Fatalf("write updated source: %v", err)
	}
	mustRun(t, repoDir, "git", "add", "src/app.ts")
	mustRun(t, repoDir, "git", "commit", "-m", "Update app")

	code, stdout, stderr := runApp(
		t,
		[]string{"validate-payload", repoDir, baseSHA, missingSource},
		nil,
		repoDir,
		"",
		nil,
	)
	if code != 0 {
		t.Fatalf("validate-payload code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"payload_source": "synthetic"`) {
		t.Fatalf("expected synthetic payload: %q", stdout)
	}
	if !strings.Contains(stdout, `"payload_schema_valid": false`) {
		t.Fatalf("expected payload_schema_valid=false: %q", stdout)
	}
	if !strings.Contains(stdout, `"payload_schema_errors": [`) || !strings.Contains(stdout, "payload_missing_or_malformed") {
		t.Fatalf("expected payload schema errors in output: %q", stdout)
	}
	if !strings.Contains(stdout, "Codex did not return a structured payload") {
		t.Fatalf("missing synthetic blocker message: %q", stdout)
	}
}

func TestValidatePayloadIncludesThreadIDAndSchemaMetadata(t *testing.T) {
	t.Parallel()

	repoDir, baseSHA := setupPayloadRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "src", "app.ts"), []byte("console.log('updated')\n"), 0o644); err != nil {
		t.Fatalf("write updated source: %v", err)
	}
	mustRun(t, repoDir, "git", "add", "src/app.ts")
	mustRun(t, repoDir, "git", "commit", "-m", "Update app")

	source := filepath.Join(repoDir, "out.txt")
	content := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-abc123"}`,
		"<!-- runoq:payload:codex-return -->",
		"```json",
		`{"status":"completed","tests_run":true,"tests_passed":true,"test_summary":"ok","build_passed":true,"blockers":[],"notes":"ok"}`,
		"```",
	}, "\n")
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	code, stdout, stderr := runApp(
		t,
		[]string{"validate-payload", repoDir, baseSHA, source},
		nil,
		repoDir,
		"",
		nil,
	)
	if code != 0 {
		t.Fatalf("validate-payload code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"thread_id": "thread-abc123"`) {
		t.Fatalf("expected thread_id in output: %q", stdout)
	}
	if !strings.Contains(stdout, `"payload_schema_valid": true`) {
		t.Fatalf("expected payload_schema_valid=true: %q", stdout)
	}
	if !strings.Contains(stdout, `"payload_schema_errors": []`) {
		t.Fatalf("expected empty payload schema errors: %q", stdout)
	}
	if !strings.Contains(stdout, `"payload_source": "clean"`) {
		t.Fatalf("expected payload_source=clean: %q", stdout)
	}
	if !strings.Contains(stdout, `"commits_pushed": [`) || !strings.Contains(stdout, `"files_added": [`) {
		t.Fatalf("expected truth-backed commit/file data in output: %q", stdout)
	}
}

func TestValidatePayloadFlagsSchemaErrorsForInvalidFieldTypes(t *testing.T) {
	t.Parallel()

	repoDir, baseSHA := setupPayloadRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "src", "app.ts"), []byte("console.log('updated')\n"), 0o644); err != nil {
		t.Fatalf("write updated source: %v", err)
	}
	mustRun(t, repoDir, "git", "add", "src/app.ts")
	mustRun(t, repoDir, "git", "commit", "-m", "Update app")

	source := filepath.Join(repoDir, "out-invalid.txt")
	content := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-invalid"}`,
		"<!-- runoq:payload:codex-return -->",
		"```json",
		`{"status":"completed","tests_run":true,"tests_passed":"yes","test_summary":"","build_passed":"true","blockers":[],"notes":""}`,
		"```",
	}, "\n")
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	code, stdout, stderr := runApp(
		t,
		[]string{"validate-payload", repoDir, baseSHA, source},
		nil,
		repoDir,
		"",
		nil,
	)
	if code != 0 {
		t.Fatalf("validate-payload code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"payload_schema_valid": false`) {
		t.Fatalf("expected payload_schema_valid=false: %q", stdout)
	}
	if !strings.Contains(stdout, "tests_passed_missing_or_non_boolean") {
		t.Fatalf("expected tests_passed schema error: %q", stdout)
	}
	if !strings.Contains(stdout, "build_passed_missing_or_non_boolean") {
		t.Fatalf("expected build_passed schema error: %q", stdout)
	}
	if strings.Contains(stdout, "commits_pushed_missing_or_non_string_array") || strings.Contains(stdout, "files_changed_missing_or_non_string_array") {
		t.Fatalf("did not expect git fact schema errors after contract narrowing: %q", stdout)
	}
}

func runApp(
	t *testing.T,
	args []string,
	env []string,
	cwd string,
	stdin string,
	configure func(*App),
) (int, string, string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(args, env, cwd, strings.NewReader(stdin), &stdout, &stderr)
	if configure != nil {
		configure(app)
	}
	code := app.Run(t.Context())
	return code, stdout.String(), stderr.String()
}

func setupPayloadRepo(t *testing.T) (string, string) {
	t.Helper()

	repoDir := t.TempDir()
	mustRun(t, repoDir, "git", "init", "-b", "main")
	mustRun(t, repoDir, "git", "config", "user.name", "Test User")
	mustRun(t, repoDir, "git", "config", "user.email", "test@example.com")
	mustRun(t, repoDir, "git", "remote", "add", "origin", "git@github.com:owner/example.git")

	if err := os.MkdirAll(filepath.Join(repoDir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "src", "app.ts"), []byte("console.log('hello')\n"), 0o644); err != nil {
		t.Fatalf("write app.ts: %v", err)
	}
	mustRun(t, repoDir, "git", "add", "src/app.ts")
	mustRun(t, repoDir, "git", "commit", "-m", "Add app")

	return repoDir, strings.TrimSpace(mustRun(t, repoDir, "git", "rev-parse", "HEAD"))
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
