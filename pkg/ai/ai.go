// Package ai provides a minimal Anthropic Claude inference client.
// It communicates with the Anthropic Messages API over HTTPS using the
// standard library only — no additional dependencies required.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultModel     = "claude-haiku-4-5-20251001"
	DefaultMaxTokens = 1024
	anthropicVersion = "2023-06-01"
	messagesURL      = "https://api.anthropic.com/v1/messages"
)

// Client calls the Anthropic Messages API.
type Client struct {
	apiKey     string
	httpClient *http.Client
}

// New returns a Client using the given Anthropic API key.
func New(apiKey string) *Client {
	return &Client{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// InferRequest is the input for a single inference call.
type InferRequest struct {
	// Prompt is the user message.
	Prompt string `json:"prompt"`
	// Model defaults to DefaultModel when empty.
	Model string `json:"model,omitempty"`
	// System is an optional system prompt.
	System string `json:"system,omitempty"`
	// MaxTokens defaults to DefaultMaxTokens when zero.
	MaxTokens int `json:"max_tokens,omitempty"`
	// CacheSystem, when true, adds cache_control to the system prompt so
	// Anthropic's prompt caching can reuse it across repeated calls.
	// Adds the anthropic-beta: prompt-caching-2024-07-31 header automatically.
	CacheSystem bool `json:"cache_system,omitempty"`
}

// InferResponse is the result of a successful inference call.
type InferResponse struct {
	Model        string `json:"model"`
	Content      string `json:"content"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	// CacheReadTokens and CacheWriteTokens are non-zero when prompt caching was active.
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// anthropicSystemBlock is a system prompt block, optionally with cache_control.
type anthropicSystemBlock struct {
	Type         string                   `json:"type"`
	Text         string                   `json:"text"`
	CacheControl *anthropicCacheControl   `json:"cache_control,omitempty"`
}

type anthropicCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// anthropicRequest mirrors the Anthropic Messages API request body.
// System is interface{} so it can be a plain string OR a []anthropicSystemBlock
// for prompt-caching requests.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    any                `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse mirrors the Anthropic Messages API response body.
type anthropicResponse struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Usage struct {
		InputTokens            int `json:"input_tokens"`
		OutputTokens           int `json:"output_tokens"`
		CacheReadInputTokens   int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// buildSystem converts a system string into either a plain string (no caching)
// or a []anthropicSystemBlock with cache_control set (prompt caching).
func buildSystem(system string, cache bool) any {
	if system == "" {
		return nil
	}
	if !cache {
		return system
	}
	return []anthropicSystemBlock{{
		Type:         "text",
		Text:         system,
		CacheControl: &anthropicCacheControl{Type: "ephemeral"},
	}}
}

// Infer sends a single prompt to Claude and returns the response text.
func (c *Client) Infer(ctx context.Context, req InferRequest) (InferResponse, error) {
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    buildSystem(req.System, req.CacheSystem),
		Messages: []anthropicMessage{
			{Role: "user", Content: req.Prompt},
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return InferResponse{}, fmt.Errorf("ai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, messagesURL, bytes.NewReader(data))
	if err != nil {
		return InferResponse{}, fmt.Errorf("ai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	if req.CacheSystem {
		httpReq.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return InferResponse{}, fmt.Errorf("ai: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return InferResponse{}, fmt.Errorf("ai: read body: %w", err)
	}

	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return InferResponse{}, fmt.Errorf("ai: decode response: %w", err)
	}
	if ar.Error != nil {
		return InferResponse{}, fmt.Errorf("ai: anthropic error (%s): %s", ar.Error.Type, ar.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return InferResponse{}, fmt.Errorf("ai: unexpected status %d: %s", resp.StatusCode, string(raw))
	}

	var text string
	for _, block := range ar.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}

	return InferResponse{
		Model:            ar.Model,
		Content:          text,
		InputTokens:      ar.Usage.InputTokens,
		OutputTokens:     ar.Usage.OutputTokens,
		CacheReadTokens:  ar.Usage.CacheReadInputTokens,
		CacheWriteTokens: ar.Usage.CacheCreationInputTokens,
	}, nil
}

// ── streaming ─────────────────────────────────────────────────────────────────

// StreamRequest is the input for a streaming inference call.
// It extends InferRequest with multi-turn message history.
type StreamRequest struct {
	Messages  []Message `json:"messages"`
	Model     string    `json:"model,omitempty"`
	System    string    `json:"system,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

// Message is a single turn in a conversation.
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"`
}

// StreamChunk is a single SSE delta event emitted by Stream.
type StreamChunk struct {
	// Type is "delta", "stop", or "error".
	Type  string `json:"type"`
	// Text carries incremental content when Type=="delta".
	Text  string `json:"text,omitempty"`
	// Error carries an error message when Type=="error".
	Error string `json:"error,omitempty"`
}

// anthropicStreamRequest is the Anthropic API request body with stream=true.
type anthropicStreamRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream"`
}

// Stream sends a streaming request to Claude and emits chunks on the returned
// channel. The channel is closed when the stream ends or ctx is cancelled.
// The caller must drain the channel. Errors are emitted as StreamChunk{Type:"error"}.
func (c *Client) Stream(ctx context.Context, req StreamRequest) <-chan StreamChunk {
	ch := make(chan StreamChunk, 32)
	go func() {
		defer close(ch)

		model := req.Model
		if model == "" {
			model = DefaultModel
		}
		maxTokens := req.MaxTokens
		if maxTokens <= 0 {
			maxTokens = DefaultMaxTokens
		}

		msgs := make([]anthropicMessage, len(req.Messages))
		for i, m := range req.Messages {
			msgs[i] = anthropicMessage{Role: m.Role, Content: m.Content}
		}

		body := anthropicStreamRequest{
			Model:     model,
			MaxTokens: maxTokens,
			System:    req.System,
			Messages:  msgs,
			Stream:    true,
		}
		data, err := json.Marshal(body)
		if err != nil {
			ch <- StreamChunk{Type: "error", Error: fmt.Sprintf("marshal: %v", err)}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, messagesURL, bytes.NewReader(data))
		if err != nil {
			ch <- StreamChunk{Type: "error", Error: fmt.Sprintf("build request: %v", err)}
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-api-key", c.apiKey)
		httpReq.Header.Set("anthropic-version", anthropicVersion)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			ch <- StreamChunk{Type: "error", Error: fmt.Sprintf("http: %v", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			ch <- StreamChunk{Type: "error", Error: fmt.Sprintf("status %d: %s", resp.StatusCode, string(raw))}
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				break
			}

			var ev struct {
				Type  string `json:"type"`
				Delta *struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Error *struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}

			switch ev.Type {
			case "content_block_delta":
				if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
					select {
					case ch <- StreamChunk{Type: "delta", Text: ev.Delta.Text}:
					case <-ctx.Done():
						return
					}
				}
			case "message_stop":
				ch <- StreamChunk{Type: "stop"}
				return
			case "error":
				if ev.Error != nil {
					ch <- StreamChunk{Type: "error", Error: ev.Error.Message}
				}
				return
			}
		}
		ch <- StreamChunk{Type: "stop"}
	}()
	return ch
}

// ── multi-turn helpers ────────────────────────────────────────────────────────

// InferMultiTurn sends a full conversation (history + new user message) to
// Claude and returns the assistant's reply as a non-streaming InferResponse.
func (c *Client) InferMultiTurn(ctx context.Context, history []Message, system, model string, maxTokens int) (InferResponse, error) {
	if model == "" {
		model = DefaultModel
	}
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	msgs := make([]anthropicMessage, len(history))
	for i, m := range history {
		msgs[i] = anthropicMessage{Role: m.Role, Content: m.Content}
	}
	body := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  msgs,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return InferResponse{}, fmt.Errorf("ai: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, messagesURL, bytes.NewReader(data))
	if err != nil {
		return InferResponse{}, fmt.Errorf("ai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return InferResponse{}, fmt.Errorf("ai: http: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var ar anthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return InferResponse{}, fmt.Errorf("ai: decode: %w", err)
	}
	if ar.Error != nil {
		return InferResponse{}, fmt.Errorf("ai: anthropic error (%s): %s", ar.Error.Type, ar.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return InferResponse{}, fmt.Errorf("ai: status %d: %s", resp.StatusCode, string(raw))
	}

	var text string
	for _, block := range ar.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return InferResponse{
		Model:        ar.Model,
		Content:      text,
		InputTokens:  ar.Usage.InputTokens,
		OutputTokens: ar.Usage.OutputTokens,
	}, nil
}
