package ai

import "fmt"

// NewReviewer returns a Reviewer for the given provider and model.
// If model is empty, each provider uses a sensible default.
func NewReviewer(provider, model string) (Reviewer, error) {
	switch provider {
	case "anthropic":
		return newAnthropicReviewer(model)
	case "deepseek", "kimi", "ollama":
		return newOpenAICompatReviewer(provider, model)
	default:
		return nil, fmt.Errorf("unsupported provider %q — valid: anthropic, deepseek, kimi, ollama", provider)
	}
}
