package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const usageText = `Usage:
  state.sh save <issue-number> [--state-dir DIR]
  state.sh load <issue-number> [--state-dir DIR]
  state.sh record-mention <comment-id> [--state-dir DIR]
  state.sh has-mention <comment-id> [--state-dir DIR]
  state.sh extract-payload <codex-output-file>
  state.sh validate-payload <worktree> <base-sha> <codex-output-file>
`

type App struct {
	args        []string
	env         []string
	cwd         string
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	execCommand shell.CommandExecutor
	nowFn       func() time.Time
}

type contractError struct {
	message string
}

func (e contractError) Error() string {
	return e.message
}

type normalizedPayload struct {
	Status              string   `json:"status"`
	CommitsPushed       []string `json:"commits_pushed"`
	CommitRange         string   `json:"commit_range"`
	FilesChanged        []string `json:"files_changed"`
	FilesAdded          []string `json:"files_added"`
	FilesDeleted        []string `json:"files_deleted"`
	TestsRun            bool     `json:"tests_run"`
	TestsPassed         bool     `json:"tests_passed"`
	TestSummary         string   `json:"test_summary"`
	BuildPassed         bool     `json:"build_passed"`
	Blockers            []string `json:"blockers"`
	Notes               string   `json:"notes"`
	PayloadSchemaValid  bool     `json:"payload_schema_valid"`
	PayloadSchemaErrors []string `json:"payload_schema_errors"`
	ThreadID            string   `json:"thread_id,omitempty"`
	PayloadSource       string   `json:"payload_source"`
	PatchedFields       []string `json:"patched_fields"`
	Discrepancies       []string `json:"discrepancies"`
}

type groundTruth struct {
	CommitsPushed []string
	CommitRange   string
	FilesChanged  []string
	FilesAdded    []string
	FilesDeleted  []string
}

func New(args []string, env []string, cwd string, stdin io.Reader, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:        append([]string(nil), args...),
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdin:       stdin,
		stdout:      stdout,
		stderr:      stderr,
		execCommand: shell.RunCommand,
		nowFn:       time.Now,
	}
}

func (a *App) SetCommandExecutor(execFn shell.CommandExecutor) {
	if execFn == nil {
		a.execCommand = shell.RunCommand
		return
	}
	a.execCommand = execFn
}

func (a *App) SetNowFunc(nowFn func() time.Time) {
	if nowFn == nil {
		a.nowFn = time.Now
		return
	}
	a.nowFn = nowFn
}

func (a *App) Run(ctx context.Context) int {
	subcommand := ""
	if len(a.args) > 0 {
		subcommand = a.args[0]
	}

	switch subcommand {
	case "save":
		return a.runSave(ctx, a.args[1:])
	case "load":
		return a.runLoad(a.args[1:])
	case "record-mention":
		return a.runRecordMention(a.args[1:])
	case "has-mention":
		return a.runHasMention(a.args[1:])
	case "extract-payload":
		return a.runExtractPayload(a.args[1:])
	case "validate-payload":
		return a.runValidatePayload(ctx, a.args[1:])
	default:
		a.printUsage()
		return 1
	}
}

func (a *App) runSave(ctx context.Context, args []string) int {
	if len(args) < 1 {
		a.printUsage()
		return 1
	}

	issue, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return shell.Fail(a.stderr, "State payload must be valid JSON")
	}

	stateDirArg, rest, err := parseStateDirArg(args[1:])
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}
	if len(rest) != 0 {
		a.printUsage()
		return 1
	}

	stateDir, code := a.stateDir(ctx, stateDirArg)
	if code != 0 {
		return code
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return shell.Failf(a.stderr, "Failed to create state directory: %v", err)
	}

	payloadBytes, err := io.ReadAll(a.stdin)
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read state payload: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return shell.Fail(a.stderr, "State payload must be valid JSON")
	}

	now := a.nowFn().UTC().Format(time.RFC3339)
	statePath := filepath.Join(stateDir, fmt.Sprintf("%d.json", issue))
	startedAt := now

	if shell.FileExists(statePath) {
		current, err := a.loadStateJSON(statePath)
		if err != nil {
			return shell.Fail(a.stderr, err.Error())
		}
		startedAt = stringOrDefault(current["started_at"], "")
		fromPhase := jsonScalarString(current["phase"])
		toPhase := jsonScalarString(payload["phase"])
		if err := validatePhaseTransition(fromPhase, toPhase); err != nil {
			return shell.Fail(a.stderr, err.Error())
		}
	}

	// Preserve any caller-provided started_at only when present and non-null.
	if _, ok := payload["started_at"]; !ok || payload["started_at"] == nil {
		payload["started_at"] = startedAt
	}
	payload["updated_at"] = now
	payload["issue"] = issue

	if err := writeAtomicJSON(statePath, payload); err != nil {
		return shell.Failf(a.stderr, "Failed to write state file: %v", err)
	}
	return a.writeStateFile(statePath)
}

func (a *App) runLoad(args []string) int {
	if len(args) < 1 {
		a.printUsage()
		return 1
	}

	issue := args[0]
	stateDirArg, rest, err := parseStateDirArg(args[1:])
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}
	if len(rest) != 0 {
		a.printUsage()
		return 1
	}

	stateDir, code := a.stateDir(context.Background(), stateDirArg)
	if code != 0 {
		return code
	}
	statePath := filepath.Join(stateDir, fmt.Sprintf("%s.json", issue))
	if !shell.FileExists(statePath) {
		return shell.Failf(a.stderr, "State file not found for issue %s", issue)
	}
	return a.writeStateFile(statePath)
}

func (a *App) runRecordMention(args []string) int {
	if len(args) < 1 {
		a.printUsage()
		return 1
	}

	commentID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return shell.Fail(a.stderr, "comment-id must be a number")
	}

	stateDirArg, rest, err := parseStateDirArg(args[1:])
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}
	if len(rest) != 0 {
		a.printUsage()
		return 1
	}

	stateDir, code := a.stateDir(context.Background(), stateDirArg)
	if code != 0 {
		return code
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return shell.Failf(a.stderr, "Failed to create state directory: %v", err)
	}

	mentionsPath := filepath.Join(stateDir, "processed-mentions.json")
	mentions, err := readMentions(mentionsPath)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}
	if !slices.Contains(mentions, commentID) {
		mentions = append(mentions, commentID)
	}

	if err := writeAtomicJSON(mentionsPath, mentions); err != nil {
		return shell.Failf(a.stderr, "Failed to write processed mentions: %v", err)
	}
	return writeJSON(a.stdout, a.stderr, mentions)
}

func (a *App) runHasMention(args []string) int {
	if len(args) < 1 {
		a.printUsage()
		return 1
	}

	commentID, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return shell.Fail(a.stderr, "comment-id must be a number")
	}

	stateDirArg, rest, err := parseStateDirArg(args[1:])
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}
	if len(rest) != 0 {
		a.printUsage()
		return 1
	}

	stateDir, code := a.stateDir(context.Background(), stateDirArg)
	if code != 0 {
		return code
	}
	mentionsPath := filepath.Join(stateDir, "processed-mentions.json")
	mentions, err := readMentions(mentionsPath)
	if err != nil {
		return shell.Fail(a.stderr, err.Error())
	}

	if slices.Contains(mentions, commentID) {
		_, _ = io.WriteString(a.stdout, "true\n")
		return 0
	}
	_, _ = io.WriteString(a.stdout, "false\n")
	return 1
}

func (a *App) runExtractPayload(args []string) int {
	if len(args) != 1 {
		a.printUsage()
		return 1
	}

	block, err := extractPayloadBlock(args[0])
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read payload file: %v", err)
	}
	if block == "" {
		return shell.Fail(a.stderr, "No fenced payload block found")
	}
	_, _ = io.WriteString(a.stdout, block)
	return 0
}

func (a *App) runValidatePayload(ctx context.Context, args []string) int {
	if len(args) != 3 {
		a.printUsage()
		return 1
	}

	output, err := ValidatePayloadJSON(ctx, a.execCommand, a.cwd, args[0], args[1], args[2])
	if err != nil {
		return shell.Failf(a.stderr, "Failed to collect git ground truth: %v", err)
	}
	if _, err := a.stdout.Write(output); err != nil {
		return shell.Failf(a.stderr, "Failed to write output: %v", err)
	}
	return 0
}

func ValidatePayloadJSON(ctx context.Context, exec shell.CommandExecutor, cwd string, worktree string, baseSHA string, source string) ([]byte, error) {
	app := New(nil, nil, cwd, nil, io.Discard, io.Discard)
	app.SetCommandExecutor(exec)

	block, err := extractPayloadBlock(source)
	threadID, _ := extractThreadID(source)

	truth, err := app.groundTruth(ctx, worktree, baseSHA)
	if err != nil {
		return nil, err
	}

	if err != nil || block == "" {
		synthesized := synthesizePayload(truth)
		if threadID != "" {
			synthesized.ThreadID = threadID
		}
		return marshalPayloadJSON(synthesized)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(block), &payload); err != nil {
		synthesized := synthesizePayload(truth)
		if threadID != "" {
			synthesized.ThreadID = threadID
		}
		return marshalPayloadJSON(synthesized)
	}

	normalized := normalizePayload(payload, truth)
	if threadID != "" {
		normalized.ThreadID = threadID
	}
	return marshalPayloadJSON(normalized)
}

func marshalPayloadJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (a *App) groundTruth(ctx context.Context, worktree string, baseSHA string) (groundTruth, error) {
	repo := gitops.OpenCLI(ctx, worktree, a.execCommand)

	commitLog, err := repo.CommitLog(baseSHA, "HEAD")
	if err != nil {
		return groundTruth{}, err
	}

	commits := make([]string, 0, len(commitLog))
	for _, c := range commitLog {
		commits = append(commits, c.SHA)
	}

	commitRange := ""
	if len(commits) > 0 {
		commitRange = commits[0] + ".." + commits[len(commits)-1]
	}

	diffChanges, err := repo.DiffNameStatus(baseSHA, "HEAD")
	if err != nil {
		return groundTruth{}, err
	}

	filesChanged := make([]string, 0)
	filesAdded := make([]string, 0)
	filesDeleted := make([]string, 0)
	for _, fc := range diffChanges {
		switch fc.Status {
		case "A":
			filesAdded = append(filesAdded, fc.Path)
		case "D":
			filesDeleted = append(filesDeleted, fc.Path)
		default:
			filesChanged = append(filesChanged, fc.Path)
		}
	}

	return groundTruth{
		CommitsPushed: commits,
		CommitRange:   commitRange,
		FilesChanged:  filesChanged,
		FilesAdded:    filesAdded,
		FilesDeleted:  filesDeleted,
	}, nil
}

func (a *App) loadStateJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		issue := strings.TrimSuffix(filepath.Base(path), ".json")
		return nil, contractError{message: fmt.Sprintf("State file is corrupted for issue %s", issue)}
	}
	return state, nil
}

func (a *App) writeStateFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return shell.Failf(a.stderr, "Failed to read state file: %v", err)
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		issue := strings.TrimSuffix(filepath.Base(path), ".json")
		return shell.Failf(a.stderr, "State file is corrupted for issue %s", issue)
	}
	return writeJSON(a.stdout, a.stderr, parsed)
}

func (a *App) stateDir(ctx context.Context, stateDirArg string) (string, int) {
	if strings.TrimSpace(stateDirArg) != "" {
		return stateDirArg, 0
	}
	if value, ok := shell.EnvLookup(a.env, "RUNOQ_STATE_DIR"); ok && strings.TrimSpace(value) != "" {
		return value, 0
	}
	targetRoot, code := a.targetRoot(ctx)
	if code != 0 {
		return "", code
	}
	return filepath.Join(targetRoot, ".runoq", "state"), 0
}

func (a *App) targetRoot(_ context.Context) (string, int) {
	if value, ok := shell.EnvLookup(a.env, "TARGET_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, 0
	}
	root, err := gitops.FindRoot(a.cwd)
	if err != nil {
		return "", shell.Fail(a.stderr, "Run runoq from inside a git repository.")
	}
	return root, 0
}

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}

func normalizePayload(payload map[string]any, truth groundTruth) normalizedPayload {
	schemaErrors := payloadSchemaErrors(payload)
	result := normalizedPayload{
		Status:              validStatus(payload["status"]),
		CommitsPushed:       append([]string(nil), truth.CommitsPushed...),
		CommitRange:         truth.CommitRange,
		FilesChanged:        append([]string(nil), truth.FilesChanged...),
		FilesAdded:          append([]string(nil), truth.FilesAdded...),
		FilesDeleted:        append([]string(nil), truth.FilesDeleted...),
		TestsRun:            boolOr(payload["tests_run"], false),
		TestsPassed:         boolOr(payload["tests_passed"], false),
		TestSummary:         stringOrDefault(payload["test_summary"], ""),
		BuildPassed:         boolOr(payload["build_passed"], false),
		Blockers:            stringArrayOr(payload["blockers"], []string{}),
		Notes:               stringOrDefault(payload["notes"], ""),
		PayloadSchemaValid:  len(schemaErrors) == 0,
		PayloadSchemaErrors: schemaErrors,
		PayloadSource:       "clean",
	}
	if len(schemaErrors) > 0 {
		result.PayloadSource = "patched"
	}

	patched := make([]string, 0)
	if !isValidStatus(payload["status"]) {
		patched = append(patched, "status")
	}
	if _, ok := payload["tests_run"].(bool); !ok {
		patched = append(patched, "tests_run")
	}
	if _, ok := payload["tests_passed"].(bool); !ok {
		patched = append(patched, "tests_passed")
	}
	if _, ok := payload["test_summary"].(string); !ok {
		patched = append(patched, "test_summary")
	}
	if _, ok := payload["build_passed"].(bool); !ok {
		patched = append(patched, "build_passed")
	}
	if blockers, has := payload["blockers"]; has {
		if _, ok := blockers.([]any); !ok {
			patched = append(patched, "blockers")
		}
	} else {
		patched = append(patched, "blockers")
	}
	if notes, has := payload["notes"]; has {
		if _, ok := notes.(string); !ok {
			patched = append(patched, "notes")
		}
	} else {
		patched = append(patched, "notes")
	}
	result.PatchedFields = uniqueSorted(patched)

	discrepancies := make([]string, 0)
	if _, ok := payload["commits_pushed"]; ok && truthBackedMismatch(payload["commits_pushed"], truth.CommitsPushed) {
		discrepancies = append(discrepancies, "commits_pushed_mismatch")
	}
	if _, ok := payload["commit_range"]; ok && truthBackedMismatch(payload["commit_range"], truth.CommitRange) {
		discrepancies = append(discrepancies, "commit_range_mismatch")
	}
	if _, ok := payload["files_changed"]; ok && truthBackedMismatch(payload["files_changed"], truth.FilesChanged) {
		discrepancies = append(discrepancies, "files_changed_mismatch")
	}
	if _, ok := payload["files_added"]; ok && truthBackedMismatch(payload["files_added"], truth.FilesAdded) {
		discrepancies = append(discrepancies, "files_added_mismatch")
	}
	if _, ok := payload["files_deleted"]; ok && truthBackedMismatch(payload["files_deleted"], truth.FilesDeleted) {
		discrepancies = append(discrepancies, "files_deleted_mismatch")
	}
	result.Discrepancies = uniqueSorted(discrepancies)

	if result.CommitsPushed == nil {
		result.CommitsPushed = []string{}
	}
	if result.FilesChanged == nil {
		result.FilesChanged = []string{}
	}
	if result.FilesAdded == nil {
		result.FilesAdded = []string{}
	}
	if result.FilesDeleted == nil {
		result.FilesDeleted = []string{}
	}
	if result.Blockers == nil {
		result.Blockers = []string{}
	}
	if result.PatchedFields == nil {
		result.PatchedFields = []string{}
	}
	if result.Discrepancies == nil {
		result.Discrepancies = []string{}
	}
	if result.PayloadSchemaErrors == nil {
		result.PayloadSchemaErrors = []string{}
	}
	return result
}

func synthesizePayload(truth groundTruth) normalizedPayload {
	return normalizedPayload{
		Status:              "failed",
		CommitsPushed:       truth.CommitsPushed,
		CommitRange:         truth.CommitRange,
		FilesChanged:        truth.FilesChanged,
		FilesAdded:          truth.FilesAdded,
		FilesDeleted:        truth.FilesDeleted,
		TestsRun:            false,
		TestsPassed:         false,
		TestSummary:         "",
		BuildPassed:         false,
		Blockers:            []string{"Codex did not return a structured payload"},
		Notes:               "",
		PayloadSchemaValid:  false,
		PayloadSchemaErrors: []string{"payload_missing_or_malformed"},
		PayloadSource:       "synthetic",
		PatchedFields: []string{
			"status",
			"commits_pushed",
			"commit_range",
			"files_changed",
			"files_added",
			"files_deleted",
			"tests_run",
			"tests_passed",
			"test_summary",
			"build_passed",
			"blockers",
			"notes",
		},
		Discrepancies: []string{"payload_missing_or_malformed"},
	}
}

func payloadSchemaErrors(payload map[string]any) []string {
	errorsList := make([]string, 0, 7)
	if !isValidStatus(payload["status"]) {
		errorsList = append(errorsList, "status_missing_or_invalid")
	}
	if _, ok := payload["tests_run"].(bool); !ok {
		errorsList = append(errorsList, "tests_run_missing_or_non_boolean")
	}
	if _, ok := payload["tests_passed"].(bool); !ok {
		errorsList = append(errorsList, "tests_passed_missing_or_non_boolean")
	}
	if _, ok := payload["test_summary"].(string); !ok {
		errorsList = append(errorsList, "test_summary_missing_or_non_string")
	}
	if _, ok := payload["build_passed"].(bool); !ok {
		errorsList = append(errorsList, "build_passed_missing_or_non_boolean")
	}
	if _, ok := parseStringArray(payload["blockers"]); !ok {
		errorsList = append(errorsList, "blockers_missing_or_non_string_array")
	}
	if _, ok := payload["notes"].(string); !ok {
		errorsList = append(errorsList, "notes_missing_or_non_string")
	}
	return uniqueSorted(errorsList)
}

func validatePhaseTransition(from string, to string) error {
	if from == to {
		return nil
	}
	allowed := map[string]struct{}{
		"INIT:DEVELOP":    {},
		"INIT:FINALIZE":   {},
		"INIT:FAILED":     {},
		"DEVELOP:VERIFY":  {},
		"DEVELOP:FAILED":  {},
		"VERIFY:REVIEW":   {},
		"VERIFY:DECIDE":   {},
		"VERIFY:FAILED":   {},
		"REVIEW:DECIDE":   {},
		"REVIEW:FAILED":   {},
		"DECIDE:DEVELOP":  {},
		"DECIDE:FINALIZE": {},
		"DECIDE:FAILED":   {},
		"FINALIZE:DONE":   {},
		"FINALIZE:FAILED": {},
		"FAILED:INIT":     {}, // retry from scratch
	}
	key := from + ":" + to
	if _, ok := allowed[key]; ok {
		return nil
	}
	if from == "DONE" || from == "FAILED" {
		return contractError{message: fmt.Sprintf("Invalid transition from terminal phase %s to %s", from, to)}
	}
	return contractError{message: fmt.Sprintf("Invalid phase transition: %s -> %s", from, to)}
}

func parseStateDirArg(args []string) (string, []string, error) {
	if len(args) > 0 && args[0] == "--state-dir" {
		if len(args) < 2 {
			return "", nil, errors.New("--state-dir requires a value")
		}
		return args[1], args[2:], nil
	}
	return "", args, nil
}

func extractPayloadBlock(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(data), "\n")

	sawMarker := false
	inBlock := false
	var block strings.Builder
	for _, line := range lines {
		if !sawMarker {
			if line == "<!-- runoq:payload:codex-return -->" {
				sawMarker = true
			}
			continue
		}
		if strings.HasPrefix(line, "```") {
			if !inBlock {
				inBlock = true
				block.Reset()
				continue
			}
			return block.String(), nil
		}
		if inBlock {
			block.WriteString(line)
			block.WriteString("\n")
		}
	}

	inBlock = false
	block.Reset()
	lastBlock := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inBlock {
				lastBlock = block.String()
				block.Reset()
				inBlock = false
			} else {
				inBlock = true
				block.Reset()
			}
			continue
		}
		if inBlock {
			block.WriteString(line)
			block.WriteString("\n")
		}
	}
	return lastBlock, nil
}

func extractThreadID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	threadID := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}

		eventType := stringOrDefault(payload["type"], "")
		if eventType == "" {
			eventType = stringOrDefault(payload["event"], "")
		}
		if eventType != "thread.started" {
			continue
		}

		if id := threadIDFromEvent(payload); id != "" {
			threadID = id
		}
	}

	return threadID, nil
}

func threadIDFromEvent(event map[string]any) string {
	if id := strings.TrimSpace(stringOrDefault(event["thread_id"], "")); id != "" {
		return id
	}
	threadValue, ok := event["thread"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(stringOrDefault(threadValue["id"], ""))
}

func readMentions(path string) ([]int64, error) {
	if !shell.FileExists(path) {
		return []int64{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var mentions []int64
	if err := json.Unmarshal(data, &mentions); err != nil {
		return nil, contractError{message: "Processed mentions file is corrupted"}
	}
	return mentions, nil
}

func truthBackedStringArray(value any, fallback []string) []string {
	parsed, ok := parseStringArray(value)
	if ok && slices.Equal(parsed, fallback) {
		return parsed
	}
	return append([]string(nil), fallback...)
}

func truthBackedString(value any, fallback string) string {
	s, ok := value.(string)
	if ok && s == fallback {
		return s
	}
	return fallback
}

func truthBackedMismatch(value any, fallback any) bool {
	if value == nil {
		return true
	}
	switch expected := fallback.(type) {
	case string:
		actual, ok := value.(string)
		if !ok {
			return true
		}
		return actual != expected
	case []string:
		actual, ok := parseStringArray(value)
		if !ok {
			return true
		}
		return !slices.Equal(actual, expected)
	default:
		return true
	}
}

func stringArrayOr(value any, fallback []string) []string {
	parsed, ok := parseStringArray(value)
	if !ok {
		return append([]string(nil), fallback...)
	}
	return parsed
}

func parseStringArray(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, s)
	}
	return result, true
}

func isValidStatus(value any) bool {
	status, ok := value.(string)
	if !ok {
		return false
	}
	return status == "completed" || status == "failed" || status == "stuck"
}

func validStatus(value any) string {
	if isValidStatus(value) {
		return value.(string)
	}
	return "failed"
}

func boolOr(value any, fallback bool) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return fallback
}

func stringOrDefault(value any, fallback string) string {
	if s, ok := value.(string); ok {
		return s
	}
	return fallback
}

func jsonScalarString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	unique := make([]string, 0, len(seen))
	for value := range seen {
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func writeAtomicJSON(path string, value any) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	temp, err := os.CreateTemp(dir, "."+base+".")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	data, err := marshalJSON(value)
	if err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writeJSON(w io.Writer, stderr io.Writer, value any) int {
	data, err := marshalJSON(value)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "runoq: Failed to encode JSON output: %v\n", err)
		return 1
	}
	_, _ = w.Write(data)
	return 0
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return data, nil
}
