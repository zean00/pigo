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

func TestMistralProviderReturnsToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "devstral-test" {
			t.Fatalf("model = %v", payload["model"])
		}

		_, _ = fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": [{"type": "text", "text": "Working on it."}],
					"tool_calls": [{
						"id": "abc123xyz",
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

	provider := MistralProvider()
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "mistral",
		Model:    "devstral-test",
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
