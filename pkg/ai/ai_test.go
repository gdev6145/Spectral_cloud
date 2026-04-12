package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gdev6145/Spectral_cloud/pkg/ai"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// anthropicResp builds a minimal Anthropic Messages API response JSON.
func anthropicResp(model, text string) []byte {
	b, _ := json.Marshal(map[string]any{
		"id":    "msg_test",
		"model": model,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		"content": []any{
			map[string]any{"type": "text", "text": text},
		},
	})
	return b
}

// anthropicErrResp returns an Anthropic-style error body.
func anthropicErrResp(errType, msg string) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{"type": errType, "message": msg},
	})
	return b
}

// toolUseResp returns a response whose stop_reason is "tool_use".
func toolUseResp(model, toolID, toolName string, input any) []byte {
	inputJSON, _ := json.Marshal(input)
	b, _ := json.Marshal(map[string]any{
		"id":          "msg_tool",
		"model":       model,
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 20, "output_tokens": 8},
		"content": []any{
			map[string]any{
				"type":  "tool_use",
				"id":    toolID,
				"name":  toolName,
				"input": json.RawMessage(inputJSON),
			},
		},
	})
	return b
}

// endTurnResp returns a plain end_turn text response.
func endTurnResp(model, text string) []byte {
	b, _ := json.Marshal(map[string]any{
		"id":          "msg_end",
		"model":       model,
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 15, "output_tokens": 7},
		"content": []any{
			map[string]any{"type": "text", "text": text},
		},
	})
	return b
}

// ── Infer ─────────────────────────────────────────────────────────────────────

func TestInfer_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("expected api key header, got %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("anthropic-version header missing")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(anthropicResp("claude-test", "Hello, World!"))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	resp, err := c.Infer(context.Background(), ai.InferRequest{Prompt: "Hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello, World!" {
		t.Errorf("expected %q, got %q", "Hello, World!", resp.Content)
	}
	if resp.Model != "claude-test" {
		t.Errorf("model: expected %q, got %q", "claude-test", resp.Model)
	}
	if resp.InputTokens != 10 || resp.OutputTokens != 5 {
		t.Errorf("token counts: got in=%d out=%d", resp.InputTokens, resp.OutputTokens)
	}
}

func TestInfer_AnthropicError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // Anthropic returns 200 with an error field
		_, _ = w.Write(anthropicErrResp("invalid_request_error", "bad prompt"))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	_, err := c.Infer(context.Background(), ai.InferRequest{Prompt: "Hi"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bad prompt") {
		t.Errorf("error should mention 'bad prompt', got: %v", err)
	}
}

func TestInfer_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Non-200 with a non-JSON body exercises the decode-error path.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal server error`))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	_, err := c.Infer(context.Background(), ai.InferRequest{Prompt: "Hi"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Either a JSON decode error or an unexpected-status error is acceptable;
	// the important thing is that an error is propagated to the caller.
}

func TestInfer_DefaultsApplied(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(anthropicResp("x", "ok"))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	// Pass zero values — client should fill in defaults.
	_, _ = c.Infer(context.Background(), ai.InferRequest{Prompt: "test"})

	model, _ := gotBody["model"].(string)
	if model == "" {
		t.Error("expected a default model to be set in request body")
	}
	maxTokens, _ := gotBody["max_tokens"].(float64)
	if maxTokens <= 0 {
		t.Error("expected max_tokens > 0")
	}
}

func TestInfer_CacheSystemHeader(t *testing.T) {
	var betaHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		betaHeader = r.Header.Get("anthropic-beta")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(anthropicResp("x", "cached"))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	_, _ = c.Infer(context.Background(), ai.InferRequest{
		Prompt:      "prompt",
		System:      "you are a helper",
		CacheSystem: true,
	})

	if !strings.Contains(betaHeader, "prompt-caching") {
		t.Errorf("expected prompt-caching beta header, got %q", betaHeader)
	}
}

// ── Stream ────────────────────────────────────────────────────────────────────

func TestStream_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		events := []string{
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" World"}}`,
			`data: {"type":"message_stop"}`,
		}
		for _, e := range events {
			fmt.Fprintln(w, e)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	ch := c.Stream(context.Background(), ai.StreamRequest{
		Messages: []ai.Message{{Role: "user", Content: "hi"}},
	})

	var chunks []ai.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
	}

	var text string
	for _, chunk := range chunks {
		if chunk.Type == "delta" {
			text += chunk.Text
		}
	}
	if text != "Hello World" {
		t.Errorf("expected %q, got %q", "Hello World", text)
	}
	// Last chunk should be stop.
	last := chunks[len(chunks)-1]
	if last.Type != "stop" {
		t.Errorf("expected last chunk type 'stop', got %q", last.Type)
	}
}

func TestStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	ch := c.Stream(context.Background(), ai.StreamRequest{
		Messages: []ai.Message{{Role: "user", Content: "hi"}},
	})

	var errChunk *ai.StreamChunk
	for chunk := range ch {
		if chunk.Type == "error" {
			cp := chunk
			errChunk = &cp
		}
	}
	if errChunk == nil {
		t.Fatal("expected an error chunk")
	}
	if !strings.Contains(errChunk.Error, "503") {
		t.Errorf("error should mention status 503, got: %v", errChunk.Error)
	}
}

func TestStream_Cancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Never send anything; the context will be cancelled.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := ai.NewWithBaseURL("test-key", srv.URL)
	ch := c.Stream(ctx, ai.StreamRequest{
		Messages: []ai.Message{{Role: "user", Content: "hi"}},
	})
	cancel() // cancel immediately

	// Drain channel; it must close without blocking.
	for range ch {
	}
}

// ── InferMultiTurn ────────────────────────────────────────────────────────────

func TestInferMultiTurn_Success(t *testing.T) {
	var gotMsgs []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if msgs, ok := body["messages"].([]any); ok {
			for _, m := range msgs {
				if mm, ok := m.(map[string]any); ok {
					gotMsgs = append(gotMsgs, mm)
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(anthropicResp("claude-m", "Sure!"))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	history := []ai.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "How are you?"},
	}
	resp, err := c.InferMultiTurn(context.Background(), history, "Be friendly.", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Sure!" {
		t.Errorf("expected %q, got %q", "Sure!", resp.Content)
	}
	if len(gotMsgs) != 3 {
		t.Errorf("expected 3 messages sent, got %d", len(gotMsgs))
	}
}

// ── InferWithTools ────────────────────────────────────────────────────────────

func TestInferWithTools_EndTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(endTurnResp("claude-t", "Task complete."))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	resp, err := c.InferWithTools(context.Background(), ai.ToolsRequest{
		Messages: []ai.Message{{Role: "user", Content: "do something"}},
		Tools: []ai.Tool{
			{
				Name:        "my_tool",
				Description: "Does something",
				InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %q", resp.StopReason)
	}
	if resp.TextContent() != "Task complete." {
		t.Errorf("expected text content %q, got %q", "Task complete.", resp.TextContent())
	}
	if len(resp.ToolUseCalls()) != 0 {
		t.Errorf("expected no tool_use calls, got %d", len(resp.ToolUseCalls()))
	}
}

func TestInferWithTools_ToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(toolUseResp("claude-t", "call_1", "my_tool", map[string]string{"arg": "val"}))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)
	resp, err := c.InferWithTools(context.Background(), ai.ToolsRequest{
		Messages: []ai.Message{{Role: "user", Content: "use my_tool"}},
		Tools: []ai.Tool{
			{
				Name:        "my_tool",
				Description: "Does something",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StopReason != "tool_use" {
		t.Errorf("expected stop_reason 'tool_use', got %q", resp.StopReason)
	}
	calls := resp.ToolUseCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool_use call, got %d", len(calls))
	}
	if calls[0].Name != "my_tool" {
		t.Errorf("expected tool name 'my_tool', got %q", calls[0].Name)
	}
	if calls[0].ID != "call_1" {
		t.Errorf("expected tool ID 'call_1', got %q", calls[0].ID)
	}
}

func TestInferWithTools_RawAndToolResultMessages(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(endTurnResp("claude-t", "Done."))
	}))
	defer srv.Close()

	c := ai.NewWithBaseURL("test-key", srv.URL)

	// Build a conversation that includes a raw assistant tool-use turn and a
	// user tool-result turn, as produced by AppendToolResults.
	history := []ai.Message{{Role: "user", Content: "do it"}}
	blocks := []ai.ContentBlock{
		{Type: "tool_use", ID: "t1", Name: "my_tool", Input: json.RawMessage(`{"x":1}`)},
	}
	results := []ai.ToolResult{{ToolUseID: "t1", Content: "result-ok"}}
	history = ai.AppendToolResults(history, blocks, results)

	_, err := c.InferWithTools(context.Background(), ai.ToolsRequest{
		Messages: history,
		Tools:    []ai.Tool{{Name: "my_tool", InputSchema: json.RawMessage(`{}`)}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The body must contain 3 messages: user, assistant (raw), user (tool_result).
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages in request, got %d", len(msgs))
	}
}

// ── AppendToolResults ─────────────────────────────────────────────────────────

func TestAppendToolResults(t *testing.T) {
	history := []ai.Message{{Role: "user", Content: "start"}}
	blocks := []ai.ContentBlock{
		{Type: "tool_use", ID: "c1", Name: "ping", Input: json.RawMessage(`{}`)},
	}
	results := []ai.ToolResult{{ToolUseID: "c1", Content: "pong"}}
	got := ai.AppendToolResults(history, blocks, results)

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	// Second message must be marked as raw (assistant tool_use turn).
	if !got[1].IsRaw {
		t.Error("second message should have IsRaw=true")
	}
	if got[1].Role != "assistant" {
		t.Errorf("second message role: expected 'assistant', got %q", got[1].Role)
	}
	// Third message must be a tool_result (user turn).
	if !got[2].IsToolResult {
		t.Error("third message should have IsToolResult=true")
	}
	if got[2].Role != "user" {
		t.Errorf("third message role: expected 'user', got %q", got[2].Role)
	}
	// The tool result JSON must encode the ToolResult slice.
	var decoded []ai.ToolResult
	if err := json.Unmarshal([]byte(got[2].Content), &decoded); err != nil {
		t.Fatalf("third message content is not valid JSON: %v", err)
	}
	if len(decoded) != 1 || decoded[0].ToolUseID != "c1" || decoded[0].Content != "pong" {
		t.Errorf("unexpected tool results in third message: %+v", decoded)
	}
}

// ── ToolsResponse helpers ─────────────────────────────────────────────────────

func TestToolsResponse_TextContent(t *testing.T) {
	resp := ai.ToolsResponse{
		Content: []ai.ContentBlock{
			{Type: "text", Text: "Hello"},
			{Type: "tool_use", Name: "x"},
			{Type: "text", Text: " world"},
		},
	}
	if got := resp.TextContent(); got != "Hello world" {
		t.Errorf("TextContent: expected %q, got %q", "Hello world", got)
	}
}

func TestToolsResponse_ToolUseCalls(t *testing.T) {
	resp := ai.ToolsResponse{
		Content: []ai.ContentBlock{
			{Type: "text", Text: "preamble"},
			{Type: "tool_use", ID: "t1", Name: "a"},
			{Type: "tool_use", ID: "t2", Name: "b"},
		},
	}
	calls := resp.ToolUseCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool_use calls, got %d", len(calls))
	}
	if calls[0].ID != "t1" || calls[1].ID != "t2" {
		t.Errorf("unexpected call IDs: %v %v", calls[0].ID, calls[1].ID)
	}
}
