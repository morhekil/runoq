package issuerunner

import (
	"bufio"
	"context"
	"encoding/json"
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

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	execCommand shell.CommandExecutor
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
		execCommand: shell.RunCommand,
	}
}

// logAgent writes a progress line to stderr, tagged with the agent name and issue number.
func (a *App) logAgent(agent string, input *inputPayload, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(a.stderr, "[%s] #%d: %s\n", agent, input.IssueNumber, msg)
}

func (a *App) SetCommandExecutor(execFn shell.CommandExecutor) {
	if execFn == nil {
		a.execCommand = shell.RunCommand
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
			return shell.Fail(a.stderr, "usage: issue-runner run <payload-json-file>")
		}
		return a.runIssue(ctx, a.args[1])
	default:
		return shell.Failf(a.stderr, "unknown command: %s", a.args[0])
	}
}

func (a *App) runIssue(ctx context.Context, payloadPath string) int {
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		return shell.Failf(a.stderr, "failed to read payload: %v", err)
	}

	var input inputPayload
	if err := json.Unmarshal(data, &input); err != nil {
		return shell.Failf(a.stderr, "failed to parse payload: %v", err)
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
			return shell.Failf(a.stderr, "failed to create log dir: %v", err)
		}
	}

	// Get baseline hash
	repo := gitops.OpenCLI(ctx, input.Worktree, a.execCommand)
	baseline, err := repo.ResolveHEAD()
	if err != nil {
		return shell.Failf(a.stderr, "failed to get baseline: %v", err)
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
	result := a.developmentLoop(ctx, &input, state, specRequirements, repo)

	// Emit output
	return shell.WriteJSON(a.stdout, a.stderr, result)
}

func (a *App) developmentLoop(ctx context.Context, input *inputPayload, state *roundState, specRequirements string, repo gitops.Repo) *outputPayload {
	round := state.round

	// Budget check before starting a round.
	if input.MaxTokenBudget > 0 && state.cumulativeTokens >= input.MaxTokenBudget {
		a.logAgent("issue-runner", input, "budget exhausted before round %d", round)
		return a.emitResult("budget_exhausted", input, state, specRequirements,
			nil,
			false, nil, nil, nil,
			fmt.Sprintf("Token budget exhausted before round %d", round),
			[]string{"Token budget exhausted"})
	}

	a.logAgent("codex", input, "round %d/%d — starting", round, input.MaxRounds)
	prompt := a.buildCodexPrompt(input, state, specRequirements)
	eventLog, lastMsgFile := a.roundPaths(state, round, "")
	payloadFile := filepath.Join(state.logDir, fmt.Sprintf("round-%d-payload.json", round))

	codexErr := a.invokeCodex(ctx, input, state, prompt, eventLog, lastMsgFile)

	a.logAgent("codex", input, "round %d — finished", round)

	// Short-circuit on transient errors — don't burn the round.
	if isTransient, reason := a.classifyTransientError(eventLog, codexErr); isTransient {
		a.logAgent("codex", input, "round %d — transient error: %s", round, reason)
		return a.emitResult("transient_error", input, state, specRequirements,
			nil,
			false, nil, nil, nil,
			reason, []string{reason})
	}

	threadID := a.extractThreadID(eventLog)
	state.threadID = threadID

	payloadValid := a.validatePayload(ctx, input.Worktree, state.baseline, lastMsgFile, payloadFile)
	roundTokens := a.extractTokens(eventLog)

	const maxSchemaRetries = 2
	schemaRetryCount := 0
	for !payloadValid && threadID != "" && schemaRetryCount < maxSchemaRetries {
		schemaRetryCount++
		retryEventLog, retryLastMsg := a.roundPaths(state, round, fmt.Sprintf("schema-retry-%d", schemaRetryCount))
		retryPrompt := a.buildSchemaRetryPrompt()

		_ = a.resumeCodex(ctx, input, state, threadID, retryPrompt, retryEventLog, retryLastMsg)

		if tid := a.extractThreadID(retryEventLog); tid != "" {
			threadID = tid
			state.threadID = tid
		}

		payloadValid = a.validatePayload(ctx, input.Worktree, state.baseline, retryLastMsg, payloadFile)
		roundTokens += a.extractTokens(retryEventLog)
	}

	a.extractCommits(ctx, input, state, repo)
	state.cumulativeTokens += roundTokens

	if input.MaxTokenBudget > 0 && state.cumulativeTokens >= input.MaxTokenBudget {
		return a.emitResult("budget_exhausted", input, state, specRequirements,
			nil,
			false, nil, nil, nil,
			fmt.Sprintf("Token budget exhausted after round %d", round),
			[]string{"Token budget exhausted"})
	}

	verificationPayload, _ := a.readPayload(payloadFile)
	changedFiles := a.changedFilesFromPayload(verificationPayload)
	caveats := []string{}
	summary := fmt.Sprintf("Development round %d completed; ready for VERIFY", round)
	if !payloadValid {
		reason := "codex payload schema invalid"
		if threadID != "" {
			reason += fmt.Sprintf(" after %d resume attempt(s)", schemaRetryCount)
		} else {
			reason += " and thread_id missing from codex events"
		}
		caveats = append(caveats, reason)
		summary = fmt.Sprintf("Development round %d completed with schema issues; VERIFY should record the failure", round)
	}

	return a.emitResult("completed", input, state, specRequirements,
		verificationPayload,
		false, nil, changedFiles, nil,
		summary, caveats)
}

// runoqRoot returns the RUNOQ_ROOT path from the environment.
func (a *App) runoqRoot() string {
	root, _ := shell.EnvLookup(a.env, "RUNOQ_ROOT")
	return root
}

// roundPaths returns the event log and last-message file paths for a round (or retry suffix).
func (a *App) roundPaths(state *roundState, round int, suffix string) (eventLog, lastMsg string) {
	if suffix == "" {
		eventLog = filepath.Join(state.logDir, fmt.Sprintf("round-%d-codex-events.jsonl", round))
		lastMsg = filepath.Join(state.logDir, fmt.Sprintf("round-%d-last-message.md", round))
	} else {
		eventLog = filepath.Join(state.logDir, fmt.Sprintf("round-%d-%s-events.jsonl", round, suffix))
		lastMsg = filepath.Join(state.logDir, fmt.Sprintf("round-%d-%s-last-message.md", round, suffix))
	}
	return
}

// buildCodexPrompt constructs the prompt for codex invocation.
func (a *App) buildCodexPrompt(input *inputPayload, state *roundState, specRequirements string) string {
	schemaBlock := requiredPayloadSchemaBlock()

	if state.previousChecklist == "None — first round" {
		var protectedWarning string
		if input.CriteriaCommit != "" {
			protectedWarning = "\nIMPORTANT: Do not modify acceptance-criteria files set by the bar-setter.\n"
		}
		return fmt.Sprintf(`Implement the following spec. Read the spec file and all AGENTS.md files for rules and constraints.

Spec: %s
%sCommit granularity: make one commit per semantic unit of work.
When done, push your branch: git push origin %s

Then print the required final stdout payload block:
%s`, input.SpecPath, protectedWarning, input.Branch, schemaBlock)
	}

	return fmt.Sprintf(`Address the following code review or verification feedback.

Checklist:
%s

Original spec: %s
Read all AGENTS.md files for rules and constraints.
Commit granularity: make one commit per semantic unit of work.
When done, push your branch: git push origin %s

Then print the required final stdout payload block:
%s`, state.previousChecklist, input.SpecPath, input.Branch, schemaBlock)
}

// requiredPayloadSchemaBlock returns the schema block codex must emit.
func requiredPayloadSchemaBlock() string {
	return `<!-- runoq:payload:codex-return -->
` + "```json" + `
{
  "status": "completed" | "failed" | "stuck",
  "commits_pushed": ["<sha>", "..."],
  "commit_range": "<first-sha>..<last-sha>",
  "files_changed": ["path", "..."],
  "files_added": ["path", "..."],
  "files_deleted": ["path", "..."],
  "tests_run": true | false,
  "tests_passed": true | false,
  "test_summary": "<short summary>",
  "build_passed": true | false,
  "blockers": ["message", "..."],
  "notes": "<short note>"
}
` + "```"
}

// buildSchemaRetryPrompt returns the prompt for a schema retry.
func (a *App) buildSchemaRetryPrompt() string {
	return fmt.Sprintf(`Your last payload block did not satisfy the required payload schema.

Return ONLY a corrected payload block using this exact schema (verbatim):
%s

Do not run additional commands. Re-emit only the corrected final payload block with strict JSON types.`, requiredPayloadSchemaBlock())
}

// invokeCodex runs the codex binary with the given prompt.
func (a *App) invokeCodex(ctx context.Context, input *inputPayload, state *roundState, prompt, eventLogPath, lastMsgPath string) error {
	codexBin := "codex"
	if bin, ok := shell.EnvLookup(a.env, "RUNOQ_CODEX_BIN"); ok && bin != "" {
		codexBin = bin
	}

	absLastMsg, _ := filepath.Abs(lastMsgPath)

	// Ensure parent dirs exist.
	if err := os.MkdirAll(filepath.Dir(eventLogPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absLastMsg), 0o755); err != nil {
		return err
	}

	eventFile, _ := os.Create(eventLogPath)
	defer func() {
		_ = eventFile.Close()
	}()

	captureDir := filepath.Join(state.logDir, fmt.Sprintf("codex-round-%d", state.round))
	env := shell.EnvSet(a.env, "RUNOQ_CODEX_CAPTURE_DIR", captureDir)

	return a.execCommand(ctx, shell.CommandRequest{
		Name:   codexBin,
		Args:   []string{"exec", "--dangerously-bypass-approvals-and-sandbox", "--json", "-o", absLastMsg, prompt},
		Dir:    input.Worktree,
		Env:    env,
		Stdout: eventFile,
		Stderr: a.stderr,
	})
}

// resumeCodex resumes a codex thread with a retry prompt.
func (a *App) resumeCodex(ctx context.Context, input *inputPayload, state *roundState, threadID, prompt, eventLogPath, lastMsgPath string) error {
	codexBin := "codex"
	if bin, ok := shell.EnvLookup(a.env, "RUNOQ_CODEX_BIN"); ok && bin != "" {
		codexBin = bin
	}

	absLastMsg, _ := filepath.Abs(lastMsgPath)

	if err := os.MkdirAll(filepath.Dir(eventLogPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absLastMsg), 0o755); err != nil {
		return err
	}

	eventFile, _ := os.Create(eventLogPath)
	defer func() {
		_ = eventFile.Close()
	}()

	return a.execCommand(ctx, shell.CommandRequest{
		Name:   codexBin,
		Args:   []string{"exec", "resume", threadID, "--json", "-o", absLastMsg, prompt},
		Dir:    input.Worktree,
		Env:    a.env,
		Stdout: eventFile,
		Stderr: a.stderr,
	})
}

// extractThreadID parses event JSONL for a thread.started event and returns the thread_id.
func (a *App) extractThreadID(eventsPath string) string {
	f, err := os.Open(eventsPath)
	if err != nil {
		return ""
	}
	defer func() {
		_ = f.Close()
	}()

	var lastThreadID string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		eventType := ""
		if t, ok := event["type"].(string); ok {
			eventType = t
		} else if t, ok := event["event"].(string); ok {
			eventType = t
		}
		if eventType != "thread.started" {
			continue
		}
		if tid, ok := event["thread_id"].(string); ok && tid != "" {
			lastThreadID = tid
		} else if thread, ok := event["thread"].(map[string]any); ok {
			if tid, ok := thread["id"].(string); ok && tid != "" {
				lastThreadID = tid
			}
		}
	}
	return lastThreadID
}

// transientPatterns matches known transient error substrings from codex event logs.
var transientPatterns = []string{
	"at capacity",
	"rate limit",
	"rate_limit",
	"overloaded",
	"429",
	"503",
}

// classifyTransientError inspects a codex event log for transient failures.
// It returns true with a reason when the failure is transient (capacity, rate
// limit, network) and should be retried rather than escalated.
func (a *App) classifyTransientError(eventsPath string, execErr error) (bool, string) {
	f, err := os.Open(eventsPath)
	if err != nil {
		// Can't read log at all — if codex also failed, treat as transient.
		if execErr != nil {
			return true, fmt.Sprintf("codex failed (%v) and event log unreadable", execErr)
		}
		return false, ""
	}
	defer func() {
		_ = f.Close()
	}()

	hasOutput := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		eventType := ""
		if t, ok := event["type"].(string); ok {
			eventType = t
		} else if t, ok := event["event"].(string); ok {
			eventType = t
		}

		// Any successful thread or turn means codex did produce work.
		if eventType == "thread.started" || eventType == "turn.completed" {
			hasOutput = true
		}

		// Check for transient error events.
		if eventType == "turn.failed" {
			errMsg := ""
			if e, ok := event["error"].(string); ok {
				errMsg = strings.ToLower(e)
			} else if e, ok := event["message"].(string); ok {
				errMsg = strings.ToLower(e)
			}
			for _, pattern := range transientPatterns {
				if strings.Contains(errMsg, pattern) {
					return true, fmt.Sprintf("transient codex error: %s", event["error"])
				}
			}
		}
	}

	// Codex exited with error and produced no useful output.
	if execErr != nil && !hasOutput {
		return true, fmt.Sprintf("codex failed (%v) with no output", execErr)
	}

	return false, ""
}

// tokenPattern matches lines like "tokens: 12345" or "token_usage: 12345".
var tokenPattern = regexp.MustCompile(`(?i)tokens?[_ ]*(?:used|usage|count)?\s*[:=]\s*(\d+)`)

// extractTokens sums token counts from a codex event log.
func (a *App) extractTokens(logPath string) int {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0
	}
	// Find the last match (matches shell behavior: tail -1).
	matches := tokenPattern.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		return 0
	}
	last := matches[len(matches)-1]
	n, _ := strconv.Atoi(last[1])
	return n
}

// validatePayload calls state.sh validate-payload and writes the result to payloadFile.
// Returns true if the payload schema is valid.
func (a *App) validatePayload(ctx context.Context, worktree, baseline, lastMsgFile, payloadFile string) bool {
	root := a.runoqRoot()
	if root == "" {
		return false
	}
	stateScript := filepath.Join(root, "scripts", "state.sh")

	out, err := shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
		Name: stateScript,
		Args: []string{"validate-payload", worktree, baseline, lastMsgFile},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		return false
	}

	// Write the validate output to the payload file.
	if err := os.WriteFile(payloadFile, []byte(out), 0o644); err != nil {
		return false
	}

	// Check if payload_schema_valid is true.
	var parsed map[string]any
	if json.Unmarshal([]byte(out), &parsed) != nil {
		return false
	}
	if v, ok := parsed["payload_schema_valid"].(bool); ok {
		return v
	}
	return false
}

func (a *App) readPayload(payloadFile string) (map[string]any, error) {
	data, err := os.ReadFile(payloadFile)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// extractCommits updates state with commit info from baseline..HEAD.
func (a *App) extractCommits(ctx context.Context, input *inputPayload, state *roundState, repo gitops.Repo) {
	commits, err := repo.CommitLog(state.baseline, "HEAD")
	if err != nil {
		return
	}

	var subjects []string
	for _, c := range commits {
		subjects = append(subjects, c.SHA+" "+c.Subject)
	}
	state.commitSubjects = subjects

	// Update head hash.
	if head, err := repo.ResolveHEAD(); err == nil {
		state.headHash = head
	}
}

// runVerification calls verify.sh round and parses the result.
func (a *App) runVerification(ctx context.Context, worktree, branch, baseline, payloadFile string) (verifyResult, error) {
	root := a.runoqRoot()
	var vr verifyResult
	if root == "" {
		vr.Failures = []string{"RUNOQ_ROOT not set"}
		return vr, fmt.Errorf("RUNOQ_ROOT not set")
	}
	verifyScript := filepath.Join(root, "scripts", "verify.sh")

	out, err := shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
		Name: verifyScript,
		Args: []string{"round", worktree, branch, baseline, payloadFile},
		Dir:  a.cwd,
		Env:  a.env,
	})
	if err != nil {
		vr.Failures = []string{fmt.Sprintf("verify.sh failed: %v", err)}
		return vr, err
	}

	if err := json.Unmarshal([]byte(out), &vr); err != nil {
		vr.Failures = []string{fmt.Sprintf("failed to parse verify output: %v", err)}
		return vr, err
	}
	return vr, nil
}

// postVerificationComment posts verification failures as a PR comment.
func (a *App) postVerificationComment(ctx context.Context, input *inputPayload, state *roundState, round int, roundBaseline string, failures []string) {
	root := a.runoqRoot()
	if root == "" || input.PRNumber == 0 {
		return
	}
	lifecycleScript := filepath.Join(root, "scripts", "gh-pr-lifecycle.sh")

	// Build comment body.
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- runoq:bot:verifier -->\n")
	fmt.Fprintf(&b, "## Verification failure — round %d\n\n", round)
	fmt.Fprintf(&b, "> Posted by `issue-runner` / `verify.sh` — round %d of %d, branch `%s`\n\n", round, input.MaxRounds, input.Branch)
	fmt.Fprintf(&b, "**Commit range**: `%s..%s`\n", short(roundBaseline), short(state.headHash))
	fmt.Fprintf(&b, "\n### Failures (%d)\n\n", len(failures))
	for _, f := range failures {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	fmt.Fprintf(&b, "\n---\n_This is an automated verification check. The developer agent will attempt to fix these issues in the next round._\n")

	// Write to temp file and invoke lifecycle script.
	tmpFile := filepath.Join(state.logDir, fmt.Sprintf("round-%d-verify-comment.md", round))
	if err := os.WriteFile(tmpFile, []byte(b.String()), 0o644); err != nil {
		return
	}

	_ = a.execCommand(ctx, shell.CommandRequest{
		Name: lifecycleScript,
		Args: []string{"comment", input.Repo, fmt.Sprintf("%d", input.PRNumber), tmpFile},
		Dir:  a.cwd,
		Env:  a.env,
	})
}

// postVerificationSuccessComment posts a verification-passed PR comment.
func (a *App) postVerificationSuccessComment(ctx context.Context, input *inputPayload, state *roundState, round int, roundBaseline string) {
	root := a.runoqRoot()
	if root == "" || input.PRNumber == 0 {
		return
	}
	lifecycleScript := filepath.Join(root, "scripts", "gh-pr-lifecycle.sh")

	var b strings.Builder
	fmt.Fprintf(&b, "<!-- runoq:bot:verifier -->\n")
	fmt.Fprintf(&b, "## Verification passed — round %d\n\n", round)
	fmt.Fprintf(&b, "> Posted by `issue-runner` / `verify.sh` — round %d of %d, branch `%s`\n\n", round, input.MaxRounds, input.Branch)
	fmt.Fprintf(&b, "**Commit range**: `%s..%s`\n", short(roundBaseline), short(state.headHash))

	tmpFile := filepath.Join(state.logDir, fmt.Sprintf("round-%d-verify-success-comment.md", round))
	if err := os.WriteFile(tmpFile, []byte(b.String()), 0o644); err != nil {
		return
	}

	_ = a.execCommand(ctx, shell.CommandRequest{
		Name: lifecycleScript,
		Args: []string{"comment", input.Repo, fmt.Sprintf("%d", input.PRNumber), tmpFile},
		Dir:  a.cwd,
		Env:  a.env,
	})
}

// short returns the first 7 characters of a hash.
func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

func (a *App) changedFilesFromPayload(payload map[string]any) []string {
	if len(payload) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var result []string
	for _, key := range []string{"files_changed", "files_added", "files_deleted"} {
		values, ok := payload[key].([]any)
		if !ok {
			continue
		}
		for _, value := range values {
			path, ok := value.(string)
			if !ok || strings.TrimSpace(path) == "" || seen[path] {
				continue
			}
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

// mergeChangedFiles combines all file lists from a verify result into a deduplicated slice.
func (a *App) mergeChangedFiles(vr verifyResult) []string {
	seen := make(map[string]bool)
	var result []string
	for _, lists := range [][]string{vr.Actual.FilesChanged, vr.Actual.FilesAdded, vr.Actual.FilesDeleted} {
		for _, f := range lists {
			if !seen[f] {
				seen[f] = true
				result = append(result, f)
			}
		}
	}
	return result
}

// expandReviewScope finds files that reference the changed files.
func (a *App) expandReviewScope(ctx context.Context, worktree string, changedFiles []string) []string {
	if len(changedFiles) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	for _, f := range changedFiles {
		seen[f] = true
	}

	var related []string
	for _, changed := range changedFiles {
		base := filepath.Base(changed)
		ext := filepath.Ext(base)
		nameNoExt := strings.TrimSuffix(base, ext)
		if nameNoExt == "" {
			continue
		}

		out, err := shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
			Name: "grep",
			Args: []string{"-rl", "--include=*.ts", "--include=*.js", "--include=*.py", "--include=*.go", nameNoExt, worktree + "/"},
			Dir:  a.cwd,
			Env:  a.env,
		})
		if err != nil || out == "" {
			continue
		}

		for _, hit := range strings.Split(out, "\n") {
			hit = strings.TrimSpace(hit)
			if hit == "" {
				continue
			}
			rel := strings.TrimPrefix(hit, worktree+"/")

			// Filter vendored/generated dirs.
			if strings.HasPrefix(rel, "node_modules/") || strings.HasPrefix(rel, "vendor/") ||
				strings.HasPrefix(rel, "dist/") || strings.HasPrefix(rel, "build/") {
				continue
			}
			// Filter test files.
			if isTestFile(rel) {
				continue
			}
			if !seen[rel] {
				seen[rel] = true
				related = append(related, rel)
			}
		}
	}
	return related
}

// isTestFile returns true if the path looks like a test file.
func isTestFile(rel string) bool {
	base := filepath.Base(rel)
	for _, pat := range []string{".test.", ".spec.", "_test.", "_spec."} {
		if strings.Contains(base, pat) {
			return true
		}
	}
	for _, prefix := range []string{"test/", "tests/", "__tests__/"} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// emitResult constructs an outputPayload with the given parameters.
func (a *App) emitResult(status string, input *inputPayload, state *roundState, specRequirements string,
	verificationPayload map[string]any,
	verificationPassed bool, verificationFailures []string,
	changedFiles []string, relatedFiles []string,
	summary string, caveats []string) *outputPayload {

	return &outputPayload{
		Status:               status,
		Round:                state.round,
		TotalRounds:          input.MaxRounds,
		LogDir:               state.logDir,
		BaselineHash:         state.baseline,
		HeadHash:             state.headHash,
		CommitRange:          state.baseline + ".." + state.headHash,
		ReviewLogPath:        filepath.Join(state.logDir, fmt.Sprintf("round-%d-diff-review.md", state.round)),
		SpecRequirements:     specRequirements,
		ChangedFiles:         changedFiles,
		RelatedFiles:         relatedFiles,
		CumulativeTokens:     state.cumulativeTokens,
		VerificationPayload:  verificationPayload,
		VerificationPassed:   verificationPassed,
		VerificationFailures: verificationFailures,
		Caveats:              caveats,
		Summary:              summary,
	}
}

// NewDirect creates an App for direct Go calls (not subprocess).
func NewDirect(env []string, cwd string, logWriter io.Writer) *App {
	stderr := io.Writer(io.Discard)
	if logWriter != nil {
		stderr = logWriter
	}
	return &App{
		env:         append([]string(nil), env...),
		cwd:         cwd,
		stdout:      io.Discard,
		stderr:      stderr,
		execCommand: shell.RunCommand,
	}
}

// RunDevelop runs the development loop directly (no subprocess).
// The caller passes the payload file path, same as the "run" subcommand.
func (a *App) RunDevelop(ctx context.Context, payloadPath string) (*outputPayload, error) {
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload: %v", err)
	}

	var input inputPayload
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("failed to parse payload: %v", err)
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

	specRequirements := ""
	if input.SpecPath != "" {
		if specData, err := os.ReadFile(input.SpecPath); err == nil {
			specRequirements = strings.TrimSpace(string(specData))
		}
	}

	logDir := input.LogDir
	if logDir == "" {
		logDir = filepath.Join(a.cwd, "log", fmt.Sprintf("issue-%d-%d", input.IssueNumber, os.Getpid()))
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create log dir: %v", err)
		}
	}

	repo := gitops.OpenCLI(ctx, input.Worktree, a.execCommand)
	baseline, err := repo.ResolveHEAD()
	if err != nil {
		return nil, fmt.Errorf("failed to get baseline: %v", err)
	}

	state := &roundState{
		round:             input.Round,
		logDir:            logDir,
		baseline:          baseline,
		headHash:          baseline,
		cumulativeTokens:  input.CumulativeTokens,
		previousChecklist: input.PreviousChecklist,
	}

	result := a.developmentLoop(ctx, &input, state, specRequirements, repo)
	return result, nil
}

// OutputPayload returns the exported type alias for outputPayload.
type OutputPayload = outputPayload

func (a *App) printUsage() {
	_, _ = io.WriteString(a.stderr, usageText)
}
