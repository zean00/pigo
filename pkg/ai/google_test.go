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

func TestGoogleProviderReturnsToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			t.Fatalf("method = %q", req.Method)
		}
		if req.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.URL.Query().Get("key") != "test-key" {
			t.Fatalf("api key query = %q", req.URL.Query().Get("key"))
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		contents, _ := payload["contents"].([]any)
		if len(contents) == 0 {
			t.Fatal("missing contents")
		}

		_, _ = fmt.Fprint(w, `{
			"candidates": [{
				"content": {
					"role": "model",
					"parts": [
						{"text": "Working on it.", "thoughtSignature": "text-sig"},
						{"functionCall": {"id": "call-1", "name": "math", "args": {"a": 15, "b": 27}}, "thoughtSignature": "tool-sig"}
					]
				},
				"finishReason": "STOP"
			}],
			"usageMetadata": {
				"promptTokenCount": 10,
				"candidatesTokenCount": 6,
				"thoughtsTokenCount": 2,
				"cachedContentTokenCount": 3,
				"totalTokenCount": 18
			}
		}`)
	}))
	defer server.Close()

	provider := GoogleProvider()
	result, events, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google",
		Model:    "gemini-test",
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
			APIKey:     "test-key",
			BaseURL:    server.URL + "/v1beta",
			ToolChoice: "auto",
		},
		Messages: []Message{{Role: "user", Content: "calculate"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != "toolUse" {
		t.Fatalf("stopReason = %q", result.StopReason)
	}
	if result.Usage == nil {
		t.Fatal("missing usage")
	}
	if result.Usage.Input != 7 || result.Usage.Output != 8 || result.Usage.CacheRead != 3 || result.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	if len(events) == 0 || events[0].Type != "start" || events[len(events)-1].Type != "done" {
		t.Fatalf("events = %#v", events)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content len = %d", len(result.Content))
	}
	text, _ := result.Content[0].(map[string]any)
	if text["textSignature"] != "text-sig" {
		t.Fatalf("text = %#v", text)
	}
	tool, _ := result.Content[1].(map[string]any)
	if tool["thoughtSignature"] != "tool-sig" {
		t.Fatalf("tool = %#v", tool)
	}
}

func TestGoogleProviderStreamingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1beta/models/gemini-test:streamGenerateContent" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if req.URL.Query().Get("alt") != "sse" {
			t.Fatalf("alt = %q", req.URL.Query().Get("alt"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\",\"thoughtSignature\":\"sig-1\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n\n")
	}))
	defer server.Close()

	provider := GoogleProvider()
	result, _, err := provider.Complete(context.Background(), CompletionRequest{
		Provider: "google",
		Model:    "gemini-test",
		Options: ChatOptions{
			APIKey:  "test-key",
			BaseURL: server.URL + "/v1beta",
			Stream:  true,
		},
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" {
		t.Fatalf("text = %q", result.Text)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
	text, _ := result.Content[0].(map[string]any)
	if text["textSignature"] != "sig-1" {
		t.Fatalf("text = %#v", text)
	}
}

func TestGoogleProviderSendsThoughtSignatures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		contents, _ := payload["contents"].([]any)
		assistant, _ := contents[0].(map[string]any)
		parts, _ := assistant["parts"].([]any)
		if len(parts) != 3 {
			t.Fatalf("parts = %#v", parts)
		}
		text, _ := parts[0].(map[string]any)
		if text["thoughtSignature"] != "text-sig" {
			t.Fatalf("text = %#v", text)
		}
		thinking, _ := parts[1].(map[string]any)
		if thinking["thought"] != true || thinking["thoughtSignature"] != "thinking-sig" {
			t.Fatalf("thinking = %#v", thinking)
		}
		tool, _ := parts[2].(map[string]any)
		if tool["thoughtSignature"] != "tool-sig" {
			t.Fatalf("tool = %#v", tool)
		}
		_, _ = fmt.Fprint(w, `{
			"candidates": [{
				"content": {"role": "model", "parts": [{"text": "ok"}]},
				"finishReason": "STOP"
			}],
			"usageMetadata": {"promptTokenCount": 1, "candidatesTokenCount": 1, "totalTokenCount": 2}
		}`)
	}))
	defer server.Close()

	_, _, err := GoogleProvider().Complete(context.Background(), CompletionRequest{
		Provider: "google",
		Model:    "gemini-test",
		Options: ChatOptions{
			APIKey:  "test-key",
			BaseURL: server.URL + "/v1beta",
		},
		Messages: []Message{{
			Role: "assistant",
			Content: []ContentBlock{
				{Type: "text", Text: "hello", TextSignature: "text-sig"},
				{Type: "thinking", Thinking: "reason", ThinkingSignature: "thinking-sig"},
				{Type: "toolCall", ID: "call-1", Name: "math", Arguments: map[string]any{"a": 1}, ThoughtSignature: "tool-sig"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}
