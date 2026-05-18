package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type providerConfig struct {
	baseURL       string
	envKey        string
	defaultModel  string
}

var providerDefaults = map[string]providerConfig{
	"deepseek": {"https://api.deepseek.com/v1", "DEEPSEEK_API_KEY", "deepseek-v4-pro"},
	"kimi":     {"https://api.moonshot.cn/v1", "MOONSHOT_API_KEY", "kimi-k2-turbo-preview"},
	"ollama":   {"http://localhost:11434/v1", "", "qwen3-coder"},
}

type openAICompatReviewer struct {
	baseURL string
	apiKey  string
	model   string
}

func newOpenAICompatReviewer(provider, model string) (*openAICompatReviewer, error) {
	cfg, ok := providerDefaults[provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}

	apiKey := ""
	if cfg.envKey != "" {
		apiKey = os.Getenv(cfg.envKey)
		if apiKey == "" {
			return nil, fmt.Errorf("%s not set (required for provider %q)", cfg.envKey, provider)
		}
	}

	if model == "" {
		model = cfg.defaultModel
	}

	return &openAICompatReviewer{baseURL: cfg.baseURL, apiKey: apiKey, model: model}, nil
}

func (r *openAICompatReviewer) Review(ctx context.Context, pr PRContext, out io.Writer) error {
	payload, _ := json.Marshal(map[string]any{
		"model":  r.model,
		"stream": true,
		"messages": []map[string]string{
			{"role": "user", "content": buildPrompt(pr)},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	httpClient := &http.Client{Timeout: 180 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api error %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			fmt.Fprint(out, chunk.Choices[0].Delta.Content)
		}
	}

	return scanner.Err()
}
