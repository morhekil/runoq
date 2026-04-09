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

	"net/http"

	"github.com/saruman/runoq/internal/gh"
	"github.com/saruman/runoq/internal/gitops"
	"github.com/saruman/runoq/internal/shell"
)

func metadataFromIssueView(issue issueView) IssueMetadata {
	return IssueMetadata{
		Number:              issue.Number,
		Title:               issue.Title,
		Body:                issue.Body,
		URL:                 issue.URL,
		EstimatedComplexity: "medium",
		Type:                "task",
	}
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


type reviewVerdictResult struct {
	ReviewType string
	Verdict    string
	Score      string
	Checklist  string
	Scorecard  string // Full PERFECT-D scorecard + metrics sections from the review log
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
	var scorecardLines []string
	inChecklist := false
	inScorecard := false

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		if inChecklist {
			checklistLines = append(checklistLines, line)
			continue
		}

		// Capture scorecard sections: "## Diff Metrics", "## PERFECT-D Scorecard", "## Issues Found"
		if strings.HasPrefix(line, "## Diff Metrics") ||
			strings.HasPrefix(line, "## PERFECT-D Scorecard") {
			inScorecard = true
		}
		// Stop scorecard capture at terminal verdict block
		if inScorecard && (strings.HasPrefix(line, "REVIEW-TYPE:") || strings.HasPrefix(line, "VERDICT:") || strings.HasPrefix(line, "SCORE:") || line == "CHECKLIST:") {
			inScorecard = false
		}
		if inScorecard {
			scorecardLines = append(scorecardLines, line)
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
	result.Scorecard = strings.TrimSpace(strings.Join(scorecardLines, "\n"))
	return result, nil
}

// parseScoreNumber extracts the numeric score from a string like "38/40" or "38".
func parseScoreNumber(score string) int {
	s := strings.TrimSpace(score)
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}
	n, _ := strconv.Atoi(s)
	return n
}

func (a *App) prepareAuth(ctx context.Context, root string, env []string) []string {
	homeDir := ""
	if h, err := os.UserHomeDir(); err == nil {
		homeDir = h
	}
	// Use TARGET_ROOT for identity resolution; fall back to a.cwd.
	cwd := a.cwd
	if tr, ok := shell.EnvLookup(env, "TARGET_ROOT"); ok && tr != "" {
		cwd = tr
	}
	client := gh.NewClient(a.execCommand, http.DefaultClient, env, cwd, homeDir)
	if err := client.EnsureToken(ctx); err != nil {
		a.logInfo("Token mint failed or skipped (will use ambient credentials)")
		return env
	}

	// EnsureToken may have set GH_TOKEN on the client's env
	clientEnv := client.Env()
	if token, ok := shell.EnvLookup(clientEnv, "GH_TOKEN"); ok && token != "" {
		a.logInfo("Token mint succeeded")
		return shell.EnvSet(env, "GH_TOKEN", token)
	}
	a.logInfo("Token mint skipped (no identity or ambient credentials)")
	return env
}

func (a *App) targetRoot(ctx context.Context, env []string) (string, error) {
	if value, ok := shell.EnvLookup(env, "TARGET_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	root, err := gitops.FindRoot(a.cwd)
	if err != nil {
		return "", errors.New("run runoq from inside a git repository")
	}
	return root, nil
}

func (a *App) configureGitBotIdentity(ctx context.Context, env []string, dir string) error {
	slug := a.cfg.IdentityHandle
	if slug == "" {
		return nil
	}

	repo := gitops.OpenCLI(ctx, dir, a.execCommand)
	if err := repo.SetConfig("user.name", slug+"[bot]"); err != nil {
		return err
	}

	appID := a.resolveAppID(env, dir)
	if appID == "" {
		return nil
	}

	return repo.SetConfig("user.email", fmt.Sprintf("%s+%s[bot]@users.noreply.github.com", appID, slug))
}

// resolveAppID returns the GitHub App ID from RUNOQ_APP_ID env or identity.json.
func (a *App) resolveAppID(env []string, dir string) string {
	if appID, ok := shell.EnvLookup(env, "RUNOQ_APP_ID"); ok && appID != "" {
		return appID
	}

	// identity.json lives at {TARGET_ROOT}/.runoq/identity.json, not in the worktree.
	targetRoot := dir
	if tr, ok := shell.EnvLookup(env, "TARGET_ROOT"); ok && tr != "" {
		targetRoot = tr
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

func (a *App) configureGitBotRemote(ctx context.Context, env []string, dir string, ghRepo string) error {
	token, _ := shell.EnvLookup(env, "GH_TOKEN")
	if token == "" {
		return nil
	}

	url := fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", token, ghRepo)
	return a.execCommand(ctx, shell.CommandRequest{
		Name: "git",
		Args: []string{"-C", dir, "remote", "set-url", "origin", url},
		Dir:  a.cwd,
		Env:  env,
	})
}

// formatAuditComment builds the comment body with event marker, optional state block, and human-readable content.
func formatAuditComment(event string, stateJSON string, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- runoq:bot:orchestrator:%s -->\n", event)
	fmt.Fprintf(&b, "%s%s%s\n", markerStatePrefix, stateJSON, markerStateSuffix)
	fmt.Fprintf(&b, "> Posted by `orchestrator` — %s phase\n\n%s\n", event, body)
	return b.String()
}

func (a *App) postAuditCommentWithState(ctx context.Context, root string, env []string, repo string, prNumber int, event string, stateJSON string, body string) error {
	content := formatAuditComment(event, stateJSON, body)
	return a.commentPR(ctx, repo, prNumber, content)
}

func (a *App) ghOutput(ctx context.Context, env []string, args ...string) (string, error) {
	return shell.CommandOutput(ctx, a.execCommand, shell.CommandRequest{
		Name: envOrDefault(env, "GH_BIN", "gh"),
		Args: args,
		Dir:  a.cwd,
		Env:  env,
	})
}

func (a *App) runProgram(ctx context.Context, env []string, name string, args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	if a.logWriter != nil {
		stdout = teeWriter(stdout, a.logWriter)
		stderr = teeWriter(stderr, a.logWriter)
	}
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

// teeWriter returns a writer that writes to both w and log.
// If w is io.Discard, returns log directly to avoid unnecessary copies.
func teeWriter(w io.Writer, log io.Writer) io.Writer {
	if w == io.Discard {
		return log
	}
	return io.MultiWriter(w, log)
}

func (a *App) runoqRoot() string {
	if root, ok := shell.EnvLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(root) != "" {
		return root
	}
	if a.cwd != "" && shell.FileExists(filepath.Join(a.cwd, "config", "runoq.json")) {
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

func finalizeDecision(state struct {
	PRNumber int      `json:"pr_number"`
	Worktree string   `json:"worktree"`
	Verdict  string   `json:"verdict"`
	Decision string   `json:"decision"`
	Score    string   `json:"score"`
	Round    int      `json:"round"`
	Caveats  []string `json:"caveats"`
	Summary  string   `json:"summary"`
}, cfg OrchestratorConfig) (finalizeVerdict string, issueStatus string, finalizeReason string) {
	if state.Verdict != "PASS" {
		return "needs-review", "needs-review", fmt.Sprintf("Review verdict was %s (not PASS).", defaultString(state.Verdict, "FAIL"))
	}
	if len(state.Caveats) > 0 {
		return "needs-review", "needs-review", "Caveats present: " + strings.Join(state.Caveats, ", ")
	}
	if !cfg.AutoMergeEnabled {
		return "needs-review", "needs-review", "Auto-merge is disabled in config."
	}
	if cfg.AutoMergeMinScore > 0 {
		score := parseScoreNumber(state.Score)
		if score < cfg.AutoMergeMinScore {
			return "needs-review", "needs-review", fmt.Sprintf("Score %d below auto-merge threshold %d.", score, cfg.AutoMergeMinScore)
		}
	}

	return "auto-merge", "done", ""
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

// deriveStateFromGitHub finds the linked PR for an issue and extracts the latest
// orchestrator state from audit comments. Returns found=false if no PR exists.
func (a *App) deriveStateFromGitHub(ctx context.Context, env []string, repo string, issueNumber int) (stateJSON string, prNumber int, found bool, err error) {
	// Find linked PR via search
	prListOut, err := a.ghOutput(ctx, env, "pr", "list", "--repo", repo, "--search", fmt.Sprintf("closes #%d", issueNumber), "--json", "number,headRefName")
	if err != nil {
		return "", 0, false, fmt.Errorf("failed to list PRs for issue #%d: %v", issueNumber, err)
	}
	var prs []struct {
		Number      int    `json:"number"`
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(prListOut), &prs); err != nil {
		return "", 0, false, fmt.Errorf("failed to parse PR list: %v", err)
	}
	if len(prs) == 0 {
		return "", 0, false, nil
	}
	pr := prs[0]

	// Fetch PR comments
	prViewOut, err := a.ghOutput(ctx, env, "pr", "view", strconv.Itoa(pr.Number), "--repo", repo, "--json", "comments")
	if err != nil {
		return "", pr.Number, false, fmt.Errorf("failed to view PR #%d comments: %v", pr.Number, err)
	}
	var prView struct {
		Comments json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal([]byte(prViewOut), &prView); err != nil {
		return "", pr.Number, false, fmt.Errorf("failed to parse PR view: %v", err)
	}

	// Try to extract structured state from comments
	state, err := parseStateFromComments(string(prView.Comments))
	if err != nil {
		return "", pr.Number, false, err
	}
	return state, pr.Number, state != "", nil
}

func envOrDefault(env []string, key string, fallback string) string {
	if value, ok := shell.EnvLookup(env, key); ok && value != "" {
		return value
	}
	return fallback
}

const (
	markerSummaryStart = "<!-- runoq:summary:start -->"
	markerSummaryEnd   = "<!-- runoq:summary:end -->"
	markerStatePrefix  = "<!-- runoq:state:"
	markerStateSuffix  = " -->"
)

// parseStateFromComments scans PR comments JSON (array of objects with "body" field)
// and returns the state JSON from the latest <!-- runoq:state:{...} --> block.
// Returns empty string if no state block is found.
func parseStateFromComments(commentsJSON string) (string, error) {
	var comments []struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(commentsJSON), &comments); err != nil {
		return "", err
	}

	var latest string
	for _, c := range comments {
		for line := range strings.SplitSeq(c.Body, "\n") {
			if strings.HasPrefix(line, markerStatePrefix) && strings.HasSuffix(line, markerStateSuffix) {
				latest = line[len(markerStatePrefix) : len(line)-len(markerStateSuffix)]
			}
		}
	}
	return latest, nil
}

func replaceMarkerContent(body, startMarker, endMarker, content string) string {
	startIdx := strings.Index(body, startMarker)
	endIdx := strings.Index(body, endMarker)
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		return body
	}
	return body[:startIdx+len(startMarker)] + "\n" + content + "\n" + body[endIdx:]
}

func (a *App) updatePRBody(ctx context.Context, env []string, repo string, prNumber int, summary string, verdict string, score string, round int, maxRounds int, caveats []string) error {
	bodyJSON, err := a.ghOutput(ctx, env, "pr", "view", strconv.Itoa(prNumber), "--repo", repo, "--json", "body")
	if err != nil {
		return err
	}
	var prBody struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(bodyJSON), &prBody); err != nil {
		return err
	}

	updatedBody := prBody.Body

	if strings.TrimSpace(summary) != "" {
		updatedBody = replaceMarkerContent(updatedBody, markerSummaryStart, markerSummaryEnd, summary)
	}

	updatedBody += fmt.Sprintf(
		"\n## Final Status\n| Field | Value |\n|-------|-------|\n| **Verdict** | %s |\n| **Score** | %s |\n| **Rounds** | %d / %d |\n",
		defaultString(verdict, "FAIL"),
		defaultString(score, "0"),
		max(round, 1),
		maxRounds,
	)

	if len(caveats) > 0 {
		updatedBody += "\n## Areas for Human Attention\n"
		for _, caveat := range caveats {
			updatedBody += "- " + caveat + "\n"
		}
	}

	tmpFile, err := os.CreateTemp("", "runoq-pr-body.*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()
	if _, err := io.WriteString(tmpFile, updatedBody); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	_, err = a.ghOutput(ctx, env, "pr", "edit", strconv.Itoa(prNumber), "--repo", repo, "--body-file", tmpFile.Name())
	return err
}
