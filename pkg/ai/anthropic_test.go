package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicProviderReturnsToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("x-api-key = %q", req.Header.Get("x-api-key"))
		}
		if req.Header.Get("anthropic-version") != "2023-06-01" {
			t.Fatalf("anthropic-version = %q", req.Header.Get("anthropic-version"))
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "claude-test" {
			t.Fatalf("model = %v", payload["model"])
		}

		_, _ = fmt.Fprint(w, `{
			"content": [
				{"type": "text", "text": "I can do that."},
				{"type": "tool_use", "id": "toolu_123", "name": "math", "input": {"a": 15, "b": 27}}
			],
			"usage": {"input_tokens": 5, "output_tokens": 20},
			"stop_reason": "tool_use"
		}`)
	}))
	defer server.Close()

	provider := AnthropicProvider()
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "anthropic",
		Model:    "claude-test",
		Tools: []Tool{{
			Name:        "math",
			Description: "add numbers",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "number"},
					"b": map[string]any{"type": "number"},
				},
			},
		}},
		Options: ChatOptions{
			APIKey:  "test-key",
			BaseURL: server.URL,
		},
		Messages: []Message{{Role: "user", Content: "calculate"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(events) == 0 || events[0].Type != "start" || events[len(events)-1].Type != "done" {
		t.Fatalf("events = %#v", events)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content len = %d", len(result.Content))
	}
}

func TestAnthropicProviderUsesOAuthBearerHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.Header.Get("Authorization"); got != "Bearer oauth-token" {
			t.Fatalf("authorization = %q", got)
		}
		if got := req.Header.Get("x-api-key"); got != "" {
			t.Fatalf("x-api-key = %q", got)
		}
		_, _ = fmt.Fprint(w, `{
			"content": [{"type": "text", "text": "ok"}],
			"usage": {"input_tokens": 1, "output_tokens": 1},
			"stop_reason": "end_turn"
		}`)
	}))
	defer server.Close()

	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "oauth-token")
	provider := AnthropicProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "anthropic",
		Model:    "claude-test",
		Options: ChatOptions{
			BaseURL: server.URL,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" {
		t.Fatalf("text = %q", result.Text)
	}
}

func TestAnthropicProviderStreamingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("x-api-key = %q", req.Header.Get("x-api-key"))
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["stream"] != true {
			t.Fatalf("stream = %#v", payload["stream"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_start\ndata: {\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"math\",\"input\":{}}}\n\n")
		_, _ = fmt.Fprint(w, "event: content_block_delta\ndata: {\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"a\\\":15}\"}}\n\n")
		_, _ = fmt.Fprint(w, "event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":6}}\n\n")
	}))
	defer server.Close()

	provider := AnthropicProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "anthropic",
		Model:    "claude-test",
		Options: ChatOptions{
			APIKey:  "test-key",
			BaseURL: server.URL,
			Stream:  true,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
}
