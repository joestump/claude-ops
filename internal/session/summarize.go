package session

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// Governing: SPEC-0021 REQ "Session Summary Generation"
const summarizeSystemPrompt = "You are a concise technical summarizer. Summarize the following infrastructure monitoring session output in 2-4 sentences. Focus on: what was checked, what issues were found (if any), and what actions were taken. Be specific about service names and outcomes."

// summarizeResponse calls the Anthropic Messages API to generate a short
// plain-text summary of a session response. The model parameter should be
// an Anthropic model identifier (e.g. "haiku").
//
// Governing: SPEC-0021 REQ "Session Summary Generation"
func summarizeResponse(ctx context.Context, response string, model string) (string, error) {
	client := anthropic.NewClient()

	msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 200,
		System: []anthropic.TextBlockParam{
			{Text: summarizeSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(response)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("anthropic messages: %w", err)
	}

	// Extract text from the response content blocks.
	for _, block := range msg.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", fmt.Errorf("no text block in response")
}
