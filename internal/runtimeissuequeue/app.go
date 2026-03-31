package runtimeissuequeue

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const usageText = `Usage:
  gh-issue-queue.sh list <repo> <ready-label>
  gh-issue-queue.sh next <repo> <ready-label>
  gh-issue-queue.sh set-status <repo> <issue-number> <status>
  gh-issue-queue.sh create <repo> <title> <body> [--depends-on N,M] [--priority N] [--estimated-complexity value] [--type task|epic] [--parent-epic N]
  gh-issue-queue.sh epic-status <repo> <issue-number>
`

type commandRequest struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string
	Stdout io.Writer
	Stderr io.Writer
}

type commandExecutor func(context.Context, commandRequest) error

type App struct {
	args        []string
	env         []string
	cwd         string
	stdout      io.Writer
	stderr      io.Writer
	execCommand commandExecutor
	tokenInit   bool
	httpClient  *http.Client
}

type config struct {
	Labels struct {
		Ready       string `json:"ready"`
		InProgress  string `json:"inProgress"`
		Done        string `json:"done"`
		NeedsReview string `json:"needsReview"`
		Blocked     string `json:"blocked"`
	} `json:"labels"`
}

type listedIssue struct {
	Number              int      `json:"number"`
	Title               string   `json:"title"`
	Body                string   `json:"body"`
	URL                 string   `json:"url"`
	Labels              []string `json:"labels"`
	DependsOn           []int    `json:"depends_on"`
	Priority            *int     `json:"priority"`
	EstimatedComplexity *string  `json:"estimated_complexity"`
	ComplexityRationale *string  `json:"complexity_rationale"`
	Type                string   `json:"type"`
	ParentEpic          *int     `json:"parent_epic"`
	MetadataPresent     bool     `json:"metadata_present"`
	MetadataValid       bool     `json:"metadata_valid"`
	Actionable          bool     `json:"actionable,omitempty"`
	BlockedReasons      []string `json:"blocked_reasons,omitempty"`
}

type ghIssueListItem struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type metadata struct {
	DependsOn           []int
	Priority            *int
	EstimatedComplexity *string
	ComplexityRationale *string
	Type                string
	ParentEpic          *int
	MetadataPresent     bool
	MetadataValid       bool
}

type dependencyCheck struct {
	Dependency int     `json:"dependency"`
	Done       bool    `json:"done"`
	Reason     *string `json:"reason"`
}

type nextResult struct {
	Issue   *queueIssue  `json:"issue"`
	Skipped []queueIssue `json:"skipped"`
}

type setStatusResult struct {
	Issue  int    `json:"issue"`
	Status string `json:"status"`
	Label  string `json:"label"`
}

type createOptions struct {
	DependsOn           []int
	Priority            string
	EstimatedComplexity string
	ComplexityRationale string
	IssueType           string
	ParentEpic          string
}

type createResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type epicChild struct {
	Number int `json:"number"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type epicStatusResult struct {
	AllDone  bool  `json:"all_done"`
	Children []int `json:"children"`
	Pending  []int `json:"pending"`
}

type queueIssue struct {
	Number              int      `json:"number"`
	Title               string   `json:"title"`
	Body                string   `json:"body"`
	URL                 string   `json:"url"`
	Labels              []string `json:"labels"`
	DependsOn           []int    `json:"depends_on"`
	Priority            *int     `json:"priority"`
	EstimatedComplexity *string  `json:"estimated_complexity"`
	ComplexityRationale *string  `json:"complexity_rationale"`
	Type                string   `json:"type"`
	ParentEpic          *int     `json:"parent_epic"`
	MetadataPresent     bool     `json:"metadata_present"`
	MetadataValid       bool     `json:"metadata_valid"`
	Actionable          bool     `json:"actionable"`
	BlockedReasons      []string `json:"blocked_reasons"`
}

type identityFile struct {
	AppID          int64  `json:"appId"`
	InstallationID int64  `json:"installationId"`
	PrivateKeyPath string `json:"privateKeyPath"`
}

var issueURLPattern = regexp.MustCompile(`[0-9]+$`)

func New(args []string, env []string, cwd string, stdout io.Writer, stderr io.Writer) *App {
	return &App{
		args:        slices.Clone(args),
		env:         slices.Clone(env),
		cwd:         cwd,
		stdout:      stdout,
		stderr:      stderr,
		execCommand: runCommand,
		httpClient:  http.DefaultClient,
	}
}

func (a *App) SetCommandExecutor(execFn commandExecutor) {
	if execFn == nil {
		a.execCommand = runCommand
		return
	}
	a.execCommand = execFn
}

func (a *App) Run(ctx context.Context) int {
	if len(a.args) == 0 {
		a.printUsage(a.stderr)
		return 1
	}

	switch a.args[0] {
	case "list":
		if len(a.args) != 3 {
			a.printUsage(a.stderr)
			return 1
		}
		return a.runList(ctx, a.args[1], a.args[2])
	case "next":
		if len(a.args) != 3 {
			a.printUsage(a.stderr)
			return 1
		}
		return a.runNext(ctx, a.args[1], a.args[2])
	case "set-status":
		if len(a.args) != 4 {
			a.printUsage(a.stderr)
			return 1
		}
		return a.runSetStatus(ctx, a.args[1], a.args[2], a.args[3])
	case "create":
		if len(a.args) < 4 {
			a.printUsage(a.stderr)
			return 1
		}
		return a.runCreate(ctx, a.args[1], a.args[2], a.args[3], a.args[4:])
	case "epic-status":
		if len(a.args) != 3 {
			a.printUsage(a.stderr)
			return 1
		}
		return a.runEpicStatus(ctx, a.args[1], a.args[2])
	case "-h", "--help", "help":
		a.printUsage(a.stdout)
		return 0
	default:
		a.printUsage(a.stderr)
		return 1
	}
}

func (a *App) runList(ctx context.Context, repo string, readyLabel string) int {
	issues, err := a.listIssues(ctx, repo, readyLabel)
	if err != nil {
		return a.failf("%v", err)
	}
	return a.writeJSON(issues)
}

func (a *App) runNext(ctx context.Context, repo string, readyLabel string) int {
	issues, err := a.listIssues(ctx, repo, readyLabel)
	if err != nil {
		return a.failf("%v", err)
	}

	a.log("issue-queue", fmt.Sprintf("next_issue: found %d issues with label=%s", len(issues), readyLabel))

	slices.SortFunc(issues, func(a listedIssue, b listedIssue) int {
		return cmpIssue(a, b)
	})

	result := nextResult{Skipped: []queueIssue{}}
	for _, issue := range issues {
		a.log("issue-queue", fmt.Sprintf("next_issue: evaluating issue #%d type=%s", issue.Number, issue.Type))
		if issue.Type == "epic" {
			a.log("issue-queue", fmt.Sprintf("next_issue: skipping #%d (epic — not directly dispatchable)", issue.Number))
			result.Skipped = append(result.Skipped, toQueueIssue(issue, false, []string{"epic issues are not directly dispatchable"}))
			continue
		}

		blocked := make([]string, 0, len(issue.DependsOn))
		for _, dep := range issue.DependsOn {
			status, err := a.dependencyStatus(ctx, repo, dep)
			if err != nil {
				return a.failf("%v", err)
			}
			if !status.Done && status.Reason != nil {
				blocked = append(blocked, *status.Reason)
			}
		}

		actionable := len(blocked) == 0
		queueIssue := toQueueIssue(issue, actionable, blocked)
		if actionable {
			a.log("issue-queue", fmt.Sprintf("next_issue: selected #%d as next actionable issue", issue.Number))
			result.Issue = &queueIssue
			return a.writeJSON(result)
		}

		a.log("issue-queue", fmt.Sprintf("next_issue: skipping #%d (blocked: %s)", issue.Number, strings.Join(blocked, ", ")))
		result.Skipped = append(result.Skipped, queueIssue)
	}

	a.log("issue-queue", "next_issue: no actionable issues found")
	return a.writeJSON(result)
}

func (a *App) runSetStatus(ctx context.Context, repo string, issueNumber string, status string) int {
	cfg, err := a.loadConfig()
	if err != nil {
		return a.failf("Failed to read config: %v", err)
	}

	newLabel, ok := labelForStatus(cfg, status)
	if !ok {
		return a.failf("Unknown status: %s", status)
	}

	raw, err := a.ghOutput(ctx, []string{"issue", "view", issueNumber, "--repo", repo, "--json", "labels"})
	if err != nil {
		return a.failf("%v", err)
	}

	var response struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return a.failf("issue view returned invalid JSON: %v", err)
	}

	editArgs := []string{"issue", "edit", issueNumber, "--repo", repo}
	removing := make([]string, 0, len(response.Labels))
	for _, label := range response.Labels {
		if strings.HasPrefix(label.Name, "runoq:") {
			editArgs = append(editArgs, "--remove-label", label.Name)
			removing = append(removing, label.Name)
		}
	}
	editArgs = append(editArgs, "--add-label", newLabel)
	a.log("issue-queue", fmt.Sprintf("set-status issue=#%s: removing=[%s] adding=[%s]", issueNumber, strings.Join(removing, ", "), newLabel))

	if err := a.ghRun(ctx, editArgs, io.Discard, io.Discard); err != nil {
		return a.failf("%v", err)
	}

	issueID, err := strconv.Atoi(issueNumber)
	if err != nil {
		return a.failf("invalid issue number: %s", issueNumber)
	}
	return a.writeJSON(setStatusResult{
		Issue:  issueID,
		Status: status,
		Label:  newLabel,
	})
}

func (a *App) runCreate(ctx context.Context, repo string, title string, body string, args []string) int {
	opts, err := parseCreateOptions(args)
	if err != nil {
		a.printUsage(a.stderr)
		return 1
	}

	cfg, err := a.loadConfig()
	if err != nil {
		return a.failf("Failed to read config: %v", err)
	}

	bodyFile, err := a.writeCreateBody(body, opts)
	if err != nil {
		return a.failf("Failed to write issue body: %v", err)
	}
	defer func() {
		_ = os.Remove(bodyFile)
	}()

	url, err := a.ghOutput(ctx, []string{"issue", "create", "--repo", repo, "--title", title, "--body-file", bodyFile, "--label", cfg.Labels.Ready})
	if err != nil {
		return a.failf("%v", err)
	}
	a.log("issue-queue", fmt.Sprintf("create: title=%q result_url=%s", title, url))

	if opts.ParentEpic != "" {
		newIssueNumber := issueURLPattern.FindString(url)
		if newIssueNumber == "" {
			return a.failf("failed to parse created issue number from %q", url)
		}
		childID, err := a.ghOutput(ctx, []string{"api", fmt.Sprintf("repos/%s/issues/%s", repo, newIssueNumber), "--jq", ".id"})
		if err != nil {
			return a.failf("%v", err)
		}
		if err := a.ghRun(ctx, []string{"api", fmt.Sprintf("repos/%s/issues/%s/sub_issues", repo, opts.ParentEpic), "--method", "POST", "-F", "sub_issue_id=" + childID}, io.Discard, io.Discard); err != nil {
			return a.failf("%v", err)
		}
		a.log("issue-queue", fmt.Sprintf("create: linked issue #%s as sub-issue of epic #%s", newIssueNumber, opts.ParentEpic))
	}

	return a.writeJSON(createResult{Title: title, URL: url})
}

func (a *App) runEpicStatus(ctx context.Context, repo string, issueNumber string) int {
	cfg, err := a.loadConfig()
	if err != nil {
		return a.failf("Failed to read config: %v", err)
	}

	raw, err := a.ghOutput(ctx, []string{"api", fmt.Sprintf("repos/%s/issues/%s/sub_issues", repo, issueNumber), "--paginate"})
	if err != nil {
		return a.failf("%v", err)
	}

	var children []epicChild
	if strings.TrimSpace(raw) == "" {
		children = []epicChild{}
	} else if err := json.Unmarshal([]byte(raw), &children); err != nil {
		return a.failf("sub_issues returned invalid JSON: %v", err)
	}

	result := epicStatusResult{
		AllDone:  true,
		Children: make([]int, 0, len(children)),
		Pending:  []int{},
	}
	for _, child := range children {
		result.Children = append(result.Children, child.Number)
		done := false
		for _, label := range child.Labels {
			if label.Name == cfg.Labels.Done {
				done = true
				break
			}
		}
		if !done {
			result.AllDone = false
			result.Pending = append(result.Pending, child.Number)
		}
	}

	return a.writeJSON(result)
}

func (a *App) listIssues(ctx context.Context, repo string, readyLabel string) ([]listedIssue, error) {
	raw, err := a.ghOutput(ctx, []string{"issue", "list", "--repo", repo, "--label", readyLabel, "--state", "open", "--limit", "200", "--json", "number,title,body,labels,url"})
	if err != nil {
		return nil, err
	}

	var items []ghIssueListItem
	if strings.TrimSpace(raw) == "" {
		return []listedIssue{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("issue list returned invalid JSON: %v", err)
	}

	issues := make([]listedIssue, 0, len(items))
	for _, item := range items {
		meta := parseMetadata(item.Body)
		labels := make([]string, 0, len(item.Labels))
		for _, label := range item.Labels {
			labels = append(labels, label.Name)
		}
		issues = append(issues, listedIssue{
			Number:              item.Number,
			Title:               item.Title,
			Body:                item.Body,
			URL:                 item.URL,
			Labels:              labels,
			DependsOn:           meta.DependsOn,
			Priority:            meta.Priority,
			EstimatedComplexity: meta.EstimatedComplexity,
			ComplexityRationale: meta.ComplexityRationale,
			Type:                meta.Type,
			ParentEpic:          meta.ParentEpic,
			MetadataPresent:     meta.MetadataPresent,
			MetadataValid:       meta.MetadataValid,
		})
	}
	return issues, nil
}

func (a *App) dependencyStatus(ctx context.Context, repo string, dependency int) (dependencyCheck, error) {
	cfg, err := a.loadConfig()
	if err != nil {
		return dependencyCheck{}, fmt.Errorf("failed to read config: %v", err)
	}

	raw, err := a.ghOutput(ctx, []string{"issue", "view", strconv.Itoa(dependency), "--repo", repo, "--json", "number,labels"})
	if err != nil {
		reason := fmt.Sprintf("missing dependency issue #%d", dependency)
		a.log("issue-queue", fmt.Sprintf("dependency_status: dependency #%d not found (missing issue)", dependency))
		return dependencyCheck{Dependency: dependency, Done: false, Reason: &reason}, nil
	}

	var issue struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal([]byte(raw), &issue); err != nil {
		return dependencyCheck{}, fmt.Errorf("dependency view returned invalid JSON: %v", err)
	}

	for _, label := range issue.Labels {
		if label.Name == cfg.Labels.Done {
			a.log("issue-queue", fmt.Sprintf("dependency_status: dependency #%d done=true", dependency))
			return dependencyCheck{Dependency: dependency, Done: true, Reason: nil}, nil
		}
	}

	a.log("issue-queue", fmt.Sprintf("dependency_status: dependency #%d done=false", dependency))
	reason := fmt.Sprintf("dependency #%d is not %s", dependency, cfg.Labels.Done)
	return dependencyCheck{Dependency: dependency, Done: false, Reason: &reason}, nil
}

func parseMetadata(body string) metadata {
	meta := metadata{
		DependsOn:       []int{},
		Type:            "task",
		MetadataPresent: false,
		MetadataValid:   false,
	}

	block, ok := metadataBlock(body)
	if !ok {
		return meta
	}

	meta.MetadataPresent = true
	meta.MetadataValid = true

	values := make(map[string]string)
	for line := range strings.SplitSeq(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, key := range []string{"depends_on:", "priority:", "estimated_complexity:", "complexity_rationale:", "type:", "parent_epic:"} {
			if rest, found := strings.CutPrefix(line, key); found {
				values[strings.TrimSuffix(key, ":")] = strings.TrimSpace(rest)
				break
			}
		}
	}

	if raw := values["depends_on"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &meta.DependsOn); err != nil {
			meta.DependsOn = []int{}
			meta.MetadataValid = false
		}
	} else {
		meta.MetadataValid = false
	}

	if raw := values["priority"]; raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			meta.Priority = &value
		} else {
			meta.MetadataValid = false
		}
	} else {
		meta.MetadataValid = false
	}

	if raw := values["estimated_complexity"]; raw != "" {
		value := raw
		meta.EstimatedComplexity = &value
	} else {
		meta.MetadataValid = false
	}

	if raw := values["complexity_rationale"]; raw != "" && raw != "null" {
		value := raw
		meta.ComplexityRationale = &value
	}

	if raw := values["type"]; raw == "task" || raw == "epic" {
		meta.Type = raw
	}

	if raw := values["parent_epic"]; raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			meta.ParentEpic = &value
		}
	}

	return meta
}

func metadataBlock(body string) (string, bool) {
	lines := strings.Split(body, "\n")
	inBlock := false
	var block []string
	for _, line := range lines {
		switch {
		case strings.Contains(line, "<!-- runoq:meta"):
			inBlock = true
		case inBlock && strings.Contains(line, "-->"):
			return strings.Join(block, "\n"), true
		case inBlock:
			block = append(block, line)
		}
	}
	return "", false
}

func parseCreateOptions(args []string) (createOptions, error) {
	opts := createOptions{
		DependsOn:           []int{},
		Priority:            "3",
		EstimatedComplexity: "medium",
		IssueType:           "task",
	}

	for i := 0; i < len(args); {
		switch args[i] {
		case "--depends-on":
			if i+1 >= len(args) {
				return createOptions{}, errors.New("missing depends-on value")
			}
			if args[i+1] != "" {
				for part := range strings.SplitSeq(args[i+1], ",") {
					part = strings.TrimSpace(part)
					if part == "" {
						continue
					}
					value, err := strconv.Atoi(part)
					if err != nil {
						return createOptions{}, err
					}
					opts.DependsOn = append(opts.DependsOn, value)
				}
			}
			i += 2
		case "--priority":
			if i+1 >= len(args) {
				return createOptions{}, errors.New("missing priority value")
			}
			opts.Priority = args[i+1]
			i += 2
		case "--estimated-complexity":
			if i+1 >= len(args) {
				return createOptions{}, errors.New("missing estimated complexity value")
			}
			opts.EstimatedComplexity = args[i+1]
			i += 2
		case "--complexity-rationale":
			if i+1 >= len(args) {
				return createOptions{}, errors.New("missing complexity rationale value")
			}
			opts.ComplexityRationale = args[i+1]
			i += 2
		case "--type":
			if i+1 >= len(args) {
				return createOptions{}, errors.New("missing type value")
			}
			opts.IssueType = args[i+1]
			i += 2
		case "--parent-epic":
			if i+1 >= len(args) {
				return createOptions{}, errors.New("missing parent epic value")
			}
			opts.ParentEpic = args[i+1]
			i += 2
		default:
			return createOptions{}, fmt.Errorf("unknown option: %s", args[i])
		}
	}

	return opts, nil
}

func (a *App) writeCreateBody(body string, opts createOptions) (string, error) {
	file, err := os.CreateTemp("", "runoq-issue-create.*")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	var depends strings.Builder
	depends.WriteByte('[')
	for i, dep := range opts.DependsOn {
		if i > 0 {
			depends.WriteByte(',')
		}
		depends.WriteString(strconv.Itoa(dep))
	}
	depends.WriteByte(']')

	if _, err := fmt.Fprintln(file, "<!-- runoq:meta"); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(file, "depends_on: %s\n", depends.String()); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(file, "priority: %s\n", opts.Priority); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(file, "estimated_complexity: %s\n", opts.EstimatedComplexity); err != nil {
		return "", err
	}
	if opts.ComplexityRationale != "" {
		if _, err := fmt.Fprintf(file, "complexity_rationale: %s\n", opts.ComplexityRationale); err != nil {
			return "", err
		}
	}
	if _, err := fmt.Fprintf(file, "type: %s\n", opts.IssueType); err != nil {
		return "", err
	}
	if opts.ParentEpic != "" {
		if _, err := fmt.Fprintf(file, "parent_epic: %s\n", opts.ParentEpic); err != nil {
			return "", err
		}
	}
	if _, err := fmt.Fprintf(file, "-->\n\n%s\n", body); err != nil {
		return "", err
	}

	return file.Name(), nil
}

func (a *App) ghOutput(ctx context.Context, args []string) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := a.ghRun(ctx, args, &stdout, &stderr); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (a *App) ghRun(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	if err := a.ensureGHToken(ctx); err != nil {
		return err
	}
	request := commandRequest{
		Name:   a.ghBin(),
		Args:   args,
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: stdout,
		Stderr: stderr,
	}
	if err := a.execCommand(ctx, request); err != nil {
		return err
	}
	return nil
}

func (a *App) ensureGHToken(ctx context.Context) error {
	if _, ok := envLookup(a.env, "GH_TOKEN"); ok {
		return nil
	}
	if _, ok := envLookup(a.env, "RUNOQ_NO_AUTO_TOKEN"); ok {
		return nil
	}
	if a.tokenInit {
		return nil
	}
	a.tokenInit = true

	targetRoot, err := a.targetRoot(ctx)
	if err != nil {
		return nil
	}
	identityPath := filepath.Join(targetRoot, ".runoq", "identity.json")
	data, err := os.ReadFile(identityPath)
	if err != nil {
		return nil
	}

	var identity identityFile
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil
	}
	if identity.AppID == 0 || identity.InstallationID == 0 || identity.PrivateKeyPath == "" {
		return nil
	}
	keyPath := strings.Replace(identity.PrivateKeyPath, "~", os.Getenv("HOME"), 1)
	token, err := a.mintBotToken(identity.AppID, identity.InstallationID, keyPath)
	if err != nil || token == "" {
		return nil
	}
	a.env = envSet(a.env, "GH_TOKEN", token)
	return nil
}

func (a *App) mintBotToken(appID int64, installationID int64, keyPath string) (string, error) {
	privateKey, err := loadPrivateKey(keyPath)
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payloadJSON := fmt.Sprintf(`{"iat":%d,"exp":%d,"iss":"%d"}`, now, now+540, appID)
	payload := base64.RawURLEncoding.EncodeToString([]byte(payloadJSON))
	unsigned := header + "." + payload

	sum := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	jwt := unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "runoq-runtime")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token request failed: %s", resp.Status)
	}

	var payloadResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payloadResp); err != nil {
		return "", err
	}
	return payloadResp.Token, nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid PEM private key")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return key, nil
}

func (a *App) loadConfig() (config, error) {
	path, err := a.configPath()
	if err != nil {
		return config{}, err
	}
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

func (a *App) configPath() (string, error) {
	if value, ok := envLookup(a.env, "RUNOQ_CONFIG"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	if value, ok := envLookup(a.env, "RUNOQ_ROOT"); ok && strings.TrimSpace(value) != "" {
		return filepath.Join(value, "config", "runoq.json"), nil
	}
	return "", errors.New("RUNOQ_CONFIG is not set")
}

func (a *App) targetRoot(ctx context.Context) (string, error) {
	if value, ok := envLookup(a.env, "TARGET_ROOT"); ok && strings.TrimSpace(value) != "" {
		return value, nil
	}
	var stdout bytes.Buffer
	err := a.execCommand(ctx, commandRequest{
		Name:   "git",
		Args:   []string{"rev-parse", "--show-toplevel"},
		Dir:    a.cwd,
		Env:    a.env,
		Stdout: &stdout,
		Stderr: io.Discard,
	})
	if err != nil {
		return "", errors.New("run runoq from inside a git repository")
	}
	return strings.TrimSpace(stdout.String()), nil
}

func labelForStatus(cfg config, status string) (string, bool) {
	switch status {
	case "ready":
		return cfg.Labels.Ready, true
	case "in-progress":
		return cfg.Labels.InProgress, true
	case "done":
		return cfg.Labels.Done, true
	case "needs-review":
		return cfg.Labels.NeedsReview, true
	case "blocked":
		return cfg.Labels.Blocked, true
	default:
		return "", false
	}
}

func cmpIssue(a listedIssue, b listedIssue) int {
	aPriority := 999999
	if a.Priority != nil {
		aPriority = *a.Priority
	}
	bPriority := 999999
	if b.Priority != nil {
		bPriority = *b.Priority
	}
	if aPriority != bPriority {
		return aPriority - bPriority
	}
	return a.Number - b.Number
}

func (a *App) ghBin() string {
	if value, ok := envLookup(a.env, "GH_BIN"); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return "gh"
}

func (a *App) writeJSON(value any) int {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return a.failf("Failed to encode JSON: %v", err)
	}
	if _, err := a.stdout.Write(buffer.Bytes()); err != nil {
		return a.failf("Failed to write output: %v", err)
	}
	return 0
}

func toQueueIssue(issue listedIssue, actionable bool, blocked []string) queueIssue {
	return queueIssue{
		Number:              issue.Number,
		Title:               issue.Title,
		Body:                issue.Body,
		URL:                 issue.URL,
		Labels:              issue.Labels,
		DependsOn:           issue.DependsOn,
		Priority:            issue.Priority,
		EstimatedComplexity: issue.EstimatedComplexity,
		ComplexityRationale: issue.ComplexityRationale,
		Type:                issue.Type,
		ParentEpic:          issue.ParentEpic,
		MetadataPresent:     issue.MetadataPresent,
		MetadataValid:       issue.MetadataValid,
		Actionable:          actionable,
		BlockedReasons:      append([]string{}, blocked...),
	}
}

func (a *App) fail(message string) int {
	_, _ = fmt.Fprintf(a.stderr, "runoq: %s\n", message)
	return 1
}

func (a *App) failf(format string, args ...any) int {
	return a.fail(fmt.Sprintf(format, args...))
}

func (a *App) printUsage(w io.Writer) {
	_, _ = io.WriteString(w, usageText)
}

func (a *App) log(prefix string, message string) {
	if value, ok := envLookup(a.env, "RUNOQ_LOG"); ok && value != "" {
		_, _ = fmt.Fprintf(a.stderr, "[%s] %s\n", prefix, message)
	}
}

func envLookup(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], prefix); ok {
			return value, true
		}
	}
	return "", false
}

func envSet(env []string, key string, value string) []string {
	prefix := key + "="
	updated := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				updated = append(updated, prefix+value)
				replaced = true
			}
			continue
		}
		updated = append(updated, entry)
	}
	if !replaced {
		updated = append(updated, prefix+value)
	}
	return updated
}

func runCommand(ctx context.Context, req commandRequest) error {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	return cmd.Run()
}
