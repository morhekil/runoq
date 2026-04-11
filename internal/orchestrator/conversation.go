package orchestrator

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// UnprocessedComment represents a comment on an issue or PR that hasn't been
// responded to yet (no +1 reaction from the bot).
type UnprocessedComment struct {
	ID                int
	Author            string
	Body              string
	CommenterIdentity string // "human:{login}", "agent:{name}", or "bot"
	SourceType        string // "issue-comment", "review-comment", or "review"
	ArtifactType      string // "issue" or "pr"
	ArtifactNumber    int
	CreatedAt         string
}

var (
	agentMarkerPattern   = regexp.MustCompile(`<!-- runoq:agent:(\S+) -->`)
	respondMarkerPattern = regexp.MustCompile(`<!--\s*runoq:bot:orchestrator:respond\s+source:([a-z-]+:\d+)\s*-->`)
)

// findUnprocessedComments queries GitHub for comments on an issue or PR,
// returning those without a +1 reaction (the "processed" marker).
// Filters out bot-generated comments (runoq:bot markers).
func (a *App) findUnprocessedComments(ctx context.Context, repo string, artifactType string, number int) ([]UnprocessedComment, error) {
	comments, err := a.fetchConversationComments(ctx, repo, artifactType, number)
	if err != nil {
		return nil, err
	}
	botLogin := a.cfg.IdentityHandle + "[bot]"
	processedSources := make(map[string]struct{}, len(comments))
	for _, c := range comments {
		if !strings.Contains(c.Body, "runoq:bot:orchestrator:respond") {
			continue
		}
		if !isBotIdentity(c.Author, botLogin) {
			continue
		}
		match := respondMarkerPattern.FindStringSubmatch(c.Body)
		if len(match) < 2 {
			continue
		}
		processedSources[match[1]] = struct{}{}
	}

	var result []UnprocessedComment

	for _, c := range comments {
		// Skip bot-generated comments (audit trail, not conversation)
		if strings.Contains(c.Body, "runoq:bot") {
			continue
		}
		sourceKey := fmt.Sprintf("%s:%d", c.SourceType, c.ID)
		if _, ok := processedSources[sourceKey]; ok {
			continue
		}

		// Determine commenter identity
		identity := identityFromComment(c.Body, c.Author, botLogin)

		result = append(result, UnprocessedComment{
			ID:                c.ID,
			Author:            c.Author,
			Body:              c.Body,
			CommenterIdentity: identity,
			SourceType:        c.SourceType,
			ArtifactType:      artifactType,
			ArtifactNumber:    number,
			CreatedAt:         c.CreatedAt,
		})
	}

	slices.SortFunc(result, func(a, b UnprocessedComment) int {
		return cmp.Or(strings.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})

	return result, nil
}

// identityFromComment determines who posted a comment:
// - Agent comments have <!-- runoq:agent:name --> markers → "agent:{name}"
// - Bot comments (from our bot login) without agent marker → "bot"
// - Everything else → "human:{login}"
func identityFromComment(body, login, botLogin string) string {
	if m := agentMarkerPattern.FindStringSubmatch(body); len(m) > 1 {
		return "agent:" + m[1]
	}
	if isBotIdentity(login, botLogin) {
		return "bot"
	}
	return "human:" + login
}

type conversationComment struct {
	ID         int
	Body       string
	Author     string
	CreatedAt  string
	SourceType string
}

func (a *App) fetchConversationComments(ctx context.Context, repo string, artifactType string, number int) ([]conversationComment, error) {
	issueComments, err := a.fetchIssueComments(ctx, repo, number)
	if err != nil {
		return nil, fmt.Errorf("fetch comments for %s #%d: %w", artifactType, number, err)
	}
	if artifactType != "pr" {
		return issueComments, nil
	}

	reviewComments, err := a.fetchReviewComments(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	reviews, err := a.fetchReviews(ctx, repo, number)
	if err != nil {
		return nil, err
	}

	all := make([]conversationComment, 0, len(issueComments)+len(reviewComments)+len(reviews))
	all = append(all, issueComments...)
	all = append(all, reviewComments...)
	all = append(all, reviews...)
	return all, nil
}

func (a *App) fetchIssueComments(ctx context.Context, repo string, number int) ([]conversationComment, error) {
	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", repo, number)
	raw, err := a.ghOutput(ctx, a.env, "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("fetch issue comments: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var payload []struct {
		ID        int    `json:"id"`
		Body      string `json:"body"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("parse issue comments: %w", err)
	}
	result := make([]conversationComment, 0, len(payload))
	for _, item := range payload {
		result = append(result, conversationComment{
			ID:         item.ID,
			Body:       item.Body,
			Author:     item.User.Login,
			CreatedAt:  item.CreatedAt,
			SourceType: "issue-comment",
		})
	}
	return result, nil
}

func (a *App) fetchReviewComments(ctx context.Context, repo string, number int) ([]conversationComment, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/comments", repo, number)
	raw, err := a.ghOutput(ctx, a.env, "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("fetch review comments: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var payload []struct {
		ID        int    `json:"id"`
		Body      string `json:"body"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("parse review comments: %w", err)
	}
	result := make([]conversationComment, 0, len(payload))
	for _, item := range payload {
		result = append(result, conversationComment{
			ID:         item.ID,
			Body:       item.Body,
			Author:     item.User.Login,
			CreatedAt:  item.CreatedAt,
			SourceType: "review-comment",
		})
	}
	return result, nil
}

func (a *App) fetchReviews(ctx context.Context, repo string, number int) ([]conversationComment, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/reviews", repo, number)
	raw, err := a.ghOutput(ctx, a.env, "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("fetch reviews: %w", err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var payload []struct {
		ID          int    `json:"id"`
		Body        string `json:"body"`
		State       string `json:"state"`
		SubmittedAt string `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("parse reviews: %w", err)
	}
	result := make([]conversationComment, 0, len(payload))
	for _, item := range payload {
		if strings.TrimSpace(item.Body) == "" {
			continue
		}
		result = append(result, conversationComment{
			ID:         item.ID,
			Body:       item.Body,
			Author:     item.User.Login,
			CreatedAt:  cmp.Or(item.SubmittedAt, "9999-12-31T23:59:59Z"),
			SourceType: "review",
		})
	}
	return result, nil
}

func isBotIdentity(login, botLogin string) bool {
	return strings.TrimSpace(login) == strings.TrimSpace(botLogin)
}

func reactionEndpointForComment(repo string, comment UnprocessedComment) string {
	switch comment.SourceType {
	case "issue-comment":
		return fmt.Sprintf("repos/%s/issues/comments/%d/reactions", repo, comment.ID)
	case "review-comment":
		return fmt.Sprintf("repos/%s/pulls/comments/%d/reactions", repo, comment.ID)
	default:
		return ""
	}
}

func responseSourceKey(comment UnprocessedComment) string {
	return comment.SourceType + ":" + strconv.Itoa(comment.ID)
}
