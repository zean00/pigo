package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIProviderReturnsToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}
		if req.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-mini" {
			t.Fatalf("model = %v", payload["model"])
		}

		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "I can do that.",
					"tool_calls": [{
						"id": "tc-1",
						"type": "function",
						"function": {
							"name": "math",
							"arguments": "{\"a\":15,\"b\":27}"
						}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {
				"prompt_tokens": 5,
				"completion_tokens": 20,
				"total_tokens": 25
			}
		}`)
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
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
			APIKey:    "test-key",
			Stream:    false,
			MaxTokens: 128,
		},
		Messages: []Message{
			{Role: "user", Content: "calculate"},
		},
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
	if first, ok := result.Content[0].(map[string]any); !ok || first["type"] != "text" {
		t.Fatalf("content[0] = %#v", result.Content[0])
	}
}

func TestOpenAIProviderStreamingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var chunks bytes.Buffer
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"\"}]}\n")
		chunks.WriteString("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"tc-1\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"value\\\":\\\"ok\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n")
		chunks.WriteString("data: [DONE]\n")
		if _, err := w.Write(chunks.Bytes()); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	provider := OpenAIProvider(WithOpenAIBaseURL(server.URL + "/v1"))
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "openai",
		Model:    "gpt-mini",
		Tools:    []Tool{{Name: "echo"}},
		Options: ChatOptions{
			APIKey: "test-key",
			Stream: true,
		},
		Messages: []Message{{Role: "user", Content: "please"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if len(events) < 4 {
		t.Fatalf("events = %#v", events)
	}
	if result.Text != "hello world" {
		t.Fatalf("text = %q", result.Text)
	}
}
