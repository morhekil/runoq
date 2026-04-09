package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var prNumberFromURL = regexp.MustCompile(`/pull/(\d+)`)

// createDraftPR creates a draft PR linked to the given issue.
func (a *App) createDraftPR(ctx context.Context, repo, branch string, issueNumber int, title string) (struct{ URL string; Number int }, error) {
	// Build the PR body from the template
	templatePath := ""
	if root := a.runoqRoot(); root != "" {
		templatePath = root + "/templates/pr-template.md"
	}

	body := fmt.Sprintf("## Summary\n<!-- runoq:summary:start -->\nPending.\n<!-- runoq:summary:end -->\n\n## Linked Issue\nCloses #%d\n", issueNumber)
	if templatePath != "" {
		if data, err := os.ReadFile(templatePath); err == nil {
			body = strings.ReplaceAll(string(data), "ISSUE_NUMBER", strconv.Itoa(issueNumber))
		}
	}

	bodyFile, err := os.CreateTemp("", "runoq-pr-create.*")
	if err != nil {
		return struct{ URL string; Number int }{}, err
	}
	defer os.Remove(bodyFile.Name())
	if _, err := io.WriteString(bodyFile, body); err != nil {
		bodyFile.Close()
		return struct{ URL string; Number int }{}, err
	}
	bodyFile.Close()

	url, err := a.ghOutput(ctx, a.env, "pr", "create", "--repo", repo, "--draft", "--title", title, "--head", branch, "--body-file", bodyFile.Name())
	if err != nil {
		return struct{ URL string; Number int }{}, fmt.Errorf("gh pr create: %w", err)
	}

	url = strings.TrimSpace(url)
	var number int
	if m := prNumberFromURL.FindStringSubmatch(url); len(m) > 1 {
		number, _ = strconv.Atoi(m[1])
	}

	return struct{ URL string; Number int }{URL: url, Number: number}, nil
}

// commentPR posts a comment on a PR.
func (a *App) commentPR(ctx context.Context, repo string, prNumber int, body string) error {
	bodyFile, err := os.CreateTemp("", "runoq-pr-comment.*")
	if err != nil {
		return err
	}
	defer os.Remove(bodyFile.Name())
	if _, err := io.WriteString(bodyFile, body); err != nil {
		bodyFile.Close()
		return err
	}
	bodyFile.Close()

	_, err = a.ghOutput(ctx, a.env, "pr", "comment", strconv.Itoa(prNumber), "--repo", repo, "--body-file", bodyFile.Name())
	return err
}

// pollMentions returns open issue/PR comments that mention the given handle.
func (a *App) pollMentions(ctx context.Context, repo, handle string) ([]json.RawMessage, error) {
	out, err := a.ghOutput(ctx, a.env, "api", fmt.Sprintf("repos/%s/issues?state=open&per_page=100", repo))
	if err != nil {
		return nil, fmt.Errorf("poll-mentions list issues: %w", err)
	}

	var items []struct {
		Number      int  `json:"number"`
		PullRequest *any `json:"pull_request"`
	}
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return nil, fmt.Errorf("poll-mentions parse issues: %w", err)
	}

	mention := "@" + handle
	var results []json.RawMessage
	for _, item := range items {
		commentsOut, err := a.ghOutput(ctx, a.env, "api", fmt.Sprintf("repos/%s/issues/%d/comments", repo, item.Number))
		if err != nil {
			continue
		}
		var comments []struct {
			ID        int    `json:"id"`
			Body      string `json:"body"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
			CreatedAt string `json:"created_at"`
		}
		if err := json.Unmarshal([]byte(commentsOut), &comments); err != nil {
			continue
		}
		for _, c := range comments {
			if !strings.Contains(c.Body, mention) {
				continue
			}
			if strings.Contains(c.Body, "runoq:payload") || strings.Contains(c.Body, "runoq:bot") {
				continue
			}
			ctxType := "issue"
			var prNumber *int
			var issueNumber *int
			if item.PullRequest != nil {
				ctxType = "pr"
				n := item.Number
				prNumber = &n
			} else {
				n := item.Number
				issueNumber = &n
			}
			entry, _ := json.Marshal(map[string]any{
				"comment_id":   c.ID,
				"author":       c.User.Login,
				"body":         c.Body,
				"created_at":   c.CreatedAt,
				"context_type": ctxType,
				"pr_number":    prNumber,
				"issue_number": issueNumber,
			})
			results = append(results, json.RawMessage(entry))
		}
	}
	if results == nil {
		results = []json.RawMessage{}
	}
	return results, nil
}

// finalizePR marks a PR as ready and either auto-merges or assigns a reviewer.
func (a *App) finalizePR(ctx context.Context, repo string, prNumber int, verdict string, reviewer string) error {
	prStr := strconv.Itoa(prNumber)

	// Mark as ready for review
	readyOut, err := a.ghOutput(ctx, a.env, "pr", "ready", prStr, "--repo", repo)
	if err != nil {
		// Tolerate "already ready" errors
		if !strings.Contains(readyOut, "already") && !strings.Contains(err.Error(), "already") {
			return fmt.Errorf("pr ready: %w", err)
		}
	}

	switch verdict {
	case "auto-merge":
		mergeOut, mergeErr := a.ghOutput(ctx, a.env, "pr", "merge", prStr, "--repo", repo, "--auto", "--squash")
		if mergeErr != nil {
			// Fall back to direct merge if auto-merge not available
			if strings.Contains(mergeOut, "Protected branch") || strings.Contains(mergeOut, "enablePullRequestAutoMerge") ||
				strings.Contains(mergeErr.Error(), "Protected branch") || strings.Contains(mergeErr.Error(), "enablePullRequestAutoMerge") {
				_, err := a.ghOutput(ctx, a.env, "pr", "merge", prStr, "--repo", repo, "--squash", "--delete-branch", "--body", "")
				if err != nil {
					return fmt.Errorf("pr merge fallback: %w", err)
				}
			} else {
				return fmt.Errorf("pr auto-merge: %w", mergeErr)
			}
		}
	case "needs-review":
		if reviewer != "" {
			// Best effort — assignment may fail if user lacks permissions
			_, _ = a.ghOutput(ctx, a.env, "pr", "edit", prStr, "--repo", repo, "--add-reviewer", reviewer, "--add-assignee", reviewer)
		}
	}

	return nil
}
