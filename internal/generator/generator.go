package generator

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/mbcaira/scout/internal/store"
)

type Package struct {
	CoverLetter string
	Questions   map[string]string
}

type Generator struct {
	client  *anthropic.Client
	profile string
}

func New(apiKey, profile string) *Generator {
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &Generator{client: client, profile: profile}
}

func (g *Generator) Generate(ctx context.Context, job store.Job, questions []string) (Package, error) {
	coverLetter, err := g.coverLetter(ctx, job)
	if err != nil {
		return Package{}, err
	}

	answers := make(map[string]string)
	for _, q := range questions {
		answer, err := g.answer(ctx, job, q)
		if err != nil {
			return Package{}, err
		}
		answers[q] = answer
	}

	return Package{
		CoverLetter: coverLetter,
		Questions:   answers,
	}, nil
}

func (g *Generator) coverLetter(ctx context.Context, job store.Job) (string, error) {
	prompt := fmt.Sprintf(`
Write a cover letter for this job application.

Candidate context:
%s

Job:
Company: %s
Title: %s
Description: %s

Rules:
- 3 short paragraphs max
- No "I am excited to apply" or similar openers
- Technically specific — reference actual work, real numbers
- Reads like a person wrote it, not an LLM
- No sign-off needed, end with the last paragraph
`, g.profile, job.Company, job.Title, job.Description)

	return g.complete(ctx, prompt)
}

func (g *Generator) answer(ctx context.Context, job store.Job, question string) (string, error) {
	prompt := fmt.Sprintf(`
Answer this job application question for the candidate.

Candidate context:
%s

Job: %s at %s

Question: %s

Rules:
- Direct and honest
- 2-4 sentences unless the question needs more
- Technically specific where relevant
- Sounds human, not corporate
`, g.profile, job.Title, job.Company, question)

	return g.complete(ctx, prompt)
}

func (g *Generator) complete(ctx context.Context, prompt string) (string, error) {
	msg, err := g.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model("claude-haiku-4-5-20251001")),
		MaxTokens: anthropic.F(int64(1024)),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	if err != nil {
		return "", err
	}

	return msg.Content[0].Text, nil
}
