package ai

import (
	"context"
	"fmt"
	"io"
	"os"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

type anthropicReviewer struct {
	model string
}

func newAnthropicReviewer(model string) (*anthropicReviewer, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	return &anthropicReviewer{model: model}, nil
}

func (r *anthropicReviewer) Review(ctx context.Context, pr PRContext, out io.Writer) error {
	client := anthropic.NewClient() // reads ANTHROPIC_API_KEY automatically

	stream := client.Messages.NewStreaming(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(r.model),
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildPrompt(pr))),
		},
	})

	for stream.Next() {
		event := stream.Current()
		switch ev := event.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			fmt.Fprint(out, ev.Delta.Text)
		}
	}

	return stream.Err()
}
