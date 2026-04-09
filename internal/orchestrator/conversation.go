package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// UnprocessedComment represents a comment on an issue or PR that hasn't been
// responded to yet (no +1 reaction from the bot).
type UnprocessedComment struct {
	ID                int
	Author            string
	Body              string
	CommenterIdentity string // "human:{login}", "agent:{name}", or "bot"
	ArtifactType      string // "issue" or "pr"
	ArtifactNumber    int
	CreatedAt         string
}

var agentMarkerPattern = regexp.MustCompile(`<!-- runoq:agent:(\S+) -->`)

// findUnprocessedComments queries GitHub for comments on an issue or PR,
// returning those without a +1 reaction (the "processed" marker).
// Filters out bot-generated comments (runoq:bot markers).
func (a *App) findUnprocessedComments(ctx context.Context, repo string, artifactType string, number int) ([]UnprocessedComment, error) {
	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", repo, number)
	raw, err := a.ghOutput(ctx, a.env, "api", endpoint, "--paginate")
	if err != nil {
		return nil, fmt.Errorf("fetch comments for %s #%d: %w", artifactType, number, err)
	}

	var apiComments []struct {
		ID        int    `json:"id"`
		Body      string `json:"body"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt string `json:"created_at"`
		Reactions struct {
			ThumbsUp int `json:"+1"`
		} `json:"reactions"`
	}
	if err := json.Unmarshal([]byte(raw), &apiComments); err != nil {
		return nil, fmt.Errorf("parse comments: %w", err)
	}

	botLogin := a.cfg.IdentityHandle + "[bot]"
	var result []UnprocessedComment

	for _, c := range apiComments {
		// Skip already-processed comments (+1 reaction present)
		if c.Reactions.ThumbsUp > 0 {
			continue
		}

		// Skip bot-generated comments (audit trail, not conversation)
		if strings.Contains(c.Body, "runoq:bot") {
			continue
		}

		// Determine commenter identity
		identity := identityFromComment(c.Body, c.User.Login, botLogin)

		result = append(result, UnprocessedComment{
			ID:                c.ID,
			Author:            c.User.Login,
			Body:              c.Body,
			CommenterIdentity: identity,
			ArtifactType:      artifactType,
			ArtifactNumber:    number,
			CreatedAt:         c.CreatedAt,
		})
	}

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
	if login == botLogin {
		return "bot"
	}
	return "human:" + login
}
