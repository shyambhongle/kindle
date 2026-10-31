// Package llm is a minimal Claude Messages API client that returns parsed JSON.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

func New(apiKey, model string) *Client {
	if model == "" {
		model = "claude-sonnet-4-5"
	}
	return &Client{APIKey: apiKey, Model: model, BaseURL: "https://api.anthropic.com", HTTP: &http.Client{Timeout: 240 * time.Second}}
}

func (c *Client) Configured() bool { return c != nil && c.APIKey != "" }

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// JSON sends a system prompt and a user prompt and decodes the model's reply as JSON into out.
// The reply may wrap the JSON in a code fence or prose; the first {...} block is used.
func (c *Client) JSON(ctx context.Context, system, user string, maxTokens int, out any) (Usage, error) {
	if !c.Configured() {
		return Usage{}, errors.New("ANTHROPIC_API_KEY is not set")
	}
	body := map[string]any{
		"model":      c.Model,
		"max_tokens": maxTokens,
		"system":     system,
		"messages":   []map[string]any{{"role": "user", "content": user}},
	}
	payload, _ := json.Marshal(body)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/messages", bytes.NewReader(payload))
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("claude HTTP %d: %s", resp.StatusCode, trim(raw))
			time.Sleep(time.Duration(attempt+1) * 5 * time.Second)
			continue
		}
		if resp.StatusCode >= 300 {
			return Usage{}, fmt.Errorf("claude HTTP %d: %s", resp.StatusCode, trim(raw))
		}
		var res struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage Usage `json:"usage"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return Usage{}, fmt.Errorf("claude reply: %w", err)
		}
		var text strings.Builder
		for _, part := range res.Content {
			if part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
		if err := DecodeJSON(text.String(), out); err != nil {
			return res.Usage, err
		}
		return res.Usage, nil
	}
	return Usage{}, lastErr
}

// DecodeJSON extracts the first JSON object from text and decodes it.
func DecodeJSON(text string, out any) error {
	s := strings.TrimSpace(text)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(s, "json")
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return errors.New("no JSON object in model reply")
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), out); err != nil {
		return fmt.Errorf("model reply is not valid JSON: %w", err)
	}
	return nil
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
