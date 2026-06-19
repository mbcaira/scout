package scorer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/mbcaira/scout/internal/store"
)

type Result struct {
	Score     int    `json:"score"`
	Reasoning string `json:"reasoning"`
	Relevant  bool   `json:"relevant"`
}

type Scorer struct {
	client  *anthropic.Client
	profile string
}

func New(apiKey, profile string) *Scorer {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Scorer{client: client, profile: profile}
}

func (s *Scorer) Score(ctx context.Context, job store.Job) (Result, error) {
	prompt := fmt.Sprintf(`
You are evaluating a job posting for a specific candidate. Score the job 0-100 based on fit.

Candidate profile:
%s

Job posting:
Company: %s
Title: %s
Location: %s
Description: %s

Return JSON only, no markdown:
{"score": <0-100>, "relevant": <true|false>, "reasoning": "<one sentence>"}

Score 0 if it requires TypeScript, JavaScript, or is frontend-only.
Score high for Rust, C++, systems programming, low-latency, networking, distributed systems.
`, s.profile, job.Company, job.Title, job.Location, job.Description)

	msg, err := s.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model("claude-haiku-4-5-20251001")),
		MaxTokens: anthropic.F(int64(256)),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	if err != nil {
		return Result{}, err
	}

	raw := strings.TrimSpace(msg.Content[0].Text)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var result Result
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return Result{}, fmt.Errorf("parse score response %q: %w", raw, err)
	}

	return result, nil
}
