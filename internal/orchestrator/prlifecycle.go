package orchestrator

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var prNumberFromURL = regexp.MustCompile(`/pull/(\d+)`)

// createDraftPR creates a draft PR linked to the given issue.
func (a *App) createDraftPR(ctx context.Context, repo, branch string, issueNumber int, title string) (struct {
	URL    string
	Number int
}, error) {
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
		return struct {
			URL    string
			Number int
		}{}, err
	}
	defer func() {
		_ = os.Remove(bodyFile.Name())
	}()
	if _, err := io.WriteString(bodyFile, body); err != nil {
		_ = bodyFile.Close()
		return struct {
			URL    string
			Number int
		}{}, err
	}
	if err := bodyFile.Close(); err != nil {
		return struct {
			URL    string
			Number int
		}{}, err
	}

	url, err := a.ghOutput(ctx, a.env, "pr", "create", "--repo", repo, "--draft", "--title", title, "--head", branch, "--body-file", bodyFile.Name())
	if err != nil {
		return struct {
			URL    string
			Number int
		}{}, fmt.Errorf("gh pr create: %w", err)
	}

	url = strings.TrimSpace(url)
	var number int
	if m := prNumberFromURL.FindStringSubmatch(url); len(m) > 1 {
		number, _ = strconv.Atoi(m[1])
	}

	return struct {
		URL    string
		Number int
	}{URL: url, Number: number}, nil
}

// commentPR posts a comment on a PR.
func (a *App) commentPR(ctx context.Context, repo string, prNumber int, body string) error {
	bodyFile, err := os.CreateTemp("", "runoq-pr-comment.*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(bodyFile.Name())
	}()
	if _, err := io.WriteString(bodyFile, body); err != nil {
		_ = bodyFile.Close()
		return err
	}
	if err := bodyFile.Close(); err != nil {
		return err
	}

	_, err = a.ghOutput(ctx, a.env, "pr", "comment", strconv.Itoa(prNumber), "--repo", repo, "--body-file", bodyFile.Name())
	return err
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
			if _, err := a.ghOutput(ctx, a.env, "pr", "edit", prStr, "--repo", repo, "--add-reviewer", reviewer, "--add-assignee", reviewer); err != nil {
				return fmt.Errorf("pr assign reviewer %q: %w", reviewer, err)
			}
		}
	}

	return nil
}
