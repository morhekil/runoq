package orchestrator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/saruman/runoq/internal/shell"
)

func metadataFromIssueView(issue issueView) IssueMetadata {
	meta := parseMetadataBlock(issue.Body)
	complexity := meta.EstimatedComplexity
	if complexity == "" {
		complexity = "medium"
	}
	issueType := meta.Type
	if issueType == "" {
		issueType = "task"
	}

	return IssueMetadata{
		Number:              issue.Number,
		Title:               issue.Title,
		Body:                issue.Body,
		URL:                 issue.URL,
		EstimatedComplexity: complexity,
		ComplexityRationale: nullableString(meta.ComplexityRationale),
		Type:                issueType,
	}
}

func formatSkippedSummary(skipped []queueSelectionIssue) string {
	if len(skipped) == 0 {
		return ""
	}

	parts := make([]string, 0, len(skipped))
	for _, issue := range skipped {
		number := "?"
		if issue.Number > 0 {
			number = strconv.Itoa(issue.Number)
		}
		reasons := issue.BlockedReasons
		if len(reasons) == 0 {
			reasons = []string{"unknown"}
		}
		parts = append(parts, "#"+number+" — "+strings.Join(reasons, ", "))
	}
	return strings.Join(parts, "; ")
}

func IssueMetadataFromQueue(raw string, issueNumber int) (IssueMetadata, bool) {
	var queueEntries []struct {
		Number              int     `json:"number"`
		Title               string  `json:"title"`
		Body                string  `json:"body"`
		URL                 string  `json:"url"`
		EstimatedComplexity string  `json:"estimated_complexity"`
		ComplexityRationale *string `json:"complexity_rationale"`
		Type                string  `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &queueEntries); err != nil {
		return IssueMetadata{}, false
	}
	for _, entry := range queueEntries {
		if entry.Number != issueNumber {
			continue
		}
		complexity := entry.EstimatedComplexity
		if complexity == "" {
			complexity = "medium"
		}
		issueType := entry.Type
		if issueType == "" {
			issueType = "task"
		}
		return IssueMetadata{
			Number:              entry.Number,
			Title:               entry.Title,
			Body:                entry.Body,
			URL:                 entry.URL,
			EstimatedComplexity: complexity,
			ComplexityRationale: entry.ComplexityRationale,
			Type:                issueType,
		}, true
	}
	return IssueMetadata{}, false
}

type metadataBlock struct {
	EstimatedComplexity string
	ComplexityRationale string
	Type                string
}

func parseMetadataBlock(body string) metadataBlock {
	block := extractMetaBlock(body)
	if block == "" {
		return metadataBlock{}
	}

	meta := metadataBlock{}
	for line := range strings.SplitSeq(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "estimated_complexity":
			meta.EstimatedComplexity = value
		case "complexity_rationale":
			meta.ComplexityRationale = value
		case "type":
			meta.Type = value
		}
	}
	return meta
}

func extractMetaBlock(body string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "<!-- runoq:meta") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}

	var block strings.Builder
	for _, line := range lines[start:] {
		if strings.Contains(line, "-->") {
			break
		}
		if block.Len() > 0 {
			block.WriteByte('\n')
		}
		block.WriteString(line)
	}
	return block.String()
}

func parsePRNumber(raw string) (int, bool) {
	var payload prCreateResult
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0, false
	}
	switch value := payload.Number.(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	default:
		return 0, false
	}
}

type reviewVerdictResult struct {
	ReviewType string
	Verdict    string
	Score      string
	Checklist  string
}

func parseReviewVerdict(path string) (reviewVerdictResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return reviewVerdictResult{}, err
	}
	defer func() {
		_ = file.Close()
	}()

	result := reviewVerdictResult{}
	var checklistLines []string
	inChecklist := false

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if inChecklist {
			checklistLines = append(checklistLines, line)
			continue
		}

		switch {
		case strings.HasPrefix(line, "REVIEW-TYPE:"):
			result.ReviewType = strings.TrimSpace(strings.TrimPrefix(line, "REVIEW-TYPE:"))
		case strings.HasPrefix(line, "VERDICT:"):
			result.Verdict = strings.TrimSpace(strings.TrimPrefix(line, "VERDICT:"))
		case strings.HasPrefix(line, "SCORE:"):
			result.Score = strings.TrimSpace(strings.TrimPrefix(line, "SCORE:"))
		case line == "CHECKLIST:":
			inChecklist = true
		}
	}
	if err := scanner.Err(); err != nil {
		return reviewVerdictResult{}, err
	}

	result.Checklist = strings.Join(checklistLines, "\n")
	return result, nil
}

func (a *App) prepareAuth(ctx context.Context, root string, env []string) []string {
	authEnv := shell.EnvSet(env, "RUNOQ_FORCE_REFRESH_TOKEN", "1")
	var stdout bytes.Buffer
	err := a.execCommand(ctx, shell.CommandRequest{
		Name: "bash",
		Args: []string{
			"-lc",
			`if eval "$("$1" export-token)" 2>/dev/null; then printf 'ok\n%s' "${GH_TOKEN:-}"; else printf 'fail\n%s' "${GH_TOKEN:-}"; fi`,
			"bash",
			filepath.Join(root, "scripts", "gh-auth.sh"),
		},
		Dir:    a.cwd,
		Env:    authEnv,
		Stdout: &stdout,
		Stderr: a.stderr,
	})
	if err != nil {
		a.logInfo("Token mint failed or skipped (will use ambient credentials)")
		return authEnv
	}

	out := strings.TrimSpace(stdout.String())
	status, token, _ := strings.Cut(out, "\n")
	if status == "ok" {
		a.logInfo("Token mint succeeded")
	} else {
		a.logInfo("Token mint failed or skipped (will use ambient credentials)")
	}
	if strings.TrimSpace(token) != "" {
		authEnv = shell.EnvSet(authEnv, "GH_TOKEN", strings.TrimSpace(token))
	}
	return authEnv
}

func (a *App) targetRoot(ctx context.Context, env []string) (string, error) {
	if value, ok := shell.EnvLookup(env, "TARGET_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	out, err := shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
		Name: "git",
		Args: []string{"rev-parse", "--show-toplevel"},
		Dir:  a.cwd,
		Env:  env,
	})
	if err != nil {
		return "", errors.New("run runoq from inside a git repository")
	}
	return out, nil
}

func (a *App) configureGitBotIdentity(ctx context.Context, root string, env []string, dir string) error {
	return a.runProgram(ctx, env, "bash", []string{
		"-lc",
		`source "$1"; runoq::configure_git_bot_identity "$2"`,
		"bash",
		filepath.Join(root, "scripts", "lib", "common.sh"),
		dir,
	}, nil, io.Discard, io.Discard)
}

func (a *App) configureGitBotRemote(ctx context.Context, root string, env []string, dir string, repo string) error {
	return a.runProgram(ctx, env, "bash", []string{
		"-lc",
		`source "$1"; runoq::configure_git_bot_remote "$2" "$3"`,
		"bash",
		filepath.Join(root, "scripts", "lib", "common.sh"),
		dir,
		repo,
	}, nil, io.Discard, io.Discard)
}

func (a *App) postAuditComment(ctx context.Context, root string, env []string, repo string, prNumber int, event string, body string) error {
	commentFile, err := os.CreateTemp("", "runoq-audit.*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(commentFile.Name())
	}()

	if _, err := fmt.Fprintf(commentFile, "<!-- runoq:event:%s -->\n> Posted by `orchestrator` — %s phase\n\n%s\n", event, event, body); err != nil {
		_ = commentFile.Close()
		return err
	}
	if err := commentFile.Close(); err != nil {
		return err
	}

	return a.runScript(ctx, root, env, "gh-pr-lifecycle.sh", []string{"comment", repo, strconv.Itoa(prNumber), commentFile.Name()}, nil, io.Discard, io.Discard)
}

func (a *App) ghOutput(ctx context.Context, env []string, args ...string) (string, error) {
	return shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
		Name: envOrDefault(env, "GH_BIN", "gh"),
		Args: args,
		Dir:  a.cwd,
		Env:  env,
	})
}

func (a *App) scriptOutput(ctx context.Context, root string, env []string, script string, args []string, stdin io.Reader) (string, error) {
	var stdout bytes.Buffer
	err := a.runScript(ctx, root, env, script, args, stdin, &stdout, io.Discard)
	return strings.TrimSpace(stdout.String()), err
}

func (a *App) scriptOutputWithStderr(ctx context.Context, root string, env []string, script string, args []string, stdin io.Reader) (string, string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := a.runScript(ctx, root, env, script, args, stdin, &stdout, &stderr)
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func (a *App) runScript(ctx context.Context, root string, env []string, script string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	return a.runProgram(ctx, env, filepath.Join(root, "scripts", script), args, stdin, stdout, stderr)
}

func (a *App) runProgram(ctx context.Context, env []string, name string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	return a.execCommand(ctx, shell.CommandRequest{
		Name:   name,
		Args:   append([]string(nil), args...),
		Dir:    a.cwd,
		Env:    append([]string(nil), env...),
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
}

func (a *App) runoqRoot() string {
	if root, ok := shell.EnvLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(root) != "" {
		return root
	}
	if a.cwd != "" && shell.FileExists(filepath.Join(a.cwd, "scripts", "lib", "common.sh")) {
		return a.cwd
	}
	return ""
}

func marshalJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func nullableString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return &trimmed
}

func stderrOrUnknown(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return "unknown error"
	}
	return stderr
}

func finalizeDecision(state struct {
	PRNumber int      `json:"pr_number"`
	Worktree string   `json:"worktree"`
	Verdict  string   `json:"verdict"`
	Decision string   `json:"decision"`
	Score    string   `json:"score"`
	Round    int      `json:"round"`
	Caveats  []string `json:"caveats"`
}, cfg queueConfig, complexity string) (finalizeVerdict string, issueStatus string, finalizeReason string, complexityOK bool) {
	if state.Verdict != "PASS" {
		return "needs-review", "needs-review", fmt.Sprintf("Review verdict was %s (not PASS).", defaultString(state.Verdict, "FAIL")), false
	}
	if len(state.Caveats) > 0 {
		return "needs-review", "needs-review", "Caveats present: " + strings.Join(state.Caveats, ", "), false
	}
	if !cfg.AutoMerge.Enabled {
		return "needs-review", "needs-review", "Auto-merge is disabled in config.", false
	}

	complexityOK = autoMergeComplexityAllowed(complexity, autoMergeMaxComplexity(cfg))
	if complexityOK {
		return "auto-merge", "done", "", true
	}
	return "needs-review", "needs-review", fmt.Sprintf("Complexity %q exceeds auto-merge threshold %q.", complexity, autoMergeMaxComplexity(cfg)), false
}

func autoMergeComplexityAllowed(complexity string, maxComplexity string) bool {
	switch maxComplexity {
	case "high":
		return true
	case "medium":
		return complexity == "low" || complexity == "medium"
	default:
		return complexity == "low"
	}
}

func autoMergeMaxComplexity(cfg queueConfig) string {
	value := strings.TrimSpace(cfg.AutoMerge.MaxComplexity)
	if value == "" {
		return "low"
	}
	return value
}

func complexityDecisionValue(ok bool) string {
	if ok {
		return "true"
	}
	return "false"
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func truncateHash(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	if len(trimmed) <= 7 {
		return trimmed
	}
	return trimmed[:7]
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}

	type exitCoder interface {
		ExitCode() int
	}

	var exitErr exitCoder
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func firstReviewer(reviewers []string) string {
	for _, reviewer := range reviewers {
		trimmed := strings.TrimSpace(reviewer)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func updateStateJSON(stateJSON string, update func(map[string]any)) (string, error) {
	var state map[string]any
	if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
		return "", err
	}
	update(state)
	return marshalJSON(state)
}

func envOrDefault(env []string, key string, fallback string) string {
	if value, ok := shell.EnvLookup(env, key); ok && value != "" {
		return value
	}
	return fallback
}
